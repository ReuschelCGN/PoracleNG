package tracker

import (
	"hash/maphash"
	"sync"
	"time"
)

// expiringSetShards is the number of independently-locked shards. Sweeping
// takes one shard's lock at a time, so a full sweep of a multi-million-entry
// set never blocks the webhook path for more than a fraction of the total
// scan.
const expiringSetShards = 16

// expiringSweepInterval is how often the background sweeper reclaims expired
// fingerprints. Reads expire lazily regardless, so this only bounds memory,
// never correctness.
const expiringSweepInterval = time.Minute

// expiringSet is a set of key fingerprints with per-entry expiry.
//
// It exists because storing dedup keys in a ttlcache cost ~265 B per entry —
// a boxed item, the retained key string, a container/list node and an
// expiry-heap entry — to remember a single bit. At scanner throughput the
// live set runs to millions of keys, which measured 1.42 GB (44.8% of live
// heap) in a production profile. Hashing the key to a uint64 and storing only
// its expiry brings that to ~25 B per entry.
//
// Keys are fingerprinted, not retained, so a 64-bit collision would read as a
// false duplicate and drop one alert. Against a live set of ~6M entries that
// is ~3.3e-13 per insert, or roughly one dropped alert every few decades at
// production insert rates.
type expiringSet struct {
	seed   maphash.Seed
	shards [expiringSetShards]expiringShard
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

type expiringShard struct {
	mu sync.Mutex
	// entries maps a key fingerprint to its expiry in unix nanoseconds.
	// Nanoseconds rather than seconds so a sub-second TTL is not truncated
	// into the past on write; the value is 8 bytes either way.
	entries map[uint64]int64
}

func newExpiringSet() *expiringSet {
	s := &expiringSet{
		seed: maphash.MakeSeed(),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	for i := range s.shards {
		s.shards[i].entries = make(map[uint64]int64)
	}
	go s.sweepLoop()
	return s
}

func (s *expiringSet) fingerprint(key string) uint64 {
	return maphash.String(s.seed, key)
}

func (s *expiringSet) shardFor(fp uint64) *expiringShard {
	return &s.shards[fp%expiringSetShards]
}

// Has reports whether key is present and unexpired. Entries past their expiry
// read as absent even when the sweeper has not reclaimed them yet.
func (s *expiringSet) Has(key string) bool {
	fp := s.fingerprint(key)
	sh := s.shardFor(fp)

	sh.mu.Lock()
	defer sh.mu.Unlock()

	expiry, ok := sh.entries[fp]
	return ok && expiry > time.Now().UnixNano()
}

// Add records key for the given ttl, replacing any existing expiry.
func (s *expiringSet) Add(key string, ttl time.Duration) {
	fp := s.fingerprint(key)
	sh := s.shardFor(fp)

	expiry := time.Now().Add(ttl).UnixNano()

	sh.mu.Lock()
	sh.entries[fp] = expiry
	sh.mu.Unlock()
}

// Len returns the number of entries still held, expired or not.
func (s *expiringSet) Len() int {
	var n int
	for i := range s.shards {
		s.shards[i].mu.Lock()
		n += len(s.shards[i].entries)
		s.shards[i].mu.Unlock()
	}
	return n
}

// Close stops the background sweeper and waits for it to exit. Safe to call
// more than once.
//
// The wait matters: sweep holds a shard lock while it scans, so a Close that
// returned early would leave DuplicateCache.Close's step of the shutdown
// ordering non-quiescent. mute.Sweeper and snapshots.Sweeper use the same
// stop/done/once trio.
func (s *expiringSet) Close() {
	s.once.Do(func() { close(s.stop) })
	<-s.done
}

func (s *expiringSet) sweepLoop() {
	defer close(s.done)
	ticker := time.NewTicker(expiringSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.sweep(time.Now().UnixNano())
		}
	}
}

// sweep drops expired entries, taking one shard lock at a time. now is unix
// nanoseconds.
func (s *expiringSet) sweep(now int64) {
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		for fp, expiry := range sh.entries {
			if expiry <= now {
				delete(sh.entries, fp)
			}
		}
		sh.mu.Unlock()
	}
}
