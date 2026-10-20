# v2 API Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the PoracleWeb.NET v2 migration batch: a per-human write lock, visible and previewable squashing (`dry_run`, `previous`, one squash policy), bulk PATCH, typed tracking mutes, per-name area remove/rename, and route/doc fixes.

**Architecture:**
- **Consistency:** one process-wide keyed mutex (`internal/humanlock`) is taken at every write entry point: an HTTP middleware, the bot command hosts, the reconcilers, the button handler and the profile scheduler. It is never taken inside stores, so locks cannot nest. There are no new DB transactions.
- **Squashing:** moves behind `store.SquashPolicy`, with `RowDiffPolicy` keeping today's rule.
- **v2 handlers:** gain `dry_run` by classifying without applying.
- **PATCH:** reuses the PUT path.
- **Mutes:** carry the rule's table end to end.

**Tech Stack:** Go 1.27, huma v2.39.1 on gin (humagin), sqlx/MySQL, testify-free stdlib tests.

**Spec:** `docs/superpowers/specs/2026-10-20-v2-api-improvements-design.md`. Read it first. It holds the reasoning and every wire shape. PR #328 carries the review.

## Global Constraints

- Work on branch `v2-api-improvements` (draft PR #328). One commit per task minimum.
- **Pre-commit gate**, run from `processor/` before every commit. There is no local Go, so use Docker, with `golang:1.27.0` exactly. `1.27.3` breaks golangci-lint v2.13.1.
  ```
  docker run --rm -v "$PWD/..":/src -w /src/processor -v poracle-gomod:/go/pkg/mod -v poracle-gocache:/root/.cache/go-build golang:1.27.0 bash -c 'git config --global --add safe.directory "*"; go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.1 >/dev/null 2>&1; go build ./... && go vet ./... && go test -race -count=1 ./... && golangci-lint run --config=/src/.golangci.yml ./...'
  ```
- **Commits:** `git -c "user.name=James Berry" -c user.email=james@coppermoth.com commit`. End every message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- **No new DB transactions.** `!restore` keeps its existing one. `StripUserAreas` loses its own.
- **The lock is never taken inside `internal/store` or `internal/db`.** Only entry points take it. A nested acquisition deadlocks: `sync.Mutex` is not reentrant. In particular, no HTTP handler behind the `HumanLock` middleware may call `bot.RunLocked`, and no command run through `RunLocked` may make an HTTP call back into the processor.
- **v1 wire behaviour is frozen.** v1 may take the lock but returns exactly what it returned before.
- **The OpenAPI golden** is regenerated in any task that changes a schema: `UPDATE_GOLDEN=1 go test ./internal/api/ -run TestOpenAPIGolden`. Then check that the diff touches only the intended operations.
- **i18n:** new strings go in `processor/internal/i18n/locale/en.json` only. Other locales come through Crowdin, with per-key English fallback.
- **Canonical tracking-table keys** for mutes are `pokemon, raid, egg, quest, invasion, lure, nest, gym, fort, maxbattle`. `incident` is normalised to `invasion`.
- **A dry run never writes, reloads or pushes.** Its response is the real response plus `"dry_run": true`, with `uid: 0` for rules it would create.

## Review Focus

1. **The same human is written concurrently from two surfaces** (a bot command and an API POST). The writes must serialise and must never interleave between classify and apply. Pinned in Task 2.
2. **A dry run on any endpoint** must leave the store, reload counter and push log untouched. Pinned in Tasks 6, 7, 8, 11 and 12.
3. **The same area under different spellings** (`Gent_Centrum`, `gent centrum`, `GENT CENTRUM`). These must match `humans.area`, which keeps underscores, and `override_areas`, which stores spaces. Pinned in Tasks 11 and 12.
4. **A bare `!mute id:N`** where N is in none, one, or several of the user's tables, or belongs to another user. The expected results are refused, typed, refused with a list, and refused. Pinned in Task 10.
5. **A failing reconciler prune** must leave the membership change unsaved, so the next reconciliation retries it. Pinned in Task 3.

---

### Task 1: `humanlock` package

**Files:**
- Create: `processor/internal/humanlock/humanlock.go`, `processor/internal/humanlock/humanlock_test.go`

**Interfaces:**
- Produces: `func Lock(humanID string) (unlock func())`. It blocks until it holds the lock for `humanID`. An empty id returns a no-op unlock without locking. Entries are reference-counted and deleted when the last holder unlocks, so the map doesn't grow. Also produces `func held() int`, unexported and used by tests, which returns the number of live entries.

- [ ] **Step 1: Write the failing tests**
  - `TestLockSerialisesSameID`: 50 goroutines each `Lock("u1")`, increment a shared non-atomic `inside` counter, assert it is 1, sleep 1ms, decrement, unlock. Run under `-race`. Fails if `inside` is ever above 1.
  - `TestLockDifferentIDsDoNotBlock`: hold `Lock("a")`. `Lock("b")` must return within 100ms (use a goroutine plus `select` with `time.After`).
  - `TestLockEvictsWhenIdle`: lock and unlock "x" and "y", then `held() == 0`.
  - `TestLockEmptyIDIsNoop`: `Lock("")` twice in the same goroutine without unlocking returns, and `held() == 0`.
- [ ] **Step 2: Run them.** `go test ./internal/humanlock/` should fail because the package doesn't exist yet.
- [ ] **Step 3: Implement.** Use a package-level `sync.Mutex` guarding `map[string]*entry{mu sync.Mutex; refs int}`. Increment `refs` under the map lock, then lock `entry.mu`. On unlock, unlock `entry.mu`, then decrement `refs` under the map lock and delete the entry at 0.
- [ ] **Step 4: Run** `go test -race ./internal/humanlock/`. Expected: PASS.
- [ ] **Step 5: Gate and commit:** `feat(humanlock): process-wide per-human write lock`.

### Task 2: Take the lock at every write entry point

**Files:**
- Create: `processor/internal/api/middleware_humanlock.go`, `processor/internal/api/middleware_humanlock_test.go`, `processor/internal/bot/run_locked.go`, `processor/internal/bot/run_locked_test.go`
- Modify:
  - `processor/cmd/processor/main.go:343` (register the middleware after `RequireSecretGin`)
  - `processor/internal/discordbot/bot.go:719`, `processor/internal/telegrambot/bot.go:461`, `processor/internal/discordbot/slash/dispatcher.go:245`, `processor/internal/discordbot/autocreate.go:744`, `processor/internal/api/huma_features.go:456` (`handler.Run(ctx, args)` becomes `bot.RunLocked(handler, ctx, args)`)
  - `processor/internal/discordbot/reconciliation.go:255` (`ReconcileUser`) and `processor/internal/telegrambot/reconciliation.go:191` (`reconcileUser`): lock `id` for the whole function, which covers both the area-security and non-area-security branches (create, reactivate, disable, delete-all)
  - `processor/internal/bot/commands/apply.go:167` (`handler.Run(&targetCtx, cmdArgs)` becomes `bot.RunLocked(handler, &targetCtx, cmdArgs)`, which locks each apply target)
  - `processor/internal/bot/commands/apply.go:18` and `processor/internal/bot/commands/poracle_admin.go:17`: implement `bot.TargetLockExempt`
  - `processor/cmd/processor/helpers.go:396` (`OnBan`) and `:441` (`disableUserForDeliveryFailure`): lock the target id around the disable writes
  - `processor/internal/buttonactions/unsubscribe.go:24` (lock the clicking user's id)
  - `processor/cmd/processor/profiles.go:47` (`checkProfileSwitches`: lock each human id around its write at about line 99)
  - `processor/internal/bot/commands/restore.go:87`: no change, because `RunLocked` covers it. Add a comment noting that the transaction is deliberately kept (spec §8).

**Interfaces:**
- Consumes: `humanlock.Lock`.
- Produces:
  - `func HumanLock() gin.HandlerFunc` in package `api`. For every method except GET, HEAD and OPTIONS where `c.Param("id") != ""`, it holds `humanlock.Lock(c.Param("id"))` across `c.Next()`. It covers the v1 `/api/tracking/*/:id`, `/api/humans/:id/*` and `/api/profiles/:id/*` routes and the huma `/api/v2/humans/{id}/*` routes, because humagin registers `{id}` as `:id`.
  - `func RunLocked(cmd Command, ctx *CommandContext, args []string) []Reply` in package `bot`. It locks `ctx.TargetID` and calls `cmd.Run`, **unless** `cmd` implements `TargetLockExempt`, in which case it calls `cmd.Run` without locking.
  - `type TargetLockExempt interface{ ExemptFromTargetLock() }` in package `bot`. It is implemented by commands that never write their own target but act on other humans or on none. Those commands take the lock per human themselves.
    - `ApplyCommand` locks each target through `RunLocked`.
    - `PoracleAdminCommand` is exempt because `!pa reconcile` runs the reconciler synchronously (`poracle_admin_reconcile.go:60` → `discordbot/bot.go:982`), and the reconciler locks each user. Without the exemption, a sweep that reaches the admin's own id deadlocks.
  - Commands that delegate internally (`mute.go:46`, `track.go:63`, `untrack.go:71`, and `poracle_admin_ratelimit.go:254`, which is read-only) keep calling `Run` directly. Do not change them to `RunLocked`, because they already run under their host's lock.

- [ ] **Step 1: Failing tests**
  - `TestHumanLockMiddleware_SerialisesSameHuman`: a gin engine with the middleware and `POST /api/x/:id`. The handler increments a shared `inside` counter, asserts it is 1, sleeps 5ms and decrements. 20 concurrent requests for `id=u1` must all return 200 with no overlap.
  - `TestHumanLockMiddleware_GetNotLocked`: hold `humanlock.Lock("u1")` in the test. `GET /api/x/u1` must still return within 100ms.
  - `TestRunLocked_SerialisesWithAPI`: hold `humanlock.Lock("u1")`. `RunLocked` with a fake `Command` and a `ctx.TargetID="u1"` must not return until the test releases the lock (assert via a channel and timeout).
  - `TestRunLocked_ExemptCommandCanLockItsOwnTarget`: a fake command implementing `TargetLockExempt`, run through `RunLocked` with `TargetID="admin"`, calls `humanlock.Lock("admin")` in `Run`. It must return within 100ms. This pins the `!pa reconcile` deadlock (blocker B1).
  - `TestApply_LocksEachTargetIncludingSelf`: `!apply` run by admin `a1` against targets `[a1, u2]` with a fake inner command. Running it through `RunLocked` completes, and with `humanlock.Lock("u2")` held by the test, it blocks on the `u2` target until the lock is released (blocker B2).
- [ ] **Step 2: Run them.** Expected: FAIL (undefined).
- [ ] **Step 3: Implement both helpers and change the call sites listed above.** Add a sentence to the `CLAUDE.md` "Command System" section saying hosts must call `bot.RunLocked`.
- [ ] **Step 4: Run** `go test -race ./internal/api/ ./internal/bot/... ./internal/discordbot/... ./internal/telegrambot/...`. Expected: PASS.
- [ ] **Step 5: Gate and commit:** `feat: take the per-human lock at every write entry point`.

### Task 3: #254 alignment: no transaction in `StripUserAreas`; prune before saving membership

**Files:**
- Modify: `processor/internal/store/human_sql.go:677` (`StripUserAreas`), `processor/internal/store/mock_human.go:528-541`, `processor/internal/discordbot/reconciliation.go:440-473`, `processor/internal/telegrambot/reconciliation.go:~339-360`
- Test: `processor/internal/discordbot/reconciliation_prune_test.go`, `processor/internal/telegrambot/reconciliation_prune_test.go`, `processor/internal/store/user_locations_sql_test.go` (existing SQL tests, which must still pass with `PORACLENG_TEST_DSN` set)

**Interfaces:**
- Produces:
  - The `MockHumanStore` fields `StripUserAreasErr error` and `StripOverrideAreasErr error`, returned by the corresponding methods when non-nil.
  - A `MockHumanStore.UpdateCalls []MockUpdateCall` field, where `MockUpdateCall{ID string; Fields map[string]any}` is a copy of each `Update` call's arguments. The existing `Calls []string` only records method names.
  - The signatures of `StripUserAreas` and `StripOverrideAreas` are unchanged.

- [ ] **Step 1: Failing tests**
  - `TestDiscordReconcile_PruneFailureLeavesMembershipUnsaved` and the Telegram twin: a community change with fences loaded, and `StripUserAreasErr = errors.New("boom")`. After `reconcileAreaSecurity`, assert from `UpdateCalls` that no `Update` call's `Fields` contains `community_membership` or `area_restriction`. Assert on the map contents, because a name sync in the same run legitimately calls `Update`. Include a renamed user in the fixture so that case is exercised. On a second run with the error cleared, assert that the prune ran and the membership was saved.
  - `TestDiscordReconcile_PruneThenSave`: on the happy path, the recorded call order is the strip call(s), then `Update`.
- [ ] **Step 2: Run them.** Expected: FAIL. The current order is save first, and a prune error is only logged.
- [ ] **Step 3: Implement.**
  - In both reconcilers, when `communityChanged`, call `PruneForMembership` **before** `humanStore.Update`. On a prune error, log it and `delete` `community_membership` and `area_restriction` from `updates` before saving the rest, so the change stays pending.
  - The "no fences loaded" skip stays as it is, and is not treated as an error.
  - In `StripUserAreas`, drop the `Beginx`/`FOR UPDATE`/`Commit` and use plain `Get`/`Select`/`Exec` on `s.db` in the same order. The doc comment should say it is idempotent and that callers hold `humanlock`.
- [ ] **Step 4: Run** the reconciler and store tests, plus the SQL tests with `PORACLENG_TEST_DSN` exported (e.g. `docker run -d --name pmysql -e MYSQL_ROOT_PASSWORD=x -e MYSQL_DATABASE=poracle mysql:8`, joined to the same docker network). Confirm the SQL tests **ran** rather than skipped. Expected: PASS.
- [ ] **Step 5: Gate and commit:** `fix(reconciliation): prune before saving membership; StripUserAreas without a transaction`.

### Task 4: `SquashPolicy`, the scenario table, the batch-claim fix, maxbattle tags

**Files:**
- Create: `processor/internal/store/squash.go`, `processor/internal/store/squash_scenarios_test.go`
- Modify: `processor/internal/store/tracking.go:35-146` (`DiffResult`, `DiffAndClassify`, `ApplyDiff`), `processor/internal/db/tracking_queries.go:706-726` (maxbattle tags and comment)

**Interfaces:**
- Produces:
  ```go
  type SquashPolicy[T any] interface {
      Classify(existing, candidates []T) DiffResult[T]
      Conflicts(existing []T, edited *T, editedUID int64) (otherUID int64, conflict bool)
  }
  type RowDiffPolicy[T any] struct{ GetUID func(*T) int64; SetUID func(*T, int64) }
  ```
  - `DiffResult[T]` gains `Replaced []T`. `Replaced[i]` is the stored row that `Updates[i]` replaces, and it has the same uid.
  - `DiffAndClassify` and `ApplyDiff` keep their signatures and delegate to `RowDiffPolicy`.
  - `Conflicts` reports the first *other* row (uid ≠ `editedUID`) that `db.DiffTracking(other, edited)` classifies as a duplicate or an update.

- [ ] **Step 1: Failing scenario tests**, table-driven over `db.MonsterTrackingAPI`, `db.RaidTrackingAPI` and `db.MaxbattleTrackingAPI`:
  - IV90 existing, IV80 submitted → one update of the existing uid, with `Replaced[0].MinIV == 90`.
  - IV90 existing, an identical rule submitted → `AlreadyPresent`.
  - IV90 at d1000 existing, IV95 at d500 submitted → insert. This is recorded behaviour (spec §3.2 row 6); assert it so a policy change shows up in the diff.
  - **Batch claim:** two existing rows, A (IV90) and B (IV70). Submit [IV85, IV80], both updatable against A. Exactly one candidate updates A, and the other is classified against the remainder (B, so it updates B). No uid appears twice in `Updates`.
  - **Maxbattle:** existing at d1000, submitted the same rule at d500 → update (it was an insert before the tag fix).
  - `Conflicts`: editing rule 1 into an exact duplicate of rule 2 → `(2, true)`. Editing it into a one-updatable-field neighbour of rule 2 → `(2, true)`. Editing rule 1 against only itself → `(0, false)`.
- [ ] **Step 2: Run them.** Expected: FAIL (`Replaced` and `SquashPolicy` undefined; the batch-claim and maxbattle cases fail).
- [ ] **Step 3: Implement.** Keep the reverse candidate loop and the first-match order. Track the claimed existing uids, and skip a claimed row when looking for a match. Give `clean`, `distance` and `template` the `diff:"update"` tag on `MaxbattleTrackingAPI`, and replace the "always inserts" comment.
- [ ] **Step 4: Run** `go test ./internal/store/ ./internal/db/ ./internal/bot/... ./internal/api/`. Expected: PASS. If an existing test encoded the old maxbattle or double-claim behaviour, update it and say so in the commit.
- [ ] **Step 5: Gate and commit:** `feat(store): SquashPolicy with scenario table; batch-claim fix; maxbattle update tags`.

### Task 5: Wildcard constants and the exhaustive null round-trip guarantee

**Files:**
- Create: `processor/internal/api/v2_wildcards.go`, `processor/internal/api/v2_roundtrip_all_test.go`
- Modify: the Translate and ToRule functions in all 11 `processor/internal/api/v2_<type>.go` files. No bounds change is expected: the fields without a maximum (`max_weight`, `min_time` and `pvp_ranking_min_cp` at `v2_pokemon.go:53-56,67`) stay unbounded on purpose. Maxbattle `evolution` (`v2_maxbattle.go:30`) gets `minimum:"0"` only if a test shows a negative value can reach storage.

**Interfaces:**
- Produces: one named constant per stored wildcard, e.g. `wcPokemonMaxIV = 100`, `wcPokemonMaxWeight = 9000000`, `wcRaidAny = 9000`, `wcRaidLevelAny = 90`, `wcFortIncludeEmpty = true`. `Translate` (`valueOr(req.X, wc…)`) and `ToRule` (`ptrUnless(row.X, wc…)`) must both refer to the same constant. No literal wildcard number may remain in either function.

- [ ] **Step 1: Failing test** `TestV2_EveryNullableFieldRoundTripsNull`. For each of the 11 types, using its `newV2<Type>TestAPI`:
  1. POST the type's minimal valid body (a table in the test, e.g. quest `{"reward_type":3}`, lure `{"lure_id":501}`).
  2. GET the rule. Reflecting over the Req struct, every field tagged `nullable:"true"` (and every slice) that the minimal body omitted must be JSON `null`.
  3. PUT the GET result back without its uid, and expect `updated` with a byte-identical rule.
  4. POST the GET result back, and expect it under `unchanged`.

  Also seed legacy pokemon rows (`rarity 0`, `size 0`, `pvp_ranking_best 0`, `pvp_ranking_worst 0`) through the mock store. A GET must show them as `null`, and POSTing that GET back must give `unchanged`.
- [ ] **Step 2: Run it.** It should fail on any field that isn't null. Fix those through the constants, not inside the test.
- [ ] **Step 3: Replace the literals with the constants.** Don't add a maximum anywhere it would equal a sentinel (spec §4.2). Regenerate the golden only if the doc strings changed.
- [ ] **Step 4: Run** `go test ./internal/api/`. Expected: PASS.
- [ ] **Step 5: Gate and commit:** `test(api): every nullable v2 field round-trips null; wildcard constants shared by translate and read`.

### Task 6: `previous` and `dry_run` on v2 POST

**Files:**
- Modify: `processor/internal/api/v2_response.go` (`v2RuleEnvelope` at `:41`/`:31`/`:110`, `v2CreateOutput` at `:140`), `processor/internal/api/v2_tracking.go` (`v2CreateInput` at `:374`, `v2HandleCreate` at `:402`)
- Test: `processor/internal/api/v2_dryrun_test.go` (new)

**Interfaces:**
- Consumes: `DiffResult.Replaced` (Task 4).
- Produces:
  - `v2RuleEnvelope[Req]` gains `Previous *v2RuleEnvelope[Req]` with `json:"previous,omitempty"`. `MarshalJSON` emits it, and the schema provider adds an optional `previous` property that references the same rule schema.
  - `v2CreateOutput` and `v2DeleteOutput` bodies gain `DryRun bool` with `json:"dry_run,omitempty"`.
  - A query field `DryRun bool` with `query:"dry_run"` on `v2CreateInput`, `v2PutInput` and `v2BulkDeleteInput`, and on a **new** `v2DeleteItemInput` (a copy of `v2ItemInput` plus `DryRun`) used only by the single-delete op. `v2ItemInput` is shared with GET-by-uid, and the ops set `RejectUnknownQueryParameters`, so adding `dry_run` to it would make GET accept the parameter without complaint.
  - `v2HandleCreate`: with `DryRun` set, it calls `store.DiffAndClassify` on the scoped rows, leaves inserts at uid 0, and skips `ApplyDiff`, `reloadState` and `v2PushDiff`.

- [ ] **Step 1: Failing tests**
  - `TestV2Create_UpdatedCarriesPrevious`: seed pokemon IV90 and POST IV80. `updated[0].previous.min_iv == 90` and `updated[0].previous.uid == updated[0].uid`.
  - `TestV2Create_DryRunWritesNothing`: the same POST with `?dry_run=true` returns the same `updated` and `previous` plus `"dry_run": true`. Afterwards the store rows are unchanged (`AllRows()`), the reload counter is 0, and the push log is empty. A new rule appears in `created` with `uid: 0`.
  - `TestV2Create_CommitReportsActualReplacement`: dry-run POST IV80 against IV90, then directly change the stored row to IV70 (simulating an intervening write), then commit the POST. `previous.min_iv == 70`, not 90 (spec §3.4).
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.** Build `previous` from `Replaced[i]` through `ToRule` and `buildRuleEnvelopes` (with a description when `include_descriptions`). Regenerate the golden.
- [ ] **Step 4: Run** `go test ./internal/api/`. Expected: PASS.
- [ ] **Step 5: Gate and commit:** `feat(api): v2 POST reports previous and supports dry_run`.

### Task 7: PUT conflict check, `dry_run` on PUT and DELETE, and a body uid list on bulk DELETE

**Files:**
- Modify: `processor/internal/api/v2_tracking.go` (`v2HandlePut` at `:448-521`, single delete at `:305-328`, bulk delete at `:331-366`, `v2BulkDeleteInput` at `:168`)
- Test: `processor/internal/api/v2_dryrun_test.go`, `processor/internal/api/v2_put_conflict_test.go` (new)

**Interfaces:**
- Consumes: `RowDiffPolicy.Conflicts` (Task 4) and `DryRun` (Task 6).
- Produces:
  - PUT returns 409 with the detail `would duplicate rule <uid>` whenever `Conflicts` reports another rule, which replaces the exact-duplicate-only check. PUT's `updated[0].previous` is the stored rule before the edit.
  - `v2BulkDeleteInput.UID` becomes optional (`required:"false"`), and the input gains an optional body `{ "uids": []int64 }` (`minItems:"1"`). The `Body` field must be tagged `required:"false"`, otherwise huma rejects the existing `?uid=` form, which has an empty body. `TestV2BulkDelete_QueryFormStillWorks` pins this. Both present, or neither, is a 422 with detail `send uid or a body uids list, not both` / `uid or uids is required`.

- [ ] **Step 1: Failing tests**
  - `TestV2Put_RefusesNearDuplicateOfOtherRule`: rules 1 (IV90 d0) and 2 (IV80 d0). PUT rule 1 as IV80 at d500, which is one updatable field away from rule 2. Expect 409 naming uid 2, with both rows unchanged.
  - `TestV2Put_DryRunWritesNothing` and `TestV2Delete_DryRunWritesNothing` (single and bulk): the same response with `dry_run: true`, the store unchanged, no reload and no push.
  - `TestV2BulkDelete_BodyUIDs`: a body with `{"uids":[…300 uids…]}` deletes them all. Both forms, or neither, gives 422.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.** Regenerate the golden.
- [ ] **Step 4: Run** `go test ./internal/api/`. Expected: PASS.
- [ ] **Step 5: Gate and commit:** `feat(api): PUT refuses near-duplicates; dry_run on PUT/DELETE; bulk DELETE accepts a body uid list`.

### Task 8: Bulk PATCH (#325)

**Files:**
- Create: `processor/internal/api/v2_patch.go`, `processor/internal/api/v2_patch_test.go`
- Modify: `processor/internal/api/v2_tracking.go` (`registerV2TrackingOps` at `:233`: register `v2-patch-<Name>`, `PATCH` on the base path)

**Interfaces:**
- Consumes: `Translate`, `ToRule`, `RowDiffPolicy.Conflicts`, `UIDReassigner`, `DryRun`, and the PUT write sequence (DeleteByUID → Insert → ReassignUID).
- Produces:
  - `v2PatchInput` has `ID`, `Profile`, `IncludeDescriptions`, `Silent`, `Trusted`, `DryRun`, and `Body{ UIDs []int64 \`json:"uids" minItems:"1"\`; Set v2PatchSet \`json:"set"\` }`.
  - `v2PatchSet` has `Distance *int`, `Clean *bool`, `Edit *bool`, `Summary *bool`, `Template *string` and `Ping *string`, with the same tags and bounds as the rule fields. At least one must be set, otherwise 422.
  - The output body is `{updated, unchanged, refused []v2PatchRefusal, dry_run}`, where `v2PatchRefusal{UID int64; Reason string (enum invalid,conflict,not_found); Detail string; ConflictsWith int64 \`json:"conflicts_with,omitempty"\`}`.
  - `applyV2PatchSet(req any, set v2PatchSet) error` sets the Req fields by JSON tag through reflection. A type whose Req lacks a named field (e.g. `summary` on non-quest), or whose type rejects `edit` (`rejectEdit`), gets a whole-request 422 before anything is touched.

- [ ] **Step 1: Failing tests**
  - `TestV2Patch_SetsDistanceKeepsUIDAndOtherFields`: three pokemon rules with different IVs. PATCH `{distance:500}` → all three `updated`, each with the same uid and IVs, and `previous.distance` showing the old value.
  - `TestV2Patch_PerRuleRefusals`: a rule with `override_areas` and `distance:500` gives `invalid`. A uid owned by u2 gives `not_found`. Two rules that differ only in distance gives one `updated` and the other `conflict` with `conflicts_with` set, and the first in `uids` order wins.
  - `TestV2Patch_UnsupportedFieldIs422`: `{summary:true}` on raid, and `{edit:true}` on gym.
  - `TestV2Patch_DryRunWritesNothing`.
  - `TestV2Patch_UnchangedWhenAlreadySet`.
- [ ] **Step 2: Run them.** Expected: FAIL (405).
- [ ] **Step 3: Implement.** For each uid in order: find the owned row (`v2FindOwnedRow`) or report `not_found`; take `ToRule(row)`; apply the set; `Translate` (an error becomes `invalid` with the error's detail); compare with the stored row (equal means `unchanged`); run `Conflicts` against the rows **as already patched in this request**; then write. Regenerate the golden. Update the "No PATCH" lines in `docs/v2-api-design.md:94` and `:311`.
- [ ] **Step 4: Run** `go test ./internal/api/`. Expected: PASS.
- [ ] **Step 5: Gate and commit:** `feat(api): v2 bulk PATCH of rule settings with per-rule outcomes (#325)`.

### Task 9: Typed tracking mutes, matching side (#323)

**Files:**
- Modify:
  - `processor/internal/mute/mute.go` (`Entry` at `:39`, `Event` at `:53`, `matchScope` at `:242-249`, `sameScopeValue` at `:127`)
  - `processor/internal/webhook/types.go:226`
  - `processor/internal/matching/human.go` (`:129`, `:234`, `raidUserData` at `:240`)
  - `processor/internal/matching/generic.go` (`trackingUserData` at `:33`, `:136`)
  - `processor/internal/matching/gym.go:210`
  - the matchers that build `trackingUserData` and `raidUserData` (`raid.go:114,188`, `invasion.go:69`, `nest.go:63`, `quest.go:72`, `fort.go:69`, `maxbattle.go:93`, `lure.go:53`, `gym.go:79`)
  - `processor/cmd/processor/helpers.go:260`
  - `processor/cmd/processor/quest_summary_dispatch.go:415-418`
- Test: `processor/internal/mute/mute_test.go`, `processor/internal/matching/*_test.go` (one assertion per matcher), `processor/cmd/processor/quest_summary_filters_test.go`

**Interfaces:**
- Produces:
  - `mute.Entry.RuleTable string`, set only for `ScopeTracking`.
  - `mute.Event.MatchedRuleTable string`.
  - `webhook.MatchedUser.RuleTable string` with `json:"rule_table,omitempty"`.
  - `func mute.CanonicalRuleTable(typeName string) (string, bool)`, which maps the 11 v2/bot type names to the 10 canonical keys (`incident` → `invasion`).
  - A tracking mute matches only when the table and the uid are both equal.
  - Identity becomes (scope, table, value) for **both** `Add` (replace-on-duplicate goes through `sameScopeValue`, `mute.go:127`, which gains the table) and `Remove`. The new signature is `Remove(humanID, scopeType, ruleTable, scopeValue string) bool`.
  - Each matcher sets `RuleTable` to its own table key: `raid` vs `egg` by matcher, quest summaries `quest`.

- [ ] **Step 1: Failing tests**
  - `TestTrackingMuteRequiresTable`: an entry `{tracking, raid, "60"}` matches the event `{raid, 60}`, but not `{pokemon, 60}` and not `{egg, 60}`.
  - `TestAddKeepsDifferentTables`: adding `{tracking, raid, 60}` and then `{tracking, pokemon, 60}` leaves two entries, with `replaced == false`.
  - `TestRaidAndEggSameUIDGetOwnTables` (matching): seed one raid rule and one egg rule with the **same** uid. `MatchRaid` returns `RuleTable "raid"` and `MatchEgg` returns `"egg"`. Both matchers build the shared `raidUserData` (`human.go:240`), so the table must be set by the matcher (`raid.go:114` and `:188`), not taken from the struct type.
  - In each matcher's tests, assert `RuleTable` on a matched user (pokemon, raid, egg, quest, invasion, lure, nest, gym, fort, maxbattle).
  - Summary: a raid-table mute with uid N does not drop a quest row whose rule uid is N.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.** Update every existing `Remove` caller (`bot/commands/mute.go`, `api/v2_mutes.go`) to pass a table. Pass `""` for non-tracking scopes.
- [ ] **Step 4: Run** `go test ./internal/mute/ ./internal/matching/ ./cmd/processor/`. Expected: PASS.
- [ ] **Step 5: Gate and commit:** `fix(mute): tracking mutes match on rule table and uid (#323)`.

### Task 10: Typed tracking mutes: bot, v2 API and button

**Files:**
- Modify:
  - `processor/internal/bot/commands/mute.go` (`stripMuteTypePrefix` at `:198`, `runMuteTrackingUID` at `:219`, `runUnmuteTrackingUID` at `:514`, the comment at `:107-111`)
  - `processor/internal/bot/commands/tracked.go:66` (`propertyMuteValueLabel`)
  - `processor/internal/i18n/locale/en.json`
  - `processor/internal/api/v2_mutes.go` (`:37-135`, `:262`)
  - `processor/internal/buttonactions/mute.go:113-118`
  - `processor/internal/buttons/buttons.go:49` (comment)
- Test: `processor/internal/bot/commands/mute_test.go`, `processor/internal/api/v2_mutes_test.go`, `processor/internal/buttonactions/buttonactions_test.go`

**Interfaces:**
- Consumes: `mute.CanonicalRuleTable`, `Entry.RuleTable`, and `ctx.Tracking` (`store.TrackingStores`).
- Produces:
  - `func findRuleTables(ctx *bot.CommandContext, uid int64) []string`, which scans each store's `SelectByIDProfile(ctx.TargetID, ctx.ProfileNo)` for the uid and returns the canonical tables that hold it.
  - New i18n keys: `msg.mute.uid_not_found` ("No rule id:{0} found."), `msg.mute.uid_ambiguous` ("id:{0} matches more than one of your rules ({1}). Use e.g. !{2} mute id:{0}."), `msg.mute.rule_label` ("{0} rule {1}").
  - v2: `v2MuteItem` and `v2CreateMuteInput.Body` gain `TrackingType *string` (`json:"tracking_type"`, enum of the 11 names). It is required for `scope=tracking` and is a 422 for other scopes. GET returns the canonical key. `v2DeleteMutesInput` gains `TrackingType` (`query:"tracking_type"`). POST also checks that the uid is one of the human's rules in that table, and returns 422 `no <type> rule <uid> for this human` if not.

- [ ] **Step 1: Failing tests**
  - Bot: `!raid mute id:60` stores table `raid`. With the user owning raid 60 and pokemon 60, a bare `!mute id:60` refuses with the ambiguous message listing both. A bare `!mute id:60` with only raid 60 stores table `raid`. A bare `!mute id:999` refuses as not found. `!tracked` shows `raid rule 60`.
  - v2: POST `{scope:tracking, tracking_type:raid, value:"60"}` returns 201 and GET shows `tracking_type: "raid"`. Omitting `tracking_type` gives 422. `tracking_type` with `scope:gym` gives 422. Another user's uid gives 422. `incident` is stored as `invasion`. DELETE with `tracking_type` removes only that entry.
  - Button: a mute button with scope=tracking on a snapshot `{AlertType:"raid", TrackingUIDs:[60]}` adds the entry `{tracking, raid, "60"}`.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.** Map the snapshot `AlertType` to a table key with the same switch as `deleteTrackingByAlertType` (`unsubscribe.go:61`). Extract it into `func alertTypeTable(alertType string) (string, bool)` and use it from both places. Regenerate the golden.
- [ ] **Step 4: Run** `go test ./internal/bot/... ./internal/api/ ./internal/buttonactions/`. Expected: PASS.
- [ ] **Step 5: Gate and commit:** `feat: typed tracking mutes in the bot, v2 API and alert buttons (#323)`.

### Task 11: Remove one area name (#252, #253)

**Files:**
- Modify:
  - `processor/internal/store/human.go` (interface), `processor/internal/store/human_sql.go`, `processor/internal/store/user_locations_sql.go` (next to `StripOverrideAreas` at `:138`), `processor/internal/store/mock_human.go`
  - `processor/internal/api/v2_humans.go` (`v2SetAreasInput.AllProfiles` doc at `:569`, the `registerV2HumanSetAreas` Description, and new `registerV2HumanRemoveArea` after `:573`)
- Test: `processor/internal/store/mock_human_test.go`, `processor/internal/store/user_locations_sql_test.go`, `processor/internal/api/v2_humans_test.go`

**Interfaces:**
- Produces:
  - `HumanStore.RemoveAreaFromProfiles(id, name string, profileNos []int) (removedFrom []int, err error)`. It changes the listed profile rows, and `humans.area` only when `current_profile_no` is in `profileNos`. Profiles come back ascending. It uses no transaction.
  - `HumanStore.EditOverrideAreas(id string, profileNos []int, from, to string) (broadened map[string][]int64, err error)`. It walks `trackingTablesWithOverrideAreas` filtered to `profile_no IN profileNos`. An empty `to` removes `from`; otherwise `from` is replaced by `to`, collapsing duplicates. The result maps canonical v2 type keys (`monsters`→`pokemon`, `lures`→`lure`, `nests`→`nest`, `forts`→`fort`, others as they are) to the uids whose override list became empty. Those become `NULL`.
  - `func areaNameMatches(stored, requested string) bool`, which compares with `strings.EqualFold` or `geofence.NormalizeAreaKey` equality. `RemoveAreaFromProfiles` and `EditOverrideAreas` both use it.
  - `DELETE /api/v2/humans/{id}/areas?name=&profile=&all_profiles=&dry_run=` returns `{name, removed_from []int, broadened {type: [uid]}, dry_run}`. `removed_from` and `broadened` are never null.
  - The `AllProfiles` doc string becomes: `Replace the areas on EVERY profile the human owns with the submitted list. This is a whole-list replace, not a per-name removal: profiles that currently hold different areas all end up with this list. To remove one area from every profile, use DELETE /api/v2/humans/{id}/areas?name=…&all_profiles=true. Mutually exclusive with profile.`

- [ ] **Step 1: Failing tests.** Take the four API tests and two mock tests from `docs/superpowers/plans/2026-10-11-v2-set-areas-profile-ops.md`, Tasks 2–3, unchanged. Add:
  - `TestV2Humans_RemoveArea_StripsOverridesAndReportsBroadened`. Seed pokemon rule 12 with `override_areas ["private"]`, pokemon rule 40 with `["private","alpha"]`, and raid rule 7 with `["private"]`. Expect `broadened == {"pokemon":[12],"raid":[7]}`, with rule 40 keeping `["alpha"]`. This needs the test deps to set `Tracking`, and the mock `EditOverrideAreas` to act on the tracking mock stores. Rule 40's list kept another area, so it is not reported.
  - `TestV2Humans_RemoveArea_DryRunWritesNothing`.
  - `TestV2Humans_RemoveArea_SpellingVariants`: `?name=Private`, `?name=PRIVATE` and the underscore/space forms all remove `private`.
  - An SQL twin `TestEditOverrideAreas_SQL`, gated on `PORACLENG_TEST_DSN`.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.** Profiles first, then overrides, then `reloadState` if anything changed. A dry run computes the same result from reads only (add `dryRun bool` parameters, or read-only variants; pick one and use it for Task 12 too). Mark the 2026-10-11 plan as superseded with a header line. Regenerate the golden.
- [ ] **Step 4: Run** the store and API tests, including SQL with the DSN. Expected: PASS.
- [ ] **Step 5: Gate and commit:** `feat(api): DELETE /v2/humans/{id}/areas removes one name everywhere, incl. overrides (#252, #253)`.

### Task 12: Rename one area name (#259)

**Files:**
- Modify: `processor/internal/api/v2_humans.go` (new `registerV2HumanRenameArea`), `processor/internal/store/human.go`, `human_sql.go`, `mock_human.go`
- Test: `processor/internal/api/v2_humans_test.go`, `processor/internal/store/mock_human_test.go`

**Interfaces:**
- Consumes: `EditOverrideAreas`, `areaNameMatches`, `settableAreaSet` (`v2_humans.go:649`), `bot.PermittedAreas`.
- Produces:
  - `HumanStore.RenameAreaInProfiles(id, from, to string, profileNos []int) (renamedIn []int, err error)`. It follows the same current-profile rule for `humans.area`, and if a profile already holds `to`, it ends up holding it once.
  - **Spelling written:** `humans.area` and `profiles.area` get `strings.ToLower(fence.Name)` for the matched fence, the same form set-areas writes (`v2_humans.go:601,669`, underscores kept). `override_areas` gets `geofence.NormalizeAreaKey(to)` (spaces), the same form `normalizeOverrideAreas` writes. The duplicate collapse in both places compares with `areaNameMatches`, so two spellings of `to` can't both survive.
  - `PATCH /api/v2/humans/{id}/areas?from=&to=&profile=&all_profiles=&trusted=&dry_run=` returns `{from, to, renamed_in []int, dry_run}`. `to` must be in `settableAreaSet(deps, human, admin, trusted)`, otherwise 422 `area not permitted: <to>`, and nothing changes. `from` needs no check.

- [ ] **Step 1: Failing tests**
  - `TestV2Humans_RenameArea_AllProfiles`: profiles [private, alpha] and [private] become [<to>, alpha] and [<to>], and override `["private"]` becomes `[<to>]`.
  - `TestV2Humans_RenameArea_ToNotLoadedIs422AndChangesNothing`.
  - `TestV2Humans_RenameArea_Idempotent`: a second call returns 200 with `renamed_in: []`.
  - `TestV2Humans_RenameArea_HoldsBothCollapses`: the profile holds `["gent_centrum", "private"]` and the override holds `["gent centrum", "private"]`. Rename `private` → `Gent_Centrum`. The profile ends `["gent_centrum"]` and the override ends `["gent centrum"]`, each holding exactly one entry.
  - `TestV2Humans_RenameArea_DryRunWritesNothing`.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.** Regenerate the golden.
- [ ] **Step 4: Run** `go test ./internal/api/ ./internal/store/`. Expected: PASS.
- [ ] **Step 5: Gate and commit:** `feat(api): PATCH /v2/humans/{id}/areas renames one name everywhere (#259)`.

### Task 13: Routes, `/health` in the spec, and the 401 shape

**Files:**
- Modify:
  - `processor/internal/api/huma_setup.go:17-36` (set `cfg.Servers = []*huma.Server{{URL: "/api"}}`)
  - `processor/internal/api/middleware.go:16-33`
  - `processor/internal/api/api.go:105` (a huma-registered description of `/health`)
  - `processor/cmd/processor/main.go:389-390,409-411` (stale comments)
- Test: `processor/internal/api/middleware_secret_test.go` (new), `processor/internal/api/health_test.go`, the golden

**Interfaces:**
- Produces:
  - **401 shape:** `RequireSecretGin` sends the legacy body `{"status":"authError","reason":"incorrect or missing api secret"}` for paths starting with `/api/tracking/`, `/api/humans/` or `/api/profiles/` (v1). Every other path gets `application/problem+json` `{"status":401,"title":"Unauthorized","detail":"incorrect or missing api secret"}`.
  - **`/health` in the spec:** do **not** `huma.Register` it. The huma API is bound to `apiGroup` (`huma_setup.go:35`), so registering it would create a real `/api/health` route behind the secret. Instead, in `NewHumaAPI`, add `humaAPI.OpenAPI().Paths["/health"]` by hand: a GET operation with `Security: []map[string][]string{}`, a path-level `Servers: []*huma.Server{{URL: "/"}}`, and a response schema of `{status string, version string, capabilities object<string,bool>}`. The gin handler and its response are unchanged. `TestNoAPIHealthRoute` asserts that `GET /api/health` is 404.

- [ ] **Step 1: Failing tests**
  - `TestRequireSecret_V1KeepsLegacyBody` (POST `/api/tracking/pokemon/u1`).
  - `TestRequireSecret_HumaRoutesGetProblemJSON` (GET `/api/v2/humans/u1`, and GET `/api/config/poracleWeb`).
  - `TestOpenAPI_DocumentsHealthAndServers` (the spec has `servers[0].url == "/api"` and a `/health` path with empty security).
  - `TestHandleHealthReturnsCapabilities` must still pass unchanged.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.** Regenerate the golden.
- [ ] **Step 4: Run** `go test ./internal/api/`. Expected: PASS.
- [ ] **Step 5: Gate and commit:** `feat(api): /health in the spec, servers prefix, problem+json 401 on huma routes`.

### Task 14: Documentation and changelog

**Files:**
- Modify: `CHANGELOG.md` (`[Unreleased]`: Added at `:9`, Changed at `:66`), `docs/v2-api-design.md` (`## Changes` at `:11`; `:94`, `:248` and `:311` if not already done; a new "API stability" section), `docs/v1-to-v2-migration-guide.md` (tracking `:31`, humans `:50`), `CLAUDE.md` (v2 surface, mute infrastructure, area security prune ordering, `humanlock`)

- [ ] **Step 1: Write the entries.**
  - **Added:** `dry_run`; `previous`; bulk PATCH; bulk DELETE body; area DELETE/PATCH; typed mute `tracking_type`; `/health` and `servers` in the spec.
  - **Changed (breaking):** mutes `tracking_type` required for tracking scope; 401 problem+json on huma routes; PUT 409 on near-duplicate; maxbattle edits now squash; the reconciler saves membership after the prune.
  - **Stability statement:** the spec §9 bullets, copied as written.
  - **The `null` contract** in the `docs/v2-api-design.md` wildcard section (`:105-146`) and the migration guide sentinels section (`:116`): "`null` means unconstrained. A v2 client never needs to know how that is stored, and the stored representation may change without notice." Keep the sentinel table there, labelled as internal and for v1 readers only.
  - **CLAUDE.md:** replace "Tracking-UID mutes are fully wired: …" with the table-aware description, and add `humanlock` to the directory list and the API Security / Command System notes.
- [ ] **Step 2: Check** that `grep -n "transaction" CLAUDE.md docs/v2-api-design.md` mentions only `!restore`.
- [ ] **Step 3: Gate and commit:** `docs: v2 API improvements — changelog, design doc, migration guide, CLAUDE.md`.
- [ ] **Step 4:** Push, and update the #328 description from "design for comment" to "ready for review". Ask the user before marking it ready.
- [ ] **Step 5 (user action, not code):** the spec §10 release steps. Tag `v5.2.1` on the `main` commit that bumped the version (`6f25eb6c`), and tag `v5.3.0` when `develop` is released. Each tag triggers the semver Docker images (`.github/workflows/docker.yml`). Tags are public, so they need the user's go-ahead.
