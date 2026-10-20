# v2 API Improvements for Full Client Migration — Design for Comment

**Status:** draft, for comment by PoracleWeb.NET before implementation
**Date:** 2026-10-20
**Branch:** `v2-api-improvements` (off `develop`, with #254 / #243 merged in)
**Issues:** #326, #325, #324, #323, #259, #253, #252, #243, plus two questions asked out of band (versioning of unversioned routes, release tagging)

## How to read this

PoracleWeb.NET has set out to stop reading and writing the PoracleNG database directly and to use only the API. That goal is ours too. This document goes through every request in that programme and states what we intend to build.

Some requests are accepted as asked, some are accepted with changes, and some are answered with a different design. Where we diverge, the reasoning is given in full so it can be argued with. The aim is a v2 API that is coherent on its own terms, rather than one that mirrors a client's existing database operations one-for-one. That way the next client gets the same benefit.

Nothing here is built yet. Comments on any section are welcome. The open questions are collected in [§11](#11-open-questions-for-poracleweb-net).

## 1. Principles applied

1. **The server is the only authority on its own invariants.** If a client has to re-implement server logic to behave correctly, the API is missing something. The fix is to expose the server's decision, not to document the logic so it can be copied.
2. **v2 hides the storage representation.** Sentinels (`9000`, `100`, `-1`, …) are internal. A v2 client reads `null` and writes `null` (or omits the field) for "unconstrained". This was committed to in the v2 RFC (#138).
3. **One mechanism, many uses.** Where several requests share a need, they get one general facility rather than a flag each.
4. **Mutations that can partially apply report exactly what they did** (from #215). A mutation that can be previewed reports the same shape when previewed.
5. **Defaults do not move under existing callers.** The bot, v1 and current v2 clients keep their behaviour unless a section says otherwise and lists it as a change.

## 2. Summary

| # | Request | Our answer |
|---|---|---|
| #326 | `?on_match=refuse` strict create | **Alternative.** Squashing stays and becomes visible: `dry_run` on every v2 mutation, `previous` on every squashed rule, one squash policy for POST/PUT/PATCH, and a per-human write lock. ([§3](#3-tracking-rule-squashing-326)) |
| #324 | Machine-readable wildcard (`x-wildcard`) | **Declined, with an alternative.** A tested guarantee that `null` round-trips on every nullable field, schema bounds for display, and a stated contract that a v2 client never needs the stored value. ([§4](#4-wildcards-324)) |
| #325 | Bulk partial update | **Accepted with changes.** `PATCH …/tracking/{type}` with a uid list in the body, per-rule outcomes, and the squash policy applied. ([§5](#5-bulk-update-325)) |
| #323 | Tracking mutes ignore rule type | **Accepted.** This is a bug. Mutes carry the tracking table on both sides, and the alert button can create them. ([§6](#6-typed-tracking-mutes-323)) |
| #252 / #253 / #259 | Area doc fix; remove one area name everywhere; rename one area name everywhere | **Accepted.** Override areas are removed or renamed by the same operation, and `broadened` rules are reported. Per-human lock plus idempotent retry, with no DB transactions. ([§7](#7-area-name-operations-252-253-259)) |
| #243 | Fences exempt from area security | **Merged into this branch** from #254 (approved). Its one DB transaction is replaced by the per-human lock. ([§8](#8-243-exemptfromareasecurity)) |
| — | v2 copies of unversioned routes? | **No.** They are stable and already documented. Small fixes are listed. ([§9](#9-unversioned-routes)) |
| — | Release tags? | **Yes.** ([§10](#10-releases-and-versioning)) |

---

## 3. Tracking rule squashing (#326)

### 3.1 Why squashing exists

When a webhook matches several of one human's rules, the human still receives **one** message. The pokemon path merges matched users by id (`enrichment/peruser.go`), and every other matcher dedupes in the matcher (`matching/generic.go`). So overlapping rules never double-alert. They cause three other problems:

- **An edit does nothing.** Tightening Pikachu from IV 90 to IV 95 by adding a rule leaves the IV 90 rule matching, so the user sees no change.
- **Settings are chosen arbitrarily.** The first matching rule supplies template, clean and ping, and which rule matches first depends on load order.
- **The rule list stops explaining the behaviour.** Deleting a rule doesn't stop the alerts, because another rule still covers them.

PoracleNG therefore **squashes at write time**. A submitted rule that is a near-duplicate of an existing one replaces it rather than sitting beside it. This was introduced deliberately, because rule sets used to fill up with ineffective near-duplicates. It is a property of the rule set, not a bot convenience, and it should hold no matter which surface wrote the rule.

### 3.2 The current rule and its gaps

Today's rule, `db.DiffTracking`, works row against row:
- Fields tagged `match` must be equal.
- If exactly one `update`-tagged field differs, the candidate replaces that row (keeping its uid since #303).
- If no field differs, the candidate is a duplicate.
- Anything else is a new rule.

It has several gaps, which this work closes or records:

| Gap | Effect | Here |
|---|---|---|
| PUT checks only for an *exact* duplicate of another rule | PUT can turn rule A into a near-duplicate of rule B, and both then match | Closed: PUT uses the same policy ([§3.5](#35-one-policy-every-surface)) |
| A bulk PATCH could create near-duplicates | Setting `distance` on two rules that differed in distance and template leaves two rules differing only in template | Closed: PATCH uses the same policy ([§5](#5-bulk-update-325)) |
| No lock or transaction between classify and write | A concurrent write can land between the decision and the write. This is the race #326 describes. | Closed: per-human write lock (§3.6) |
| Maxbattle `clean`/`distance`/`template` are not `update`-tagged | `!maxbattle … d500` after `d1000` makes a second rule instead of replacing the first | Fixed: tags aligned with raid ([§3.7](#37-maxbattle)) |
| v1 maxbattle POST never diffs | "Add twice stores the same rule twice" (#326) is a v1 behaviour. v2 already returns the second as `unchanged`. | v1 is frozen. Moving creates to v2 fixes it. |
| Two *updatable* fields changed at once counts as a new rule | Changing IV and distance in one `!track` leaves two overlapping rules | **Recorded, not changed.** A set-based model handles this properly; see [Appendix A](#appendix-a-set-based-squashing-dominance-for-debate) |
| Two candidates in one batch can both choose the same existing row | The second update clobbers the first | Fixed: an existing row is claimed by at most one candidate per request; the second is classified against the remainder |

### 3.3 Why not `?on_match=refuse` as asked

The request is to refuse any candidate that would update or duplicate, and to have the client then explain the refusal. We agree with what it is trying to achieve: an "Add" in a UI should never change another rule without the user seeing it. We don't think a second write mode is the best way to get there:

- **It splits the invariant across modes.** Under `refuse`, the client is responsible for resolving a near-duplicate, and any client that resolves it by inserting anyway reintroduces exactly what squashing exists to prevent. The server should stay responsible in every mode.
- **It solves only POST.** Bulk PATCH (#325), area removal and rename (#253/#259) and PUT have the same "show me what this will do" need. A refusal flag on POST doesn't help any of them.
- **The underlying problem is visibility, not substitution.** v2 already reports squashed rules under `updated`. The complaint is that the client finds out after the write, and can't tell what changed because only the new state is returned. Both can be fixed directly.

### 3.4 What we propose instead

**(a) `?dry_run=true` on every v2 tracking and area mutation.** The request is validated and classified exactly as a real one would be, using the same code path, and returns the response the real request would have returned. Nothing is written, no state reload is triggered, and no confirmation message is pushed. The response carries `"dry_run": true`. Endpoints: tracking POST, PUT, PATCH (§5), single and bulk DELETE, area DELETE and PATCH (§7).

**(b) `previous` on every rule that a write modified.** Each rule in `updated` carries the stored state it replaced:

```json
{
  "created":   [],
  "updated":   [{ "uid": 12, "pokemon_id": 25, "min_iv": 95, "previous": { "uid": 12, "pokemon_id": 25, "min_iv": 90 } }],
  "unchanged": []
}
```

`previous` uses the same v2 rule shape (and gets a `description` when `include_descriptions=true`). With it, a client can always say truthfully what happened ("Your IV 90 Pikachu rule is now IV 95"), whether or not it previewed.

**The resulting UI flow for "Add":**
1. `POST …?dry_run=true`. If `updated` is empty, commit.
2. If not, show "This will change your existing rule: IV 90 → IV 95. Change it / Cancel".
3. On confirm, send the same POST without `dry_run`. The response lists exactly what was done.

The client needs no copy of the diff rules: the server decides once, in the preview, and again in the commit, using the same code.

### 3.5 One policy, every surface

Squashing moves behind a single policy type in `internal/store`, used by every write path. Today it is called separately from the bot commands, v1 POST, v2 POST and (partially) v2 PUT.

```go
// SquashPolicy decides how a candidate rule relates to a profile's existing
// rules. It is the single definition of "this rule replaces that one".
type SquashPolicy[T any] interface {
    Classify(existing []T, candidates []T) Classification[T] // inserts, updates (with previous), duplicates
    Conflicts(existing []T, edited T, editedUID int64) (otherUID int64, ok bool)
}
```

- **POST (all surfaces):** `Classify`. The behaviour is unchanged apart from the batch-claim fix in §3.2.
- **PUT and PATCH (v2):** `Conflicts`. These address a named uid, so an edit that would make the rule a squash target of *another* rule is **refused** (`409`, or a per-rule refusal in PATCH) and names the other uid. It is not merged. Merging there would mean "editing rule A deleted rule B", which is the kind of hidden side effect this work is meant to remove.
- **The policy is replaceable.** The current row heuristic becomes `RowDiffPolicy`. Every surface is tested against **one shared scenario table** (existing rules + submitted rule → expected outcome). Changing the squash rule later, for example to the set-based model in Appendix A, is then a contained change: a new policy plus an edited scenario table, with the effect on every surface visible in one test diff.

### 3.6 Concurrency and failure: a per-human lock, no DB transactions

PoracleNG keeps its consistency rules in the application, not the database. The tracking tables have no transactions, and this work does not add any. One rule applies across the codebase:

- **A per-human write lock.** PoracleNG is a single process, so an in-process lock keyed by human id serialises every write that touches one human's data. That covers tracking writes from the bot, v1 and v2; area and profile writes; and reconciliation prunes. Two writers can no longer interleave between "decide" and "write". It replaces the row locks (`SELECT … FOR UPDATE`) that #254's `StripUserAreas` uses today.
- **Multi-row operations are idempotent and safe to retry, not transactional.** Area removal, area rename, the override rewrites that go with them, and prunes all produce the same end state when repeated. If one fails partway (for example, the database connection drops), the caller gets an error and repeats it. A repeat finishes the job and leaves already-done rows unchanged. Responses always report the resulting state.
- **Every retry path must actually run.** An API client retries on error. A reconciler prune is only triggered by a community-membership change, so the new membership is saved **after** the prune succeeds. A failed prune then leaves the change pending, and the next cycle repeats it.
- **Tracking replace (`ApplyDiff`: delete, then insert):** with the lock, no other writer can see or interleave with the gap. A failure between the delete and the insert can still lose that one rule, as today. This is recorded rather than changed.

This also removes the two existing DB transactions, #254's `StripUserAreas` and `!restore` (§8). None remain afterwards.

### 3.7 Maxbattle

`MaxbattleTrackingAPI` gets `diff:"update"` on `clean`, `distance` and `template`, matching raid. Its comment ("always inserts") is corrected: v2 and the bot already diff maxbattle through `ApplyDiff`. v1 maxbattle POST remains insert-only because v1 is frozen. This is listed in the changelog.

---

## 4. Wildcards (#324)

### 4.1 What is asked, and why we are declining it

The request is a machine-readable `x-wildcard` on each nullable field, so that a client can turn the `null` it reads back into the stored sentinel (`100`, `9000`, …).

The client needs this because its internal model holds stored values: it currently reads our database and creates rules through v1. That is a transitional state, and the request would make it permanent. Publishing the sentinels in the schema turns them into contract, which the v2 design explicitly set out to avoid ("remove all magic numbers from the API surface", #138). After that, every storage change becomes a breaking change, and every future client is invited to depend on them too.

The risk described ("our edit path re-reads a rule before every write, so a wrong wildcard gets written back") only exists when a client converts `null` into a number. If a v2 client reads `null` and writes `null`, it round-trips exactly, with nothing to get wrong.

### 4.2 What we provide instead

1. **A round-trip guarantee, enforced by test.** For all 11 types, every nullable field is checked for: `null` / omitted on write reads back as `null`, and GET → PUT and GET → POST of any stored rule (including legacy alias values such as `rarity 0`) result in `unchanged`. The existing round-trip helper covers this partly; it becomes exhaustive and is generated from the field table in point 4.
2. **Display bounds in the schema.** Every range field carries `minimum`/`maximum` (IV is already 0–100), so a UI can draw a slider or say "up to 100" without the stored value.
3. **A stated contract** in the API docs: "`null` means unconstrained. A v2 client never needs to know how that is stored, and the stored representation may change without notice."
4. **One internal field table per type** (wildcard, kind, bounds) that drives `Translate` defaults, the `ToRule` null projection and the documentation. The same value is currently written separately in all three places. This removes drift on our side. It also feeds the squash policy (Appendix A). It stays internal.

### 4.3 The two special cases

- **`raid.level`** (stored 90 for "any level", 9000 when `pokemon_id` is set): v2 reads `null` in both cases, and writing `null` is correct in both cases.
- **`fort.include_empty`**: the v2 default is `true` and the v1 default is `false`. This is a deliberate v2 correction already listed in the changelog. v2 reads `null` when the field is `true`.

### 4.4 During the migration

Until creates move to v2, PoracleWeb.NET needs its own sentinel table for v1 writes. §3 is what makes moving creates to v2 possible. Once reads and writes are both on v2, the table can be deleted rather than generated from our schema.

---

## 5. Bulk update (#325)

Accepted, with changes to the shape.

```
PATCH /api/v2/humans/{id}/tracking/{type}[?profile=N][&dry_run=true][&silent=true][&include_descriptions=true]
{
  "uids": [1, 2, 3],
  "set":  { "distance": 500 }
}
```

`{id}` is the **human**, as in the create path. The rule uids are in the body because this operation acts on many rules at once:

```
POST  /api/v2/humans/123456789/tracking/pokemon     create rules for human 123456789
PATCH /api/v2/humans/123456789/tracking/pokemon     bulk-edit that human's pokemon rules
      { "uids": [12, 40, 41], "set": { "distance": 500 } }
PUT   /api/v2/humans/123456789/tracking/pokemon/12  replace one rule (uid in the path)
```

The human in the path is the ownership scope. A uid that isn't that human's pokemon rule is reported as `not_found` and left alone.

- **uids go in the body**, not the query string. This avoids the length limits they described, and the same applies to bulk delete ([§11](#11-open-questions-for-poracleweb-net), Q4). An explicit list is required, so there is no "every rule of this type". We agree with their own caution: a client that wants every rule sends the uids from a GET.
- **Settable fields:** `distance`, `clean`, `edit`, `summary` (quest only), `template`, `ping`. These are the delivery and "where" settings shared by every type. Filter fields are not bulk-settable: setting `min_iv` on 300 different rules is a different operation and invites mass squashing. Unknown or unsupported fields get a 422 for the whole request.
- **The merge happens in place.** Each rule keeps its uid, using an `UPDATE` of only the named columns rather than delete-and-reinsert. Every field not named stays exactly as it is.
- **Per-rule outcomes, never all-or-nothing:**

```json
{
  "updated":   [{ "uid": 1, "...": "...", "previous": { "...": "..." } }],
  "unchanged": [{ "uid": 2, "...": "..." }],
  "refused":   [{ "uid": 3, "reason": "invalid", "detail": "override_areas and distance are mutually exclusive" },
                { "uid": 4, "reason": "conflict", "detail": "would duplicate rule 9", "conflicts_with": 9 },
                { "uid": 5, "reason": "not_found" }]
}
```

  `reason` is one of `invalid` (the per-rule validation from #211), `conflict` (the squash policy's `Conflicts`, §3.5) or `not_found` (not owned by this human, or not of this type). `detail` is a human-readable message. A client switches on `reason`, not `detail`.
- **One lock.** The whole PATCH runs under the per-human lock (§3.6). It is idempotent, so a PATCH that fails partway is safe to repeat. The squash check sees each rule's post-PATCH state, so two rules in the same PATCH that would collide with each other are reported, and the first in `uids` order wins.
- `dry_run` gives the same response without writing.

The v2 design doc's "No `PATCH`/partial-update in v2" is corrected. It was already out of date, because `PATCH /v2/humans/{id}/profiles/{profile_no}` exists. Tracking PUT stays a full replace, and tracking PATCH is defined only for this bulk settings case.

---

## 6. Typed tracking mutes (#323)

Accepted as a bug. Each tracking table numbers its rules separately, so one user can own raid rule #60 *and* pokemon rule #60. They are different rules that share a number. Today:

1. The user runs `!raid mute id:60`, meaning their raid rule.
2. The bot drops `raid` and stores only `tracking: 60` (`stripMuteTypePrefix`).
3. At match time the matcher compares only the matched rule's uid with 60, never its table.
4. So the user's pokemon rule #60 is silenced too, and nothing tells them.

Mutes are per user, so a collision needs one user to hold the same number in two tables. That is most likely for long-standing users and heavy trackers. The bot's comment says uids are "unique across the user's tracking", which is wrong.

- **`mute.Entry`** gains `RuleType`: the tracking type of the rule. It is `pokemon`, `raid`, `egg` and so on, using the v2 type names. `invasion` and `incident` share one table and one uid space, and both names resolve to it.
- **`webhook.MatchedUser`** gains `RuleType`, set by each matcher next to `RuleUID`. It is the table the rule came from, not the webhook type. One raid webhook goes through either the raid matcher or the egg matcher, and each sets its own type. Quest summary entries record the type alongside their buffered rule uids.
- **Matching:** a tracking mute matches only when both type and uid match. Mutes are in memory and none survive a restart, so there are no legacy untyped entries to migrate.
- **v2 mutes API:** `scope: "tracking"` takes a separate `tracking_type` field rather than encoding it into `value` (`"raid:60"`). It is required for `tracking` and rejected for other scopes. The uid is checked to exist and belong to the human; it currently isn't. GET returns `tracking_type`. DELETE addresses `?scope=tracking&tracking_type=raid&value=60`. This **is a breaking change**: the mutes API shipped in 5.2.1. It is listed under **Changed** for 5.3.0. We accept the break rather than keep an untyped form, because an untyped tracking mute is the bug itself. PoracleWeb.NET does not create per-rule mutes today (#323).
- **Bot:**
  - `!raid mute id:60` keeps the type instead of discarding it.
  - A bare `!mute id:60` looks the uid up across the human's tables. One hit uses that type. Several hits are refused with a list (`id:60 is a raid rule and a pokemon rule — use !raid mute id:60 or !track mute id:60`). No hit is refused.
  - `!tracked` shows `raid rule 60` rather than `tracking:60`.
- **Alert button:** `scope=tracking` stops being a no-op. The snapshot already stores `AlertType` and `TrackingUIDs`, so the button mutes each rule that produced that alert for that user, typed by the alert's table.

---

## 7. Area name operations (#252, #253, #259)

The shapes were agreed on the issues. This section records the additions from PoracleWeb.NET's answer on #259.

- **#252:** the `?all_profiles=` description on `POST …/areas` is corrected to say it is a whole-list replace. Documentation only.
- **#253:** `DELETE /api/v2/humans/{id}/areas?name=<area>[&profile=N|&all_profiles=true]` removes one name from each targeted profile's own list. There is no existence or permission check, because a removal can't widen alerts and the fence is usually already gone.
  - **Also removed from `override_areas`** on that human's rules in the targeted profiles, as part of the same operation. Leaving a dead name would make the rule fail its next write (`area not permitted`), as noted on #259.
  - Rules whose **only** override area was removed now fall back to the human's area list, which may be broader. They are reported:
    ```json
    { "name": "animal kingdom", "removed_from": [1, 2], "broadened": { "pokemon": [12, 40], "raid": [7] } }
    ```
  - `?on_last_override=` is not adopted. Reporting `broadened`, together with `dry_run` for an advance warning, lets the client offer "delete or rescope these rules" with full information, and avoids a server-side policy choice we would have to defend for every client.
- **#259:** `PATCH /api/v2/humans/{id}/areas?from=<old>&to=<new>[&profile=N|&all_profiles=true][&trusted=true]` renames the name everywhere it is held. `to` gets the same checks as set-areas (`userSelectable`/`trusted`, area security, `exemptFromAreaSecurity`). If `to` is not a loaded fence the response is a 422 and nothing changes. Matching `override_areas` entries are renamed in the same operation. A rule that held both names ends up holding `to` once.
- **Consistency without DB transactions (§3.6).** Both operations run under the per-human lock across `humans`, `profiles` and the tracking tables' `override_areas`. Both are idempotent: if one fails partway, the client gets an error and repeats the same request, which finishes the job. Once the first call has run, the old name is no longer held anywhere, so a repeated rename changes nothing that is already done. Both support `dry_run`. #254's `StripOverrideAreas` is reused.
- The existing #252/#253 implementation plan (`docs/superpowers/plans/2026-10-11-v2-set-areas-profile-ops.md`) is superseded where it differs: no DB transaction, plus the override and `broadened` additions. The plan for this branch replaces it.

---

## 8. #243 exemptFromAreaSecurity

PR #254 implements the agreed design. PoracleWeb.NET approved it, and it is merged into this branch so the area operations in §7 build on `PermittedAreas`. The pre-merge check they asked for is still to do, and it happens before the branch goes to `develop`: a production query for community members with no `area_restriction` whose `override_areas` reference fences outside their community's `allowed_areas`. Those rules would start returning 422 on their next write.

**One change to #254 here, for consistency with §3.6.** Its `SQLHumanStore.StripUserAreas` prunes `humans.area` and every profile inside a DB transaction with `SELECT … FOR UPDATE` row locks. That becomes the same model as everything else:
- plain sequential writes under the per-human lock;
- an idempotent prune (it already is: it removes denied names, so a repeat is a no-op);
- in both reconcilers, the community membership is saved **only after** the prune succeeds, so a failed prune is retried on the next cycle instead of being lost.

The prune's behaviour and its tests (mock and real MySQL) are otherwise unchanged. #254's partial-load protection, the denied-set design, is untouched.

**`!restore`** (`bot/commands/restore.go`) is the only other DB transaction in the codebase. It clears and refills every tracking table for a profile from a backup file. That is also idempotent, since repeating a restore produces the same result. It is aligned in the same way: per-human lock, no transaction. A restore that fails partway now leaves a partly restored profile until the admin runs it again; today the transaction rolls back to the old rules. This is listed in the changelog.

---

## 9. Unversioned routes

**Answer: treat them as stable. There are no plans for v2 copies.** The config, geofence, masterdata, geocode, test, summaries and reload routes already run on the same huma instance as v2. They are typed, they appear in the OpenAPI spec at `/openapi.json`, and they return problem+json errors. "v2" names the redesigned tracking and humans resources, which needed a clean break from v1. These routes didn't, and duplicating them would add a migration with no change of contract.

What "stable" means, now written into `docs/v2-api-design.md`:
- Additive changes (new fields, new optional parameters) can arrive in any release.
- A breaking change to one of these routes is listed under **Changed** in the changelog for that release, with a migration note. Where practical it is preceded by a release that accepts both shapes.
- v1 tracking, humans and profiles routes stay frozen, as before.

**What is not on huma.** Every route under `/api` is on huma except the frozen v1 tracking, humans and profiles routes. Each of those has a v2 equivalent, and they stay on gin by design. Outside `/api`:

| Route | Decision |
|---|---|
| `GET /health` | **Documented in the OpenAPI spec.** Clients use it for `version` and `capabilities` (feature detection), and it matters more once releases are tagged (§10). It stays outside `/api`, unauthenticated, with its response shape unchanged. It is added to the spec as an operation with no security requirement. |
| `POST /` (Golbat webhook receiver) | Stays on gin. It is scanner input, not a client API. |
| `/metrics`, `/debug/pprof` | Stay on gin. They are operational endpoints, not API. |
| `/openapi.json`, `/docs` | Stay on gin. They serve the spec itself. |

Small fixes found while checking:
- **The 401 from the secret check** is still the legacy `{"status":"authError"}` body. It becomes problem+json for huma-served routes (all of the above, and `/api/v2`). The frozen v1 gin routes keep the legacy body.
- **The spec has no `servers` entry**, so its paths appear without the `/api` prefix. `servers: [{ "url": "/api" }]` is added ([§11](#11-open-questions-for-poracleweb-net), Q5, in case it affects their code generation).
- **Stale references** are removed: the design doc names `/v2/summaries` (the path is `/summaries`), and `main.go` comments say test and tile routes "stay on gin" (they moved to huma).

## 10. Releases and versioning

**Answer: yes.** The version already exists (`processor/version.go`), and the Docker workflow already publishes semver image tags from `v*` git tags. What has been missing is the tags themselves.

- **`v5.2.1`** is tagged retroactively on the `main` commit that carries it.
- **#230 and #303 are on `develop` and ship in 5.3.0.** This branch is intended to ship in 5.3.0 as well, so its breaking changes arrive together with #303's: the mutes `tracking_type`, the 401 shape on huma routes, and PUT refusing near-duplicates.
- From 5.3.0 on, each release is a `vX.Y.Z` tag plus a GitHub Release whose notes are that version's CHANGELOG section. `[Unreleased]` is renamed on release.
- `/health` already returns `version`, so a client can check what it is talking to.

## 11. Open questions for PoracleWeb.NET

1. **§3 instead of `on_match=refuse`:** does `dry_run` + `previous` give your Add flow what it needs? Is there a case where only a refusal would do?
2. **§3.5:** PUT and PATCH refuse an edit that collides with another rule, rather than merging it. Does that suit your edit screens?
3. **§4:** with the round-trip guarantee and schema bounds, is there anything left that needs the stored value once reads and creates are on v2?
4. **§5:** should bulk DELETE also take a body uid list (as `POST …/tracking/{type}/delete`, mirroring v1), for the same length reason?
5. **§9:** would adding `servers: [{url: "/api"}]` to the spec break your client generation?
6. **§6:** is a separate `tracking_type` field acceptable, rather than `value: "raid:60"`?

## 12. Out of scope (recorded)

- Set-based squashing ([Appendix A](#appendix-a-set-based-squashing-dominance-for-debate)): designed for debate, not built.
- Choosing which rule wins when overlapping rules have different settings. Today it is the first match; most-specific-wins is a candidate.
- A read-only report of existing rule sets that already break the squash invariant, and an opt-in cleanup.
- DB transactions of any kind. Consistency is held in the application by design (§3.6).
- Recovering the single rule a failure can lose between delete and insert in a tracking replace (§3.6).
- v1 maxbattle diffing (v1 is frozen).

## 13. Implementation outline

A detailed task plan follows comment on this document. The intended order, smallest blast radius first:

1. The per-human write lock (§3.6), wired into every write path: tracking (bot, v1, v2), humans, profiles and areas, and reconciliation. Then #254's `StripUserAreas` and `!restore` move off DB transactions, with reconciliation saving membership only after a prune succeeds (§8).
2. Field tables per type (§4.2.4) and the exhaustive round-trip test (§4.2.1).
3. `SquashPolicy` + scenario table, the batch-claim fix, maxbattle tags (§3.5, §3.7). No change in behaviour beyond the listed fixes.
4. `previous` and `dry_run` on the existing v2 tracking mutations (§3.4), then the PUT conflict check (§3.5).
5. Bulk PATCH (§5).
6. Typed mutes (§6).
7. Area DELETE/PATCH with override handling and `broadened` (§7).
8. Route fixes, `/health` in the spec, and the stability statement (§9). Then the changelog, API.md and the golden spec.

---

## Appendix A: Set-based squashing (dominance), for debate

The row heuristic is a stand-in for "this rule adds nothing, or contradicts another". The two can be told apart directly.

A rule is a box in filter space. Every filter field is one of three kinds:
- a **range** (IV, CP, level, weight, stats, PVP rank);
- an **any-or-value** field (form 0, gender 0, team 4, move 9000, `pokemon_id` 0 = everything);
- a **where** field (the same anchor with a smaller radius, or a subset of areas).

Whether one box contains another can be decided field by field. A rule is **dominated** when its match set lies inside another rule's in the same profile and template, clean, edit and ping are equal. A dominated rule can never change what the user receives.

**Invariant:** after any write, no rule in a profile is dominated. The table below compares the two models on some cases:

| Existing → submitted | Row heuristic | Dominance |
|---|---|---|
| IV90 → IV80 | replace ✓ | submitted dominates existing → replace ✓ |
| IV90 → IV95 | replace ✓ | submitted is dominated → **redundant** (needs intent; see below) |
| IV90 / 1000 m → IV95 / 500 m | **second, overlapping rule** ✗ | dominated → redundant ✓ |
| IV100 anywhere + IV80 within 500 m | two rules ✓ | neither dominates → two rules ✓ |

Dominance is symmetric and order-independent, and it catches the case where two fields changed. Where it disagrees with the heuristic is *tightening*: on its own, IV95 next to IV90 is redundant, but the user meant "replace". That suggests separating two things the current code merges:
- **the invariant** (no dominated rules), enforced on every surface;
- **intent**: in the bot, "replace the closest rule"; in the API, explicit (POST adds; PUT and PATCH edit a named uid).

Open points before this could be built:
- Fields that aren't ranges, like PVP league and cap (probably a match key), `rsvp_changes`, gym `slot_changes`/`battle_changes`, and the unencountered-pokemon skip.
- Whether overlap with *different* settings is a warning, a refusal, or fixed by most-specific-wins.
- What to do with existing rule sets that break the invariant.

The `SquashPolicy` boundary in §3.5 exists so this can be added later as a new policy, verified against the same scenario table.
