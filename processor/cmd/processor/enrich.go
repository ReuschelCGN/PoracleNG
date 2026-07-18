package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/pokemon/poracleng/processor/internal/dts"
	"github.com/pokemon/poracleng/processor/internal/enrichment"
	"github.com/pokemon/poracleng/processor/internal/matching"
	"github.com/pokemon/poracleng/processor/internal/staticmap"
	"github.com/pokemon/poracleng/processor/internal/tracker"
	"github.com/pokemon/poracleng/processor/internal/webhook"
)

// enrichResult holds the ingredients needed to build a LayeredView.
type enrichResult struct {
	templateType  string
	base          map[string]any
	perLang       map[string]any
	perUser       map[string]any
	webhookFields map[string]any
	tilePending   *staticmap.TilePending
	extras        map[string]any // derived-type state: original, affected, rsvp, group. Nil for plain types.
}

// enrichForType resolves a DTS-name synonym (e.g. "monster", "monsterNoIv",
// "egg") via dtsAlias, dispatches to the enrich* function for the underlying
// webhook type, and returns the resulting enrichResult with its templateType
// forced to the alias's TemplateType.
//
// The alias is the single source of truth for template-type selection.
// Without the override at the end of this function, names that share a
// WebhookType but must render under distinct DTS template types collapse
// onto whatever the enrich* function hardcodes or infers from the payload:
// "monster" and "monsterNoIv" both dispatch through enrichPokemon (which
// always sets templateType "monster"), and "raid"/"egg" both dispatch
// through enrichRaid (which infers "raid" vs "egg" from the payload's
// PokemonID, not from which name the caller asked for).
func (ps *ProcessorService) enrichForType(name string, raw json.RawMessage, language string, freshenStaleTime bool) (*enrichResult, error) {
	src, ok := dtsAlias(name)
	if !ok {
		return nil, fmt.Errorf("unsupported webhook type: %s", name)
	}

	var result *enrichResult
	var err error

	// Derived names (monsterChanged, rsvpChanges, questSummary, incident,
	// weatherchange) don't arrive as their own Golbat wire event — they're
	// synthesized from processor-internal state (an encounter change, an
	// RSVP update, a summary digest, a weather-cell transition, ...).
	// "incident" is one exception: it IS parsed from a raw webhook — the
	// same PokestopEvent shape as "invasion" — just rendered under a
	// distinct DTS template type (see enrichIncident), so it's marked
	// Derived in the alias table purely to flag that it has no direct 1:1
	// webhook-type spelling of its own for the switch below, not because
	// it's synthesized from internal state. "weatherchange" is a partial
	// exception: enrichWeatherChange parses a fully-specified partial (the
	// testdata.json sample / editor preview payload) supplying the old/new
	// weather IDs and affected-pokemon list directly, rather than deriving
	// them from live tracker state the way the real consumeWeatherChanges
	// path does — see enrichWeatherChange's doc comment. "questSummary" is
	// likewise a partial exception: enrichQuestSummary takes a
	// fully-specified single reward group (the testdata.json sample /
	// editor preview payload) rather than pulling buffered quests from the
	// live SummaryBuffer and grouping across possibly-different reward
	// keys the way the real DispatchQuestSummary does — see
	// enrichQuestSummary's doc comment. "monster-changed" is likewise a
	// partial exception: enrichMonsterChanged takes a fully-specified
	// {old,new} pokemon-webhook pair (the testdata.json sample / editor
	// preview payload) rather than diffing two live sightings of the same
	// encounter_id via tracker.EncounterTracker.Track the way the real
	// dispatchPokemonAlert path does — see enrichMonsterChanged's doc
	// comment.
	// TODO(derived types): Task 7 dispatches the remaining derived builder
	// (rsvpChanges) here instead of erroring.
	if src.Derived {
		switch src.WebhookType {
		case "incident":
			result, err = ps.enrichIncident(raw, language, freshenStaleTime)
		case "weather-change":
			result, err = ps.enrichWeatherChange(raw, language, freshenStaleTime)
		case "quest-summary":
			result, err = ps.enrichQuestSummary(raw, language, freshenStaleTime)
		case "monster-changed":
			result, err = ps.enrichMonsterChanged(raw, language, freshenStaleTime)
		default:
			return nil, fmt.Errorf("derived type not yet supported (implemented in a later task): %s", name)
		}
	} else {
		// Resolve to the webhook type the switch below dispatches on, so
		// enrichForType("monster", ...) behaves exactly like
		// enrichForType("pokemon", ...).
		//
		// "pokestop" is deliberately excluded from this rewrite even though it's
		// the canonical WebhookType for both "invasion" and "lure": it isn't a
		// name this switch dispatches on (disambiguating it requires peeking at
		// the payload, see resolveDTSTypeFromRaw in test.go), so rewriting to it
		// would break the "invasion" and "lure" cases that already work today
		// under their own names.
		webhookType := src.WebhookType
		if webhookType == "pokestop" {
			webhookType = name
		}

		switch webhookType {
		case "pokemon", "monster":
			result, err = ps.enrichPokemon(raw, language, freshenStaleTime)
		case "raid":
			result, err = ps.enrichRaid(raw, language, false, freshenStaleTime)
		case "egg":
			result, err = ps.enrichRaid(raw, language, true, freshenStaleTime)
		case "quest":
			result, err = ps.enrichQuest(raw, language)
		case "invasion":
			result, err = ps.enrichInvasion(raw, language, freshenStaleTime)
		case "lure":
			result, err = ps.enrichLure(raw, language)
		case "nest":
			result, err = ps.enrichNest(raw, language)
		case "gym":
			result, err = ps.enrichGym(raw, language)
		case "fort_update", "fort-update":
			result, err = ps.enrichFort(raw, language)
		case "max_battle", "maxbattle":
			result, err = ps.enrichMaxbattle(raw, language)
		default:
			return nil, fmt.Errorf("unsupported webhook type: %s", webhookType)
		}
	}
	if err != nil {
		return nil, err
	}

	// The alias is authoritative: it disambiguates names that share a
	// WebhookType but must render under distinct DTS template types (see
	// the doc comment above).
	if src.TemplateType != "" {
		result.templateType = src.TemplateType
	}

	return result, nil
}

