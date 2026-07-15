# Pokémon Costume Tracking — Design

Status: draft / for review
Date: 2026-07-15

## Summary

Add **costume** as a first-class dimension of Pokémon tracking: filter tracking
rules by costume, resolve costume names in commands, surface a Pokémon's
recently-seen costumes (and a global costume list) in `!info`, weave the costume
into the displayed `fullName`, and expose it on the v2 Pokémon API.

## Background

Costumed Pokémon (e.g. costumed Pikachu) are spawning heavily. In the webhook,
**costume is independent of form** — the observed Pikachu are all `form: 598`
("Normal") with `costume` varying (`1` = Holiday 2016, `8` = …). So form-based
tracking cannot distinguish or exclude them; a dedicated costume filter is needed.

Two real operator needs:
1. Track a **specific** costume (e.g. only Holiday-2016 Pikachu).
2. **Exclude** costumes (track normal Pikachu without the costume-spawn flood).

## Data model (verified)

- **`resources/rawdata/costumes.json`** — 87 entries `{id, name, proto, noEvolve}`
  (`0` = "Unset"/no costume, `1` = "Holiday 2016", …). This is the master list.
- **`costume_{id}` gamelocale keys** (87) — translated costume names.
- The webhook `costume` int is already parsed (`webhook/types.go`) and exposed as
  a raw `costume` field in enrichment; it is already used in the icon URL. It is
  present even **pre-encounter** (`seen_type: wild` carries it), so costume
  filtering works regardless of the encounter-only-stat-skip rule.
- `fullName` does **not** currently include the costume — `buildFullName`
  (`enrichment/translate.go:82`) composes base name + form + mega, and
  `BuildFullNameWithAlignment` adds the alignment prefix.

## Decisions (from design discussion)

| # | Decision |
|---|----------|
| D1 | **Filter sentinel:** the `monsters.costume` column uses **9000 = any** (the existing `bot.WildcardID`), **0 = explicitly no costume**, **N = that costume**. Default 9000 keeps existing rules unchanged; `costume:0` expresses "no costume" to dodge the spam. (Deliberately different from `form`, where 0 = any — because costume 0 is a meaningful "no costume" webhook value.) |
| D2 | **Costume is an independent filter from form** — both apply in the matcher. |
| D3 | **Command syntax:** `costume:<name-or-id>`, name resolved via the existing multi-word vocabulary + underscore-substitution (same path as items/moves/forms) — `costume:holiday_2016`, eager-joined `costume:holiday 2016`, or `costume:1`. `costume:0` = no costume. |
| D4 | **`!info costumes`** lists all costumes (global reference). **`!info <pokemon>`** shows a **recently-seen** costume list for that species, sourced from a `RecentActivity` map (mirrors the slash-autocomplete recency mechanism). |
| D5 | **Display:** weave the costume into `fullName`, **parenthesised** — `Pikachu (Holiday 2016)` — so every existing `{{fullName}}` template shows it with no edits. Applied to the **spawn's** name only, not PVP/evolution ranking entries. Also add a standalone `costumeName` field. No default-template change required. |
| D6 | **v2 Pokémon API** gains a nullable `Costume *int` mirroring `Form` (omit/null = wildcard 9000). |

## Components

### 1. Gamedata — load `costumes.json`
Add `Costumes map[int]CostumeInfo` (`{ID, Name, Proto, NoEvolve}`) to the game
data, loaded from `resources/rawdata/costumes.json` at startup (mirror the
existing rawdata loaders). Display names come from `costume_{id}` translations;
`costumes.json` is the id enumeration (+ `noEvolve` for future evolution logic).
A `CostumeTranslationKey(id) → "costume_{id}"` helper alongside the existing
`FormTranslationKey` etc.

### 2. DB storage
- Migration `0000NN_add_monster_costume.{up,down}.sql`:
  `ALTER TABLE monsters ADD COLUMN costume INT NOT NULL DEFAULT 9000;`
- `db.MonsterTracking` gains `Costume int \`db:"costume"\``; `MonsterTrackingAPI`
  gains `Costume flexInt` (lenient clients). Default 9000 in the API struct.

### 3. Matcher (`matching/pokemon.go`)
In `matchMonsters`, alongside the existing form check:
```
if rule.Costume != 9000 && rule.Costume != webhook.Costume { continue }
```
`MonsterData` / the parsed pokemon already carry the webhook `Costume`; thread it
into `matchMonsters` next to `Form`. 9000 = any; any other value (incl. 0) is an
exact match. Independent of the form check.

### 4. Enrichment / display
- Thread the webhook `costume` into `translateNames` → `buildFullName`. When
  `costume > 0`, append the translated `costume_{id}` name **parenthesised**
  after the base+form+mega composition: `"<name> (<costumeName>)"`. Do the same
  for `fullNameEng`. **Do not** apply to the PVP/evolution ranking entries
  (`pokemon.go` PVP path) — those are hypothetical rank rows, not the costumed
  spawn.
- Add `m["costumeName"]` (per-language, via `costume_{id}`); keep the raw
  `costume` id.
- Register `costumeName` in the monster DTS field metadata (`dts_fields.go`).

