package commands

import (
	"testing"

	"github.com/pokemon/poracleng/processor/internal/bot"
	"github.com/pokemon/poracleng/processor/internal/mute"
)

func muteTestCtx(t *testing.T) (*bot.CommandContext, *mute.Store) {
	t.Helper()
	ctx, _ := testCtx(t)
	store := mute.NewStore()
	ctx.MuteStore = store
	reg := bot.NewRegistry()
	reg.Register(&MuteCommand{})
	reg.Register(&UnmuteCommand{})
	ctx.Registry = reg
	return ctx, store
}

func onlyMute(t *testing.T, store *mute.Store) mute.Entry {
	t.Helper()
	list := store.List("user1")
	if len(list) != 1 {
		t.Fatalf("mutes = %+v, want exactly one", list)
	}
	return list[0]
}

// Per-type aliases prepend the type name (`!invasion mute pokestop X` runs
// `!mute invasion pokestop X`). Only `<type> id:N` used to be understood;
// any other scope after the type was read as a pokemon name (#280).
func TestMuteTypeAliasWithScope(t *testing.T) {
	cases := []struct {
		typ       string
		args      []string
		wantScope string
		wantValue string
	}{
		{"invasion", []string{"mute", "pokestop", "stop1"}, mute.ScopePokestop, "stop1"},
		{"maxbattle", []string{"mute", "station", "st1"}, mute.ScopeStation, "st1"},
		{"raid", []string{"mute", "everything"}, mute.ScopeEverything, ""},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			ctx, store := muteTestCtx(t)
			replies := RouteToMuteFromType(ctx, tc.typ, tc.args)
			if len(replies) == 0 || replies[0].React != "🔇" {
				t.Fatalf("expected a mute, got %+v", replies)
			}
			e := onlyMute(t, store)
			if e.ScopeType != tc.wantScope || e.ScopeValue != tc.wantValue {
				t.Errorf("mute = %s/%q, want %s/%q", e.ScopeType, e.ScopeValue, tc.wantScope, tc.wantValue)
			}
		})
	}
}

// !track routes its alias as type "pokemon", which is also a scope noun:
// `!track mute id:N` became a pokemon mute named "id:N" (#280).
func TestTrackMuteByRuleID(t *testing.T) {
	ctx, store := muteTestCtx(t)
	replies := RouteToMuteFromType(ctx, "pokemon", []string{"mute", "id:42"})
	if len(replies) == 0 || replies[0].React != "🔇" {
		t.Fatalf("expected a mute, got %+v", replies)
	}
	e := onlyMute(t, store)
	if e.ScopeType != mute.ScopeTracking || e.ScopeValue != "42" {
		t.Errorf("mute = %s/%q, want tracking/42", e.ScopeType, e.ScopeValue)
	}

	replies = RouteToMuteFromType(ctx, "pokemon", []string{"unmute", "id:42"})
	if len(replies) == 0 || replies[0].React != "✅" {
		t.Fatalf("expected unmute, got %+v", replies)
	}
	if n := len(store.List("user1")); n != 0 {
		t.Errorf("mutes after unmute = %d, want 0", n)
	}
}

// The unmute alias must strip the type the same way mute does.
func TestUnmuteTypeAliasWithScope(t *testing.T) {
	ctx, store := muteTestCtx(t)
	RouteToMuteFromType(ctx, "invasion", []string{"mute", "pokestop", "stop1"})
	onlyMute(t, store)
	replies := RouteToMuteFromType(ctx, "invasion", []string{"unmute", "pokestop", "stop1"})
	if len(replies) == 0 || replies[0].React != "✅" {
		t.Fatalf("expected unmute, got %+v", replies)
	}
	if n := len(store.List("user1")); n != 0 {
		t.Errorf("mutes after unmute = %d, want 0", n)
	}
}

// `!gym mute pokestop X` — the type token is also a scope noun; the scope
// after it must win.
func TestGymAliasWithOtherScope(t *testing.T) {
	ctx, store := muteTestCtx(t)
	RouteToMuteFromType(ctx, "gym", []string{"mute", "pokestop", "stop1"})
	e := onlyMute(t, store)
	if e.ScopeType != mute.ScopePokestop || e.ScopeValue != "stop1" {
		t.Errorf("mute = %s/%q, want pokestop/stop1", e.ScopeType, e.ScopeValue)
	}
}