// EnrichWebhook runs a raw webhook through the enrichment pipeline, builds a
// LayeredView (with aliases, emoji, computed fields), and returns a flattened
// variable map matching what Handlebars templates see during real rendering.
func (ps *ProcessorService) EnrichWebhook(webhookType string, raw json.RawMessage, language, platform string) (map[string]any, error) {
	result, err := ps.enrichForType(webhookType, raw, language, true)
	if err != nil {
		return nil, err
	}

	// isPokemon mirrors the WebhookType the requested name resolves to (not
	// the alias-authoritative result.templateType — "monster" and
	// "monsterNoIv" both resolve to the "pokemon" WebhookType and get PVP
	// display computed for the editor preview below, same as before
	// enrichForType existed). enrichForType already validated that
	// dtsAlias(webhookType) resolves (it returned no error), so the lookup
	// here cannot fail.
	src, _ := dtsAlias(webhookType)
	isPokemon := src.WebhookType == "pokemon"

	// Compute PVP display data with a synthetic user (no filters = show all PVP
	// entries). In normal rendering this is per-user, but for the editor preview
	// we show everything. Only pokemon carries per-user PVP display data.
	if isPokemon && ps.enricher.PVPDisplay != nil && result.perLang != nil {
		syntheticUser := webhook.MatchedUser{
			ID:       "_editor",
			Language: language,
		}
		perUserMap := ps.enricher.PokemonPerUser(
			map[string]map[string]any{language: result.perLang},
			[]webhook.MatchedUser{syntheticUser},
		)
		result.perUser = perUserMap["_editor"]
	}

	// Resolve tile synchronously if pending (the normal render path does this async)
	if result.tilePending != nil {
		select {
		case url := <-result.tilePending.Result:
			result.tilePending.Apply(url)
		case <-time.After(10 * time.Second):
			result.tilePending.Apply(result.tilePending.Fallback)
		}
	}

	// Build LayeredView with aliases, emoji resolution, and computed fields —
	// the same path as real template rendering.
	if ps.dtsRenderer != nil {
		lv := dts.NewLayeredView(
			ps.dtsRenderer.ViewBuilder(),
			result.templateType,
			result.base,
			result.perLang,
			result.perUser,
			result.webhookFields,
			platform,
			nil, // no matched areas
		)
		return injectOriginalExtra(lv.Flatten(), result), nil
	}

	// Fallback if DTS renderer isn't available — return raw merge
	return injectOriginalExtra(mergeEnrichment(result.base, result.perLang, result.webhookFields), result), nil
}

