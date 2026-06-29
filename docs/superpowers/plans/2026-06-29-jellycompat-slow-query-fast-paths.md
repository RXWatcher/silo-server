# Jellycompat slow-query fast-paths

Plan derived from a 3-hour production log window (2026-06-29 08:27–11:27, ~62k lines)
and an adversarial code+log review of each finding. Commands assume the repository
root is the cwd. Branch: `perf/jellycompat-fast-paths`.

Goal for each confirmed issue: cut the hot query to **sub-100ms**, preferring reuse
of an existing Silo native fast path. 100% Jellyfin fidelity is **not** required — an
~80%-fidelity fast path is acceptable where called out.

## Operational note: Postgres restart NOT required

The startup WARN `postgres restart required to finish applying auto-tuned settings`
is a false alarm. The autotuner re-applies via `ALTER SYSTEM` on every silo startup,
which makes Postgres emit the WARN even when nothing changed. Verified on prod:
`pending_restart = true` returns **no rows**, and `postgresql.auto.conf` (written
08:27) holds values **identical** to the running config (shared_buffers ≈ 12 GB,
effective_cache_size ≈ 37 GB, work_mem ≈ 88 MB, max_connections 120). The tuned
config has been live since the 2026-06-20 PG restart. Do not restart prod PG for this.

## Severity snapshot (from logs)

| # | Route | Count | Avg | Max | Verdict |
|---|---|---|---|---|---|
| 1 | `/UserItems/Resume` (Continue Watching) | 157 | 16.5s | 91s | CONFIRMED |
| 2 | `/Shows/{id}/Episodes` (`adjacentTo`) | 475 | 0.96s | 13s | CONFIRMED |
| 3 | Home hub sections (blank rails) | 8 fails | — | — | CONFIRMED (+2nd site) |
| 4 | `/Items` played-history | 4×>13s | — | 15.7s | CONFIRMED |
| 5 | `/Items/Latest` (multi-library) | 74 | 2.1s | 4.8s | CONFIRMED (EXPLAIN-proven) |
| 6 | per-item `GetItemDetail` N+1 | — | — | — | PARTIALLY CONFIRMED |

---

## Issue 1 — Resume endpoint (P0, 91s worst case)

**Root cause.** Every observed request sends `includeItemTypes=...`, so the
`len(typeSet)==0` fast-path guard at `internal/jellycompat/handlers_items.go:2132`
never fires. The operative gate is the slow-loop early-exit at
`handlers_items.go:2217` (`if !query.enableTotalRecordCount && len(items) >= query.limit`):
- `enableTotalRecordCount=true` → exit gated off → scans the **entire** in-progress list (~50s avg).
- `enableTotalRecordCount=false` but visible resumable count < limit → also never reaches limit → full scan (25s tail).

Amplifier: per batch the loop re-runs `FilterResumeProgress` →
`SupersededEpisodeProgressIDs` → `CompletedProgressSnapshots`
(`internal/catalog/continue_watching_progress.go:89`), which re-paginates the
profile's **entire completed history** every batch. Cost = O(in_progress × completed).
No singleflight, so Wholphin 0.6.4 retry storms stack full scans → 91s.

**DECISION (2026-06-29): use the `sections.Fetcher` delegation, NOT the
`enableTotalRecordCount=false` one-liner.** The one-liner was ruled out:
- It only rewrites the `true` cohort to `false` behavior, but the logs show the
  `false` cohort *already* has 25s tails (max 25,943ms with `enableTotalRecordCount=false`),
  so it does nothing for them. Any user whose visible resumable count is below the page
  limit never reaches the `len(items) >= limit` exit (`handlers_items.go:2217`) and
  full-scans regardless of the flag.
- Even best-case it lands at ~1.3s, not <100ms, because the dominant cost — per-batch
  `CompletedProgressSnapshots` re-paginating the entire completed history
  (`continue_watching_progress.go:89`) — is untouched by the flag.

