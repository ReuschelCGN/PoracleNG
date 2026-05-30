package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/pokemon/poracleng/processor/internal/db"
	"github.com/pokemon/poracleng/processor/internal/store"
)

// humaLookupHuman mirrors lookupHuman but takes plain parameters instead of a
// gin.Context. profileNo is the resolved profile number: -1 means "use the
// human's current profile". Returns (nil, 0, nil) when the human is not found;
// the caller should return a 404 in that case.
func humaLookupHuman(deps *TrackingDeps, id string, profileNo int) (*store.HumanLite, int, error) {
	human, err := deps.Humans.GetLite(id)
	if err != nil {
		return nil, 0, err
	}
	if human == nil {
		return nil, 0, nil
	}

	pNo := human.CurrentProfileNo
	if profileNo >= 0 {
		pNo = profileNo
	}

	return human, pNo, nil
}

// listMonsterInput is the huma input type for GET /api/tracking/pokemon/{id}.
//
// huma does not support pointer types for query parameters. We use -1 as a
// sentinel meaning "not provided"; the handler then falls back to the human's
// current profile number. This mirrors the logic in lookupHuman.
type listMonsterInput struct {
	ID        string `path:"id"          doc:"Human/channel/webhook id"`
	ProfileNo int    `query:"profile_no" doc:"Profile number; defaults to the user's active profile" default:"-1"`
}

// listMonsterOutput is the huma output type — preserves the legacy
// {"status":"ok","pokemon":[...]} envelope.
type listMonsterOutput struct {
	Body struct {
		Status  string `json:"status"`
		Pokemon any    `json:"pokemon"`
	}
}

// RegisterTrackingMonster registers the GET /tracking/pokemon/{id} huma operation
// on the given huma.API. The path is relative to the /api group so the full
// public path is /api/tracking/pokemon/{id}.
func RegisterTrackingMonster(humaAPI huma.API, deps *TrackingDeps) {
	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-monster-tracking",
		Method:      http.MethodGet,
		Path:        "/tracking/pokemon/{id}",
		Summary:     "List pokemon tracking rules",
		Tags:        []string{"tracking"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(ctx context.Context, in *listMonsterInput) (*listMonsterOutput, error) {
		human, profileNo, err := humaLookupHuman(deps, in.ID, in.ProfileNo)
		if err != nil {
			return nil, humaNewError(http.StatusInternalServerError, err.Error())
		}
		if human == nil {
			return nil, humaNewError(http.StatusNotFound, "User not found")
		}

		monsters, err := db.SelectMonstersByIDProfile(deps.DB, human.ID, profileNo)
		if err != nil {
			return nil, humaNewError(http.StatusInternalServerError, "database error")
		}

		tr := translatorFor(deps, human)

		type monsterWithDesc struct {
			db.MonsterTrackingAPI
			Description string `json:"description"`
		}

		result := make([]monsterWithDesc, len(monsters))
		for i := range monsters {
			mt := toMonsterTracking(&monsters[i])
			result[i] = monsterWithDesc{
				MonsterTrackingAPI: monsters[i],
				Description:        deps.RowText.MonsterRowText(tr, mt),
			}
		}

		out := &listMonsterOutput{}
		out.Body.Status = "ok"
		out.Body.Pokemon = result
		return out, nil
	})
}