// injectOriginalExtra copies extras["original"] (built by
// enrichMonsterChanged) onto the flattened variable map so the
// /api/dts/enrich editor preview exposes {{original.X}} the same way the
// live RenderPokemonChanged path and !poracle-test do. LayeredView.Flatten
// doesn't include it: the "original" nested-map exposure is a raymond
// FieldResolver special-case (LayeredView.GetField) that only applies during
// actual template rendering, not the flattened variable-browser map this
// function builds. A no-op for every non-monsterChanged type, since only
// enrichMonsterChanged sets extras["original"].
func injectOriginalExtra(vars map[string]any, result *enrichResult) map[string]any {
	if original, ok := result.extras["original"].(map[string]any); ok {
		vars["original"] = original
	}
	return vars
}

// mergeEnrichment is a simple fallback when the DTS renderer is not available.
func mergeEnrichment(base, perLang, webhookFields map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(perLang)+len(webhookFields))
	maps.Copy(result, webhookFields)
	maps.Copy(result, base)
	maps.Copy(result, perLang)
	return result
}

// enrichPokemon parses and enriches a pokemon webhook. freshenStaleTime, when
// true, bumps an already-past DisappearTime into the near future — this is an
// editor-preview affordance (canned/typed sample JSON often carries a stale
// timestamp) and must stay false for the live /api/test path, which never
// applied this correction before enrichPokemon existed (see enrich_test.go /
// task-1 notes for the parity investigation).
func (ps *ProcessorService) enrichPokemon(raw json.RawMessage, language string, freshenStaleTime bool) (*enrichResult, error) {
	var pokemon webhook.PokemonWebhook
	if err := json.Unmarshal(raw, &pokemon); err != nil {
		return nil, fmt.Errorf("parse pokemon: %w", err)
	}

	if freshenStaleTime && pokemon.DisappearTime > 0 && pokemon.DisappearTime < time.Now().Unix() {
		pokemon.DisappearTime = time.Now().Unix() + 600
	}

	rarityGroup := ps.stats.GetRarityGroup(pokemon.PokemonID)
	processed := matching.ProcessPokemonWebhook(&pokemon, rarityGroup, ps.pvpCfg)
	base, tilePending := ps.enricher.Pokemon(&pokemon, processed, enrichment.TileModeURL)

	var perLang map[string]any
	if ps.enricher.GameData != nil && ps.enricher.Translations != nil {
		perLang = ps.enricher.PokemonTranslate(base, &pokemon, language)
	}

	return &enrichResult{
		templateType:  "monster",
		base:          base,
		perLang:       perLang,
		webhookFields: parseWebhookFields(raw),
		tilePending:   tilePending,
		extras:        map[string]any{"encountered": processed.Encountered},
	}, nil
}

