## Selected upstream account per client API key

### Goal

Add a management-only control in the API key quota status area that lets each client API key optionally select exactly one upstream account.

Required semantics:

- Blank means existing mode with current routing behavior.
- At most one upstream account can be selected per client API key.
- If a selected upstream account is disabled, missing, or otherwise not usable for the request, inference must fail instead of falling back.
- The control must live in the API key quota status area in `internal/api/assets/quota.html`.

### Design choice

Use the existing `client-api-key-policies` persistence path and add one optional field:

- `selected-auth-index`

Persist the user-facing `auth_index` in config, then resolve `auth_index -> auth.ID` at request time and reuse the existing runtime pinning path.

Why this shape:

- `auth_index` is already exposed to the quota UI via `auth_files`.
- Runtime selection already supports a pinned auth path.
- This keeps the diff local to existing quota UI, policy storage, and request execution seams.

---

## Phase 1 - Config model and validation

### Files

- `internal/config/sdk_config.go`
- `internal/config/client_api_key_policies_test.go`

### Changes

1. Add an optional field to `ClientAPIKeyPolicy`:
   - `SelectedAuthIndex string \`yaml:"selected-auth-index,omitempty" json:"selected-auth-index,omitempty"\``
2. Update `NormalizeClientAPIKeyPolicies()` to trim the field.
3. Update `ValidateClientAPIKeyPolicies()` with these rules:
   - empty is allowed
   - only a single string value is allowed
   - reject invalid control characters
   - keep validation syntactic here; runtime existence checks happen later

### Notes

- Do not introduce a list field.
- Do not persist internal runtime auth IDs in config.

### Verification

- Add normalize test for trimming `selected-auth-index`.
- Add validation test for invalid control characters.
- Add config round-trip coverage for the new field.

---

## Phase 2 - Management API persistence

### Files

- `internal/api/handlers/management/client_api_key_policies.go`
- `internal/api/handlers/management/client_api_key_policies_test.go`

### Changes

1. Extend PATCH request handling so `selected-auth-index` can be created, updated, or cleared.
2. Ensure GET responses include the new field.
3. Update `clientAPIKeyPolicyHasMeaningfulConfig()` so a policy row with only `selected-auth-index` still counts as meaningful.
4. Blank `selected-auth-index` must clear the restriction.
5. If clearing the field leaves no alias/quota/concurrency/restriction settings, preserve current behavior and remove the policy row.

### Notes

- Reuse the existing save path and comment-preserving config write flow.
- Keep PATCH and PUT behavior aligned with current policy semantics.

### Verification

- Test creating a selected-auth-only policy.
- Test updating an existing policy to set the field.
- Test clearing the field with blank input.
- Test removal of the row when the restriction was the last meaningful field.

---

## Phase 3 - Quota status payload for UI rendering

### Files

- `internal/api/handlers/management/quota_status.go`
- `internal/api/handlers/management/quota_status_test.go`
- `internal/api/handlers/management/auth_files.go`

### Changes

1. Reuse `auth_files` as the selectable account source.
2. Enrich each `client_api_key_quotas` row with selected-auth metadata joined by `auth_index`:
   - `selected_auth_index`
   - `selected_auth_label`
   - `selected_auth_provider`
   - `selected_auth_disabled`
   - `selected_auth_missing`
3. Keep viewer behavior unchanged:
   - management users can see/edit
   - self viewers remain read-only and should not gain new account-management capability

### Notes

- The UI already has the auth/account list and disabled state from `auth_files`.
- Joining here keeps the frontend simple and avoids duplicate lookup logic.

### Verification

- Test that a quota row includes selected auth metadata when configured.
- Test that a disabled selected auth is marked disabled.
- Test that a missing selected auth is marked missing.

---

## Phase 4 - Quota UI control in API key quota status area

### Files

- `internal/api/assets/quota.html`

### Changes

1. In `renderQuotas()`, add a management-only single-select control on each API key quota card.
2. Options source:
   - blank option first: existing mode
   - downstream options from `auth_files`, keyed by `auth_index`
