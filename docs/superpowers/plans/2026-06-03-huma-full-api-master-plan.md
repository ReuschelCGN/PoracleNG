# Huma Full-API Master Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.
> **Execution gate:** write/refine now; **do not start P3–P4 (v2) until GitHub issue #138 feedback is in** (it may reshape the v2 resource model). P0–P2 (foundation + in-place) can proceed independently.

**Goal:** Migrate the *entire* PoracleNG `/api` HTTP surface to huma in one coordinated effort: document the simple/new endpoints **in place** at `/api/*`, and deliver a **clean, strict `/api/v2`** for the tracking/humans/profiles CRUD — all in a single OpenAPI spec, with `problem+json` errors throughout.

**Architecture:** **One** huma API instance, mounted on the existing authenticated `/api` gin group via `humagin.NewWithGroup` (`api.NewHumaAPI`). Op paths are relative to `/api`: in-place ops use `/reload`, `/weather`, … (→ `/api/…`); v2 ops use `/v2/tracking/{type}`, `/v2/humans/{id}`, … (→ `/api/v2/…`). One spec at `/openapi.json`, one docs page at `/docs`. v1 tracking/humans/profiles stay on **gin, frozen, untouched**. Errors are RFC 9457 `problem+json` everywhere (the legacy `{status,message}` override is removed). Success bodies are per-op: in-place endpoints preserve their current success JSON; v2 endpoints are bare typed bodies.

**Tech Stack:** Go 1.26, gin + `humagin`, `huma/v2`, `net/http/httptest`.

