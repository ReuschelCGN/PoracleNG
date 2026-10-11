package bot

import (
	"slices"
	"testing"

	"github.com/pokemon/poracleng/processor/internal/config"
	"github.com/pokemon/poracleng/processor/internal/geofence"
	"github.com/pokemon/poracleng/processor/internal/store"
)

func pruneFixture() ([]geofence.Fence, *config.Config, *store.MockHumanStore, *store.Human) {
	fences := []geofence.Fence{
		{Name: "alpha", UserSelectable: true},
		{Name: "beta", UserSelectable: true},
		{Name: "priv", UserSelectable: false}, // granted via trusted, in coast
		{Name: "drawn", UserSelectable: false, ExemptFromAreaSecurity: true},
		{Name: "gent_centrum", UserSelectable: true},
	}
	cfg := &config.Config{}
	cfg.Area.Enabled = true
	cfg.Area.Communities = []config.CommunityConfig{
		{Name: "coast", AllowedAreas: []string{"alpha", "priv", "gent_centrum"}},
		{Name: "harbour", AllowedAreas: []string{"beta"}},
	}
	hs := store.NewMockHumanStore()
	all := []string{"alpha", "beta", "priv", "drawn", "gent_centrum"}
	hs.AddHuman(&store.Human{ID: "u1", CurrentProfileNo: 1, Area: all, CommunityMembership: []string{"coast", "harbour"}})
	hs.SeedProfile(store.Profile{ID: "u1", ProfileNo: 1, Area: all})
	// The human as reconciliation sees it AFTER the change: harbour lost.
	after := &store.Human{ID: "u1", CommunityMembership: []string{"coast"}}
	return fences, cfg, hs, after
}

// prune_user_areas on: harbour's beta goes; coast's areas, the trusted-granted
// non-selectable priv (Review Focus 2), the exempt drawn fence, and the
// underscore fence (Review Focus 3) all stay.
func TestPruneForMembership_UserAreasOn(t *testing.T) {
	fences, cfg, hs, after := pruneFixture()
	removed, err := PruneForMembership(hs, fences, cfg, after, false, PruneOptions{Overrides: true, UserAreas: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(removed, []string{"beta"}) {
		t.Fatalf("removed = %v, want [beta]", removed)
	}
	h, _ := hs.Get("u1")
	if !slices.Equal(h.Area, []string{"alpha", "priv", "drawn", "gent_centrum"}) {
		t.Errorf("humans.area = %v", h.Area)
	}
	// override_areas are keyed with spaces: "gent centrum" must be permitted.
	p := hs.PrunedAreaCalls["u1"]
	for _, k := range []string{"alpha", "priv", "drawn", "gent centrum", "gent_centrum"} {
		if !p[k] {
			t.Errorf("override permitted set missing %q: %v", k, p)
		}
	}
	if p["beta"] {
		t.Errorf("override permitted set must not hold beta: %v", p)
	}
}

// Default (prune_user_areas off): humans.area is untouched, but the
// override_areas prune still runs, as it does today.
func TestPruneForMembership_UserAreasOff(t *testing.T) {
	fences, cfg, hs, after := pruneFixture()
	if _, err := PruneForMembership(hs, fences, cfg, after, false, PruneOptions{Overrides: true}); err != nil {
		t.Fatal(err)
	}
	h, _ := hs.Get("u1")
	if len(h.Area) != 5 {
		t.Errorf("humans.area pruned with prune_user_areas off: %v", h.Area)
	}
	if hs.PrunedAreaCalls["u1"] == nil {
		t.Error("override prune must still run")
	}
}

// Review Focus 1: no fences loaded must never wipe anything.
func TestPruneForMembership_NoFencesSkipsEverything(t *testing.T) {
	_, cfg, hs, after := pruneFixture()
	removed, err := PruneForMembership(hs, nil, cfg, after, false, PruneOptions{Overrides: true, UserAreas: true})
	if err != nil || removed != nil {
		t.Fatalf("removed=%v err=%v, want a skip", removed, err)
	}
	h, _ := hs.Get("u1")
	if len(h.Area) != 5 || hs.PrunedAreaCalls["u1"] != nil {
		t.Fatalf("pruned with no fences: area=%v overrides=%v", h.Area, hs.PrunedAreaCalls["u1"])
	}
}

// Review Focus 5: an admin is never community-filtered, so never pruned.
func TestPruneForMembership_AdminKeepsAll(t *testing.T) {
	fences, cfg, hs, after := pruneFixture()
	removed, _ := PruneForMembership(hs, fences, cfg, after, true, PruneOptions{Overrides: true, UserAreas: true})
	if len(removed) != 0 {
		t.Fatalf("admin pruned %v", removed)
	}
}
