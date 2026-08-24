# Silo Playback Compatibility Rollout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate the latest additive Silo playback contracts into Vondel server and all four native clients without weakening Vondel authentication, fabricating device capabilities, or breaking legacy clients.

**Architecture:** The server owns persisted H.264 copy-safety and typed plan invalidation. Shared contracts define the command and optional delivery claims. Each authenticated client activation owns one immutable runtime capability snapshot, playback source, replan path, and session-control socket; Watch Together reuses that bound playback owner and remains capability-blind.

**Tech Stack:** Go/PostgreSQL/WebSocket/React on server; Kotlin/Media3/Compose on Android; Swift/AVFoundation/SwiftUI on Apple; JSON conformance fixtures in vondel-client-contracts.

**Spec:** `docs/architecture/2026-08-24-silo-server-merge-client-impact.md`

## Global Constraints

- Preserve additive-only `/api/v1` compatibility and legacy session-stop behavior.
- Preserve Vondel header-auth readiness, authorized-origin policy, and sticky negotiated features.
- Never advertise a feature or validated claim until its complete runtime executor is mounted.
- Never infer capability from a handwritten device/model list; use activation-frozen runtime evidence.
- Keep Watch Together coordinators capability-blind and reuse the activation-owned playback path.
- Do not treat generic HTTP 404 as typed plan invalidation.

---

### Task 1: Server copy-safety and additive protocol

**Files:**
- Modify: `internal/playback/protocol_v3.go`
- Modify: `internal/playback/session.go`
- Modify: `internal/api/handlers/session_ws.go`
- Modify: `internal/api/handlers/playback*.go`
- Modify: `internal/scanner/*.go`
- Create: `migrations/sql/20260823182731_persist_multiple_pps_verdict.sql`
- Test: `internal/playback/*_test.go`, `internal/api/handlers/*_test.go`, `internal/scanner/*_test.go`

**Interfaces:**
- Produces: `plan_invalidated_v1`, `plan_invalidated`, persisted multiple-PPS verdict, and optional original-delivery claims.
- Preserves: legacy session stop when the feature/socket/completed result is absent.

- [ ] Add a compile-failing protocol test for the new feature and claim constants.
- [ ] Run the focused test and confirm failure is caused by missing symbols.
- [ ] Port Silo PR #734 and #737 semantically, resolving overlap in favor of Vondel header-auth/proxy rules.
- [ ] Run focused playback, API-handler, scanner, and migration tests.
- [ ] Run `go test ./internal/playback ./internal/api/handlers ./internal/scanner` and commit.

### Task 2: Shared invalidation and selected-track contracts

**Files:**
- Modify: protocol schemas and generated fixtures under the contracts repository.
- Test: contract conformance and generation tests.

**Interfaces:**
- Produces: strict command envelope, required `reason`/`plan_id`, ack/result states, selected audio ordinal, and delivery `validated_claims`.
- Consumes: server Task 1 wire names exactly.

- [ ] Add failing fixtures for exact, stale, duplicate, rejected, timed-out, and malformed invalidation commands.
- [ ] Add failing fixtures for selected audio and absent/unknown validated claims.
- [ ] Implement the minimal additive schema and regenerate checked-in artifacts.
- [ ] Run contract conformance tests and commit.

### Task 3: Android activation-owned invalidation recovery

**Files:**
- Modify: `watch/src/main/kotlin/media/vondel/watch/net/PlaybackClientFeatures.kt`
- Create: focused session-control socket and invalidation command types under `watch/.../net/`
- Modify: `watch/.../playback/PlaybackSessionController.kt`
- Modify: phone/TV activation composition roots.
- Test: focused watch/network/controller tests and both app compiles.

**Interfaces:**
- Consumes: Task 2 command contract.
- Produces: conditional feature advertisement and one bounded replacement-plan handoff.

- [ ] Add failing tests for feature gating, active/stale plan identity, ack-before-work, cancellation, deadline, replacement failure, and duplicate commands.
- [ ] Implement one activation-owned control connection and bounded failure-recovery replan.
- [ ] Preserve playback position, track selection, output generation, auth generation, and controller ownership.
- [ ] Verify Android phone and TV focused suites and commit.

### Task 4: Apple activation-owned invalidation recovery

**Files:**
- Modify: `Sources/VondelCore/Watch/PlaybackClientFeatures.swift`
- Create: focused session-control command transport under `Sources/VondelCore/Watch/`
- Modify: `Sources/VondelNavigation/Watch/WatchExperience.swift`
- Test: VondelCore and WatchExperience focused Swift tests.

**Interfaces:**
- Consumes: Task 2 command contract.
- Produces: deterministic invalidation recovery independent of AVFoundation error categorization.

- [ ] Add failing command lifecycle and activation ownership tests.
- [ ] Implement exact-plan matching, immediate ack, bounded replan, completion result, and teardown.
- [ ] Advertise only while the complete handler is available.
- [ ] Run focused Swift tests and both platform builds; commit.

### Task 5: Watch Together integration on all four clients

**Files:**
- Modify: existing Android and Apple room playback composition only where injection is required.
- Test: Android room coordinator/player tests and Apple Watch Together tests.

**Interfaces:**
- Reuses: Tasks 3 and 4 activation-owned sources/controllers.
- Preserves: capability-blind room coordinator and single playback owner.

- [ ] Add failing room invalidation tests for host/guest, stale room, cancellation, and position preservation.
- [ ] Thread the existing activation playback owner into room playback; add no second socket or capability source.
- [ ] Run four-client focused room tests and commit per repository.

### Task 6: Selected audio execution

**Files:**
- Modify: Android and Apple decoded/validated playback plan models.
- Modify: Media3 and Aether/AVFoundation load specifications.
- Test: plan decode, ordinal validation, engine selection, replan, Next Up, download, and room tests.

**Interfaces:**
- Produces: truthful `client_selected_audio_track_v1` support.
- Rejects: absent, out-of-range, or inventory-mismatched ordinals with typed bounded recovery.

- [ ] Add failing decode and engine-selection tests.
- [ ] Carry the ordinal through validation and activate the exact source stream.
- [ ] Preserve selection across all replacement paths.
- [ ] Add the claim only when the executor proof is present; run focused suites and commit.

### Task 7: Managed dynamic range claims

**Files:**
- Modify: delivery capability projection on Android and Apple.
- Modify: output-route activation generation where needed.
- Test: runtime evidence, live route changes, omitted facts, and typed fallback tests.

**Interfaces:**
- Produces: conditional `client_managed_dynamic_range_v1` only for a proven original-file executor.

- [ ] Add failing tests proving platform/model names and unknown output facts cannot mint the claim.
- [ ] Prove the actual executor owns presentation against the live route.
- [ ] Serialize the delivery-scoped claim from the frozen activation snapshot.
- [ ] Run focused playback and app compile gates; commit.

### Task 8: Cross-repository release closure

**Files:**
- Modify: rollout reports and compatibility ledgers in all four repositories.

- [ ] Verify server migration, legacy-client stop, capable-client seamless replacement, and optional-claim behavior.
- [ ] Run focused release gates in each repository.
- [ ] Record exact commands, counts, limitations, and commit hashes.
- [ ] Push each completed repository to `origin/main` in dependency order: server, contracts, Android, Apple.
