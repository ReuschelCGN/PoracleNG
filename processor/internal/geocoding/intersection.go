package geocoding

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
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
	// cache can't tie up the whole webhook worker pool. Defaults to 5.
	Concurrency int
}

// Intersection fetches the nearest street intersection for a coordinate from
// GeoNames, with optional shared-cache backing. Reuses a single HTTP client
// and bounds concurrency.
type Intersection struct {
	usernames   []string
	cache       *Cache
	cacheDetail int
	client      *http.Client
	sem         chan struct{}
	baseURL     string // overridable in tests
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
	return &Intersection{
		usernames:   cfg.Usernames,
		cache:       cfg.Cache,
		cacheDetail: detail,
		client:      &http.Client{Timeout: time.Duration(timeout) * time.Millisecond},
		sem:         make(chan struct{}, conc),
		baseURL:     defaultIntersectionBaseURL,
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
// transient failures are not, so they retry on the next sighting.
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
		// Transient failure — don't cache, so a later sighting retries.
		return ""
	}
	if i.cache != nil {
		i.cache.SetIntersection(cacheKey, result)
	}
	return result
}

// fetch performs one GeoNames request. The bool reports whether the call
// produced a usable answer (true even when the answer is "no intersection",
// which is a cacheable fact); false means a transient error not worth caching.
func (i *Intersection) fetch(lat, lon float64) (string, bool) {
	i.sem <- struct{}{}
	defer func() { <-i.sem }()

	username := i.usernames[rand.Intn(len(i.usernames))] //nolint:gosec // username pick, not security-sensitive

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

	// API-level error (e.g. credit exhaustion) — surface it and don't cache.
	if result.Status != nil {
		log.Warnf("GeoNames intersection: API error %d for %f,%f: %s", result.Status.Value, lat, lon, result.Status.Message)
		return "", false
	}

	if result.Intersection.Street1 != "" && result.Intersection.Street2 != "" {
		return fmt.Sprintf("%s & %s", result.Intersection.Street1, result.Intersection.Street2), true
	}

	// Successful call, genuinely no intersection nearby — cacheable.
	return "", true
}