3. Show current status clearly:
   - existing mode when blank
   - selected account label when configured
   - warning badge if selected auth is disabled or missing
4. Save by reusing the existing PATCH call to `/v0/management/client-api-key-policies`.
5. Keep this control on the quota card, not in the separate auth panel.

### UI behavior requirements

- Only one account may be selected.
- Blank resets to existing mode.
- If an account is selected but currently disabled, the UI should show a warning immediately.
- Keep the copy simple and consistent with the rest of the page.

### Verification

- Confirm the select is visible only when `viewer.can_manage` is true.
- Confirm blank saves and reloads as existing mode.
- Confirm selected value reloads correctly after refresh.
- Confirm disabled/missing warnings render correctly.

---

## Phase 5 - Runtime enforcement using existing pinning seam

### Files

- `sdk/api/handlers/handlers.go`
- `internal/api/server.go`
- `sdk/cliproxy/auth/conductor.go`
- `sdk/cliproxy/auth/scheduler.go`
- `sdk/cliproxy/auth/selector.go`

### Changes

1. Add one shared helper in the SDK handler layer used by both streaming and non-streaming execution paths.
2. Read the authenticated client API key from request context.
3. Look up the matching `ClientAPIKeyPolicy`.
4. If `selected-auth-index` is blank:
   - do nothing
   - preserve existing routing
5. If `selected-auth-index` is set:
   - resolve `auth_index -> auth.ID` from the live auth manager list
   - inject the resolved auth ID into execution metadata via the existing pinned-auth path
6. Fail closed when the selected auth is not usable:
   - disabled
   - missing
   - unavailable
   - cannot serve the requested model/provider
7. Do not silently fall back to unrestricted routing.

### Error behavior

- Return a deterministic error explaining that the client API key is restricted to a specific upstream account and that the selected account is not available.
- Prefer one consistent failure mode across stream and non-stream paths.

### Notes

- The runtime already supports pinned auth execution.
- This phase should reuse that seam instead of adding new scheduler behavior.

### Verification

- Test blank restriction: no pin is applied.
- Test valid selected auth: pin is applied.
- Test disabled selected auth: request fails.
- Test missing selected auth: request fails.
- Test selected auth that cannot serve the requested model/provider: request fails.
- Test stream and non-stream paths separately.

---

## Phase 6 - Edge-case review and cleanup

### Focus

1. Deleting an auth file after it was selected by one or more client API keys.
2. Disabling an auth through management after it was selected.
3. Config reload behavior after manual YAML edits.
4. Viewer mode isolation.
5. Quota page rendering when auth/account data is temporarily stale.

### Verification

- Confirm no management-only edit controls leak into self-view.
- Confirm stale selected-auth values render as warnings instead of breaking the page.
- Confirm config reload keeps behavior consistent.

---

## Verification plan

### Automated

Run targeted tests while implementing each phase, then finish with:

```bash
go test ./...
go build -o test-output ./cmd/server && rm test-output
```

If local Go is unavailable, use the documented Docker fallback from `AGENTS.md`.

### Manual

1. Open `quota.html` as a management user.
2. Pick one client API key.
3. Set selected account to blank and verify legacy routing remains unchanged.
4. Set selected account to one active auth and verify requests route only through that account.
5. Disable the selected auth from the auth section and verify inference fails.
6. Re-enable it and verify inference recovers.
7. Delete or invalidate the selected auth and verify the quota card shows a missing warning and inference fails.
8. Verify self-view cannot edit the setting.

### Success criteria

- The quota card supports zero-or-one selected upstream account.
- Blank preserves existing behavior.
- Selected disabled or missing auth fails inference without fallback.
- Stream and non-stream execution follow the same restriction.
- The UI surfaces current state clearly and stays management-only.

---

## Recommended implementation order

