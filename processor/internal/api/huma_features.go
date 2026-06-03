package api

import (
	"context"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"
	log "github.com/sirupsen/logrus"

	"github.com/pokemon/poracleng/processor/internal/bot"
	"github.com/pokemon/poracleng/processor/internal/db"
	"github.com/pokemon/poracleng/processor/internal/geofence"
)

// summaryIDInput carries the {id} path parameter for the list-for-user read.
type summaryIDInput struct {
	ID string `path:"id"`
}

// summaryAlertInput carries the {id} and {alertType} path parameters shared by
// the get / delete / trigger summary ops.
type summaryAlertInput struct {
	ID        string `path:"id"`
	AlertType string `path:"alertType"`
}

// summarySetInput carries the {id} / {alertType} path parameters plus the open
// JSON body for the schedule upsert. The body is captured raw (openJSON) so the
// handler can unmarshal it into summarySetRequest with the exact lenient logic
// the legacy gin handler used — accepting active_hours as a JSON string OR an
// array/object, with db.ParseActiveHours tolerating zero-padded ("00") ints.
type summarySetInput struct {
	ID        string   `path:"id"`
	AlertType string   `path:"alertType"`
	Body      openJSON `contentType:"application/json"`
}

// RegisterSummarySet registers POST /api/summaries/{id}/{alertType} (the
// schedule upsert) on the shared huma instance, replacing the legacy gin
// HandleSummarySet. It preserves the lenient active_hours acceptance in place
// (string-or-array body, flex "00" ints) by routing the raw body through the
// existing summarySetRequest type and db.ParseActiveHours; the strict typed
// schema is a separate v2 concern. Success body is the same {"status":"ok"};
// error paths surface as problem+json.
func RegisterSummarySet(api huma.API, deps *SummaryDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "set-summary", Method: "POST", Path: "/summaries/{id}/{alertType}",
		Summary: "Upsert a summary schedule", Tags: []string{"summaries"},
		Description: "Upsert the active-hours schedule for (id, alertType). The " +
			"`active_hours` field is an array of entries " +
			"`{day 0-6, hours 0-23, mins 0-59, optional step, end_hours, end_mins}`. " +
			"For backward compatibility with existing clients this in-place endpoint " +
			"also accepts the legacy lenient forms: `active_hours` may be a " +
			"JSON-string-encoded array, and integer fields may be zero-padded strings " +
			"(e.g. \"00\"). An empty array (or \"\"/\"[]\"/\"{}\") clears the schedule.",
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *summarySetInput) (*statusOKOutput, error) {
		if deps.Schedules == nil {
			return nil, huma.Error503ServiceUnavailable(summaryDisabledMsg)
		}
		if in.ID == "" || in.AlertType == "" {
			return nil, huma.Error400BadRequest("missing path parameter")
		}
		if !isKnownSummaryAlertType(in.AlertType) {
			return nil, huma.Error400BadRequest(unknownAlertTypeMsg)
		}

		var req summarySetRequest
		if err := json.Unmarshal(in.Body, &req); err != nil {
			return nil, huma.Error400BadRequest("invalid request body")
		}
		if req.ActiveHours == nil {
			return nil, huma.Error400BadRequest("active_hours must be specified")
		}

		var hoursJSON string
		switch v := req.ActiveHours.(type) {
		case string:
			hoursJSON = v
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return nil, huma.Error400BadRequest("active_hours could not be serialised")
			}
			hoursJSON = string(b)
		}

		// Validate through the same parser the scheduler uses; empty
		// ""/"[]"/"{}" are allowed (clears the schedule).
		if _, perr := db.ParseActiveHours(hoursJSON); perr != nil {
			return nil, huma.Error400BadRequest("active_hours is not valid JSON for an ActiveHourEntry array")
		}

		if err := deps.Schedules.Set(in.ID, in.AlertType, hoursJSON); err != nil {
			log.Errorf("Summary API: set %s/%s: %v", in.ID, in.AlertType, err)
			return nil, huma.Error500InternalServerError("database error")
		}
		if deps.ReloadFunc != nil {
			deps.ReloadFunc()
		}
		out := &statusOKOutput{}
		out.Body.Status = "ok"
		return out, nil
	})
}

