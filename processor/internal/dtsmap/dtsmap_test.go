package dtsmap

import "testing"

// TestAliasFold_CaseInsensitiveMatch covers the resolution !poracle-test
// needs (superpowers/sdd task-9): the bot parser lowercases every token
// before a command ever sees it (internal/bot/parser.go's tokenize), so a
// camelCase DTS template-type name like "monsterChanged" or "monsterNoIv"
// arrives as "monsterchanged" / "monsternoiv" — Alias (case-sensitive) can
// never match those keys from a caller who only has the lowercased form.
func TestAliasFold_CaseInsensitiveMatch(t *testing.T) {
	cases := []struct {
		token           string
		wantWebhookType string
	}{
		{"monster", "pokemon"},
		{"monsterchanged", "monster_changed"},
		{"monsternoiv", "pokemon"},
		{"maxbattle", "max_battle"},
		{"egg", "raid"},
		{"rsvpchanges", "rsvp_changes"},
		{"questsummary", "quest_summary"},
		{"MONSTER", "pokemon"},
		{"MonsterChanged", "monster_changed"},
	}

	for _, tc := range cases {
		t.Run(tc.token, func(t *testing.T) {
			src, ok := AliasFold(tc.token)
			if !ok {
				t.Fatalf("AliasFold(%q) ok = false, want true", tc.token)
			}
			if src.WebhookType != tc.wantWebhookType {
				t.Errorf("AliasFold(%q).WebhookType = %q, want %q", tc.token, src.WebhookType, tc.wantWebhookType)
			}
		})
	}
}

// TestAliasFold_Unknown confirms an unrecognized token (and the
// deliberately-absent "pokestop" identity entry — see the canonical table's
// doc comment) still resolves to ok=false.
func TestAliasFold_Unknown(t *testing.T) {
	for _, token := range []string{"not-a-real-type", "pokestop", "POKESTOP"} {
		if _, ok := AliasFold(token); ok {
			t.Errorf("AliasFold(%q) ok = true, want false", token)
		}
	}
}