1. `internal/config/sdk_config.go`
2. `internal/api/handlers/management/client_api_key_policies.go`
3. `internal/api/handlers/management/quota_status.go`
4. `internal/api/assets/quota.html`
5. `sdk/api/handlers/handlers.go`
6. tests for each phase
7. full verification

---

## Claude Code compatibility plan with archive/LiteLLM reference

### Goal

Make this repo a robust endpoint for Anthropic Claude Code clients while keeping the runtime hot path inside CLIProxyAPI's existing Claude ingress, auth manager, executor, and translator stack.

Use `archive/LiteLLM` as a pinned protocol reference and validation oracle, not as a required request-time dependency for the main implementation.

### Architectural decision

Prefer a native implementation in this repo instead of a LiteLLM sidecar-first design.

Why:

- `internal/api/server.go` already exposes `POST /v1/messages` and `POST /v1/messages/count_tokens`.
- `sdk/api/handlers/claude/code_handlers.go` already routes Claude-style requests through the shared auth-manager execution path.
- `internal/runtime/executor/claude_executor.go` already handles Anthropic auth, base URL selection, headers, streaming, token counting, and body normalization.
- `internal/translator/openai/claude/*` already provides the main bridge seam for future Claude-to-OpenAI-family translation work.
- Adding LiteLLM into the runtime path would duplicate routing/auth/observability concerns and increase protocol drift risk.

### High-level scope

1. Tighten Claude Code wire compatibility on the existing Anthropic-compatible surface.
2. Add explicit configuration and testing for Claude Code behavior and model aliasing.
3. Build a fixture-driven validation matrix using LiteLLM under `archive/LiteLLM` as a comparison target.
4. Only after Anthropic-native compatibility is stable, evaluate GPT/OpenAI-family backends behind Claude-style ingress as a separate higher-risk track.

### Non-goals

- Do not make LiteLLM a required runtime dependency in the first implementation.
- Do not replace existing management, routing, or quota behavior with LiteLLM equivalents.
- Do not broaden this work into generic translator refactors outside the Claude Code compatibility path.
- Do not treat GPT/OpenAI-family backend support as part of the first acceptance bar unless native Claude Code compatibility is already passing.

---

## Phase 0 - Reference workspace and protocol target definition

### Files / paths

- `archive/LiteLLM/` (new clone target, not yet present)
- `TODO.md`
- optional future note file under `docs/`

### Changes

1. Create `archive/` if missing.
2. Clone LiteLLM into `archive/LiteLLM` and pin a known-safe revision.
3. Record the exact revision/SHA being used for validation.
4. Document the protocol contract we are targeting:
   - `POST /v1/messages`
   - `POST /v1/messages/count_tokens`
   - `anthropic-beta` preservation
   - `anthropic-version` preservation
   - `X-Claude-Code-Session-Id` preservation
   - auth support via `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN`
5. Explicitly state that LiteLLM is a comparison oracle, not the main request path.

### Notes

- Avoid compromised LiteLLM releases.
- Pin by commit/tag so future compatibility debugging is reproducible.
- Keep all LiteLLM-specific work isolated under `archive/`.

### Verification

- Confirm `archive/LiteLLM` exists and the pinned revision is recorded.
- Confirm the target protocol contract is written down before implementation starts.

---

## Phase 1 - Gap audit of the current Claude Code ingress

### Files

- `internal/api/server.go`
- `sdk/api/handlers/claude/code_handlers.go`
- `sdk/api/handlers/handlers.go`
- `internal/runtime/executor/claude_executor.go`
- `sdk/api/handlers/header_filter.go`
- `test/claude_code_compatibility_sentinel_test.go`

### Changes

1. Audit the current `/v1/messages` and `/v1/messages/count_tokens` behavior against Claude Code expectations.
2. Enumerate which request headers are forwarded, defaulted, normalized, or stripped.
3. Enumerate which response headers/events are passed through or rewritten.
4. Confirm how session identity is derived and how it maps to session affinity.
5. Identify all current Claude Code specific behavior already implemented, including:
   - session header handling
   - header filtering to avoid gateway fingerprinting
   - OAuth-specific tool name shaping
   - cache-control normalization
   - count token request path