// enrichRaid parses and enriches a raid/egg webhook. freshenStaleTime, when
// true, bumps an already-past Start/End window into the near future — an
// editor-preview affordance that must stay false for the live /api/test path
// (see enrichPokemon's freshenStaleTime doc for the full rationale).
func (ps *ProcessorService) enrichRaid(raw json.RawMessage, language string, isEgg, freshenStaleTime bool) (*enrichResult, error) {
	var raid webhook.RaidWebhook
	if err := json.Unmarshal(raw, &raid); err != nil {
		return nil, fmt.Errorf("parse raid: %w", err)
	}

	if freshenStaleTime && raid.Start > 0 && raid.End < time.Now().Unix() {
		raid.Start = time.Now().Unix() + 600
		raid.End = raid.Start + 1800
	}

	templateType := "raid"
	if isEgg || raid.PokemonID == 0 {
		templateType = "egg"
	}

	base, tilePending := ps.enricher.Raid(&raid, true, enrichment.TileModeURL)

	var perLang map[string]any
	if ps.enricher.GameData != nil && ps.enricher.Translations != nil {
		perLang = ps.enricher.RaidTranslate(base, &raid, language)
	}

	return &enrichResult{
		templateType:  templateType,
		base:          base,
		perLang:       perLang,
		webhookFields: parseWebhookFields(raw),
		tilePending:   tilePending,
	}, nil
}

func (ps *ProcessorService) enrichQuest(raw json.RawMessage, language string) (*enrichResult, error) {
	var quest webhook.QuestWebhook
	if err := json.Unmarshal(raw, &quest); err != nil {
		return nil, fmt.Errorf("parse quest: %w", err)
	}

	rewards := make([]matching.QuestRewardData, 0, len(quest.Rewards))
	for _, r := range quest.Rewards {
		rewards = append(rewards, parseQuestReward(r))
	}
	base, tilePending := ps.enricher.Quest(quest.Latitude, quest.Longitude, quest.PokestopID, quest.URL, rewards, enrichment.TileModeURL)

	var perLang map[string]any
	if ps.enricher.GameData != nil && ps.enricher.Translations != nil {
		perLang = ps.enricher.QuestTranslate(base, &quest, rewards, language)
	}

	return &enrichResult{
		templateType:  "quest",
		base:          base,
		perLang:       perLang,
		webhookFields: parseWebhookFields(raw),
		tilePending:   tilePending,
	}, nil
}

// enrichInvasion parses and enriches an invasion/pokestop webhook.
// freshenStaleTime, when true, bumps an already-past IncidentExpiration into
// the near future — an editor-preview affordance that must stay false for the
// live /api/test path (see enrichPokemon's freshenStaleTime doc for the full
// rationale).
func (ps *ProcessorService) enrichInvasion(raw json.RawMessage, language string, freshenStaleTime bool) (*enrichResult, error) {
	return ps.enrichPokestopEvent(raw, language, freshenStaleTime, "invasion")
}

// enrichIncident parses and enriches an incident webhook (Gold Pokéstop,
// Kecleon, Pokémon-contest/Showcase display types). Incidents ride the exact
// same webhook.InvasionWebhook payload shape as invasions — Golbat
// distinguishes them only by gruntTypeID==0 with a display type >= 7 (see the
// isIncident split in cmd/processor/invasion.go's live ProcessInvasion path) —
// so this reuses enrichInvasion's enrichment core and only overrides the
// resulting templateType to "incident". That gives operators a simpler card
// without grunt/reward fields. freshenStaleTime has the same meaning as
// enrichInvasion's (see enrichPokemon's doc for the full rationale).
func (ps *ProcessorService) enrichIncident(raw json.RawMessage, language string, freshenStaleTime bool) (*enrichResult, error) {
	return ps.enrichPokestopEvent(raw, language, freshenStaleTime, "incident")
}