### 5. Commands (`!` and `/`)
- **Argmatcher:** add a `costume` prefix param (`arg.prefix.costume`) and a
  costume multi-word vocabulary (like items/moves) so `costume:<name>` resolves
  via `costume_{id}` (user lang + English fallback) → id. `costume:0` and
  `costume:<id>` accepted directly.
- **`!track` / `!untrack`:** parse the costume arg, store the id (default 9000),
  include it in the diff/insert. Removal by costume supported.
- **Slash `/track`:** add a `costume` option with **name autocomplete** (labels =
  costume names, resolves to id). Mapper emits `costume:<id>`.
- **`!tracked` line / rowtext (required):** the monster rule description
  rendered by `rowtext` — used by `!tracked`, the tracking-API responses, and the
  command confirmation — **must** include the costume: the translated costume
  name for a specific costume, "no costume" for `0`, and nothing at wildcard
  (9000). Without this a costume rule is indistinguishable from a normal rule in
  `!tracked`.

### 6. `!info`
- **`!info costumes`** — new subcommand (`msg.info.sub.costumes`): list all
  costumes (`id — name`) from `costumes.json` / `costume_{id}`.
- **`!info <pokemon>`** — new "recently-seen costumes" section mirroring
  `availableForms` (`info.go:403`), listing `id — name` from
  `RecentActivity.RecentCostumes(pokemonID)`. Guides the operator to
  `costume:<id>`.

### 7. RecentActivity (`tracker/recent_activity.go`)
Reuse the **shared** `RecentActivity` (the same instance the slash-command
autocomplete and `bot/command.go` already use — always constructed at startup,
not gated on slash). Chosen deliberately: it's the project's common recency model.

Two extensions beyond the existing flat `map[int]time.Time` categories:
- **Two-level key:** add `costumesByPokemon map[int]map[int]time.Time`
  (pokémon id → costume id → last seen) plus a small two-key `record` / `active`
  variant (the existing helpers are single-int-keyed).
- **New producer:** `RecordCostume(pokemonID, costume)` called from
  `cmd/processor/pokemon.go` `ProcessPokemon` (only when `costume > 0`) — the
  pokemon path does **not** currently feed `RecentActivity` at all (it only
  touches `stats`), so this is new wiring on that handler.

`RecentCostumes(pokemonID) []int` returns the recency-windowed costume ids for
`!info`. In-memory only ⇒ resets on restart (same as the existing autocomplete
recency); only costumes seen since startup appear — acceptable and
self-maintaining.

### 8. v2 Pokémon API (`api/v2_pokemon.go`)
Add a nullable `Costume *int` field mirroring `Form`, and ensure it is fully
writable and reads back with correct wildcard semantics:
- **Create (`POST`) and update (`PUT`)** accept `costume`. `valueOr(req.Costume, 9000)`
  on write: omitted / `null` ⇒ **9000 = any**; `0` ⇒ **no costume**; `N` ⇒ that
  costume. So a client can *add* a costume filter, clear it (send `null`), or
  demand no-costume (`0`).
- **Read**: `ptrUnless(row.Costume, 9000)` — returned as `null` when at the
  wildcard (9000), and as the literal value otherwise (including `0`). The null
  field therefore round-trips as "match any", consistent with `form`.
- Doc string states the 9000 / 0 / null semantics explicitly.

### 9. i18n
New keys in `internal/i18n/locale/en.json`: `msg.info.sub.costumes`,
`msg.info.available_costumes` (header for the per-species section),
`msg.info.costumes.header` (global list header), `arg.prefix.costume`,
`msg.no_costume` (label for costume 0 in rowtext), plus any command help text.

## Testing

- Matcher: costume 9000 (any) matches all; `costume:1` matches only costume 1;
  `costume:0` matches only non-costumed; costume independent of form.
- Enrichment: `fullName` = `"Pikachu (Holiday 2016)"` for costume 1; unchanged
  for costume 0; PVP entries unaffected; `costumeName` populated.
- Command: `!track pikachu costume:holiday_2016` stores costume 1;
  `!track pikachu costume:0` stores 0; `!untrack` by costume; rowtext shows it.
- v2 API: round-trip of `Costume` (present / null=wildcard / 0).
- `!info costumes` lists all; `!info pikachu` shows recently-seen after a
  `RecordCostume`.
- RecentActivity: record + retrieve; recency window.

## Out of scope / deferred

- `noEvolve` costume evolution semantics (data captured, not yet used).
- Costume-based filtering on other tracking types (raid/quest/etc.) — pokemon only.
- Negative/exclusion syntax beyond `costume:0`.

## Affected files (reference)

`internal/gamedata/*` (costumes loader), `internal/db/migrations/` + `db/monsters.go`,
`matching/pokemon.go`, `enrichment/translate.go` + `enrichment/pokemon.go`,
`bot/argmatch.go` + `bot/commands/{track,untrack,info,tracked}.go` +
`rowtext/*`, `discordbot/slash/{definitions,mappers/track,autocomplete}.go`,
`tracker/recent_activity.go` + `cmd/processor/pokemon.go`, `api/v2_pokemon.go`,
`api/dts_fields.go`, `i18n/locale/en.json`, `DTS.md`.
