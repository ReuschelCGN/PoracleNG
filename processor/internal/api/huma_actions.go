package api

import (
	"context"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"
	log "github.com/sirupsen/logrus"

	"github.com/pokemon/poracleng/processor/internal/bot"
	"github.com/pokemon/poracleng/processor/internal/delivery"
)

// testInput carries the freeform poracle-test request. The body is open because
// the `webhook` field is a polymorphic webhook payload (pokemon, raid, quest,
// ...) that huma's default schema generation would otherwise reject.
type testInput struct {
	Body openJSON
}

// RegisterTest registers POST /api/test, the poracle-test endpoint. Replaces
// gin HandleTest. The request body is open: {type, target, webhook} where
// `webhook` is a polymorphic webhook RawMessage. The body is unmarshalled into
// the exact same TestRequest type the legacy handler used, so parsing is
// byte-identical. Success body is {"status":"ok"}.
func RegisterTest(api huma.API, proc bot.TestProcessor) {
	huma.Register(api, huma.Operation{
		OperationID: "post-test", Method: "POST", Path: "/test",
		Summary:     "Generate a test alert",
		Description: "Runs a webhook through the enrichment + render pipeline (skipping matching/dedup) and delivers it to a specific target. Request body is open: {type, target, webhook} where `webhook` is a polymorphic webhook payload.",
		Tags:        []string{"test"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *testInput) (*statusOKOutput, error) {
		var req TestRequest
		if err := json.Unmarshal(in.Body, &req); err != nil {
			return nil, huma.Error400BadRequest("invalid JSON")
		}

		if req.Type == "" || req.Webhook == nil || req.Target.ID == "" {
			return nil, huma.Error400BadRequest("type, webhook, and target.id are required")
		}

		log.Infof("[Test] Processing %s test for %s %s", req.Type, req.Target.Type, req.Target.ID)

		if err := proc.ProcessTest(req.Type, req.Webhook, req.Target.toBotTarget()); err != nil {
			log.Errorf("[Test] Failed to process %s test: %s", req.Type, err)
			return nil, huma.Error500InternalServerError(err.Error())
		}

		out := &statusOKOutput{}
		out.Body.Status = "ok"
		return out, nil
	})
}

// deliverDispatcher is the minimal delivery surface the deliverMessages /
// postMessage endpoints need. *delivery.Dispatcher satisfies it; the interface
// keeps the Register signature testable without a live dispatcher.
type deliverDispatcher interface {
	Dispatch(job *delivery.Job)
}

// deliverInput carries the freeform delivery request: an array of delivery.Job
// whose `Message` field is a pre-rendered RawMessage. Kept open so any valid
// job shape parses; the body is unmarshalled into the exact same []delivery.Job
// the legacy handler used.
type deliverInput struct {
	Body openJSON
}

// deliverMessagesOutput is the typed body for the deliver-messages ops:
// {status, queued} where queued is the count of accepted jobs.
type deliverMessagesOutput struct {
	Body struct {
		Status string `json:"status"`
		Queued int    `json:"queued"`
	}
}

// RegisterDeliverMessages registers a deliver-messages op at the given opID and
// path. It accepts pre-rendered delivery jobs and dispatches them. Replaces gin
// HandleDeliverMessages. Used twice in main.go: once for /deliverMessages and
// once for the legacy /postMessage alias — both serve identically. Success body
// is {"status":"ok","queued":N}.
func RegisterDeliverMessages(api huma.API, opID, path string, dispatcher deliverDispatcher) {
	huma.Register(api, huma.Operation{
		OperationID: opID, Method: "POST", Path: path,
		Summary:     "Deliver pre-rendered messages",
		Description: "Accepts an array of pre-rendered delivery jobs and dispatches them to the delivery system. Request body is open: []Job where each job's `message` field is a pre-rendered RawMessage.",
		Tags:        []string{"delivery"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *deliverInput) (*deliverMessagesOutput, error) {
		if isNilDispatcher(dispatcher) {
			return nil, huma.Error503ServiceUnavailable("delivery dispatcher not configured")
		}

		var jobs []delivery.Job
		if err := json.Unmarshal(in.Body, &jobs); err != nil {
			return nil, huma.Error400BadRequest("invalid JSON: " + err.Error())
		}

		queued := 0
		for i := range jobs {
			if jobs[i].Target == "" || jobs[i].Type == "" {
				continue
			}
			dispatcher.Dispatch(&jobs[i])
			queued++
		}

		log.Debugf("Accepted %d delivery jobs via API", queued)

		out := &deliverMessagesOutput{}
		out.Body.Status = "ok"
		out.Body.Queued = queued
		return out, nil
	})
}

// isNilDispatcher guards against both an interface-nil and a typed-nil
// *delivery.Dispatcher, matching the legacy concrete nil check → 503.
func isNilDispatcher(d deliverDispatcher) bool {
	if d == nil {
		return true
	}
	if dd, ok := d.(*delivery.Dispatcher); ok && dd == nil {
		return true
	}
	return false
}

// resolveRequest is the freeform resolve request body. Each entity list is
// optional; `destinations` holds IDs of unknown type. Mirrors the anonymous
// struct the legacy gin handler bound into.
type resolveRequest struct {
	Discord *struct {
		Users    []string `json:"users"`
		Roles    []string `json:"roles"`
		Channels []string `json:"channels"`
		Guilds   []string `json:"guilds"`
	} `json:"discord"`
	Telegram *struct {
		Chats []string `json:"chats"`
	} `json:"telegram"`
	Destinations []string `json:"destinations"`
}

// resolveInput carries the freeform resolve request. The body is open because it
// has nested optional sections and per-entity dynamically-keyed result maps.
type resolveInput struct {
	Body openJSON
}

// RegisterResolve registers POST /api/resolve, batch-resolving Discord/Telegram
// IDs (and unknown-type destinations) to names. Replaces gin HandleResolve. The
// request body is open and unmarshalled into the same shape the legacy handler
// used. Success body is the freeform {"status":"ok", ...resolved} map.
func RegisterResolve(api huma.API, deps ResolveDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "post-resolve", Method: "POST", Path: "/resolve",
		Summary:     "Resolve Discord/Telegram IDs to names",
		Description: "Batch-resolves Discord/Telegram IDs (and unknown-type destinations) to display names. Request body is open: optional {discord:{users,roles,channels,guilds}, telegram:{chats}, destinations[]}. Response is a freeform map of resolved entities.",
		Tags:        []string{"resolve"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(ctx context.Context, in *resolveInput) (*anyBodyOutput, error) {
		var req resolveRequest
		if err := json.Unmarshal(in.Body, &req); err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		result := map[string]any{"status": "ok"}

		// Resolve unknown-type destinations by trying every category in turn.
		if len(req.Destinations) > 0 {
			destinations := make(map[string]any)
			for _, id := range req.Destinations {
				if resolved := resolveAnyDestination(ctx, deps, id); resolved != nil {
					destinations[id] = resolved
				}
			}
			result["destinations"] = destinations
		}

		// Discord resolution.
		if req.Discord != nil && deps.DiscordSession != nil {
			discord := make(map[string]any)

			if len(req.Discord.Users) > 0 {
				users := make(map[string]any)
				for _, id := range req.Discord.Users {
					if resolved := resolveDiscordUser(deps, id); resolved != nil {
						users[id] = resolved
					}
				}
				discord["users"] = users
			}

			if len(req.Discord.Roles) > 0 {
				roles := make(map[string]any)
				for _, id := range req.Discord.Roles {
					if resolved := resolveDiscordRole(deps, id); resolved != nil {
						roles[id] = resolved
					}
				}
				discord["roles"] = roles
			}

			if len(req.Discord.Channels) > 0 {
				channels := make(map[string]any)
				for _, id := range req.Discord.Channels {
					if resolved := resolveDiscordChannel(deps, id); resolved != nil {
						channels[id] = resolved
					}
				}
				discord["channels"] = channels
			}

			if len(req.Discord.Guilds) > 0 {
				guilds := make(map[string]any)
				for _, id := range req.Discord.Guilds {
					if resolved := resolveDiscordGuild(deps, id); resolved != nil {
						guilds[id] = resolved
					}
				}
				discord["guilds"] = guilds
			}

			result["discord"] = discord
		}

		// Telegram resolution.
		if req.Telegram != nil && deps.TelegramAPI != nil {
			telegram := make(map[string]any)

			if len(req.Telegram.Chats) > 0 {
				chats := make(map[string]any)
				for _, id := range req.Telegram.Chats {
					if resolved := resolveTelegramChat(ctx, deps, id); resolved != nil {
						chats[id] = resolved
					}
				}
				telegram["chats"] = chats
			}

			result["telegram"] = telegram
		}

		return &anyBodyOutput{Body: result}, nil
	})
}

// compile-time assertion that the concrete dispatcher satisfies the deliver
// interface.
var _ deliverDispatcher = (*delivery.Dispatcher)(nil)