**Companion docs (authoritative for detail):**
- v2 contract: `docs/v2-api-design.md` (+ RFC issue #138)
- v2 field semantics: `docs/superpowers/specs/huma-tracking-field-audit.md`
- In-place easy-wins detail: `docs/superpowers/plans/2026-06-03-huma-easy-wins-inplace.md` (its tasks are P1 here; **its error convention is superseded** — errors are `problem+json`, not `{status,message}`)

---

## Locked decisions (this is the "how it works")

1. **One huma instance**, mounted at `/api`; in-place ops at `/x`, v2 ops at `/v2/x`. One OpenAPI spec covering both.
2. **Errors: `problem+json` everywhere.** Remove `InstallLegacyErrorModel`/the legacy override; use huma's default error model. Success bodies unchanged per-op.
3. **v1 frozen.** Revert the in-place pokemon huma migration; restore original gin pokemon routes. Remove v1-compat huma machinery (`lenient[T]`, the flex `SchemaProvider` leniency, `monsterRuleRows`, single-or-array) — **not** needed by strict v2.
4. **In-place coverage:** all EASY (~30) + all MODERATE (~15, **including** `config/values`+`validate` with open `any`/`RawMessage` bodies). LEAVE-ON-GIN: webhook `POST /`, `/metrics`, `/openapi.json`, `/docs`, pprof.
5. **v2 = strict:** `additionalProperties:false`, required enforced, **no lenient coercion**. Enums are **pure string** (no legacy-int acceptance); game-master IDs are int. uid-global REST resource model (`/v2/tracking/{type}` + `/{uid}`).
6. **v2 humans/profiles:** **discrete action endpoints** (not PATCH-consolidated), cleaned/typed, under `/api/v2`.
7. **CHANGELOG** items: error-format change to problem+json on the new huma surface; `include_empty` default→true; v1→v2 migration encouragement.

---

## Phase 0 — Foundation rework

### Task 0.1: Switch error model to problem+json
**Files:** `internal/api/huma_setup.go`, `huma_setup_test.go`, any test asserting `{status:error,message}`.
- [ ] Remove `InstallLegacyErrorModel` (and its call in `NewHumaAPI`); delete `legacyError`/`humaNewError` OR repoint `humaNewError` to `huma.NewError` so call sites compile. Handlers return errors via `huma.Error404NotFound(...)` etc. (huma's typed constructors).
- [ ] Update/replace tests that asserted the legacy envelope to assert `problem+json` (`status`, `detail`, `errors[]`; no `{status:"error"}`).
- [ ] Keep the `$schema`-suppression (`cfg.CreateHooks = nil`) — still wanted.
- [ ] Gate + commit `refactor(api): problem+json error model for the huma surface`.

### Task 0.2: Revert in-place pokemon migration (freeze v1)
**Files:** `main.go`, `huma_tracking.go`, `huma_post_monster*.go`, `huma_delete_monster*.go`, `tracking.go`.
- [ ] Restore the gin routes for `GET/POST/DELETE /tracking/pokemon/...` + bulk in `main.go` (the original `api.HandleGetMonster` etc. still exist in `trackingMonster.go`).
- [ ] Remove the huma pokemon ops + v1-compat machinery: `monsterRuleRows`, `lenient[T]`, the flex `SchemaProvider` methods on `flexInt`/`flexBool`, `collapseClean` (re-add in v2 if needed), and now-unused helpers. Keep `flexInt`/`flexBool` themselves (still used by gin v1).
- [ ] Remove the temporary `flex_enum.go` lint exclusion plan (the enum toolkit is reworked in P3).
- [ ] Gate + commit `refactor(api): revert in-place pokemon huma migration (v1 frozen)`.

### Task 0.3: Confirm single-instance dual-path mount
- [ ] Add a test: register one trivial in-place op (`/ping`) and one v2 op (`/v2/ping`) on the same `NewHumaAPI`, assert both serve and both appear in `OpenAPI().MarshalJSON()`. Confirms the one-instance/two-path-prefix model. Gate + commit.

---

## Phase 1 — In-place EASY endpoints (~30)

Execute the tasks in `docs/superpowers/plans/2026-06-03-huma-easy-wins-inplace.md` (Tasks 1–7), with these amendments: errors are `problem+json` (Task 0.1), so drop the legacy-error notes; register on the shared instance. Clusters: reloads, read-only data (health/stats/geocode/geofence-reads/masterdata/config-schema/snapshots), tile-URL, DTS reads, feature endpoints (autocreate/run, summaries GET/DELETE/trigger, command). Worked examples (reload, weather) are in that doc.
- [ ] Complete easy-wins Tasks 1–6 (per-cluster commits).
- [ ] Easy-wins Task 7 golden test folded into the master golden test (P5).

## Phase 2 — In-place MODERATE endpoints (~15)

Open schemas for freeform fields. Each: typed input for path/query, `Body json.RawMessage` or `Body any` for the freeform part, reuse handler logic, remove gin route, test (parse boundary + success shape), commit per group.

| endpoint | freeform part | source |
|---|---|---|
| `POST /test` | `webhook` RawMessage | `HandleTest` |
| `POST /dts/render` | `view` map; resp `message` any | render handler |
| `POST /dts/enrich` | `webhook` RawMessage | enrich handler |
| `POST /dts/sendtest` | `template` any, `variables` map | sendtest handler |
| `POST /dts/templates` | `[]DTSEntry` (polymorphic `template`) | save handler |
| `POST /deliverMessages` + `POST /postMessage` | `[]delivery.Job` (`Message` RawMessage) | deliver handler |
| `POST /resolve` | nested optional + per-entity `any` | resolve handler |
| `POST /summaries/{id}/{alertType}` | `active_hours` any | upsert handler |
| `GET/POST /autocreate/templates`, `POST …/validate` | raw-JSON templates | autocreate template handlers |
| `GET /config/templates`, `GET /config/poracleWeb` | dynamic-keyed map → `Body any` | config handlers |
| `GET/POST /config/values`, `POST /config/validate` | reflection `map[string]any` → open body/resp | config handlers |

- [ ] Per group: TDD port with open schemas, remove gin route, gate + commit `feat(api): huma in-place for <group>`.
- [ ] Document in the spec that these bodies are intentionally open (`description` noting the freeform contract).

## Phase 3 — v2 tracking (gated on #138)

Strict, per `docs/v2-api-design.md` + the field audit. Resource model: `/v2/tracking/{type}` (GET list `?user=&profile=`, POST create), `/v2/tracking/{type}/{uid}` (GET/PUT/DELETE), `?uid=` bulk delete; `?silent=true` on mutations.

### Task 3.1: Strict v2 building blocks
- [ ] **Strict enum types** — rework/parallel `flex_enum.go`: v2 enums are **string-only** (no int acceptance), `additionalProperties:false`-compatible. Keep the name↔int maps for storage translation. (team, gender, fort_type, rsvp_changes; reward_type/lure_id/league/pvp_ranking_evolution stay **int**.)
- [ ] **Strict request structs** — real `bool`/`int`/string-enum fields; `clean`/`edit`/`summary` bools → packed `clean` column; required `pokemon_id` etc.; `additionalProperties:false`.
- [ ] **Resource helpers** — `uid`-global addressing; `user`/`profile` query binding; create returns `{created,updated,unchanged}` with uids; list returns `{rules:[…]}`.
- [ ] Tests for the building blocks; gate + commit.

### Task 3.2: pokemon v2 (worked example) — GET list, POST create, GET/PUT/DELETE by uid, bulk delete. Faithful to the engine; strict schemas. Commit.

### Task 3.3: Fan-out the other 10 types (raid, egg, quest, invasion, **incident**, lure, nest, gym, fort, maxbattle)
- [ ] Per type: apply the audit's per-field modeling; **invasion** exactly-one-mode (`type_id`|`grunt_id`|`everything`|`boss`) with facade down-translation to the stored grunt-type name; **incident** new type keyed by `display_type` int; `fort.include_empty` default true. One commit per type.

### Task 3.4: v2 tracking aggregates — `/v2/tracking?user=` (all types) if desired; reload alias. Commit.

## Phase 4 — v2 humans/profiles (gated on #138)

**Discrete action endpoints**, cleaned/typed, under `/api/v2`. Mirror v1's actions with proper types + problem+json + strict bodies. Reuse the store/business logic.

| v2 endpoint | from v1 | shape |
|---|---|---|
| `POST /v2/humans` | create | typed body (id,type,name,…) |
| `GET /v2/humans/{id}` | one/{id} | typed human resource |
| `GET /v2/humans/{id}/areas` | `/{id}` | available areas |
| `POST /v2/humans/{id}/enable` / `/disable` | start/stop | no body |
| `POST /v2/humans/{id}/admin-disable` | adminDisabled | `{disabled: bool}` |
| `POST /v2/humans/{id}/language` | language | `{language: string}` |
| `POST /v2/humans/{id}/location` | setLocation/{lat}/{lon} | `{lat,lon}` floats body |
| `GET /v2/humans/{id}/check-location` | checkLocation | `?lat=&lon=` |
| `POST /v2/humans/{id}/areas` | setAreas | `{areas: []string}` |
| `GET/POST /v2/humans/{id}/locations`, `DELETE …/{label}` | locations CRUD | typed |
| `GET /v2/humans/{id}/roles`, `POST/DELETE …/{roleId}` | roles | typed |
| `GET /v2/humans/{id}/admin-roles` | getAdministrationRoles | typed |
| `POST /v2/humans/{id}/profile` | switchProfile/{n} | `{profile_no: int}` |
| `GET /v2/profiles/{id}`, `POST` (add), `PATCH …/{profile_no}` (update active_hours), `DELETE …/{profile_no}`, `POST …/{profile_no}/copy` | profiles | typed |

- [ ] Field modeling: `enabled`/admin-disable → bool; `areas` → `[]string`; `location` → `{lat,lon}` floats; `language` → string (validate against locales); `blocked_alerts` → `[]string` of alert-type enum; profile `active_hours` → typed schedule (confirm shape against the profiles handler).
- [ ] Per cluster (status, location/areas, locations, roles, profiles): TDD, reuse handlers, commit.
- [ ] **Open confirm:** `active_hours` schedule shape and `blocked_alerts` enum values — finalize against the handlers during P4.

## Phase 5 — Finalize

- [ ] **Golden OpenAPI test** over the whole spec (in-place + v2), committed `testdata/openapi.golden.json`.
- [ ] **Remove dead code** — any now-unused v1-compat helpers; confirm no orphaned gin handlers for migrated in-place endpoints; lint clean (remove temporary exclusions).
- [ ] **Docs** — README/CLAUDE.md: the `/api` surface and `/api/v2` are documented at `/docs`; note the migrated endpoints, the v1-frozen status, and the v1→v2 encouragement.
- [ ] **CHANGELOG** — problem+json on the huma surface; `include_empty` default→true; new v2 surface + `incident` type.

---

## Self-review
- **Coverage:** every endpoint from the triage is assigned — EASY (P1), MODERATE incl config/values (P2), v2 tracking incl incident (P3), v2 humans/profiles discrete actions (P4), LEAVE-ON-GIN explicitly excluded. v1 frozen via P0.2.
- **Decision fidelity:** problem+json everywhere (P0.1, supersedes easy-wins legacy note); one instance/two path prefixes (P0.3); discrete humans/profiles actions (P4); max in-place coverage (P2).
- **Gating:** P0–P2 independent; P3–P4 wait on #138 — flagged at top and per-phase.
- **Detail strategy:** worked examples live in the companion docs (easy-wins reload/weather; pokemon v2 in 3.2); fan-outs are delta tables driven by the audit — consistent with the prior plans' approach.
- **Open items to finalize at build time:** strict-enum reuse vs rework of `flex_enum.go` (3.1); `active_hours`/`blocked_alerts` shapes (P4); any #138 resource-shape feedback (P3).
