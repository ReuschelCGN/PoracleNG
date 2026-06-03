package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	log "github.com/sirupsen/logrus"

	"github.com/pokemon/poracleng/processor/internal/config"
)

// RegisterConfigPoracleWeb registers GET /api/config/poracleWeb, returning the
// configuration subset PoracleWeb needs for its UI. Replaces gin
// HandleConfigPoracleWeb. The response is a dynamic-keyed map (freeform open
// body); the same {"status":"ok", ...} map the gin handler marshalled is
// re-emitted byte-for-byte via anyBodyOutput.
func RegisterConfigPoracleWeb(api huma.API, cfg *config.Config) {
	// Build the response once since config is immutable after load — mirror
	// the legacy handler's pre-computation exactly.
	resp := buildPoracleWebResponse(cfg)
	huma.Register(api, huma.Operation{
		OperationID: "get-config-poracleweb", Method: "GET", Path: "/config/poracleWeb",
		Summary:     "Server config for web UI",
		Description: "Returns the configuration subset PoracleWeb needs for its UI. Response is a dynamic-keyed map (open body).",
		Tags:        []string{"config"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, _ *struct{}) (*anyBodyOutput, error) {
		return &anyBodyOutput{Body: resp}, nil
	})
}

// configValuesInput carries the optional section query param for the values
// read, matching the legacy ?section= filter.
type configValuesInput struct {
	Section string `query:"section"`
}

// RegisterConfigValues registers GET /api/config/values, returning current
// merged config values (reflection-built map → open body). Replaces gin
// HandleConfigValues. The "overridden" field is always an empty list, preserved
// for editor backward compatibility.
func RegisterConfigValues(api huma.API, deps ConfigDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "get-config-values", Method: "GET", Path: "/config/values",
		Summary:     "Current merged config values",
		Description: "Returns current merged config values (reflection-built map). Response is freeform (open body). The `overridden` field is always an empty list, retained for editor backward compatibility.",
		Tags:        []string{"config"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *configValuesInput) (*anyBodyOutput, error) {
		values := ExtractValues(deps.Cfg, in.Section)
		return &anyBodyOutput{Body: map[string]any{
			"status":     "ok",
			"values":     values,
			"overridden": []string{},
		}}, nil
	})
}

// configWriteInput carries the freeform config-values request. The body is open
// because the editor POSTs a dynamically-keyed {section: {field: value}} map.
type configWriteInput struct {
	Body openJSON
}

// RegisterConfigSave registers POST /api/config/values, rewriting
// config/config.toml in place (with a backup to config/backups/). Replaces gin
// HandleConfigSave. The request body is open (a nested {section:{field:value}}
// map). Parse, validate, save, and reload behaviour — plus the config.toml
// rewrite side effect — are preserved exactly from the legacy handler.
func RegisterConfigSave(api huma.API, deps ConfigDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "post-config-values", Method: "POST", Path: "/config/values",
		Summary:     "Save config changes",
		Description: "Rewrites config/config.toml in place (previous version backed up to config/backups/). Request body is open: a nested {section: {field: value}} map of editor updates.",
		Tags:        []string{"config"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *configWriteInput) (*anyBodyOutput, error) {
		var updates map[string]any
		if err := json.Unmarshal(in.Body, &updates); err != nil {
			return nil, huma.Error400BadRequest("invalid request body: " + err.Error())
		}

		if len(updates) == 0 {
			return nil, huma.Error400BadRequest("no changes provided")
		}

		// Validate that all sections/fields exist in schema.
		if err := validateUpdates(updates); err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		// Per-field value validation (colour format, array length, paths).
		issues := validateConfigValues(updates, deps.ConfigDir)
		var errorIssues []ValidationIssue
		for _, iss := range issues {
			if iss.Severity == "error" {
				errorIssues = append(errorIssues, iss)
			}
		}
		if len(errorIssues) > 0 {
			details := make([]error, 0, len(errorIssues))
			for _, iss := range errorIssues {
				details = append(details, &huma.ErrorDetail{
					Message:  iss.Message,
					Location: iss.Field,
				})
			}
			return nil, huma.Error400BadRequest("validation failed", details...)
		}

		// Strip masked sensitive values ("****") so the editor can resubmit a
		// form without wiping secrets the user didn't touch.
		stripMaskedSensitiveValues(updates)

		// Convert flat table-row fields (discord_channels etc.) back into the
		// nested struct shape before persisting.
		nestTableUpdates(updates)

		// Save directly to config.toml. The previous file is backed up to
		// config/backups/ before the rewrite.
		backupRel, err := writeConfigTOML(deps.ConfigDir, updates)
		if err != nil {
			log.Errorf("config save: %v", err)
			return nil, huma.Error500InternalServerError("save failed: " + err.Error())
		}

		// Apply to in-memory config.
		config.ApplyOverrides(deps.Cfg, updates)

		// Check if restart is required.
		restartRequired, restartFields := checkRestartRequired(updates)

		// Trigger hot-reload if applicable.
		if !restartRequired && deps.ReloadFn != nil {
			deps.ReloadFn()
		}

		saved := countFields(updates)
		log.Infof("config: saved %d field(s) via API (restart_required=%v, backup=%s)", saved, restartRequired, backupRel)

		resp := map[string]any{
			"status":           "ok",
			"saved":            saved,
			"restart_required": restartRequired,
			"backup":           backupRel,
		}
		if len(restartFields) > 0 {
			resp["restart_fields"] = restartFields
		}
		return &anyBodyOutput{Body: resp}, nil
	})
}