// enrichPokestopEvent is the shared enrichment core for enrichInvasion and
// enrichIncident: both parse the same webhook.InvasionWebhook shape and
// enrich through enricher.Invasion/InvasionTranslate; only the resulting
// templateType differs between callers.
func (ps *ProcessorService) enrichPokestopEvent(raw json.RawMessage, language string, freshenStaleTime bool, templateType string) (*enrichResult, error) {
	var inv webhook.InvasionWebhook
	if err := json.Unmarshal(raw, &inv); err != nil {
		return nil, fmt.Errorf("parse invasion: %w", err)
	}

	if freshenStaleTime && inv.IncidentExpiration > 0 && inv.IncidentExpiration < time.Now().Unix() {
		inv.IncidentExpiration = time.Now().Unix() + 600
	}

	expiration := inv.IncidentExpiration
	if expiration == 0 {
		expiration = inv.IncidentExpireTimestamp
	}
	gruntTypeID := inv.IncidentGruntType
	if gruntTypeID == 0 {
		gruntTypeID = inv.GruntType
	}
	displayType := inv.DisplayType
	if displayType == 0 {
		displayType = inv.IncidentDisplayType
	}

	base, tilePending := ps.enricher.Invasion(inv.Latitude, inv.Longitude, expiration, inv.PokestopID, inv.URL, gruntTypeID, displayType, 0, enrichment.TileModeURL)

	var perLang map[string]any
	if ps.enricher.GameData != nil && ps.enricher.Translations != nil {
		perLang = ps.enricher.InvasionTranslate(base, inv.Latitude, inv.Longitude, gruntTypeID, inv.Lineup, inv.ShowcaseRankings, language)
	}

	return &enrichResult{
		templateType:  templateType,
		base:          base,
		perLang:       perLang,
		webhookFields: parseWebhookFields(raw),
		tilePending:   tilePending,
	}, nil
}

func (ps *ProcessorService) enrichLure(raw json.RawMessage, language string) (*enrichResult, error) {
	var lure webhook.LureWebhook
	if err := json.Unmarshal(raw, &lure); err != nil {
		return nil, fmt.Errorf("parse lure: %w", err)
	}

	base, tilePending := ps.enricher.Lure(&lure, enrichment.TileModeURL)

	var perLang map[string]any
	if ps.enricher.GameData != nil && ps.enricher.Translations != nil {
		perLang = ps.enricher.LureTranslate(base, &lure, language)
	}

	return &enrichResult{
		templateType:  "lure",
		base:          base,
		perLang:       perLang,
		webhookFields: parseWebhookFields(raw),
		tilePending:   tilePending,
	}, nil
}

func (ps *ProcessorService) enrichNest(raw json.RawMessage, language string) (*enrichResult, error) {
	var nest webhook.NestWebhook
	if err := json.Unmarshal(raw, &nest); err != nil {
		return nil, fmt.Errorf("parse nest: %w", err)
	}

	base, tilePending := ps.enricher.Nest(&nest, enrichment.TileModeURL)

	var perLang map[string]any
	if ps.enricher.GameData != nil && ps.enricher.Translations != nil {
		perLang = ps.enricher.NestTranslate(base, &nest, language)
	}

	return &enrichResult{
		templateType:  "nest",
		base:          base,
		perLang:       perLang,
		webhookFields: parseWebhookFields(raw),
		tilePending:   tilePending,
	}, nil
}

func (ps *ProcessorService) enrichGym(raw json.RawMessage, language string) (*enrichResult, error) {
	var gym webhook.GymWebhook
	if err := json.Unmarshal(raw, &gym); err != nil {
		return nil, fmt.Errorf("parse gym: %w", err)
	}

	gymID := gym.GymID
	if gymID == "" {
		gymID = gym.ID
	}
	teamID := gym.TeamID
	if teamID == 0 {
		teamID = gym.Team
	}
	inBattle := bool(gym.IsInBattle) || bool(gym.InBattle)

	base, tilePending := ps.enricher.Gym(gym.Latitude, gym.Longitude, teamID, 0, gym.SlotsAvailable, -1, inBattle, false, gymID, enrichment.TileModeURL)

	var perLang map[string]any
	if ps.enricher.GameData != nil && ps.enricher.Translations != nil {
		// Editor preview has no team-change context, so suppress previousControl* fields.
		perLang = ps.enricher.GymTranslate(base, gym.Latitude, gym.Longitude, teamID, -1, -1, language)
	}

	return &enrichResult{
		templateType:  "gym",
		base:          base,
		perLang:       perLang,
		webhookFields: parseWebhookFields(raw),
		tilePending:   tilePending,
	}, nil
}