### Deliverable

Produce a concrete gap list with three buckets:

- already compatible
- compatible but under-tested
- incompatible or ambiguous

### Verification

- The gap list names exact functions/files, not generic areas.
- No implementation begins until the missing/ambiguous items are enumerated.

---

## Phase 2 - Explicit Claude Code compatibility configuration

### Files

- `internal/config/config.go`
- `config.example.yaml`
- related config tests

### Changes

1. Add or formalize config fields for Claude Code compatibility behavior where currently implicit.
2. Prefer opt-in or clearly documented defaults for:
   - session affinity behavior
   - Claude header defaults
   - device-profile stabilization
   - model aliasing intended for Claude Code clients
   - strict header passthrough / preservation where applicable
3. Ensure model alias configuration can cleanly map Claude Code-visible model names onto local runtime models without changing the client-facing contract.
4. Document safe defaults in `config.example.yaml`.

### Notes

- Reuse existing config structures before adding new ones.
- Keep the operator story simple: endpoint + auth + aliasing should be enough to get started.
- Avoid mixing Claude-native compatibility knobs with the later GPT/OpenAI translation track unless necessary.

### Verification

- Config round-trip tests cover any new fields.
- Invalid config values fail clearly.
- `config.example.yaml` shows a minimal Claude Code-compatible setup.

---

## Phase 3 - Request-path fidelity hardening

### Files

- `sdk/api/handlers/claude/code_handlers.go`
- `sdk/api/handlers/handlers.go`
- `internal/runtime/executor/claude_executor.go`
- `sdk/api/handlers/header_filter.go`

### Changes

1. Harden inbound request preservation for Claude Code relevant headers and metadata.
2. Ensure `anthropic-beta` and `anthropic-version` are preserved or reconstructed correctly.
3. Ensure `X-Claude-Code-Session-Id` is accepted and forwarded consistently.
4. Verify streaming and non-streaming paths share the same header semantics.
5. Verify count token requests follow the same normalization constraints as messages requests.
6. Keep gateway-fingerprint stripping behavior intentional and test-backed.

### Notes

- This phase is about wire fidelity, not large architecture changes.
- Prefer small, local changes near the current Claude handler/executor seam.

### Verification

- Add handler/executor tests for header preservation.
- Add tests covering stream, non-stream, and count-tokens request flows.
- Confirm no required Claude Code headers are accidentally dropped.

---

## Phase 4 - Response/event fidelity hardening

### Files

- `internal/runtime/executor/claude_executor.go`
- `sdk/api/handlers/claude/code_handlers.go`
- `test/claude_code_compatibility_sentinel_test.go`
- new fixture files under `test/testdata/claude_code_sentinels/` if needed

### Changes

1. Audit streaming event shapes against Claude Code sentinel expectations.
2. Add or extend fixture-driven tests for known Claude Code event types and subtypes.
3. Verify tool progress, tool summary, session state, and control request event structures remain stable.
4. Tighten any event rewriting that could break Claude Code UX or tool orchestration.

### Notes

- The existing sentinel test file is the natural anchor for this phase.
- Prefer fixture additions over brittle inline event assertions.

### Verification

- Sentinel tests cover each important event family.
- Streaming tests verify ordering and required fields.
- Failures are precise enough to diagnose protocol regressions quickly.

---

## Phase 5 - Claude Code model alias and routing contract

### Files

- `internal/config/config.go`
- `internal/registry/model_definitions.go`
- `internal/registry/model_updater.go`
- `sdk/cliproxy/auth/oauth_model_alias.go`
- model alias tests

### Changes

1. Define the supported client-visible model naming strategy for Claude Code.
2. Ensure operators can map Claude Code model names to available local backends using existing alias/mapping seams.
3. Verify model listing and model execution resolve aliases consistently.
4. Ensure `/v1/models` behavior remains coherent for Claude-oriented clients.
5. Separate native Claude-model aliasing from future non-Anthropic backend translation aliasing.

