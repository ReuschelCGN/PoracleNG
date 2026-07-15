package gamedata

import "testing"

func TestCostumeTranslationKey(t *testing.T) {
	if got := CostumeTranslationKey(1); got != "costume_1" {
		t.Errorf("CostumeTranslationKey(1) = %q, want costume_1", got)
	}
}

func TestCostumesLoaded(t *testing.T) {
	gd := loadTestGameData(t) // reuse the existing gamedata test loader
	if gd.Costumes == nil || gd.Costumes[1].Name == "" {
		t.Fatalf("costume 1 not loaded; got %+v", gd.Costumes[1])
	}
}