func (ps *ProcessorService) enrichFort(raw json.RawMessage, language string) (*enrichResult, error) {
	var fort webhook.FortWebhook
	if err := json.Unmarshal(raw, &fort); err != nil {
		return nil, fmt.Errorf("parse fort: %w", err)
	}

	base, tilePending := ps.enricher.FortUpdate(fort.Latitude(), fort.Longitude(), fort.FortID(), &fort, enrichment.TileModeURL)
	perLang := ps.enricher.FortUpdateTranslate(fort.Latitude(), fort.Longitude(), language)

	return &enrichResult{
		templateType:  "fort-update",
		base:          base,
		perLang:       perLang,
		webhookFields: parseWebhookFields(raw),
		tilePending:   tilePending,
	}, nil
}

func (ps *ProcessorService) enrichMaxbattle(raw json.RawMessage, language string) (*enrichResult, error) {
	var mb webhook.MaxbattleWebhook
	if err := json.Unmarshal(raw, &mb); err != nil {
		return nil, fmt.Errorf("parse maxbattle: %w", err)
	}

	base, tilePending := ps.enricher.Maxbattle(mb.Latitude, mb.Longitude, mb.BattleEnd, &mb, enrichment.TileModeURL)

	var perLang map[string]any
	if ps.enricher.GameData != nil && ps.enricher.Translations != nil {
		perLang = ps.enricher.MaxbattleTranslate(base, &mb, language)
	}

	return &enrichResult{
		templateType:  "maxbattle",
		base:          base,
		perLang:       perLang,
		webhookFields: parseWebhookFields(raw),
		tilePending:   tilePending,
	}, nil
}

// enrichWeatherChange builds enrichment for the derived "weatherchange" DTS
// test type: a weather-cell transition (old→new gameplay condition) plus a
// short list of nearby pokemon affected by the boost change.
//
// Unlike the live path (consumeWeatherChanges in cmd/processor/weather.go),
// which discovers caring users and affected pokemon from live tracker state
// (WeatherCareTracker.GetCaringUsers, ActivePokemonTracker.GetAffectedPokemon)
// fed by a channel of detected changes, this function takes a
// fully-specified partial — the old/new weather IDs and the affected-pokemon
// list are supplied directly by the caller (a testdata.json sample or an
// editor preview payload), not derived from live cell state. It reuses the
// exact same pure enrichment calls consumeWeatherChanges uses
// (enricher.Weather / enricher.WeatherTranslate), so the resulting
// base/perLang fields match what a live weather alert would carry — nothing
// about rendering is re-derived here.
//
// showAlteredPokemonStaticMap is hardcoded false rather than read from
// ps.cfg.Weather: it only controls whether the tile gets per-user
// active-pokemon markers baked in (a live delivery-time concern needing a
// real per-destination tile mode) and whether a duplicate "activePokemons"
// field + tile-pending is produced for that purpose. The affected-pokemon
// list templates actually read — enrichedActivePokemons — is populated by
// WeatherTranslate whenever len(Affected) > 0, independent of this flag.
func (ps *ProcessorService) enrichWeatherChange(raw json.RawMessage, language string, freshenStaleTime bool) (*enrichResult, error) {
	var wc webhook.WeatherChangeWebhook
	if err := json.Unmarshal(raw, &wc); err != nil {
		return nil, fmt.Errorf("parse weather change: %w", err)
	}

	// freshenStaleTime, when true, bumps any already-past affected-pokemon
	// DisappearTime into the near future — an editor-preview affordance
	// mirroring enrichPokemon's DisappearTime freshening (see its doc
	// comment for the full rationale). Must stay false for the live
	// /api/test path (processTestWeatherChange).
	if freshenStaleTime {
		now := time.Now().Unix()
		for i, ap := range wc.Affected {
			if ap.DisappearTime > 0 && ap.DisappearTime < now {
				wc.Affected[i].DisappearTime = now + 600 + int64(i)*60
			}
		}
	}

	const showAlteredPokemonStaticMap = false
	base, tilePending := ps.enricher.Weather(wc.Latitude, wc.Longitude, wc.GameplayCondition, wc.Coords, showAlteredPokemonStaticMap, enrichment.TileModeURL, wc.S2CellID)

	var perLang map[string]any
	if ps.enricher.GameData != nil && ps.enricher.Translations != nil {
		perLang, _ = ps.enricher.WeatherTranslate(base, wc.OldGameplayCondition, wc.GameplayCondition, wc.Affected, language, showAlteredPokemonStaticMap, enrichment.TileModeURL, wc.S2CellID)
	}

	return &enrichResult{
		templateType:  "weatherchange",
		base:          base,
		perLang:       perLang,
		webhookFields: parseWebhookFields(raw),
		tilePending:   tilePending,
		extras:        map[string]any{"affected": wc.Affected},
	}, nil
}

