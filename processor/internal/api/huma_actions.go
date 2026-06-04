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

// apiDeliverJob is the request DTO for a single pre-rendered delivery job. It
// mirrors the caller-relevant fields of delivery.Job with proper types so the
// OpenAPI document describes the request shape. The `message` field uses
// openJSON so its schema is OPEN (arbitrary JSON object/value) rather than the
// base64 `string`/`byte` schema huma would otherwise emit for the underlying
// json.RawMessage — openJSON implements huma.SchemaProvider (returning an empty,
// constraint-free schema) and captures raw bytes via its UnmarshalJSON, so the
// pre-rendered payload round-trips verbatim into delivery.Job.Message.
//
// Internal-only fields of delivery.Job (StaticMapData, Language, ReplyToID,
// BypassRateLimit, SnapshotData — all `json:"-"`) are intentionally omitted.
type apiDeliverJob struct {
	Target  string   `json:"target" doc:"Destination ID/URL (user, channel, thread, webhook)"`
	Type    string   `json:"type" doc:"Destination type, e.g. discord:user, discord:channel, discord:thread, webhook, telegram:user, telegram:group, telegram:channel"`
	Message openJSON `json:"message" doc:"Pre-rendered platform message payload (arbitrary JSON object/value)"`
	TTH     struct {
		Days    int `json:"days,omitempty"`
		Hours   int `json:"hours,omitempty"`
		Minutes int `json:"minutes,omitempty"`
		Seconds int `json:"seconds,omitempty"`
	} `json:"tth,omitempty" doc:"Time-to-hide; when the message should be auto-deleted (with the clean bit)"`
	Clean        int     `json:"clean,omitempty" doc:"Lifecycle bitmask: 1=clean (delete on TTH), 2=edit, 3=both"`
	EditKey      string  `json:"editKey,omitempty" doc:"Non-empty = track for future in-place edits"`
	ReplyKey     string  `json:"replyKey,omitempty" doc:"Non-empty = (replyKey,target) indexes the latest sent message for reply chaining"`
	MsgType      string  `json:"msgType,omitempty" doc:"Alert type (raid, egg, pokemon, ...) for per-lifecycle-type tracking"`
	Name         string  `json:"name,omitempty" doc:"Human-readable destination name"`
	LogReference string  `json:"logReference,omitempty" doc:"Encounter/gym ID for tracing"`
	Lat          float64 `json:"lat,omitempty"`
	Lon          float64 `json:"lon,omitempty"`
}

// toJob converts the request DTO into the internal delivery.Job dispatched to
// the delivery system. The open `message` bytes become Job.Message verbatim.
func (j apiDeliverJob) toJob() delivery.Job {
	return delivery.Job{
		Target:       j.Target,
		Type:         j.Type,
		Message:      json.RawMessage(j.Message),
		TTH:          delivery.TTH{Days: j.TTH.Days, Hours: j.TTH.Hours, Minutes: j.TTH.Minutes, Seconds: j.TTH.Seconds},
		Clean:        j.Clean,
		EditKey:      j.EditKey,
		ReplyKey:     j.ReplyKey,
		MsgType:      j.MsgType,
		Name:         j.Name,
		LogReference: j.LogReference,
		Lat:          j.Lat,
		Lon:          j.Lon,
	}
}

