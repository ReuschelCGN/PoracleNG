package tracker

import (
	"testing"
	"time"
)

// TestDuplicateCacheEntryExpires covers the behaviour the dedup path depends
// on: once a key's TTL has passed the same webhook must be treated as fresh
// again, whether or not the sweeper has run.
func TestDuplicateCacheEntryExpires(t *testing.T) {
	dc := NewDuplicateCache()
	defer dc.Close()

	// disappear_time already in the past clamps the TTL to its 60s floor,
	// so drive expiry through the set directly with a sub-second TTL.
	dc.seen.Add("encounter-1", 10*time.Millisecond)

	if !dc.seen.Has("encounter-1") {
		t.Fatal("key should be present immediately after Add")
	}

	time.Sleep(30 * time.Millisecond)

	if dc.seen.Has("encounter-1") {
		t.Error("key should read as absent once its TTL has passed, before any sweep")
	}
}

// TestExpiringSetSweepReclaimsExpiredEntries asserts the sweeper actually
// frees entries rather than only hiding them from reads — the whole point of
// the change is bounded memory, and lazy expiry alone would never reclaim.
func TestExpiringSetSweepReclaimsExpiredEntries(t *testing.T) {
	s := newExpiringSet()
	defer s.Close()

	for i := range 1000 {
		s.Add(encounterIDForTest(i), time.Hour)
	}
	for i := 1000; i < 2000; i++ {
		s.Add(encounterIDForTest(i), time.Millisecond)
	}

	if got := s.Len(); got != 2000 {
		t.Fatalf("expected 2000 entries before sweep, got %d", got)
	}

	time.Sleep(10 * time.Millisecond)
	s.sweep(time.Now().UnixNano())

	if got := s.Len(); got != 1000 {
		t.Errorf("expected the 1000 expired entries to be reclaimed, got %d remaining", got)
	}
}

// TestExpiringSetSpreadsAcrossShards guards the sharding: a single-shard set
// would serialise every dedup check on one mutex at scanner throughput.
func TestExpiringSetSpreadsAcrossShards(t *testing.T) {
	s := newExpiringSet()
	defer s.Close()

	for i := range 10_000 {
		s.Add(encounterIDForTest(i), time.Hour)
	}

	populated := 0
	for i := range s.shards {
		s.shards[i].mu.Lock()
		if len(s.shards[i].entries) > 0 {
			populated++
		}
		s.shards[i].mu.Unlock()
	}

	if populated != expiringSetShards {
		t.Errorf("expected all %d shards populated, got %d", expiringSetShards, populated)
	}
}
