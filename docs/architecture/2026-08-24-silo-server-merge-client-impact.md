# Silo server merge impact on Vondel clients

Status: handoff for implementation  
Audited: 2026-08-24

## Revisions examined

- Silo server `upstream/main`: `820eef7792d24e2b5af789e448906481bd560296`
- Vondel server `origin/main`: `7a1ab884` at documentation start
- Vondel Android `origin/main`: `52436d3271a0c656f9b77b78aeb84f9116cd6a6c`
- Vondel Apple `origin/main`: `dcbfc080c7b272961763d4632cfddf9970fb73f5`
- Vondel client contracts `origin/main`: `25ee952aa51acdea6c06546d8c3bad668bf7f566`

The only new first-parent Silo merges after the Vondel server's prior Silo sync are:

1. `6f8db023` / PR #734 — optimistic H.264 remux copy-safety and plan invalidation.
2. `820eef77` / PR #737 — client-managed original HDR and selected-audio delivery claims.

Neither merge is present in Vondel server, Android, Apple, or the shared contracts at the revisions above.

## Executive conclusion

The changes are backward-compatible and do not require an emergency client rollback. Existing Vondel clients omit the new feature and claims, so Silo retains its legacy behavior. The primary user-visible risk is Android: when Silo invalidates an optimistic remux, Media3 can surface a non-recoverable source failure and require manual Retry. Apple already performs one bounded replan for a failed load, although AVFoundation classification means seamless recovery is not guaranteed for every termination.

The HDR and selected-audio additions are optional optimizations. They must not be advertised until the real playback executor can honor them. Adding the strings without executor support could select an original file the client cannot present correctly or could play the wrong audio language.

## PR #734: `plan_invalidated_v1`

Silo can now issue an H.264 remux before its multiple-PPS copy-safety scan finishes. A later positive unsafe verdict is persisted and the active plan is withdrawn.

A capable client advertises `plan_invalidated_v1` and maintains the session control WebSocket. On a `plan_invalidated` command it must:

1. immediately acknowledge the command;
2. verify that `payload.plan_id` is the active plan;
3. issue a `failure_recovery` replan with the invalidated attempt key excluded;
4. install the accepted replacement without creating a second playback owner; and
5. report command completion before the deadline.

Without the feature, live socket, or completed result, Silo stops the session. This is the intentional legacy path: the next plan uses the persisted unsafe verdict and selects a safe transcode.

### Current client behavior

- Android and Apple do not define or advertise `plan_invalidated_v1`.
- The shared contracts contain no command envelope or payload for it.
- Android filters unknown server feature tokens, so the added capability response is decode-safe.
- Apple selects only known feature tokens, so the added capability is inert.
- Android HTTP/bad-source failures are generally non-recoverable in `Media3PlaybackEngine.failureFor`; `PlaybackSessionController.handleFailure` does not automatically mint a fresh plan for that legacy stop.
- Apple `WatchExperience.recover` maps `.failedToLoad` to one `.streamRejected` replan at the observed position. Network-classified failures remain outside that recovery.
- Watch Together inherits the same local player behavior. The room coordinator must remain capability-blind; the activation-owned playback source/session should own invalidation recovery.

### Required implementation

1. Add the feature token, command envelope, required `reason` and `plan_id`, ack/result states, and conformance fixtures to Vondel client contracts.
2. Add one activation-owned realtime session-control connection to each client.
3. Make advertising conditional on the complete handler and socket lifecycle being mounted.
4. Replan once using the invalidated plan identity and attempt key; reject stale commands.
5. Preserve position, selected tracks, output-route generation, authentication state, and Watch Together ownership.
6. Add Android phone/TV and Apple iOS/tvOS tests for exact-plan, stale-plan, timeout, cancellation, replacement failure, and room playback.

Do not blanket-treat every HTTP 404 as plan invalidation. Without the typed command, a 404 is ambiguous.

## PR #737: validated original-delivery claims

Silo adds two optional values under
`client_playback_context.deliveries.original_http.validated_claims`:

- `client_managed_dynamic_range_v1`: the original-file executor accepts the declared HDR/Dolby Vision source and resolves presentation against the live output.
- `client_selected_audio_track_v1`: the executor maps `selected_tracks.audio.index` to the probed source inventory and activates that stream.

Without these claims, existing output gating and default-audio/remux behavior are unchanged. Progressive and HLS deliveries remain output-gated even when the managed-dynamic-range claim exists.

### Current client behavior

- Android's `WireDeliveryCapability` has no `validated_claims`.
- Apple's playback request `Context.Delivery` has no `validatedClaims`.
- Android decodes response `selected_tracks` as raw JSON but drops the audio ordinal before engine load.
- Apple does not decode response `selected_tracks` in `PlaybackPlan`.
- Apple already sends runtime HDR facts, so it continues receiving routes allowed by the old output-aware rules.
- Android does not yet project equivalent output HDR details and therefore remains more conservative.

### Required implementation

1. Decode and validate `selected_tracks` into the shared plan model.
2. Apply the server-selected audio ordinal in the actual engine before claiming support.
3. Preserve the selection through replan, Next Up, downloads, and Watch Together.
4. Add delivery-scoped `validated_claims` serialization.
5. Advertise each claim independently and only from runtime-proven executor support.
6. Prove managed HDR against the actual Apple/Android executor and live output route. Do not add a platform-wide hardcoded capability declaration.
7. Add negative fixtures proving omitted/unknown facts do not create a claim.

## Vondel server integration

Integrate the two Silo changes semantically rather than blindly cherry-picking. The upstream changes overlap Vondel's header-authenticated media and proxy-policy work in:

- playback v3 handlers and protocol models;
- session WebSocket and stream handlers;
- router and session lifecycle;
- transcode management and scanner code;
- protocol fixtures/docs; and
- the web playback session.

PR #734's database migration that persists the multiple-PPS verdict is mandatory. Preserve Vondel's negotiated header-auth feature set, readiness token, authorized-origin policy, and sticky attempt features while adding plan invalidation. Keep the new client claims optional so old Vondel and Silo clients remain compatible.

## Recommended delivery order

1. Merge the server persistence/optimistic-remux behavior with the migration and legacy stop path.
2. Extend shared contracts for plan invalidation.
3. Implement and gate Android invalidation recovery first.
4. Implement Apple realtime invalidation for deterministic seamless recovery.
5. Exercise all four Watch Together paths.
6. Implement selected-track decoding/application.
7. Add Apple managed-HDR and selected-audio claims only after executor proof.
8. Add Android claims only from runtime Media3 evidence; never from handwritten device lists.

## Release posture

- Safe to run current Vondel clients against the new Silo server behavior.
- Expect possible manual Retry on Android after a late unsafe-remux verdict.
- Do not advertise any new token or claim until its complete execution path is present.
- Server and clients can be deployed independently because every new behavior is opt-in or has an explicit legacy fallback.