### Notes

- This phase matters because route compatibility alone is not enough if model names do not line up.
- Prefer existing alias systems over adding a second independent mapping layer.

### Verification

- Add tests for alias visibility in model listing.
- Add tests for alias execution routing.
- Add tests for invalid or ambiguous aliases.

---

## Phase 6 - Native Claude Code acceptance matrix

### Files

- Claude handler/executor tests
- `test/claude_code_compatibility_sentinel_test.go`
- any new focused integration tests under `test/`

### Scenarios

1. `POST /v1/messages` non-streaming request succeeds.
2. `POST /v1/messages` streaming request succeeds.
3. `POST /v1/messages/count_tokens` succeeds.
4. Session affinity preserves upstream account consistency across a Claude Code session.
5. Required Anthropic headers are preserved.
6. Tool-use requests and tool-result flows preserve expected shapes.
7. Error responses remain Claude-compatible enough for the client to behave predictably.

### Verification

- Add focused tests for each scenario.
- Add at least one end-to-end style integration test covering a full Claude-like request path.
- Mark this phase as the minimum acceptance bar before any GPT/OpenAI backend experimentation.

---

## Phase 7 - archive/LiteLLM comparison harness

### Files / paths

- `archive/LiteLLM/`
- new comparison notes or scripts under `test/` or `docs/`

### Changes

1. Stand up LiteLLM locally in a controlled test configuration.
2. Use identical Claude Code-style requests against:
   - CLIProxyAPI native Claude endpoint
   - LiteLLM Anthropic-compatible endpoint
3. Compare:
   - status codes
   - required headers
   - count token behavior
   - stream event structure
   - tool-use/control event behavior where possible
4. Record any intentional differences versus regressions.

### Notes

- The goal is not identical implementation internals.
- The goal is protocol confidence and a debugging oracle when behavior differs.

### Verification

- A written comparison matrix exists.
- Any differences are classified as acceptable, bug, or follow-up item.

---

## Phase 8 - GPT/OpenAI-family backend feasibility track

### Status

Do not start this phase until Phases 1 through 7 are passing.

### Files

- `internal/translator/openai/claude/*`
- `sdk/api/handlers/handlers.go`
- `internal/runtime/executor/claude_executor.go`
- relevant OpenAI compatibility config/executor files

### Goal

Evaluate whether Claude Code requests can be translated reliably onto GPT/OpenAI-family backends while keeping the Claude Code client contract stable.

### Changes

1. Limit the first experiment to a narrow supported feature subset.
2. Define explicit non-goals for the first translation attempt, for example unsupported tool schemas or partial event fidelity.
3. Reuse the existing Claude-to-OpenAI translator seam instead of adding a parallel translator stack.
4. Add a provider capability matrix describing which Claude Code features survive translation.

### Risks

- Anthropic-to-non-Anthropic translation is the most fragile path.
- Tool-use, control events, and count-token fidelity may not map cleanly.
- Passing basic messages does not imply true Claude Code compatibility.

### Verification

- Separate native Anthropic-compatible tests from translated OpenAI-family tests.
- Document unsupported behaviors explicitly.
- Do not regress native Claude compatibility while exploring this track.

---

## Risks and watchpoints

1. **False confidence from route existence**
   - Having `/v1/messages` is not enough if event shapes or header behavior drift.
2. **Header loss**
   - `anthropic-beta`, `anthropic-version`, or session headers may be silently lost in one path but not another.
3. **Streaming drift**
   - Small event-shape differences can break Claude Code tool orchestration.
4. **Count token mismatch**
   - `/v1/messages/count_tokens` must be treated as a first-class compatibility surface.
5. **Alias ambiguity**
   - Client-visible model names must resolve deterministically.
6. **Overuse of LiteLLM in architecture**
   - Reference validation is useful; runtime dependency may add unnecessary indirection.
