package matching

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

// shinyBoost is a boosted shiny denominator (25 = a probability of 1/25).
// Regions == nil: the rate applies everywhere (detected boost). Otherwise only
// inside one of the boxes [minLat, minLon, maxLat, maxLon] (event running there
// in local time); outside them the species is back at BaseShinyRate.
type shinyBoost struct {
	Rate    int
	Regions [][4]float64
}

// shinyBoosted holds per-species boosted shiny rates; species without an entry
// use BaseShinyRate (first nextFloat() < 1/512).
var shinyBoosted atomic.Pointer[map[int]shinyBoost]

func shinyRateFor(pokemonID int, lat, lon float64) int {
	m := shinyBoosted.Load()
	if m == nil {
		return BaseShinyRate
	}
	b, ok := (*m)[pokemonID]
	if !ok {
		return BaseShinyRate
	}
	if b.Regions == nil {
		return b.Rate
	}
	for _, r := range b.Regions {
		if lat >= r[0] && lat <= r[2] && lon >= r[1] && lon <= r[3] {
			return b.Rate
		}
	}
	return BaseShinyRate
}

// StartShinyBoostedSync fetches boosted rates from pogo-ctrl-v2 now and every 5 minutes,
// so a Community Day is picked up shortly after it starts and dropped after it ends.
// It reuses the same config/shiny_link.json as the released-list sync.
func StartShinyBoostedSync(url, secret string) {
	if url == "" {
		return
	}
	go func() {
		client := &http.Client{Timeout: 15 * time.Second}
		for {
			if err := fetchShinyBoosted(client, url, secret); err != nil {
				log.Warnf("shiny-boosted: %v", err)
			}
			time.Sleep(5 * time.Minute)
		}
	}()
}

func fetchShinyBoosted(client *http.Client, baseURL, secret string) error {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/shiny/boosted?consumer=poracle", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Secret", secret)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	var body struct {
		Boosted map[string]struct {
			Rate    int          `json:"rate"`
			Regions *[][]float64 `json:"regions"`
		} `json:"boosted"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	m := make(map[int]shinyBoost, len(body.Boosted))
	for pidStr, info := range body.Boosted {
		var pid int
		if _, err := fmt.Sscanf(pidStr, "%d", &pid); err != nil || info.Rate <= 1 {
			continue
		}
		b := shinyBoost{Rate: info.Rate}
		if info.Regions != nil {
			b.Regions = [][4]float64{}
			for _, r := range *info.Regions {
				if len(r) == 4 {
					b.Regions = append(b.Regions, [4]float64{r[0], r[1], r[2], r[3]})
				}
			}
		}
		m[pid] = b
	}
	old := shinyBoosted.Swap(&m)
	if old == nil || len(*old) != len(m) {
		log.Infof("shiny-boosted: %d species with elevated rates", len(m))
	}
	return nil
}