// questSummaryPartial is the structured shape enrichQuestSummary consumes
// for the derived "questSummary" DTS type: one reward group's shared
// (type, reward, form) key plus the per-pokestop quest webhooks that belong
// to it. It's a test/editor-preview convenience, not a real Golbat wire
// event — the live path (DispatchQuestSummary, cmd/processor/quest_summary_dispatch.go)
// builds groups itself from many buffered tracker.BufferedQuest rows that
// may span several different reward keys, then splits each group into
// chunks per config; here the caller supplies exactly one already-formed
// group (see fallbacks/testdata.json's "quest_summary"/"stardust" sample —
// the underscore spelling is the !poracle-test/API wire type; the DTS
// template-type name and dtsAlias key are both "questSummary", see
// resolveDTSTypeFromRaw), rendered unchunked (chunk=1, chunks=1).
type questSummaryPartial struct {
	Reward struct {
		Type int `json:"type"`
		ID   int `json:"reward"`
		Form int `json:"form"`
	} `json:"reward"`
	Quests []json.RawMessage `json:"quests"`
}

// enrichQuestSummary parses the questSummary partial, re-enriches each
// per-pokestop quest via questEnrichOne — the exact same helper
// DispatchQuestSummary uses to re-enrich buffered quests at delivery time —
// and builds the group view via buildQuestSummaryGroupView, also shared
// with DispatchQuestSummary, so the resulting fields match a live summary
// digest byte-for-byte. Nothing about rendering is re-derived here.
//
// freshenStaleTime is accepted for signature symmetry with the other
// Derived enrich* functions dispatched from enrichForType's switch, but is
// a no-op here: quests carry no expiring timestamp of their own to freshen
// (enrichQuest, the regular quest path, has no freshenStaleTime parameter
// either) — the daily quest reset is computed from lat/lon at render time
// (geo.EndOfDay), not read off the webhook.
func (ps *ProcessorService) enrichQuestSummary(raw json.RawMessage, language string, freshenStaleTime bool) (*enrichResult, error) {
	_ = freshenStaleTime

	var partial questSummaryPartial
	if err := json.Unmarshal(raw, &partial); err != nil {
		return nil, fmt.Errorf("parse quest summary: %w", err)
	}
	if len(partial.Quests) == 0 {
		return nil, fmt.Errorf("quest summary partial has no quests")
	}

	views := make([]map[string]any, 0, len(partial.Quests))
	for _, q := range partial.Quests {
		view := ps.questEnrichOne(q, language)
		if view == nil {
			continue
		}
		views = append(views, view)
	}
	if len(views) == 0 {
		return nil, fmt.Errorf("quest summary partial: no quest entries could be enriched")
	}

	base := ps.buildQuestSummaryGroupView(
		partial.Reward.Type, partial.Reward.ID, partial.Reward.Form,
		views, len(views), 1, 1, language,
	)

	return &enrichResult{
		templateType:  "questSummary",
		base:          base,
		webhookFields: parseWebhookFields(raw),
		extras:        map[string]any{"quests": views},
	}, nil
}

