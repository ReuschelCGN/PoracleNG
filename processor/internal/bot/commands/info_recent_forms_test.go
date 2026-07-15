package commands

import (
	"strings"
	"testing"

	"github.com/pokemon/poracleng/processor/internal/bot"
	"github.com/pokemon/poracleng/processor/internal/gamedata"
	"github.com/pokemon/poracleng/processor/internal/i18n"
	"github.com/pokemon/poracleng/processor/internal/tracker"
)

// infoFormCtx mirrors infoCostumeCtx (info_costume_test.go) but wires a named
// form (680 → "Winter 2023") so !info pikachu can exercise the recent-forms
// section.
func infoFormCtx(t *testing.T) *bot.CommandContext {
	t.Helper()
	ctx, _ := testCtx(t)

	gd := &gamedata.GameData{
		Monsters: map[gamedata.MonsterKey]*gamedata.Monster{
			{ID: 25, Form: 0}:   {PokemonID: 25, FormID: 0},
			{ID: 25, Form: 680}: {PokemonID: 25, FormID: 680},
		},
		Moves: map[int]*gamedata.Move{},
		Types: map[int]*gamedata.TypeInfo{},
		Util:  &gamedata.UtilData{},
	}

	ctx.Translations.AddTranslator(i18n.NewTranslator("en", map[string]string{
		"poke_25":  "Pikachu",
		"form_680": "Winter 2023",
	}))

	ctx.Resolver = bot.NewPokemonResolver(gd, ctx.Translations, []string{"en"}, nil)
	ctx.GameData = gd
	ctx.RecentActivity = tracker.NewRecentActivity()

	return ctx
}

func TestInfo_Pokemon_RecentlySeenForms(t *testing.T) {
	ctx := infoFormCtx(t)
	ctx.RecentActivity.RecordForm(25, 680)

	cmd := &InfoCommand{}
	replies := cmd.Run(ctx, []string{"pikachu"})
	if len(replies) == 0 {
		t.Fatal("expected at least one reply, got none")
	}
	text := replies[0].Text
	if !strings.Contains(text, "680 — Winter 2023") {
		t.Errorf("expected 'id — name' recent form line, got: %q", text)
	}
	if !strings.Contains(text, "Recently-seen forms") {
		t.Errorf("expected a recently-seen forms header, got: %q", text)
	}
}

func TestInfo_Pokemon_NoRecentForms_SectionOmitted(t *testing.T) {
	ctx := infoFormCtx(t)

	cmd := &InfoCommand{}
	replies := cmd.Run(ctx, []string{"pikachu"})
	if len(replies) == 0 {
		t.Fatal("expected at least one reply, got none")
	}
	if strings.Contains(replies[0].Text, "Recently-seen forms") {
		t.Errorf("expected no recent-forms section when none recorded, got: %q", replies[0].Text)
	}
}
