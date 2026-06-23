package geocoding

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// defaultIntersectionBaseURL is the GeoNames OSM intersection endpoint. The
// OSM variant (vs the US-only findNearestIntersectionJSON used by PoracleJS)
// gives worldwide coverage.
const defaultIntersectionBaseURL = "https://api.geonames.org/findNearestIntersectionOSMJSON"

// IntersectionConfig configures intersection lookups.
type IntersectionConfig struct {
	// Usernames is the pool of GeoNames usernames; one is picked at random
	// per uncached lookup to spread credit usage. Empty disables lookups.
	Usernames []string
	// Cache, when non-nil, is shared with the reverse geocoder so intersection
	// results live in the same pogreb DB (under a separate key namespace).
	// When nil, lookups run uncached.
	Cache *Cache
	// CacheDetail is the lat/lon rounding (decimal places) for cache keys.
	// Defaults to 3 when <= 0.
	CacheDetail int
	// TimeoutMs is the per-request HTTP timeout. Defaults to 5000.
	TimeoutMs int
	// Concurrency bounds simultaneous outbound GeoNames requests so a cold
	// cache can't tie up the whole webhook worker pool. Defaults to 5. This is
	// independent of the reverse geocoder's limiter — GeoNames is a separate
	// service with its own credit pool.
	Concurrency int
	// FailureThreshold is the number of consecutive failures before the circuit
	// opens (stops calling GeoNames). Defaults to 5.
	FailureThreshold int
	// CooldownMs is how long the circuit stays open before a half-open probe.
	// Defaults to 30000.
	CooldownMs int
}

// Intersection fetches the nearest street intersection for a coordinate from
// GeoNames, with optional shared-cache backing. Reuses a single HTTP client,
// bounds concurrency, and trips a circuit breaker so a GeoNames outage can't
// park webhook workers on doomed requests.
type Intersection struct {
	usernames   []string
	cache       *Cache
	cacheDetail int
	client      *http.Client
	sem         chan struct{}
	baseURL     string // overridable in tests

	// Circuit breaker (mirrors Geocoder): after failureThreshold consecutive
	// failures the circuit opens for cooldown, then allows one half-open probe.
	failureThreshold    int
	cooldown            time.Duration
	mu                  sync.Mutex
	consecutiveErrors   int
	circuitOpenSince    time.Time
	halfOpenProbeActive bool
}

// NewIntersection builds an Intersection from config. Returns a usable (no-op
// on empty usernames) instance.
func NewIntersection(cfg IntersectionConfig) *Intersection {
	detail := cfg.CacheDetail
	if detail <= 0 {
		detail = 3
	}
	timeout := cfg.TimeoutMs
	if timeout <= 0 {
		timeout = 5000
	}
	conc := cfg.Concurrency
	if conc <= 0 {
		conc = 5
	}
	threshold := cfg.FailureThreshold
	if threshold <= 0 {
		threshold = 5
	}
	cooldownMs := cfg.CooldownMs
	if cooldownMs <= 0 {
		cooldownMs = 30000
	}
	return &Intersection{
		usernames:        cfg.Usernames,
		cache:            cfg.Cache,
		cacheDetail:      detail,
		client:           &http.Client{Timeout: time.Duration(timeout) * time.Millisecond},
		sem:              make(chan struct{}, conc),
		baseURL:          defaultIntersectionBaseURL,
		failureThreshold: threshold,
		cooldown:         time.Duration(cooldownMs) * time.Millisecond,
	}
}

// geonamesResponse models the fields we read from the GeoNames reply. Over-quota
// and other API-level errors arrive as HTTP 200 with a populated Status object.
type geonamesResponse struct {
	Intersection struct {
		Street1 string `json:"street1"`
		Street2 string `json:"street2"`
	} `json:"intersection"`
	Status *struct {
		Message string `json:"message"`
		Value   int    `json:"value"`
	} `json:"status"`
}