The native `collectContinueProgressItems` (`internal/sections/fetcher.go:476`) avoids
that cost entirely: it is hard-capped (page size 100, `continueProgressMaxScanned = 1000`,
breaks unconditionally at `len(orderedItems) >= limit`) and filters via the indexed
home-dismissal index (`dismissals.FilterProgress`) rather than re-scanning completed
history per batch. It returns episode-level `*models.MediaItem`s and the `WatchProgress`
entries carry `PositionSeconds`, so resume position is preserved. This is the only option
that reaches <100ms for all cohorts, and it reuses proven native code.

**Fix (reuse native fast path).** `ItemsHandler` already holds
`sectionsFetcher *sections.Fetcher`. Delegate `handleResumeResponse`
(`handlers_items.go:2075`) to the native continue-watching section. Map results with the
existing `mediaItemToListItem` → `resolveUserStateForContentIDs` → `mapper.itemFromList`
block that `handleSectionBrowse` already uses. Keep `loadProgressPage` as a fallback
when `sectionsFetcher == nil`.

**Cap.** Clamp the effective Resume page size to **min(requestedLimit, 50)** so a client
asking for a huge page cannot widen the scan; 50 is well above any real "Continue
Watching" rail and keeps the native scan bounded.

**Compat tradeoff.** `TotalRecordCount` becomes `len(items)` (no accurate total — not
rendered on this row). The native path filters via home-dismissals, not the jellycompat
`SupersededEpisodeProgressIDs` set, so a resume entry the user has moved past *may*
surface if the native section does not already collapse it via next-up handling —
acceptable within the <100% compat budget. **Implementation must verify** behavioral
parity on superseded-episode hiding and that `ContinueType=watching` does not inject
not-yet-started next-up items.

**Files.** `internal/jellycompat/handlers_items.go`, `internal/jellycompat/handlers_sections.go`
(extract shared map/user-state helper). No migration.

