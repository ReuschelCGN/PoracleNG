# PoracleNG v2 API — Design

**Status:** Draft (for implementor review)
**Date:** 2026-06-01
**Branch:** `huma-api-migration` (worktree)

> The **[API Shape](#api-shape-for-implementor-review)** section below is written to be extracted verbatim into a GitHub issue for third-party implementor (ReactMap, PoracleWeb, custom clients) comment before we build. Everything outside that section is internal rationale and open decisions.

---

## 1. Why v2 (and why freeze v1)

The existing `/api/*` surface is undocumented, accreted, and tolerant of malformed input by necessity (the `flexBool`/`flexInt` coercion exists because real clients send wrong types). An attempt to retrofit OpenAPI docs onto it in place meant paying two costs on every endpoint — faithfully reproducing v1's quirks **and** cleaning up the representation — while still mutating v1's contract.

Decision: build a **clean, strict, documented v2** surface and **freeze v1** untouched for existing clients. v1 keeps working exactly as today; clients migrate to v2 on their own schedule. PoracleWeb will move to v2; v1 is deprecated-but-supported.

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

`{type}` ∈ `pokemon`, `raid`, `egg`, `quest`, `invasion`, `incident`, `lure`, `nest`, `gym`, `fort`, `maxbattle`.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v2/tracking/{type}?user={id}&profile={n}` | List a user's rules of this type |
| `POST` | `/api/v2/tracking/{type}?user={id}&profile={n}` | Create rule(s) for that user/profile |
| `GET` | `/api/v2/tracking/{type}/{uid}` | Fetch one rule by global uid |
| `PUT` | `/api/v2/tracking/{type}/{uid}` | Replace one rule |
| `DELETE` | `/api/v2/tracking/{type}/{uid}` | Delete one rule |
| `DELETE` | `/api/v2/tracking/{type}?uid=1,2,3` | Bulk delete |

- **Create** body is an **array** of rule objects (bulk is the common case; a single rule is a one-element array). `user`/`profile` come from the query (one owner per request), not repeated in each rule.
- **List** returns `{ "rules": [ <rule>, ... ] }` (object wrapper leaves room for pagination metadata later).
- **Create** returns `{ "created": [<rule with uid>], "updated": [<rule>], "unchanged": [<rule>] }`.
- Every rule object carries its `uid` (int) in responses.

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

**pokemon** — `pokemon_id`* (int), `form` (int), `min_iv`/`max_iv` (int), `min_cp`/`max_cp` (int), `min_level`/`max_level` (int), `atk`/`def`/`sta` & `max_atk`/`max_def`/`max_sta` (int, 0–15), `gender` (enum `any|male|female|genderless`), `min_weight`/`max_weight` (int), `rarity`/`max_rarity` (int), `size`/`max_size` (int), `pvp_ranking_league` (int — the CP cap: `0|500|1500|2500`), `pvp_ranking_best`/`pvp_ranking_worst` (int), `pvp_ranking_min_cp` (int), `pvp_ranking_cap` (int).

**raid** — `pokemon_id` (int, `0` = any), `form` (int), `level` (int), `team` (enum `harmony|mystic|valor|instinct|any`), `exclusive` (bool), `move` (int), `evolution` (int), `gym_id` (string), `rsvp_changes` (enum `none|rsvp|rsvp_only`).

**egg** — `level` (int), `team` (enum), `exclusive` (bool), `gym_id` (string), `rsvp_changes` (enum).

**quest** — `reward_type`* (int — proto id: `2`=item, `3`=stardust, `4`=candy, `7`=pokemon, `12`=mega_energy), `reward` (int — the rewarded item/pokemon id), `amount` (int), `shiny` (bool). *(reward field set to be confirmed against the quest handler.)*

**invasion** (Rocket grunts) — target by **either** axis: `type_id` (int, grunt poke-type) [+ `gender` (enum `any|male|female`)], **or** `grunt_id` (int, the exact grunt character — implies type+gender). Plus `everything` (bool) and `boss` (bool) catch-alls.

**incident** (events) — `display_type`* (int — game `PokestopEvent` id, e.g. `9` = Showcase; names documented in the field description).

**lure** — `lure_id` (int — game item id: `0`=any, `501`=normal, `502`=glacial, `503`=mossy, `504`=magnetic, `505`=rainy, `506`=sparkly).

**nest** — `pokemon_id` (int), `form` (int), `min_spawn_avg` (number).

**gym** — `team` (enum), `slot_changes` (bool), `battle_changes` (bool), `gym_id` (string).

**fort** — `fort_type` (enum `pokestop|gym|everything`), `include_empty` (bool, **default `true`**), `change_types` (string[] of `location|new|removal|image_url|name|description`).

**maxbattle** — `pokemon_id` (int), `level` (int), `gmax` (bool), `move` (int).

\* = required.

### Examples

Create two pokemon rules for a user:
```
POST /api/v2/tracking/pokemon?user=123456&profile=1
[
  { "pokemon_id": 149, "min_iv": 95, "gender": "female", "clean": true },
  { "pokemon_id": 384, "pvp_ranking_league": 1500, "pvp_ranking_best": 1, "pvp_ranking_worst": 5, "edit": true }
]
```
Track a specific grunt by character id, and (separately) any female grass grunt:
```
POST /api/v2/tracking/invasion?user=123456&profile=1
[ { "grunt_id": 41 }, { "type_id": 12, "gender": "female" } ]
```
Track Showcase incidents:
```
POST /api/v2/tracking/incident?user=123456&profile=1
[ { "display_type": 9 } ]
```
Delete a rule by global uid:
```
DELETE /api/v2/tracking/raid/80921
```

### Questions for implementors

1. Resource shape: is `?user=&profile=` on the collection comfortable, or would you prefer `/api/v2/users/{id}/tracking/{type}`?
2. Create response: is `{created, updated, unchanged}` useful, or do you only want the resulting rules?
3. Enum-as-string vs id-as-int split (above): does it match how you think about these fields?
4. Invasion two-axis model (`type_id` vs `grunt_id`) and the separate `incident` type — does this fit your use cases?
5. Anything in v1 you rely on that isn't represented here?

---

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
- [ ] **quest** `reward`/`amount` fields — confirm exact fields against the quest handler.
- [ ] **PUT semantics** — full replace vs partial update (PATCH). Proposed: `PUT` = full replace of the rule's filter fields.
- [ ] **`everything`/`boss`** on invasion — booleans, or fold into the model differently?
- [ ] **humans & profiles v2 shape** — not yet designed; tracking first. (humans: registration/areas/locations/profile switch; profiles: CRUD.) Separate design pass.
- [ ] **Validation strictness vs unknown fields** — strict `additionalProperties:false` confirmed; confirm we want unknown query params rejected too.
- [ ] **Client transition** — deprecation header/sunset policy on v1? Timeline for PoracleWeb cutover?
- [ ] **Pagination/filtering** on list endpoints — out of scope for v1 parity, but the `{rules:[…]}` wrapper reserves room.

## 6. Remaining design walkthrough

The per-type field tables above are derived by applying the agreed classification to the field audit. Still to confirm interactively: the `quest` reward fields, `nest.min_spawn_avg` type/precision, and the humans/profiles surface. Everything else is considered decided pending implementor feedback from the GitHub issue.
