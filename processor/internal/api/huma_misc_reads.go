package api

import (
	"context"
	"crypto/md5" //nolint:gosec // not security-sensitive; matches legacy geofence-hash digest
	"encoding/json"
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"

	"github.com/pokemon/poracleng/processor/internal/gamedata"
	"github.com/pokemon/poracleng/processor/internal/i18n"
	"github.com/pokemon/poracleng/processor/internal/metrics"
	"github.com/pokemon/poracleng/processor/internal/snapshots"
	"github.com/pokemon/poracleng/processor/internal/state"
)

// RegisterGeofenceAll registers GET /api/geofence/all, returning all geofence
// data. Replaces the legacy gin HandleGeofenceAll. Body is {status, geofence}.
func RegisterGeofenceAll(api huma.API, stateMgr *state.Manager) {
	huma.Register(api, huma.Operation{
		OperationID: "get-geofence-all", Method: "GET", Path: "/geofence/all",
		Summary: "All geofence data", Tags: []string{"geofence"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, _ *struct{}) (*anyBodyOutput, error) {
		st := stateMgr.Get()
		return &anyBodyOutput{Body: map[string]any{
			"status":   "ok",
			"geofence": st.Fences,
		}}, nil
	})
}

// RegisterGeofenceHash registers GET /api/geofence/all/hash, returning MD5
// hashes of each geofence path. Replaces the legacy gin HandleGeofenceHash.
// Body is {status, areas}.
func RegisterGeofenceHash(api huma.API, stateMgr *state.Manager) {
	huma.Register(api, huma.Operation{
		OperationID: "get-geofence-hash", Method: "GET", Path: "/geofence/all/hash",
		Summary: "MD5 hashes of geofence paths", Tags: []string{"geofence"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, _ *struct{}) (*anyBodyOutput, error) {
		st := stateMgr.Get()
		areas := make(map[string]string, len(st.Fences))
		for _, f := range st.Fences {
			pathJSON, _ := json.Marshal(f.Path)
			areas[f.Name] = fmt.Sprintf("%x", md5.Sum(pathJSON)) //nolint:gosec // see import note
		}
		return &anyBodyOutput{Body: map[string]any{
			"status": "ok",
			"areas":  areas,
		}}, nil
	})
}

// RegisterGeofenceGeoJSON registers GET /api/geofence/all/geojson, returning
// geofences as a GeoJSON FeatureCollection. Replaces the legacy gin
// HandleGeofenceGeoJSON. Body is {status, geoJSON}.
func RegisterGeofenceGeoJSON(api huma.API, stateMgr *state.Manager) {
	huma.Register(api, huma.Operation{
		OperationID: "get-geofence-geojson", Method: "GET", Path: "/geofence/all/geojson",
		Summary: "Geofences as a GeoJSON FeatureCollection", Tags: []string{"geofence"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, _ *struct{}) (*anyBodyOutput, error) {
		st := stateMgr.Get()

		features := make([]map[string]any, 0, len(st.Fences))
		for _, f := range st.Fences {
			properties := map[string]any{
				"name":             f.Name,
				"color":            f.Color,
				"id":               f.ID,
				"group":            f.Group,
				"description":      f.Description,
				"userSelectable":   f.UserSelectable,
				"displayInMatches": f.DisplayInMatches,
			}

			var geomType string
			var coordinates any

			if len(f.Multipath) > 0 {
				geomType = "MultiPolygon"
				multiCoords := make([][][][2]float64, len(f.Multipath))
				for i, subpath := range f.Multipath {
					ring := make([][2]float64, len(subpath))
					for j, coord := range subpath {
						ring[j] = [2]float64{coord[1], coord[0]} // GeoJSON is [lon, lat]
					}
					if len(ring) > 0 && ring[len(ring)-1] != ring[0] {
						ring = append(ring, ring[0])
					}
					multiCoords[i] = [][][2]float64{ring}
				}
				coordinates = multiCoords
			} else {
				geomType = "Polygon"
				ring := make([][2]float64, len(f.Path))
				for i, coord := range f.Path {
					ring[i] = [2]float64{coord[1], coord[0]} // GeoJSON is [lon, lat]
				}
				if len(ring) > 0 && ring[len(ring)-1] != ring[0] {
					ring = append(ring, ring[0])
				}
				coordinates = [][][2]float64{ring}
			}

			features = append(features, map[string]any{
				"type":       "Feature",
				"properties": properties,
				"geometry": map[string]any{
					"type":        geomType,
					"coordinates": coordinates,
				},
			})
		}

		return &anyBodyOutput{Body: map[string]any{
			"status": "ok",
			"geoJSON": map[string]any{
				"type":     "FeatureCollection",
				"features": features,
			},
		}}, nil
	})
}

// RegisterConfigSchema registers GET /api/config/schema, returning the config
// schema for the editor UI. Replaces the legacy gin HandleConfigSchema. Body is
// {status, sections}.
func RegisterConfigSchema(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-config-schema", Method: "GET", Path: "/config/schema",
		Summary: "Config editor schema", Tags: []string{"config"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, _ *struct{}) (*anyBodyOutput, error) {
		return &anyBodyOutput{Body: map[string]any{
			"status":   "ok",
			"sections": configSchema,
		}}, nil
	})
}

// masterdataMonstersInput carries the optional locale query param.
type masterdataMonstersInput struct {
	Locale string `query:"locale"`
}

// RegisterMasterdataMonsters registers GET /api/masterdata/monsters, building
// the poracle-v2 monsters map from raw masterfile data + translations. Replaces
// the legacy gin HandleMasterdataMonsters. The map is re-marshalled by huma to
// the same JSON the gin handler produced via c.JSON.
func RegisterMasterdataMonsters(api huma.API, gd *gamedata.GameData, translations *i18n.Bundle) {
	huma.Register(api, huma.Operation{
		OperationID: "get-masterdata-monsters", Method: "GET", Path: "/masterdata/monsters",
		Summary: "All pokemon with names, forms, types", Tags: []string{"masterdata"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *masterdataMonstersInput) (*anyBodyOutput, error) {
		if gd == nil {
			return &anyBodyOutput{Body: []any{}}, nil
		}
		locale := in.Locale
		if locale == "" {
			locale = "en"
		}
		tr := translations.For(locale)

		nameMap := make(map[int]string)
		for key := range gd.Monsters {
			if _, ok := nameMap[key.ID]; !ok {
				nameMap[key.ID] = tr.T(fmt.Sprintf("poke_%d", key.ID))
			}
		}

		result := make(map[string]*poracle2Monster, len(gd.Monsters))
		for key, mon := range gd.Monsters {
			types := make([]poracle2TypeEntry, len(mon.Types))
			for i, tid := range mon.Types {
				types[i] = poracle2TypeEntry{
					ID:   tid,
					Name: tr.T(fmt.Sprintf("poke_type_%d", tid)),
				}
			}

			formName := ""
			if key.Form != 0 {
				formName = tr.T(fmt.Sprintf("form_%d", key.Form))
				if formName == fmt.Sprintf("form_%d", key.Form) {
					formName = ""
				}
			}

			evolutions := make([]poracle2Evo, len(mon.Evolutions))
			for i, evo := range mon.Evolutions {
				evolutions[i] = poracle2Evo{
					EvoID:     evo.PokemonID,
					ID:        evo.FormID,
					CandyCost: evo.CandyCost,
				}
			}

			mapKey := fmt.Sprintf("%d_%d", key.ID, key.Form)
			result[mapKey] = &poracle2Monster{
				Name:  nameMap[key.ID],
				ID:    key.ID,
				Types: types,
				Form: poracle2FormEntry{
					Name: formName,
					ID:   key.Form,
				},
				Stats: poracle2Stats{
					BaseAttack:  mon.Attack,
					BaseDefense: mon.Defense,
					BaseStamina: mon.Stamina,
				},
				Evolutions: evolutions,
			}
		}

		return &anyBodyOutput{Body: result}, nil
	})
}

// RegisterMasterdataGrunts registers GET /api/masterdata/grunts, building the
// poracle-v2 grunts map from classic.json grunt data. Replaces the legacy gin
// HandleMasterdataGrunts. The map is re-marshalled by huma to the same JSON the
// gin handler produced via c.Data (json.Marshal of the same value).
func RegisterMasterdataGrunts(api huma.API, gd *gamedata.GameData) {
	// Build the response once since game data is loaded at startup.
	result := buildGruntsResponse(gd)

	huma.Register(api, huma.Operation{
		OperationID: "get-masterdata-grunts", Method: "GET", Path: "/masterdata/grunts",
		Summary: "Grunt types", Tags: []string{"masterdata"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, _ *struct{}) (*anyBodyOutput, error) {
		return &anyBodyOutput{Body: result}, nil
	})
}

// SnapshotReader reads a stored snapshot by key. *snapshots.pogrebStore (via
// the snapshots.Store interface) satisfies this; a minimal interface keeps the
// Register signature testable.
type SnapshotReader interface {
	Read(ctx context.Context, key string) (*snapshots.Snapshot, error)
}

// snapshotGetInput carries the messageID path param and required target query.
type snapshotGetInput struct {
	MessageID string `path:"messageID"`
	Target    string `query:"target" required:"true"`
}

// RegisterSnapshotGet registers GET /api/snapshots/{messageID}, returning the
// stored Snapshot for a delivered message. Replaces the legacy gin
// HandleSnapshotGet. A nil store (snapshots disabled) yields 503; a missing
// snapshot yields 404; a closing store yields 503; other errors yield 500.
func RegisterSnapshotGet(api huma.API, store SnapshotReader) {
	huma.Register(api, huma.Operation{
		OperationID: "get-snapshot", Method: "GET", Path: "/snapshots/{messageID}",
		Summary: "Inspect a delivered-message snapshot", Tags: []string{"snapshots"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(ctx context.Context, in *snapshotGetInput) (*anyBodyOutput, error) {
		// proc.snapshotStore is a nil snapshots.Store interface when
		// [snapshots] enabled = false; passed through the SnapshotReader
		// param it stays a nil interface, so this check surfaces 503,
		// matching the legacy concrete nil check.
		if store == nil {
			return nil, huma.Error503ServiceUnavailable("snapshots disabled")
		}

		key := snapshots.MakeKey(in.Target, in.MessageID)
		snap, err := store.Read(ctx, key)
		if err != nil {
			if errors.Is(err, snapshots.ErrNotFound) {
				metrics.SnapshotReadsTotal.WithLabelValues("miss").Inc()
				return nil, huma.Error404NotFound("snapshot not found")
			}
			if errors.Is(err, snapshots.ErrClosed) {
				return nil, huma.Error503ServiceUnavailable("snapshot store closing")
			}
			metrics.SnapshotReadsTotal.WithLabelValues("error").Inc()
			return nil, huma.Error500InternalServerError(err.Error())
		}
		metrics.SnapshotReadsTotal.WithLabelValues("hit").Inc()
		return &anyBodyOutput{Body: snap}, nil
	})
}