// deliverInput carries the delivery request: an array of pre-rendered delivery
// jobs. The typed []apiDeliverJob body gives the OpenAPI document a real schema
// (with an OPEN `message` field) while huma validates the array shape for us.
type deliverInput struct {
	Body []apiDeliverJob
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
// path. It accepts an array of pre-rendered delivery jobs and dispatches them.
// Replaces gin HandleDeliverMessages. Used twice in main.go: once for the
// canonical /deliverMessages and once for the legacy /postMessage alias — both
// serve identically; only their summary/description differ so the docs explain
// which to use. Success body is {"status":"ok","queued":N}.
func RegisterDeliverMessages(api huma.API, opID, path, summary, description string, dispatcher deliverDispatcher) {
	huma.Register(api, huma.Operation{
		OperationID: opID, Method: "POST", Path: path,
		Summary:     summary,
		Description: description,
		Tags:        []string{"delivery"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *deliverInput) (*deliverMessagesOutput, error) {
		if isNilDispatcher(dispatcher) {
			return nil, huma.Error503ServiceUnavailable("delivery dispatcher not configured")
		}

		queued := 0
		for i := range in.Body {
			if in.Body[i].Target == "" || in.Body[i].Type == "" {
				continue
			}
			job := in.Body[i].toJob()
			dispatcher.Dispatch(&job)
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

// resolveResult is the typed top-level resolve response.
//
// Only the STABLE top-level structure is typed: status plus the three optional
// sections. The sections are modelled as dynamically-keyed maps because their
// keys are the caller-supplied entity ids and their leaf values are free-form
// resolved-entity objects whose fields vary by entity kind (a resolved channel
// carries {name,type,guild,…}; a resolved guild only {name}; etc.). huma emits
// an object-with-additionalProperties schema for each.
//
// Presence is byte-identical to the legacy map[string]any response:
//   - `discord`/`telegram`/`destinations` are pointers so a nil ⇒ key absent
//     (legacy only set them when the request included that platform/section AND,
//     for discord/telegram, the relevant bot was configured). A NON-nil but
//     EMPTY section still serialises ({} / inner {}) — matching the legacy
//     "request list present but nothing resolved" case (`make`d then assigned).
//   - inner per-entity maps (users/roles/…/chats) are added to the section only
//     when their request list was non-empty, again matching legacy.
//
// The leaf entity object is map[string]any (a free-form resolved-entity record).
//
// Field order is deliberately alphabetical (destinations, discord, status,
// telegram) to match Go's map-key sort order from the legacy map[string]any
// response, keeping the serialised bytes identical.
type resolveResult struct {
	Destinations *map[string]any            `json:"destinations,omitempty" doc:"id → resolved destination object (unknown-type lookup across platforms)"`
	Discord      *map[string]map[string]any `json:"discord,omitempty" doc:"section → (id → resolved Discord entity). Sections: users, roles, channels, guilds"`
	Status       string                     `json:"status"`
	Telegram     *map[string]map[string]any `json:"telegram,omitempty" doc:"section → (id → resolved Telegram entity). Section: chats"`
}

// resolveOutput is the typed huma output for POST /api/resolve.
type resolveOutput struct {
	Body resolveResult
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
	}, func(ctx context.Context, in *resolveInput) (*resolveOutput, error) {
		var req resolveRequest
		if err := json.Unmarshal(in.Body, &req); err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		result := resolveResult{Status: "ok"}

		// Resolve unknown-type destinations by trying every category in turn.
		if len(req.Destinations) > 0 {
			destinations := make(map[string]any)
			for _, id := range req.Destinations {
				if resolved := resolveAnyDestination(ctx, deps, id); resolved != nil {
					destinations[id] = resolved
				}
			}
			result.Destinations = &destinations
		}

		// Discord resolution.
		if req.Discord != nil && deps.DiscordSession != nil {
			discord := make(map[string]map[string]any)

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

			result.Discord = &discord
		}

		// Telegram resolution.
		if req.Telegram != nil && deps.TelegramAPI != nil {
			telegram := make(map[string]map[string]any)

			if len(req.Telegram.Chats) > 0 {
				chats := make(map[string]any)
				for _, id := range req.Telegram.Chats {
					if resolved := resolveTelegramChat(ctx, deps, id); resolved != nil {
						chats[id] = resolved
					}
				}
				telegram["chats"] = chats
			}

			result.Telegram = &telegram
		}

		return &resolveOutput{Body: result}, nil
	})
}

// compile-time assertion that the concrete dispatcher satisfies the deliver
// interface.
var _ deliverDispatcher = (*delivery.Dispatcher)(nil)