// GetIntersection returns "<street1> & <street2>" for the nearest intersection,
// or "" when none is found, lookups are disabled, or the request fails. A
// successful result (including a stable "no intersection here") is cached;
// transient failures and circuit-open skips are not, so they retry later.
func (i *Intersection) GetIntersection(lat, lon float64) string {
	if len(i.usernames) == 0 {
		return ""
	}

	var cacheKey string
	if i.cache != nil {
		cacheKey = IntersectionCacheKey(lat, lon, i.cacheDetail)
		if v, ok := i.cache.GetIntersection(cacheKey); ok {
			return v
		}
	}

	result, ok := i.fetch(lat, lon)
	if !ok {
		// Transient failure or open circuit — don't cache, so a later sighting
		// retries once the service recovers.
		return ""
	}
	if i.cache != nil {
		i.cache.SetIntersection(cacheKey, result)
	}
	return result
}

// circuitAllows reports whether a request may proceed. When the circuit is
// open and still within cooldown, it denies; after cooldown it lets exactly
// one half-open probe through.
func (i *Intersection) circuitAllows() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.consecutiveErrors < i.failureThreshold {
		return true
	}
	if time.Since(i.circuitOpenSince) < i.cooldown {
		return false
	}
	if i.halfOpenProbeActive {
		return false
	}
	i.halfOpenProbeActive = true
	return true
}

// recordSuccess closes the circuit after a healthy response.
func (i *Intersection) recordSuccess() {
	i.mu.Lock()
	i.consecutiveErrors = 0
	i.halfOpenProbeActive = false
	i.mu.Unlock()
}

// recordFailure advances the failure count and opens the circuit at the
// threshold.
func (i *Intersection) recordFailure() {
	i.mu.Lock()
	i.consecutiveErrors++
	i.halfOpenProbeActive = false
	if i.consecutiveErrors >= i.failureThreshold {
		i.circuitOpenSince = time.Now()
	}
	i.mu.Unlock()
}

// fetch performs one GeoNames request, gated by the circuit breaker and
// concurrency limiter. The bool reports whether the call produced a usable
// answer (true even when the answer is "no intersection", which is a cacheable
// fact); false means a transient error or open circuit not worth caching.
func (i *Intersection) fetch(lat, lon float64) (string, bool) {
	if !i.circuitAllows() {
		log.Debugf("GeoNames intersection: circuit open, skipping %f,%f", lat, lon)
		return "", false
	}

	i.sem <- struct{}{}
	defer func() { <-i.sem }()

	// Classify the outcome for the circuit breaker exactly once on return.
	healthy := false
	defer func() {
		if healthy {
			i.recordSuccess()
		} else {
			i.recordFailure()
		}
	}()

	username := i.usernames[rand.IntN(len(i.usernames))]

	q := url.Values{}
	q.Set("lat", strconv.FormatFloat(lat, 'f', -1, 64))
	q.Set("lng", strconv.FormatFloat(lon, 'f', -1, 64))
	q.Set("username", username)
	reqURL := i.baseURL + "?" + q.Encode()

	ctx, cancel := context.WithTimeout(context.Background(), i.client.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		log.Debugf("GeoNames intersection: build request: %v", err)
		return "", false
	}

	resp, err := i.client.Do(req)
	if err != nil {
		log.Warnf("GeoNames intersection request failed for %f,%f: %v", lat, lon, err)
		return "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Warnf("GeoNames intersection: HTTP %d for %f,%f", resp.StatusCode, lat, lon)
		return "", false
	}

	var result geonamesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Warnf("GeoNames intersection: decode response: %v", err)
		return "", false
	}

	// API-level error (e.g. credit exhaustion) — surface it and trip the breaker.
	if result.Status != nil {
		log.Warnf("GeoNames intersection: API error %d for %f,%f: %s", result.Status.Value, lat, lon, result.Status.Message)
		return "", false
	}

	healthy = true
	if result.Intersection.Street1 != "" && result.Intersection.Street2 != "" {
		return fmt.Sprintf("%s & %s", result.Intersection.Street1, result.Intersection.Street2), true
	}

	// Successful call, genuinely no intersection nearby — cacheable.
	return "", true
}