// RegisterConfigValidate registers POST /api/config/validate, a dry-run of the
// config-values validation pass (no save). Replaces gin HandleConfigValidate.
// The request body is open (a nested {section:{field:value}} map). The response
// is {"status":"ok","issues":[...]} where issues is the full validation list.
func RegisterConfigValidate(api huma.API, deps ConfigDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "post-config-validate", Method: "POST", Path: "/config/validate",
		Summary:     "Dry-run config validation",
		Description: "Runs the config-values validation pass without saving. Request body is open: a nested {section: {field: value}} map. Response is {status:\"ok\", issues:[...]} where each issue is {field, severity, message}.",
		Tags:        []string{"config"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *configWriteInput) (*anyBodyOutput, error) {
		var updates map[string]any
		if err := json.Unmarshal(in.Body, &updates); err != nil {
			return nil, huma.Error400BadRequest("invalid request body: " + err.Error())
		}

		issues := validateConfigValues(updates, deps.ConfigDir)
		return &anyBodyOutput{Body: map[string]any{
			"status": "ok",
			"issues": issues,
		}}, nil
	})
}

// buildPoracleWebResponse mirrors HandleConfigPoracleWeb's response
// construction exactly.
func buildPoracleWebResponse(cfg *config.Config) map[string]any {
	type hookFlag struct {
		Name    string
		Disable bool
	}
	hookTypes := []hookFlag{
		{"pokemon", cfg.General.DisablePokemon},
		{"raid", cfg.General.DisableRaid},
		{"pokestop", cfg.General.DisablePokestop},
		{"invasion", cfg.General.DisableInvasion},
		{"lure", cfg.General.DisableLure},
		{"quest", cfg.General.DisableQuest},
		{"weather", cfg.General.DisableWeather},
		{"nest", cfg.General.DisableNest},
		{"gym", cfg.General.DisableGym},
		{"maxbattle", cfg.General.DisableMaxBattle},
	}
	disabledHooks := make([]string, 0)
	for _, h := range hookTypes {
		if h.Disable {
			disabledHooks = append(disabledHooks, h.Name)
		}
	}

	defaultTemplateName := "1"
	if cfg.General.DefaultTemplateName != nil {
		defaultTemplateName = fmt.Sprintf("%v", cfg.General.DefaultTemplateName)
	}

	pvpCaps := cfg.PVP.LevelCaps
	if len(pvpCaps) == 0 {
		pvpCaps = []int{50}
	}

	pvpRequiresMinCp := cfg.PVP.ForceMinCP && cfg.PVP.DataSource == "webhook"
	channelNotesContainsCategory := cfg.Discord.CheckRole && cfg.Reconciliation.Discord.UpdateChannelNotes

	staticKeys := cfg.Geocoding.StaticKey
	if staticKeys == nil {
		staticKeys = []string{}
	}

	discordAdmins := cfg.Discord.Admins
	if discordAdmins == nil {
		discordAdmins = []string{}
	}
	telegramAdmins := cfg.Telegram.Admins
	if telegramAdmins == nil {
		telegramAdmins = []string{}
	}

	return map[string]any{
		"status":                       "ok",
		"version":                      Version,
		"locale":                       cfg.General.Locale,
		"prefix":                       cfg.Discord.Prefix,
		"providerURL":                  cfg.Geocoding.ProviderURL,
		"addressFormat":                cfg.Locale.AddressFormat,
		"staticKey":                    staticKeys,
		"pvpFilterMaxRank":             cfg.PVP.PVPFilterMaxRank,
		"pvpFilterGreatMinCP":          cfg.PVP.PVPFilterGreatMinCP,
		"pvpFilterUltraMinCP":          cfg.PVP.PVPFilterUltraMinCP,
		"pvpFilterLittleMinCP":         cfg.PVP.PVPFilterLittleMinCP,
		"pvpLittleLeagueAllowed":       true,
		"pvpCaps":                      pvpCaps,
		"pvpRequiresMinCp":             pvpRequiresMinCp,
		"defaultPvpCap":                cfg.Tracking.DefaultUserTrackingLevelCap,
		"defaultTemplateName":          defaultTemplateName,
		"channelNotesContainsCategory": channelNotesContainsCategory,
		"admins": map[string]any{
			"discord":  discordAdmins,
			"telegram": telegramAdmins,
		},
		"maxDistance":               cfg.Tracking.MaxDistance,
		"defaultDistance":           cfg.Tracking.DefaultDistance,
		"everythingFlagPermissions": cfg.Tracking.EverythingFlagPermissions,
		"disabledHooks":             disabledHooks,
		"gymBattles":                cfg.Tracking.EnableGymBattle,
	}
}