// RegisterSummaries registers the read/delete/trigger summary ops on the shared
// huma instance. It mirrors the legacy gin handlers (HandleSummaryListForUser,
// HandleSummaryGet, HandleSummaryDelete, HandleSummaryTrigger) byte-for-byte on
// success; error paths now surface as problem+json. The POST upsert is
// registered separately by RegisterSummarySet.
func RegisterSummaries(api huma.API, deps *SummaryDeps) {
	// GET /api/summaries/{id} — list every known alert type's schedule.
	huma.Register(api, huma.Operation{
		OperationID: "list-summaries-for-user", Method: "GET", Path: "/summaries/{id}",
		Summary: "List summary schedules for a user", Tags: []string{"summaries"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *summaryIDInput) (*anyBodyOutput, error) {
		if deps.Schedules == nil {
			return nil, huma.Error503ServiceUnavailable(summaryDisabledMsg)
		}
		if in.ID == "" {
			return nil, huma.Error400BadRequest("missing id parameter")
		}
		out := make([]summaryScheduleResponse, 0)
		for _, alertType := range knownSummaryAlertTypes {
			s, err := deps.Schedules.Get(in.ID, alertType)
			if err != nil {
				log.Errorf("Summary API: get %s/%s: %v", in.ID, alertType, err)
				return nil, huma.Error500InternalServerError("database error")
			}
			if s == nil {
				continue
			}
			out = append(out, toSummaryResponse(s))
		}
		return &anyBodyOutput{Body: map[string]any{"status": "ok", "schedules": out}}, nil
	})

	// GET /api/summaries/{id}/{alertType} — fetch one schedule.
	huma.Register(api, huma.Operation{
		OperationID: "get-summary", Method: "GET", Path: "/summaries/{id}/{alertType}",
		Summary: "Get a summary schedule", Tags: []string{"summaries"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *summaryAlertInput) (*anyBodyOutput, error) {
		if deps.Schedules == nil {
			return nil, huma.Error503ServiceUnavailable(summaryDisabledMsg)
		}
		if in.ID == "" || in.AlertType == "" {
			return nil, huma.Error400BadRequest("missing path parameter")
		}
		if !isKnownSummaryAlertType(in.AlertType) {
			return nil, huma.Error400BadRequest(unknownAlertTypeMsg)
		}
		s, err := deps.Schedules.Get(in.ID, in.AlertType)
		if err != nil {
			log.Errorf("Summary API: get %s/%s: %v", in.ID, in.AlertType, err)
			return nil, huma.Error500InternalServerError("database error")
		}
		if s == nil {
			return nil, huma.Error404NotFound("schedule not found")
		}
		return &anyBodyOutput{Body: map[string]any{"status": "ok", "schedule": toSummaryResponse(s)}}, nil
	})

	// DELETE /api/summaries/{id}/{alertType} — remove a schedule (idempotent).
	huma.Register(api, huma.Operation{
		OperationID: "delete-summary", Method: "DELETE", Path: "/summaries/{id}/{alertType}",
		Summary: "Delete a summary schedule", Tags: []string{"summaries"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *summaryAlertInput) (*anyBodyOutput, error) {
		if deps.Schedules == nil {
			return nil, huma.Error503ServiceUnavailable(summaryDisabledMsg)
		}
		if in.ID == "" || in.AlertType == "" {
			return nil, huma.Error400BadRequest("missing path parameter")
		}
		if !isKnownSummaryAlertType(in.AlertType) {
			return nil, huma.Error400BadRequest(unknownAlertTypeMsg)
		}
		if err := deps.Schedules.Delete(in.ID, in.AlertType); err != nil {
			log.Errorf("Summary API: delete %s/%s: %v", in.ID, in.AlertType, err)
			return nil, huma.Error500InternalServerError("database error")
		}
		if deps.ReloadFunc != nil {
			deps.ReloadFunc()
		}
		return &anyBodyOutput{Body: map[string]any{"status": "ok"}}, nil
	})

	// POST /api/summaries/{id}/{alertType}/trigger — flush the buffer now.
	huma.Register(api, huma.Operation{
		OperationID: "trigger-summary", Method: "POST", Path: "/summaries/{id}/{alertType}/trigger",
		Summary: "Trigger a summary dispatch", Tags: []string{"summaries"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *summaryAlertInput) (*anyBodyOutput, error) {
		if deps.Dispatch == nil {
			return nil, huma.Error503ServiceUnavailable(summaryDisabledMsg)
		}
		if in.ID == "" || in.AlertType == "" {
			return nil, huma.Error400BadRequest("missing path parameter")
		}
		if !isKnownSummaryAlertType(in.AlertType) {
			return nil, huma.Error400BadRequest(unknownAlertTypeMsg)
		}
		deps.Dispatch(in.ID, in.AlertType)
		return &anyBodyOutput{Body: map[string]any{"status": "ok"}}, nil
	})
}

// summaryDisabledMsg / unknownAlertTypeMsg keep the legacy wording stable.
const (
	summaryDisabledMsg  = "summary feature is disabled (set tracking.quest_summary_enabled = true)"
	unknownAlertTypeMsg = "unknown alert type (currently only \"quest\" is supported for summary scheduling)"
)

// commandBody mirrors commandRequest but marks every field optional so huma's
// validation matches the legacy gin ShouldBindJSON behaviour (which treated all
// fields as optional and enforced text/user_id presence in handler logic). A
// dedicated struct keeps the shared commandRequest free of huma tags.
type commandBody struct {
	Text      string `json:"text,omitempty" required:"false"`
	UserID    string `json:"user_id,omitempty" required:"false"`
	UserName  string `json:"user_name,omitempty" required:"false"`
	Platform  string `json:"platform,omitempty" required:"false"`
	ChannelID string `json:"channel_id,omitempty" required:"false"`
	GuildID   string `json:"guild_id,omitempty" required:"false"`
	IsDM      bool   `json:"is_dm,omitempty" required:"false"`
}

// commandInput wraps the JSON command body for huma binding.
type commandInput struct {
	Body commandBody
}

// RegisterCommand registers POST /api/command on the shared huma instance,
// replacing the legacy gin HandleCommand. The success body is the same
// commandResponse shape; error paths surface as problem+json.
//
// deps *bot.BotDeps must have Parser populated (added by the caller on top of
// sharedBotDeps) so the endpoint can tokenise the raw text.
func RegisterCommand(api huma.API, deps *bot.BotDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "post-command", Method: "POST", Path: "/command",
		Summary: "Execute a bot command", Tags: []string{"command"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *commandInput) (*anyBodyOutput, error) {
		req := commandRequest{
			Text:      in.Body.Text,
			UserID:    in.Body.UserID,
			UserName:  in.Body.UserName,
			Platform:  in.Body.Platform,
			ChannelID: in.Body.ChannelID,
			GuildID:   in.Body.GuildID,
			IsDM:      in.Body.IsDM,
		}

		if req.Text == "" || req.UserID == "" {
			return nil, huma.Error400BadRequest("text and user_id are required")
		}

		if deps == nil || deps.Parser == nil {
			return nil, huma.Error500InternalServerError("command parser not configured")
		}

		// Parse commands from text
		parsed := deps.Parser.Parse(req.Text)
		if len(parsed) == 0 {
			return &anyBodyOutput{Body: commandResponse{Status: "ok", Replies: nil}}, nil
		}

		// Look up user in DB for language, profile, location, area
		userLang, profileNo, hasLocation, hasArea, _ := bot.LookupUserStateFromStore(deps.Humans, req.UserID, deps.Cfg.General.Locale)

		// Check admin status
		isAdmin := bot.IsAdmin(deps.Cfg, req.Platform, req.UserID)

		// Get geofence data from state
		var spatialIndex *geofence.SpatialIndex
		var fences []geofence.Fence
		if deps.StateMgr != nil {
			if st := deps.StateMgr.Get(); st != nil {
				spatialIndex = st.Geofence
				fences = st.Fences
			}
		}

		var allReplies []bot.Reply

		// Merge consecutive cmd.apply pipe groups back into single invocations.
		parsed = bot.MergeApplyGroups(parsed)

		// Translator for maintenance suffix and error messages.
		tr := deps.Translations.For(userLang)

		for _, cmd := range parsed {
			if cmd.CommandKey == "" {
				// Unknown command
				if req.IsDM {
					allReplies = append(allReplies, bot.Reply{
						Text: "Unknown command",
					})
				}
				continue
			}

			// Look up command handler
			handler := deps.Registry.Lookup(cmd.CommandKey)
			if handler == nil {
				continue
			}

			// Check command security
			if !bot.CommandAllowed(deps.Cfg, req.Platform, cmd.CommandKey, req.UserID, nil) {
				allReplies = append(allReplies, bot.Reply{React: "🙅"})
				continue
			}

			// Build context via NewCommandContext so every BotDeps closure
			// (WebhookRate, AlertLimiter, GeocoderStats/Clear, Reconciler,
			// SlashSync, LogBuffer, etc.) is populated — identical to the
			// gateway and slash surfaces.
			ctx := bot.NewCommandContext(deps)
			// Overlay per-request fields on top of the shared BotDeps.
			ctx.UserID = req.UserID
			ctx.UserName = req.UserName
			ctx.Platform = req.Platform
			ctx.ChannelID = req.ChannelID
			ctx.GuildID = req.GuildID
			ctx.IsDM = req.IsDM
			ctx.IsAdmin = isAdmin
			ctx.Language = userLang
			ctx.ProfileNo = profileNo
			ctx.HasLocation = hasLocation
			ctx.HasArea = hasArea
			ctx.TargetID = req.UserID
			ctx.TargetName = req.UserName
			ctx.TargetType = req.Platform + ":user"
			ctx.AreaLogic = bot.NewAreaLogic(fences, deps.Cfg)
			ctx.Geofence = spatialIndex
			ctx.Fences = fences

			// Handle target override (user<id>, name<webhook>)
			target, remainingArgs, err := bot.BuildTarget(ctx, cmd.Args)
			if err != nil {
				log.Debugf("command: target resolution failed: %v", err)
				allReplies = append(allReplies, bot.Reply{React: "🙅", Text: bot.LocalizeTargetError(tr, err)})
				continue
			}
			if target != nil {
				ctx.TargetID = target.ID
				ctx.TargetName = target.Name
				ctx.TargetType = target.Type
				if target.Language != "" {
					ctx.Language = target.Language
				}
				ctx.ProfileNo = target.ProfileNo
				ctx.HasLocation = target.HasLocation
				ctx.HasArea = target.HasArea
			}

			replies := handler.Run(ctx, remainingArgs)
			// Apply maintenance suffix so callers see the paused-delivery
			// warning, matching discordbot/bot.go and telegrambot/bot.go.
			replies = bot.ApplyMaintenanceSuffix(replies, deps.Dispatcher, tr.T("cmd.maintenance.active_suffix"))
			allReplies = append(allReplies, replies...)
		}

		return &anyBodyOutput{Body: commandResponse{Status: "ok", Replies: allReplies}}, nil
	})
}