7. **Scope creep into translator internals**
   - Keep changes focused on Claude Code compatibility, not unrelated protocol cleanup.

---

## Verification plan

### Verification layers

#### Layer 1 - Fixture and event-shape validation

Purpose:

- Catch response/event contract regressions early.

Primary files:

- `test/claude_code_compatibility_sentinel_test.go`
- `test/testdata/claude_code_sentinels/`

Checks:

- `tool_progress` shape
- `session_state_changed` shape
- `tool_use_summary` shape
- `control_request` shape
- additional streaming event/delta fixtures as implementation expands

Pass criteria:

- required fields exist
- `type` and `subtype` values match expectations
- session/tool identifiers are preserved

#### Layer 2 - Executor request fidelity validation

Purpose:

- Verify what actually goes upstream, not just what the ingress accepted.

Primary files:

- `internal/runtime/executor/claude_executor_test.go`

Checks:

- `anthropic-beta` forwarding/preservation
- `anthropic-version` forwarding/preservation
- `X-Claude-Code-Session-Id` forwarding/preservation
- `/v1/messages/count_tokens` request path/body/header behavior
- count-token response shape remains `{"input_tokens": <number>}`
- stream and non-stream paths keep the same required header semantics

Pass criteria:

- captured upstream requests contain the required headers
- count-token requests use the expected endpoint and shape
- no required Claude Code headers disappear in one path but not another

#### Layer 3 - Server integration validation

Purpose:

- Verify the real `server -> handler -> auth manager -> executor` path.

Primary files:

- `internal/api/server.go`
- Claude handler/integration tests under `test/` or nearby test files

Checks:

- `POST /v1/messages` non-streaming success
- `POST /v1/messages` streaming success
- `POST /v1/messages/count_tokens` success
- session affinity keeps the same upstream account for the same Claude Code session
- model aliasing is consistent in both listing and execution
- error responses remain Claude-compatible enough for client behavior

Pass criteria:

- routes do not merely exist; they produce Claude-compatible behavior end-to-end
- session reuse does not drift across upstream auths when affinity is enabled
- model aliasing does not work in listing while failing in execution

#### Layer 4 - LiteLLM comparison validation

Purpose:

- Use LiteLLM as a comparison oracle and regression baseline.

Primary paths:

- `archive/LiteLLM/`
- comparison notes/scripts under `test/` or `docs/`

Checks:

- `/v1/messages` behavior
- `/v1/messages/count_tokens` behavior and response shape
- SSE `data:` framing and event ordering
- `anthropic-beta` handling
- prompt-cache/session-related behavior where applicable
- Claude Code-specific extras such as `[1m]` handling and telemetry-related requests if relevant

Pass criteria:

- observed differences are classified as acceptable, bug, or follow-up
- the native Anthropic-compatible path is not clearly worse without explanation

### Automated

Run focused tests during each phase, then finish with:

```bash
go test ./...
go build -o test-output ./cmd/server && rm test-output
```

If local Go is unavailable, use the Docker fallback documented in `AGENTS.md`.

### Required checklist

- [ ] `/v1/messages` non-stream path is covered
- [ ] `/v1/messages` stream path is covered
- [ ] `/v1/messages/count_tokens` path is covered
- [ ] `anthropic-beta` preservation is covered
- [ ] `anthropic-version` preservation is covered
- [ ] `X-Claude-Code-Session-Id` preservation is covered
- [ ] session affinity behavior is covered
- [ ] model alias listing behavior is covered
- [ ] model alias execution behavior is covered
- [ ] tool-use / tool-result event shape is covered
- [ ] error response compatibility is covered

### Optional but strongly recommended checks

- [ ] `[1m]` model suffix behavior is covered
- [ ] telemetry or ancillary Claude Code calls do not fail unexpectedly
- [ ] prompt-cache-related behavior is covered where supported
- [ ] unsupported feature paths fail clearly and consistently

### Manual

