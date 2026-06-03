# PoracleNG v2 API — Design

**Status:** Draft (for implementor review)
**Date:** 2026-06-01
**Branch:** `huma-api-migration` (worktree)

> The **[API Shape](#api-shape-for-implementor-review)** section below is written to be extracted verbatim into a GitHub issue for third-party implementor (ReactMap, PoracleWeb, custom clients) comment before we build. Everything outside that section is internal rationale and open decisions.

---

## 1. Why v2 (and why freeze v1)

The existing `/api/*` surface is undocumented, accreted, and tolerant of malformed input by necessity (the `flexBool`/`flexInt` coercion exists because real clients send wrong types). An attempt to retrofit OpenAPI docs onto it in place meant paying two costs on every endpoint — faithfully reproducing v1's quirks **and** cleaning up the representation — while still mutating v1's contract.

Decision: build a **clean, strict, documented v2** surface and **freeze v1** untouched for existing clients. v1 keeps working exactly as today; clients migrate to v2 on their own schedule. We will encourage all users of the v1 API to move to v2 so they can access new tracking types; v1 is deprecated-but-supported.

v2 is a **clean HTTP facade over the same store/matcher/business logic** — no domain rewrite. Where v2 exposes richer or cleaner inputs than the engine stores natively, the v2 handler translates them down to the existing stored representation (see Invasion/Incident).

## 2. Principles

- **Strict, not lenient.** Proper types, `additionalProperties: false`, required fields enforced, no silent coercion. A malformed request gets a clear `422`, not a guess. (v1 stays lenient for legacy clients.)
- **Game-master dictionary values are integers.** Any value that is a masterfile / proto ID whose set grows with the game stays an `int` (`pokemon_id`, `form`, `move`, `reward_type`, `lure_id`, invasion `type_id`/`grunt_id`, incident `display_type`). We do **not** stringify these.
- **Fixed Poracle/UI categories are string enums.** Small, stable, human-named sets read better as words (`team`, `gender`, `fort_type`, `rsvp_changes`).
- **One honest representation per field.** No bitmask packed into one field on the wire (`clean` → `clean`/`edit`/`summary` booleans); no enum hidden as a magic int where it's really a named category.
- **Resources keyed by `uid`.** A tracking rule's `uid` is unique per type across all users, so a rule is addressable as `/tracking/{type}/{uid}` without the owning user in the path.
- **OpenAPI 3.1 is the contract.** Generated from the code (huma), served publicly; the spec is the source of truth.

---

## API Shape (for implementor review)

> **This section is the RFC.** It describes the proposed PoracleNG v2 HTTP API. Feedback wanted on: resource shapes, field naming/types, the invasion/incident split, and anything that would make integration harder. v1 is unaffected by anything here.

### Base, versioning, auth

- Base path: `/api/v2`. The existing `/api/*` (v1) is unchanged and remains available.
- Auth: `X-Poracle-Secret: <secret>` request header (same secret as v1). Unauthenticated requests get `401`.
- Docs: OpenAPI spec at `GET /api/v2/openapi.json`; interactive docs at `GET /api/v2/docs` (both public, no secret).

### Errors (RFC 9457 `application/problem+json`)

All errors use a standard problem document:

```json
{
  "title": "Unprocessable Entity",
  "status": 422,
  "detail": "validation failed",
  "errors": [
    { "message": "expected integer", "location": "body.rules[0].min_iv", "value": "ninety" }
  ]
}
```

No `{ "status": "ok" }` envelope on success — success responses are the typed body directly.

### Resource model

Tracking rules are **sub-resources of the human** (the human *is* the user). `uid` is unique per type, and every item operation is **scoped by `(human, uid)`** — the ownership guard v1 enforces (`WHERE id=? AND uid=?`); you cannot touch a uid that isn't the addressed human's. `{type}` ∈ `pokemon`, `raid`, `egg`, `quest`, `invasion`, `incident`, `lure`, `nest`, `gym`, `fort`, `maxbattle`.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v2/humans/{id}/tracking` | **Full snapshot** — human + all-type rules + profiles + locations + summaries |
| `GET` | `/api/v2/humans/{id}/tracking/{type}` | List one type |
| `POST` | `/api/v2/humans/{id}/tracking/{type}` | Create rule(s) |
| `GET` | `/api/v2/humans/{id}/tracking/{type}/{uid}` | Fetch one rule |
| `PUT` | `/api/v2/humans/{id}/tracking/{type}/{uid}` | Full-replace one rule |
| `DELETE` | `/api/v2/humans/{id}/tracking/{type}/{uid}` | Delete one rule |
| `DELETE` | `/api/v2/humans/{id}/tracking/{type}?uid=1,2,3` | Bulk delete |

- `{id}` (the human) is **always in the path** — required, and the ownership scope for every op. `profile` is a query param (`?profile={n}`, defaults to the human's active profile).
- **Create** body is an **array** of rule objects (a single rule is a one-element array); the owner is the path `{id}`, not repeated per rule.
- **List one type** returns `{ "rules": [ <rule>, … ] }`. **Full snapshot** (`…/tracking`, no `{type}`) returns `{ "human": {…}, "tracking": { "pokemon": [...], "raid": [...], … }, "profiles": [...], "locations": [...], "summaries": [...] }` — replaces v1's `all/{id}`; `?all_profiles=true` spans every profile (v1's `allProfiles/{id}`).
- **Create** returns `{ "created": [<rule with uid>], "updated": [<rule>], "unchanged": [<rule>], "message": "<assembled summary>" }` (POST keeps v1's diff/upsert behaviour).
- **Mutation responses return the assembled `message`** — the human-readable added/updated/removed summary (translated status prefixes, in the human's language) — always, independent of `?silent` (which only suppresses the Discord/Telegram push). Applies to `POST`, `PUT`, `DELETE`, bulk. The rule objects are returned for their `uid`s/fields but **without** a per-rule `description` on mutations (the rowtext lives once, inside `message`). For structured per-rule text, do a read with `?include_descriptions`.
- **PUT** is a **full replace**: the body fully specifies the rule's filter fields; omitted fields reset to documented defaults. (No `PATCH`/partial-update in v2.)
- **`?include_descriptions=true`** (on list + snapshot) adds the human-readable rowtext per rule (v1 parity for PoracleWeb).
- Mutations (`POST`/`PUT`/`DELETE`) accept **`?silent=true`** (default false) — apply without notifying the user. (Single param; replaces v1's `silent`+`suppressMessage`.)
- Unknown body **and** query params are rejected (`422`) — v2 is strict. Every rule carries its `uid` (int) in responses.
- **Consistency note:** humans/profiles/locations are likewise under `/api/v2/humans/{id}/…` (see §2b), so everything user-scoped shares one prefix.

### Field conventions

- `snake_case` field names (familiar, matches the data model).
- Integers for game-master IDs and numeric ranges; booleans for flags; string enums for fixed categories; strings for free text/ids.
- All filter fields are **optional with documented defaults** unless marked **required**. Omitting a range field means "no constraint" at its documented default.

### Common fields (most tracking types)

| field | type | notes |
|---|---|---|
| `uid` | int | response/identifier; required in `PUT` body |
| `distance` | int | metres; `0` = use the profile's areas instead of a radius |
| `template` | string | template name; empty = server default |
| `clean` | bool | auto-delete the alert on expiry |
| `edit` | bool | keep the message updated in place |
| `summary` | bool | route into the summary digest (where supported) |
| `ping` | string | mention string appended to the alert |
| `override_location_label` | string? | use a saved named location instead of the profile location |
| `override_areas` | string[] | restrict this rule to these geofence areas |

(`clean`/`edit`/`summary` map to the stored `clean` bitmask: bit 1 / 2 / 4.)

### Per-type fields

**pokemon** — `pokemon_id`* (int), `form` (int), `min_iv`/`max_iv` (int), `min_cp`/`max_cp` (int), `min_level`/`max_level` (int), `atk`/`def`/`sta` & `max_atk`/`max_def`/`max_sta` (int, 0–15), `gender` (enum `any|male|female|genderless`), `rarity`/`max_rarity` (int), `size`/`max_size` (int), `pvp_ranking_league` (int — the CP cap: `0|500|1500|2500`), `pvp_ranking_best`/`pvp_ranking_worst` (int), `pvp_ranking_min_cp` (int), `pvp_ranking_cap` (int), `pvp_ranking_evolution` (int — mega/temporary-evolution discriminator: `0`=default/any, `2`=Mega X, `3`=Mega Y; **prospective — from the `pvp-mega-evolution` PR**).

**raid** — `pokemon_id` (int, `0` = any), `form` (int), `level` (int), `team` (enum `harmony|mystic|valor|instinct|any`), `exclusive` (bool), `move` (int), `evolution` (int), `gym_id` (string), `rsvp_changes` (enum `none|rsvp|rsvp_only`).

**egg** — `level` (int), `team` (enum), `exclusive` (bool), `gym_id` (string), `rsvp_changes` (enum).

**quest** — `reward_type`* (int — proto id: `2`=item, `3`=stardust, `4`=candy, `7`=pokemon, `12`=mega_energy), `reward` (int — the rewarded item/pokemon id), `amount` (int), `form` (int — for pokemon-reward forms), `shiny` (bool).

**invasion** (Rocket grunts) — target via **exactly one** mode per rule: `type_id` (int, grunt poke-type — `gender` (enum `any|male|female`) applies only here) | `grunt_id` (int, the exact grunt character, implies type+gender) | `everything` (bool) | `boss` (bool).

**incident** (events) — `display_type`* (int — game `PokestopEvent` id, e.g. `9` = Showcase; names documented in the field description).

**lure** — `lure_id` (int — game item id: `0`=any, `501`=normal, `502`=glacial, `503`=mossy, `504`=magnetic, `505`=rainy, `506`=sparkly).

**nest** — `pokemon_id` (int), `form` (int), `min_spawn_avg` (int).

**gym** — `team` (enum), `slot_changes` (bool), `battle_changes` (bool), `gym_id` (string).

**fort** — `fort_type` (enum `pokestop|gym|everything`), `include_empty` (bool, **default `true`**), `change_types` (string[] of `location|new|removal|image_url|name|description`).

**maxbattle** — `pokemon_id` (int), `level` (int), `gmax` (bool), `move` (int).

\* = required.

### Examples

Create two pokemon rules for a human:
```
POST /api/v2/humans/123456/tracking/pokemon?profile=1
[
  { "pokemon_id": 149, "min_iv": 95, "gender": "female", "clean": true },
  { "pokemon_id": 384, "pvp_ranking_league": 1500, "pvp_ranking_best": 1, "pvp_ranking_worst": 5, "edit": true }
]
```
Track a specific grunt by character id, and (separately) any female grass grunt:
```
POST /api/v2/humans/123456/tracking/invasion?profile=1
[ { "grunt_id": 41 }, { "type_id": 12, "gender": "female" } ]
```
Track Showcase incidents:
```
POST /api/v2/humans/123456/tracking/incident?profile=1
[ { "display_type": 9 } ]
```
Full snapshot (tracking + profiles + locations + summaries):
```
GET /api/v2/humans/123456/tracking?include_descriptions=true
```
Delete a rule (scoped to this human):
```
DELETE /api/v2/humans/123456/tracking/raid/80921
```

### Questions for implementors

1. Resource shape: is `?user=&profile=` on the collection comfortable, or would you prefer `/api/v2/users/{id}/tracking/{type}`?
2. Create response: is `{created, updated, unchanged}` useful, or do you only want the resulting rules?
3. Enum-as-string vs id-as-int split (above): does it match how you think about these fields?
4. Invasion two-axis model (`type_id` vs `grunt_id`) and the separate `incident` type — does this fit your use cases?
5. Anything in v1 you rely on that isn't represented here?

---

## 2b. Humans, profiles & shared schemas (v2)

humans/profiles v2 uses **discrete, typed action endpoints** under `/api/v2` (not PATCH-consolidated), mirroring v1's actions with proper types + strict bodies + `problem+json`. Endpoint list is in the master plan (P4). The schemas that previously had no real definition are pinned here.

### `active_hours` (profile schedules **and** summary posting) — proper typed schema

Today this is stored as freeform JSON and the API dumps whatever the client sends into the column. v2 defines and **validates** it. It is an **array of schedule entries** (`[]` or absent = no schedule). Each entry (derived from `db.ActiveHourEntry`):

| field | type | required | bounds |
|---|---|---|---|
| `day` | int | yes | `0`–`6` (0 = Sunday) |
| `hours` | int | yes | `0`–`23` |
| `mins` | int | yes | `0`–`59` |
| `step` | int | no | `≥ 0` hours; `> 0` ⇒ this is a **range** entry, else **single-fire** |
| `end_hours` | int | required iff `step > 0` | `0`–`23` |
| `end_mins` | int | required iff `step > 0` | `0`–`59` |

- **Single-fire**: `{day, hours, mins}` → fires once that day at `HH:MM`.
- **Range**: adds `{step, end_hours, end_mins}` → fires at `HH:MM`, `+step h`, … up to and including `end`. **No cross-midnight** — `end` must be ≥ start (reject otherwise, `422`).
- v2 is **strict ints** (no `"00"` string coercion — that was the v1 leniency) with the bounds above. Same schema is shared by `POST /v2/summaries/{id}/{alertType}` and the profile-schedule update endpoint. (Confirm `day` indexing against the scheduler at build: comment indicates `0 = Sunday`, matching Go `time.Weekday`.)

### `blocked_alerts` (read-only on the human resource)

`[]string`, **derived from `command_security` during reconciliation — not settable via the API**. Appears in `GET /v2/humans/{id}`. Enum values: `monster` (= pokemon alerts), `pvp`, `raid`, `egg`, `quest`, `invasion`, `lure`, `nest`, `gym`, `fort`, `maxbattle`, `specificgym`, `specificstation`. (Note the `monster`↔pokemon token mismatch is a v1 carry-over; documented, not "fixed," since it's an internal-derived read field.)

### Saved locations — full CRUD (one **new** capability)

A saved-locations API already exists (`user_locations`: `label` → `lat`/`lon`), but only **C/R/D** — there is no update. v2 completes CRUD:

| method | path | body | note |
|---|---|---|---|
| GET | `/v2/humans/{id}/locations` | — | list |
| GET | `/v2/humans/{id}/locations/{label}` | — | one |
| POST | `/v2/humans/{id}/locations` | `{label, lat, lon}` | create (was `…/locations/add`) |
| **PUT** | `/v2/humans/{id}/locations/{label}` | `{lat, lon}` | **NEW** — update a saved location's coordinates |
| DELETE | `/v2/humans/{id}/locations/{label}` | — | delete (`409` if referenced by a rule's `override_location_label`) |

The **PUT** is net-new functionality (v1 forces delete+re-add to move a saved location).

## 3. Internal: mapping to the engine (facade)

v2 handlers translate clean inputs to the existing stored representation; the matcher and DB schema are unchanged:

- **Enums** (`team`, `gender`, `fort_type`, `rsvp_changes`): v2 accepts the string, stores the existing int/string the column holds (name↔value maps from the field audit).
- **Invasion**: `type_id` → stored grunt-type name; `grunt_id` → resolve to its (type, gender) and store that; `everything`/`boss` → the existing catch-all names. `incident.display_type` → resolve to the event name the matcher already matches on.
- **`clean`/`edit`/`summary`** → collapse to the stored `clean` bitmask.
- **`reward_type`/`lure_id`** → stored as the integer they already are.

No changes to `internal/matching/*` or the DB schema in v2 scope.

## 4. Disposition of the in-place migration work

Done on this branch for the (now-superseded) in-place approach; triage:
- **Reuse for v2:** huma setup/constructor + public docs; the **field audit** (`huma-tracking-field-audit.md`); the enum value maps; huma mechanics learned (`$schema` suppression, schema-provider gotchas).
- **Drop (v1-compat only):** legacy `{status,message}` error override, `flexBool`/`flexInt` lenient coercion, `lenient[T]` + `additionalProperties:true`, single-object-or-array body, the temporary lint exclusion.
- **Revert:** restore v1's original gin routes for pokemon (GET/POST/DELETE/bulk) removed from `main.go`, so v1 is byte-for-byte its old self.

## 5. Open decisions

- [ ] **List/Create response shapes** — `{rules:[…]}` vs bare array; `{created,updated,unchanged}` vs just the rules. (Proposed above; confirm.)
- [ ] **Collection scoping** — `?user=&profile=` vs `/users/{id}/tracking/{type}`. (Proposed query; confirm.)
- [ ] **humans & profiles v2 shape** — not yet designed; tracking first. (humans: registration/areas/locations/profile switch; profiles: CRUD.) Separate design pass.
- [ ] **Pagination/filtering** on list endpoints — out of scope for v1 parity, but the `{rules:[…]}` wrapper reserves room.

**Decided (no longer open):**
- **Update verb** — `PUT` full-replace only; no `PATCH` in v2 scope.
- **Strictness** — strict throughout: unknown body and query params both rejected (`422`).
- **v1 deprecation** — v1 stays fully supported with no sunset date yet; add a `Deprecation` marker + link to v2 on v1 responses once v2 is established; set a hard sunset date later.
- **quest / nest / invasion fields** — quest (`reward_type`,`reward`,`amount`,`form`,`shiny`), nest `min_spawn_avg` = int, invasion exactly-one-mode (`type_id`|`grunt_id`|`everything`|`boss`).

## 6. Remaining design walkthrough

The per-type field tables above are derived by applying the agreed classification to the field audit. Still to confirm interactively: the `quest` reward fields, `nest.min_spawn_avg` type/precision, and the humans/profiles surface. Everything else is considered decided pending implementor feedback from the GitHub issue.
