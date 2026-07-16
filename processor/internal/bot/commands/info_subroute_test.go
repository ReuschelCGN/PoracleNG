package commands

import (
	"strings"
	"testing"

	"github.com/pokemon/poracleng/processor/internal/bot"
	"github.com/pokemon/poracleng/processor/internal/gamedata"
	"github.com/pokemon/poracleng/processor/internal/i18n"
	"github.com/pokemon/poracleng/processor/internal/tracker"
)

// manyFormsCtx builds a ctx whose GameData.Monsters gives species 25 (Pikachu)
// more than 10 named forms, plus the recency + costume wiring from
// infoFormCostumeCtx (info_raid_form_test.go), so roster-truncation and the
// forms subroute can both be exercised.
func manyFormsCtx(t *testing.T) *bot.CommandContext {
	t.Helper()
	ctx, _ := testCtx(t)

	monsters := map[gamedata.MonsterKey]*gamedata.Monster{
		{ID: 25, Form: 0}:   {PokemonID: 25, FormID: 0},
		{ID: 25, Form: 680}: {PokemonID: 25, FormID: 680},
	}
	translations := map[string]string{
		"poke_25":   "Pikachu",
		"form_680":  "Winter 2023",
		"costume_1": "Holiday 2016",
	}
	// Add 12 additional named forms (well past the formCap of 10).
	for i := 1; i <= 12; i++ {
		formID := 1000 + i
		monsters[gamedata.MonsterKey{ID: 25, Form: formID}] = &gamedata.Monster{PokemonID: 25, FormID: formID}
		translations[gamedata.FormTranslationKey(formID)] = "Extra Form"
	}

	gd := &gamedata.GameData{
		Monsters: monsters,
		Moves:    map[int]*gamedata.Move{},
		Types:    map[int]*gamedata.TypeInfo{},
		Util:     &gamedata.UtilData{},
		Costumes: map[int]gamedata.CostumeInfo{1: {ID: 1, Name: "Holiday 2016"}},
	}

	ctx.Translations.AddTranslator(i18n.NewTranslator("en", translations))

	ctx.Resolver = bot.NewPokemonResolver(gd, ctx.Translations, []string{"en"}, nil)
	ctx.GameData = gd
	ctx.RecentActivity = tracker.NewRecentActivity()

	return ctx
}

func TestInfo_Pokemon_FormsTruncated(t *testing.T) {
	ctx := manyFormsCtx(t) // ctx whose GameData.Monsters gives species 25 >10 named forms
	text := (&InfoCommand{}).Run(ctx, []string{"pikachu"})[0].Text
	if !strings.Contains(text, "More than 10 forms") {
		t.Errorf("expected roster truncation hint, got: %q", text)
	}
}

func TestInfo_Pokemon_FormsSubroute(t *testing.T) {
	ctx := manyFormsCtx(t)
	ctx.RecentActivity.RecordForm(25, 680)
	text := (&InfoCommand{}).Run(ctx, []string{"pikachu", "forms"})[0].Text
	// Full roster (no truncation hint) AND recent forms.
	if strings.Contains(text, "More than 10 forms") {
		t.Errorf("!info pikachu forms must show the full roster untruncated, got: %q", text)
	}
	if !strings.Contains(text, "form:winter_2023") {
		t.Errorf("!info pikachu forms should include recent forms, got: %q", text)
	}
}

func TestInfo_Pokemon_CostumesSubroute(t *testing.T) {
	ctx := infoFormCostumeCtx(t)
	ctx.RecentActivity.RecordCostume(25, 1)
	ctx.RecentActivity.RecordRaidCostume(25, 8) // a second, raid-only costume
	text := (&InfoCommand{}).Run(ctx, []string{"pikachu", "costumes"})[0].Text
	if !strings.Contains(text, "costume:holiday_2016") {
		t.Errorf("!info pikachu costumes should show combined recent costumes, got: %q", text)
	}
}