1. Point Claude Code at this repo using `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN`.
2. Verify a basic non-streaming request through `/v1/messages`.
3. Verify a streaming request with tool use enabled.
4. Verify `/v1/messages/count_tokens`.
5. Verify session reuse keeps the same upstream account when session affinity is enabled.
6. Verify custom model aliasing works from the Claude Code client side.
7. Repeat the same high-value scenarios against `archive/LiteLLM` and compare behavior.

### Success criteria

- Claude Code can use this repo as an Anthropic-compatible endpoint for the native Anthropic path.
- `/v1/messages` and `/v1/messages/count_tokens` both work reliably.
- Required headers and session semantics are preserved.
- Streaming event shapes pass the sentinel/fixture checks.
- Model aliasing is predictable and documented.
- LiteLLM comparison results are available as a regression oracle.

---

## Recommended implementation order

1. Phase 0 - archive clone and protocol contract
2. Phase 1 - gap audit
3. Phase 2 - config surface
4. Phase 3 - request fidelity
5. Phase 4 - response/event fidelity
6. Phase 5 - model alias and routing contract
7. Phase 6 - native acceptance matrix
8. Phase 7 - LiteLLM comparison harness
9. Phase 8 - GPT/OpenAI-family feasibility track

---

## Suggested atomic commit slices

1. `docs: add Claude Code compatibility implementation plan`
2. `test: add Claude Code compatibility gap fixtures`
3. `config: formalize Claude Code compatibility settings`
4. `claude: preserve required Claude Code headers and session semantics`
5. `claude: harden stream and count_tokens compatibility`
6. `models: align Claude Code alias routing and listing`
7. `test: add native Claude Code acceptance coverage`
8. `test: add LiteLLM comparison harness`
9. `feat: prototype Claude-to-OpenAI compatibility track` 

---

## Selected auth recurrence-prevention plan

### Goal

Prevent stale or mismatched `selected-auth-index` failures from recurring when auth files are regenerated, reloaded through different code paths, or updated while a client API key remains pinned to a specific upstream account.

### Problem summary

The current risk is not only "wrong plan type" or "wrong model list".
The deeper problem is that selected-auth pinning depends on a derived identity (`EnsureIndex`) whose seed can differ depending on how the same auth material is loaded.

Key current seams:

- `sdk/cliproxy/auth/types.go`
  - `stableAuthIndex()`
  - `(*Auth).indexSeed()`
  - `(*Auth).EnsureIndex()`
- `sdk/auth/filestore.go`
  - file-backed auth loading with `FileName` populated
- `internal/watcher/synthesizer/file.go`
  - watcher/file synthesis path with `source/path` attributes and Codex `plan_type` extraction
- `sdk/cliproxy/auth/conductor.go`
  - `Manager.Register()`
  - `Manager.Update()`
  - `Manager.Load()`
- `sdk/api/handlers/handlers.go`
  - `applyClientAPIKeySelectedAuth()`
  - `resolveSelectedAuth()`
  - `authSupportsRequest()`

---

## 3-layer defense strategy

### Layer 1 - Prevention by design (primary implementation priority)

#### Objective

Stop using a runtime-derived index as the long-term source of truth for selected-auth pinning.

#### Preferred direction

Persist a canonical auth identifier in client API key policy instead of relying only on `selected-auth-index`.

Recommended shape:

- add a stable policy field such as `selected-auth-id`
- treat `selected-auth-index` as a UI/display convenience or migration aid
- resolve in this order:
  1. `selected-auth-id`
  2. fallback legacy `selected-auth-index`

#### Why this is the highest-priority fix

- `auth.ID` is much less sensitive to loading-path differences than `EnsureIndex()`
- file-backed auth and watcher-synthesized auth can disagree on index seed inputs
- config can keep a stable pointer even if index derivation changes later

#### Target files

- `internal/config/sdk_config.go`
- `internal/api/handlers/management/client_api_key_policies.go`
- `internal/api/handlers/management/quota_status.go`
- `sdk/api/handlers/handlers.go`
- `internal/api/assets/quota.html`

