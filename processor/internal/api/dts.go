package api

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"

	"github.com/gin-gonic/gin"
	raymond "github.com/mailgun/raymond/v2"
	log "github.com/sirupsen/logrus"

	"github.com/pokemon/poracleng/processor/internal/dts"
)

// HandleTemplateConfig returns DTS template metadata for PoracleWeb.
// GET /api/config/templates
// Optional query parameter: ?includeDescriptions=true adds name/description fields.
func HandleTemplateConfig(ts *dts.TemplateStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		includeDescriptions := c.Query("includeDescriptions") == "true"
		metadata := ts.TemplateMetadata(includeDescriptions)

		resp := map[string]any{"status": "ok"}
		maps.Copy(resp, metadata)
		c.JSON(http.StatusOK, resp)
	}
}

// HandleDTSRender renders a DTS template on demand.
// POST /api/dts/render
//
// Request body:
//
//	{"type": "help", "id": "track", "platform": "discord", "language": "en", "view": {"prefix": "!"}}
//
// Response:
//
//	{"status": "ok", "message": {...rendered template object...}}
func HandleDTSRender(ts *dts.TemplateStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Type     string         `json:"type"`
			ID       string         `json:"id"`
			Platform string         `json:"platform"`
			Language string         `json:"language"`
			View     map[string]any `json:"view"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid request body: " + err.Error()})
			return
		}

		tmpl := ts.Get(req.Type, req.Platform, req.ID, req.Language)
		if tmpl == nil {
			c.JSON(http.StatusNotFound, gin.H{
				"status": "error",
				"error":  fmt.Sprintf("no template found for %s/%s/%s/%s", req.Type, req.Platform, req.ID, req.Language),
			})
			return
		}

		view := req.View
		if view == nil {
			view = make(map[string]any)
		}

		df := raymond.NewDataFrame()
		df.Set("language", req.Language)
		df.Set("platform", req.Platform)

		rendered, err := tmpl.ExecWith(view, df)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "template render failed: " + err.Error()})
			return
		}

		// Parse the rendered JSON string into an object
		var message any
		if err := json.Unmarshal([]byte(rendered), &message); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "rendered template is not valid JSON: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ok", "message": message})
	}
}

// HandleDTSSaveTemplates accepts an array of DTS entries and saves them.
// Each entry is saved to its own file in config/dts/ and removed from its
// previous source file. Readonly entries are rejected.
// POST /api/dts/templates
func HandleDTSSaveTemplates(ts *dts.TemplateStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		var entries []dts.DTSEntry
		if err := c.ShouldBindJSON(&entries); err != nil {
			log.Warnf("dts save: invalid request body: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "invalid request body: " + err.Error()})
			return
		}

		if len(entries) == 0 {
			log.Warnf("dts save: received empty entries array")
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "no templates provided"})
			return
		}

		// Validate required fields
		for i, entry := range entries {
			if entry.Type == "" || entry.Platform == "" {
				msg := fmt.Sprintf("entry %d missing required fields (type=%q, platform=%q, id=%q)", i, entry.Type, entry.Platform, entry.ID)
				log.Warnf("dts save: %s", msg)
				c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": msg})
				return
			}
		}

		saved := 0
		for _, entry := range entries {
			if err := ts.SaveEntry(entry); err != nil {
				log.Warnf("dts save: failed to save %s/%s/%s/%s: %v", entry.Type, entry.Platform, entry.ID, entry.Language, err)
				c.JSON(http.StatusForbidden, gin.H{"status": "error", "message": err.Error()})
				return
			}
			saved++
		}

		log.Infof("dts save: saved %d template(s) via API", saved)
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
			"saved":  saved,
		})
	}
}