// monsterChangedPartial is the structured shape enrichMonsterChanged
// consumes for the derived "monsterChanged" DTS type: the prior sighting's
// pokemon webhook (`old`) and the current sighting's pokemon webhook (`new`)
// for the same encounter. It's a test/editor-preview convenience, not a real
// Golbat wire event — the live path (dispatchPokemonAlert in
// cmd/processor/pokemon.go) derives both states from
// tracker.EncounterTracker.Track's diff against repeated sightings of the
// same encounter_id over time; here the caller supplies both snapshots
// directly (see fallbacks/testdata.json's "monster_changed"/"ditto-reveal"
// sample — the underscore spelling is the !poracle-test/API wire type; the
// DTS template-type name and dtsAlias key are both "monsterChanged", see
// resolveDTSTypeFromRaw).
type monsterChangedPartial struct {
	Old json.RawMessage `json:"old"`
	New json.RawMessage `json:"new"`
}

// enrichMonsterChanged parses the monsterChanged partial, enriches `new` via
// enrichPokemon — the exact same helper the plain "pokemon"/"monster" test
// path uses — and builds the {{original.X}} prior-sighting view from `old`
// via tracker.EncounterStateFromPokemon + dts.BuildOriginalView. This is the
// same BuildOriginalView call dispatchPokemonAlert falls back to when no
// per-language OldWebhook re-enrichment is available (see its doc comment);
// unlike that live path, this never attempts the richer
// buildPerLanguageOriginal re-enrichment, since that helper needs a live
// target-user list and a fully wired WeatherProvider — neither fits an
// editor preview / single-shot test render. So {{original.X}} here is always
// the narrower BuildOriginalView field set: identity, battle stats, and
// translated names — no map/icon URLs.
//
// freshenStaleTime only affects `new` (via enrichPokemon) — `old` is a fixed
// prior point in time with no expiring timestamp templates read
// (BuildOriginalView never surfaces DisappearTime).
func (ps *ProcessorService) enrichMonsterChanged(raw json.RawMessage, language string, freshenStaleTime bool) (*enrichResult, error) {
	var partial monsterChangedPartial
	if err := json.Unmarshal(raw, &partial); err != nil {
		return nil, fmt.Errorf("parse monster changed: %w", err)
	}
	if len(partial.New) == 0 {
		return nil, fmt.Errorf("monster changed partial missing 'new' webhook")
	}
	if len(partial.Old) == 0 {
		return nil, fmt.Errorf("monster changed partial missing 'old' webhook")
	}

	result, err := ps.enrichPokemon(partial.New, language, freshenStaleTime)
	if err != nil {
		return nil, fmt.Errorf("enrich monster changed (new): %w", err)
	}

	var oldPokemon webhook.PokemonWebhook
	if err := json.Unmarshal(partial.Old, &oldPokemon); err != nil {
		return nil, fmt.Errorf("parse monster changed (old): %w", err)
	}
	priorState := tracker.EncounterStateFromPokemon(&oldPokemon)
	original := dts.BuildOriginalView(priorState, ps.enricher.GameData, ps.translatorFor(language))

	result.templateType = "monsterChanged"
	if result.extras == nil {
		result.extras = map[string]any{}
	}
	result.extras["original"] = original

	return result, nil
}