#### Minimum implementation requirements

- Add `selected-auth-id` to policy schema and normalization/validation.
- Management PATCH/GET must read/write the new field.
- Runtime selected-auth resolution must first try `selected-auth-id` by direct auth ID match.
- Legacy `selected-auth-index` must continue working during migration.
- Quota/status payload must expose both the selected auth ID and the selected auth index so operators can see mismatches clearly.

---

### Layer 2 - Runtime drift handling and operator response

#### Objective

Even if config or auth state drifts, fail loudly and make recovery obvious instead of leaving operators to guess.

#### Required protections

1. **Explicit stale-selection surfacing**
   - If selected auth cannot be resolved, return a clear error that distinguishes:
     - selected auth ID missing
     - selected auth index missing
     - auth found but model unsupported
     - auth found but disabled/unavailable

2. **Quota/management visibility**
   - Include these fields in quota/status views where applicable:
     - `selected_auth_id`
     - `selected_auth_index`
     - `selected_auth_label`
     - `selected_auth_provider`
     - `selected_auth_missing`
     - `selected_auth_disabled`
     - optional mismatch indicator if ID and index no longer point to the same auth

3. **Safe migration behavior**
   - When legacy `selected-auth-index` resolves to nothing but `selected-auth-id` resolves correctly, continue with the resolved auth.
   - When only legacy index exists and fails, surface an actionable error instead of a generic service failure.

4. **Operational rule**
   - After deleting/recreating an auth file, selected-auth bindings must be rechecked before relying on pinned routing.

#### Target files

- `sdk/api/handlers/handlers.go`
- `internal/api/handlers/management/quota_status.go`
- optional management helpers if needed

---

### Layer 3 - Tests and release gate (non-negotiable)

#### Objective

Turn this class of issue into a repeatable regression test instead of an operator surprise.

#### Test strategy

1. **Identity parity tests**
   - Verify equivalent auth material loaded through different paths does not silently break selected-auth semantics.
   - Compare file-backed and watcher-synthesized auth resolution assumptions.

2. **Policy resolution tests**
   - `selected-auth-id` resolves correctly.
   - legacy `selected-auth-index` fallback resolves correctly.
   - stale index + valid ID still succeeds.
   - missing ID/index produces the expected explicit error.

3. **Model support tests**
   - selected auth found + supported model -> success
   - selected auth found + unsupported model -> explicit model-unavailable path
   - selected auth disabled/unavailable -> explicit disabled/unavailable path

4. **Quota/management payload tests**
   - selected auth metadata reflects the same auth runtime resolution will use
   - stale/missing states are visible in payloads

5. **Manual release gate**
   - Before shipping any selected-auth-related change, run all of:
     - text request using a pinned key
     - target model request using the same pinned key
     - image request using the same pinned key
     - quota/status inspection confirming the selected auth metadata matches the intended upstream account

#### Candidate test files

- `sdk/api/handlers/handlers_selected_auth_test.go`
- `internal/api/handlers/management/quota_status_test.go`
- new parity test near watcher/filestore auth loading if needed
- optional integration coverage in `test/`

---

## Recommended implementation order

1. Add `selected-auth-id` to policy schema and runtime resolution.
2. Preserve legacy `selected-auth-index` as fallback only.
3. Expand quota/management payloads to expose both ID and index.
4. Add stale-binding and parity regression tests.
5. Add a release checklist note so selected-auth changes always get real pinned-key smoke tests.

---

## Acceptance criteria for the next implementation turn

- A client API key policy can pin by stable auth ID.
- Existing legacy policies using only `selected-auth-index` still work.
- If auth files are recreated and runtime index changes, a policy using `selected-auth-id` continues to resolve the intended auth.
- Quota/management surfaces make stale or missing selected-auth bindings obvious.
- Automated tests cover stale index fallback, ID-based resolution, and mismatch visibility.
- Manual QA demonstrates pinned-key success for text, target model, and image requests.
