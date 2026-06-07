package enrichment

import (
	"testing"

	"github.com/pokemon/poracleng/processor/internal/gamedata"
	"github.com/pokemon/poracleng/processor/internal/i18n"
	"github.com/pokemon/poracleng/processor/internal/webhook"
)

// maxbattleTestEnricher builds an Enricher with generation data + a translator
// so we can assert the pokemonId / generation / generationName fields that DTS
// maxbattle templates reference (parity with raid/pokemon).
func maxbattleTestEnricher() *Enricher {
	bundle := i18n.NewBundle()
	bundle.AddTranslator(i18n.NewTranslator("en", map[string]string{
		"poke_6":       "Charizard",
		"generation_1": "Kanto",
		"max_battle_6": "Tier 6",
		"poke_type_4":  "Fire",
		"poke_type_12": "Grass",
	}))
	gd := &gamedata.GameData{
		Monsters: map[gamedata.MonsterKey]*gamedata.Monster{
			{ID: 6, Form: 0}: {PokemonID: 6, FormID: 0, Types: []int{4, 12}, GenID: 1, Attack: 1, Defense: 1, Stamina: 1},
		},
		Moves: map[int]*gamedata.Move{},
		Types: map[int]*gamedata.TypeInfo{},
		Util: &gamedata.UtilData{
			GenData: map[int]gamedata.GenInfo{1: {Roman: "I"}},
		},
	}
	return &Enricher{
		WeatherProvider: &mockWeather{},
		TimeLayout:      "15:04:05",
		DateLayout:      "2006-01-02",
		GameData:        gd,
		Translations:    bundle,
		DefaultLocale:   "en",
	}
}

func TestMaxbattle_SetsPokemonIdAndGeneration(t *testing.T) {
	e := maxbattleTestEnricher()
	mb := &webhook.MaxbattleWebhook{
		ID: "station1", Name: "Power Spot", Latitude: 52.5, Longitude: 13.4,
		BattleLevel: 6, BattlePokemonID: 6, BattlePokemonForm: 0,
	}
	m, _ := e.Maxbattle(52.5, 13.4, 0, mb, TileModeSkip)

	if got := m["pokemonId"]; got != 6 {
		t.Errorf("pokemonId = %v, want 6", got)
	}
	if got := m["generation"]; got != 1 {
		t.Errorf("generation = %v, want 1", got)
	}
}

func TestMaxbattleTranslate_SetsGenerationName(t *testing.T) {
	e := maxbattleTestEnricher()
	mb := &webhook.MaxbattleWebhook{
		ID: "station1", Latitude: 52.5, Longitude: 13.4,
		BattleLevel: 6, BattlePokemonID: 6, BattlePokemonForm: 0,
	}
	base, _ := e.Maxbattle(52.5, 13.4, 0, mb, TileModeSkip)
	m := e.MaxbattleTranslate(base, mb, "en")

	if got := m["generation"]; got != 1 {
		t.Errorf("generation = %v, want 1", got)
	}
	if got, _ := m["generationName"].(string); got != "Kanto" {
		t.Errorf("generationName = %q, want %q", got, "Kanto")
	}
}
