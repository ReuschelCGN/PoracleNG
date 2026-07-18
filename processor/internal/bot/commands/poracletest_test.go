package commands

import "testing"

// TestResolveDTSType_QuestSummary locks in a real defect found while wiring
// the "quest-summary" !poracle-test type (superpowers/sdd task-5): the
// Parser lowercases every arg before PoracleTestCommand.Run ever sees it
// (see internal/bot/parser.go's tokenize), so a validHooks entry can only
// ever be matched in its already-lowercase form. Multi-word CLI names must
// therefore use the lowercase-dash spelling ("quest-summary", mirroring the
// existing "fort-update"/"max-battle" entries) — never the camelCase DTS
// template-type name ("questSummary") a user could never actually type.
// resolveDTSType is the seam that maps the post-ReplaceAll wire spelling
// ("quest_summary") back to the real, case-sensitive registered DTS
// template type ("questSummary") for template-existence validation.
func TestResolveDTSType_QuestSummary(t *testing.T) {
	if got := resolveDTSType("quest_summary", nil); got != "questSummary" {
		t.Errorf(`resolveDTSType("quest_summary", nil) = %q, want "questSummary"`, got)
	}
}