**Est. latency.** <100ms cold (matches today's fast `false` requests), eliminates 50–91s tail.

---

## Issue 2 — Episodes `adjacentTo` (P0, 13s on soaps)

**Root cause.** `adjacentTo` is unimplemented (not in `itemsQuery`, not parsed —
`internal/jellycompat/query.go`). `HandleEpisodes` (`handlers_items.go:1284`) passes
`page=false`, so `slicePage` (`:1435`) never runs and `limit=1` is a no-op. The handler
materializes the whole series (`episode_repo.go:684` `ListBySeries`), resolves user
state for all episodes, and runs `fetchCompatEpisodeTargetsByContentIDs` with a
**per-episode correlated EXISTS** into `media_files` (`batch_loaders.go:230-233`). For
a ~10k-episode soap (Coronation Street, EastEnders, Emmerdale) that's 10k EXISTS probes.
All slow calls are Wholphin `adjacentTo=<ep>&limit=1`, no `Fields=`.

**Fix (reuse existing index).** Parse `AdjacentTo` in `query.go` (decode via
`decodeItemID`). Add `episode_repo.go` `ListAdjacentInSeries(seriesID, season, episode)`:
resolve the target's `(season_number, episode_number)`, then
`WHERE series_id=$1 AND (season_number,episode_number) > ($2,$3) ORDER BY ... LIMIT 1`
(next) + mirror DESC (previous) + the target row. All bounded by existing
`idx_episodes_series (series_id, season_number, episode_number)`
(`001_schema.sql:1752`, `102_catalog_perf_indexes.sql:66`). In `HandleEpisodes`, when
`adjacentTo != ""`, run the downstream batch on only those ≤3 IDs; do **not** route
through `writeSeriesEpisodesResponse`.

**Compat tradeoff.** Returns prev/self/next (≤3 items), not Jellyfin's exact window —
satisfies Wholphin's autoplay/skip use.

**Files.** `internal/jellycompat/query.go`, `internal/catalog/episode_repo.go`,
`internal/jellycompat/handlers_items.go`, `internal/jellycompat/handlers_items_test.go`.
No migration.

**Est. latency.** <50ms regardless of series size.

---

## Issue 3 — Blank home hub sections (P1, correctness)

**Root cause.** Jellycompat always injects `ExcludedMediaTypes=["audiobook","podcast"]`
(`internal/jellycompat/content_direct.go:54`). The section preview path calls
`ApplySectionAccessFilter("ece", ...)`, which emits `NOT (ece.type = ANY($N))`
(`internal/catalog/access_filter.go:50-54`), but `episode_catalog_entries` has **no
`type` column** (migration 142). Postgres returns `42703`; the handler degrades to an
empty array (`handlers_sections.go:116-118`), so clients render blank rails. Bug exists
at **two** call sites: `episode_catalog_entries_executor.go:245` (logged) and `:87`
(latent, user-state path).

**Fix.** `episode_catalog_entries` is episodes-only (populated from `type='series'`,
FK to `episodes`), so the audiobook/podcast exclusion is a provable no-op there. Add a
local helper that zeroes `access.ExcludedMediaTypes` (by-value param, no caller leak)
before calling `ApplySectionAccessFilter("ece", ...)`; use it at both call sites.
Preserve `content_rating` filtering (that column exists). Reject teaching the shared
helper about type-less tables (~30 call sites).

**Compat tradeoff.** None — zero result rows change; audiobook/podcast libraries are
already excluded upstream by `isCompatHiddenLibraryType`.

**Files.** `internal/catalog/episode_catalog_entries_executor.go` (single file).
No migration. Suggested subject: `fix(catalog): drop media-type exclusion on episode_catalog_entries section filter`.

---

## Issue 4 — `/Items` played-history (P1, 14–15s)

**Root cause.** `handlePlayedItems` → `loadProgressPage(... "completed", typeSet, libraryID)`.
With `includeItemTypes` + `parentId`, the fast-path gate (`handlers_items.go:2132`)
fails; the scan-from-zero loop reads the full completed set in 60-row batches and
discovers type+library matches only after hydrating each batch (type check `:2204`,
library drop implicit in `hydrateProgressItems`). The store query
(`internal/userstore/pgstore/progress.go:394-410`) has **no JOIN to media_items /
media_item_libraries and no type column** — pure in-memory post-filter, O(total history).

**Fix (SQL push-down).** Add `ListProgressFiltered(profileID, status, types, libraryID,
limit, offset)` to the store: reuse the `completed`-branch SQL and AND-in EXISTS
subqueries (separate movie branch via `media_items`, episode branch via
`episodes → media_items`), `ORDER BY updated_at DESC LIMIT/OFFSET`. Serves off existing
partial index `idx_uwp_profile_completed` (`102_catalog_perf_indexes.sql:38-40`); EXISTS
hit `idx_item_libraries_content` + PKs. Route `handlePlayedItems` through it; keep the
in-memory access re-check as a correctness backstop.

**Compat tradeoff.** None — identical result set/ordering (effectively 100% fidelity).
`enableTotalRecordCount=true` would need a filtered COUNT; observed traffic sends
`false`, so the hot path needs no count.

**Files.** `internal/userstore/pgstore/progress.go`, `internal/userstore` interface +
`storetest/suite.go`, `internal/jellycompat/userdata_direct.go`,
`internal/jellycompat/handlers_items.go`, jellycompat test mocks. No migration.

**Est. latency.** <100ms (indexed scan + EXISTS over ~20 rows, single hydration batch).

---

## Issue 5 — `/Items/Latest` multi-library (P1, 2s)

**Root cause (EXPLAIN-proven).** No-parentId ⇒ `LibraryID=0` ⇒
`singleLibraryNoDedup=false` (`internal/catalog/browse.go:315`) ⇒ `recently_added`
branch uses `MIN(mil.first_seen_at)` + `GROUP BY` (~40 cols) (`browse.go:393-397`).
The migration-107 index leads with `media_folder_id`, so it only helps single-library.
Live EXPLAIN ANALYZE (7 libs, ~183k items): single-library = **1.5ms** index-only walk;
multi-library = **373–726ms** parallel scan of all 182,994 rows → HashAggregate over
182k groups → top-N heapsort. Wide projection + cold cache + Go overlay → observed ~2s.

**Fix (Option A — per-library walk + k-way merge, no DDL).** This user has 7 libraries;
each single-library query is the proven 1.5ms path. In `BrowseItems`
(`content_direct.go:318`), when `Sort=="recently_added" && LibraryID==0`, resolve
accessible library IDs (existing pattern, `content_direct.go:275-277`), call
`BrowsePage` per library with `LibraryID=id` (trips `singleLibraryNoDedup`), then merge
the already-sorted slices by `AddedAt` DESC, dedup on `content_id` keeping earliest
`first_seen_at` (preserves `MIN` semantics), take `limit`. Put the merge in a testable
`internal/catalog/browse.go` helper.

**Option B (alternative, needs DDL + verification).**
`CREATE INDEX CONCURRENTLY idx_mil_seen_content ON media_item_libraries (first_seen_at DESC, content_id, media_folder_id)`
then drop the GROUP BY and do top-N index scan with app-side dedup. **Unverified** — DDL
was correctly blocked on prod during review. Use only with a controlled EXPLAIN window.

**Compat tradeoff.** Cross-library dedup becomes app-side/approximate; earliest
`first_seen_at` on dedup preserves today's semantics. Pagination `offset>0` is a
follow-up (hot path is always `offset=0, limit≈25`).

**Files.** `internal/jellycompat/content_direct.go` (primary), `internal/catalog/browse.go`
(merge helper) + tests. No migration for Option A.

**Est. latency.** 10–40ms.

---

## Issue 6 — per-item `GetItemDetail` N+1 (P2, partially confirmed)

**Verdict correction.** The N+1 loops are real and uncapped at
`handlers_items.go:1002-1015`, `:1736-1751`, `:1955-1962`, `:1997-2006`, and
`GetItemDetail` fires ~10+ queries/item (claim under-counted). **But** the log does not
attribute the worst latency to this: the dominant `/Items/Latest` pattern (503/515)
requests `MediaSourceCount` (list-served), not `MediaSources`, so
`requestedFieldsNeedDetail` is false and the loop never runs — yet those still avg 436ms
(that's Issue 5, not N+1). NextUp/Resume detail-upgrades are already capped at
`maxDetailUpgrades=100` and page-sliced. The Resume 25–50s is Issue 1's full-scan, not N+1.
`LocalizeItemModel` singular (`content_direct.go:384`,`:498`) is a no-op today (no
presentation language configured).

**Genuine N+1 worth fixing.** Latest + browse **when `MediaSources` is requested**
(`/Items/Latest`+MediaSources avg 1763ms n=6; `/Items` browse+MediaSources avg 799ms n=19).

**Fix.** Replace the four per-item loops with type-grouped batch fetches: episodes →
reuse `GetEpisodeDetailsForSeries` (`catalog/detail.go:964`) grouped by series; movies/
series → add `DetailService.GetItemDetailsByIDs(ctx, ids, filter)` batching
`EnsureAccessible`, `fetchCredits`, `GetByContentID`, `LocalizeItemModels` (none exists
today). Optionally swap singular→batch localize (P3, low value now).

**Compat tradeoff.** Do **not** omit MediaSources/MediaStreams on rows — load-bearing
for Infuse/SenPlayer/Wholphin (comments at `handlers_items.go:1514`, `2247`); serve them
from the batch. Standard clients refetch via `/Items/{id}/PlaybackInfo` on play.

**Files.** `internal/jellycompat/handlers_items.go`, `internal/catalog/detail.go`,
`internal/jellycompat/content_direct.go` (P3). No migration.

**Est. latency.** ~100–200ms (from 0.8–1.8s) for detail-field pages.

---

## Suggested implementation order & commit grouping

Order by impact-per-risk. Each fix is its own concern; commits split by fix (and within
a fix, by file where the diff is large), per the user's instruction.

1. **Issue 3** — single file, zero behavior risk, restores blank rails. Easy first.
2. **Issue 1** — biggest UX win (91s → <100ms), reuses native section fetcher.
3. **Issue 2** — soap-opera 13s → <50ms, reuses existing index.
4. **Issue 4** — 15s → <100ms, store push-down.
5. **Issue 5** — 2s → <40ms, per-library merge.
6. **Issue 6 (P2)** — Latest/browse + MediaSources batch; lowest priority.

Not in scope here but noted from logs: 1,496 `person refresh worker: refresh failed`
(SQLSTATE 23505 retry loop, never bumps `updated_at`) — background churn, separate fix
(`internal/metadata/person_refresh.go:169`, `internal/worker/person_refresh.go:129`).
