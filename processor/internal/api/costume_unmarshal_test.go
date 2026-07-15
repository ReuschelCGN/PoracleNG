package api

import (
	"encoding/json"
	"testing"

	"github.com/pokemon/poracleng/processor/internal/db"
)

func TestMonsterTrackingAPI_CostumeDefaults(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"absent → 9000", `{"pokemon_id":25}`, 9000},
		{"explicit 0 → 0", `{"pokemon_id":25,"costume":0}`, 0},
		{"explicit 5 → 5", `{"pokemon_id":25,"costume":5}`, 5},
	}
	for _, c := range cases {
		var m db.MonsterTrackingAPI
		if err := json.Unmarshal([]byte(c.body), &m); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if m.Costume != c.want {
			t.Errorf("%s: Costume = %d, want %d", c.name, m.Costume, c.want)
		}
	}
}
