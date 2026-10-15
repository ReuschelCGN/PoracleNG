package matching

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

// shinyReleased holds the Pokédex numbers that have a shiny release. While it
// is nil (pogo-ctrl-v2 not reachable yet) no species counts as shiny, so a
// restart of pogo-ctrl-v2 cannot cause alerts for species without a release.
var shinyReleased atomic.Pointer[map[int]struct{}]

// SetShinyReleased replaces the list of species with a shiny release.
func SetShinyReleased(ids []int) {
	set := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	shinyReleased.Store(&set)
}

// shinyReleasedFor reports whether the species can be shiny at all.
func shinyReleasedFor(pokemonID int) bool {
	set := shinyReleased.Load()
	if set == nil {
		return false
	}
	_, ok := (*set)[pokemonID]
	return ok
}

// StartShinyReleasedSync loads the release list from pogo-ctrl-v2
// (config/shiny_link.json: url + secret) now and every 30 minutes.
func StartShinyReleasedSync(baseDir string) {
	raw, err := os.ReadFile(filepath.Join(baseDir, "config", "shiny_link.json"))
	if err != nil {
		return
	}
	var cfg struct {
		URL    string `json:"url"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.URL == "" {
		log.Warnf("shiny-released: shiny_link.json unusable: %v", err)
		return
	}
	go func() {
		client := &http.Client{Timeout: 15 * time.Second}
		for {
			if n, err := fetchShinyReleased(client, cfg.URL, cfg.Secret); err != nil {
				log.Warnf("shiny-released: %v", err)
				time.Sleep(time.Minute)
				continue
			} else {
				log.Debugf("shiny-released: %d species", n)
			}
			time.Sleep(30 * time.Minute)
		}
	}()
}

func fetchShinyReleased(client *http.Client, url, secret string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(url, "/")+"/api/shiny/released", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Api-Secret", secret)
	res, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	var body struct {
		PokemonIDs []int `json:"pokemon_ids"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return 0, err
	}
	if len(body.PokemonIDs) == 0 {
		return 0, fmt.Errorf("empty list")
	}
	SetShinyReleased(body.PokemonIDs)
	return len(body.PokemonIDs), nil
}
