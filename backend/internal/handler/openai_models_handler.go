package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *GatewayHandler) pinnedOpenAIModels(c *gin.Context, group *service.Group) {
	if c.Request.Context().Err() != nil {
		return
	}
	if h.openAIGatewayService == nil {
		writeOpenAIModelsError(c, http.StatusInternalServerError, "api_error", "OpenAI model discovery is not configured")
		return
	}
	etag := c.GetHeader("If-None-Match")
	if c.Param("model") != "" {
		etag = "" // A collection ETag cannot validate a single-model representation.
	}
	response, account, err := h.openAIGatewayService.FetchPinnedOpenAIModelsList(
		c.Request.Context(), group, h.maxAccountSwitches, etag,
	)
	if c.Request.Context().Err() != nil {
		return
	}
	if err != nil {
		if errors.Is(err, service.ErrNoPinnedCodexModelsAccounts) {
			writeOpenAIModelsError(c, http.StatusServiceUnavailable, "upstream_error", "No available OpenAI model discovery accounts")
			return
		}
		writeOpenAIModelsError(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
		return
	}
	setOpsSelectedAccount(c, account.ID, account.Platform)
	writeOpenAIModelsResponse(c, response)
}

func writeOpenAIModelsError(c *gin.Context, status int, errorType, message string) {
	c.JSON(status, gin.H{"error": gin.H{"type": errorType, "message": message}})
}

func writeOpenAIModelsResponse(c *gin.Context, manifest *service.OpenAIModelsResponse) {
	if c.GetBool(autoModelListingKey) && !manifest.NotModified {
		modelIDs := []string{autoModelID}
		if value, ok := c.Get(autoModelListingModelsKey); ok {
			if listed, ok := value.([]string); ok && len(listed) > 0 {
				modelIDs = listed
			}
		}
		body, err := appendVirtualModelsToCatalog(manifest.Body, modelIDs)
		if err != nil {
			writeOpenAIModelsError(c, http.StatusBadGateway, "upstream_error", "Invalid model catalogue")
			return
		}
		clone := *manifest
		clone.Body = body
		clone.ETag = service.CodexModelsManifestETag(body)
		clone.NotModified = c.Param("model") == "" && service.CodexModelsManifestETagMatches(c.GetString(autoModelListingETagKey), clone.ETag)
		manifest = &clone
	}
	if policy := service.ModelAliasesFromContext(c.Request.Context()); policy != nil && len(policy.Groups) > 0 && !manifest.NotModified {
		body, err := policy.CanonicalizeCatalog(manifest.Body)
		if err != nil {
			writeOpenAIModelsError(c, http.StatusBadGateway, "upstream_error", "Invalid model catalogue")
			return
		}
		clone := *manifest
		clone.Body = body
		clone.ETag = service.CodexModelsManifestETag(body)
		clone.NotModified = c.Param("model") == "" && service.CodexModelsManifestETagMatches(c.GetString("model_alias_if_none_match"), clone.ETag)
		manifest = &clone
	}
	if c.Param("model") != "" {
		writeRetrievedModel(c, manifest.Body)
		return
	}
	if manifest.ETag != "" {
		c.Header("ETag", manifest.ETag)
	}
	if manifest.NotModified {
		c.Status(http.StatusNotModified)
		c.Writer.WriteHeaderNow()
		return
	}
	c.Data(http.StatusOK, "application/json", manifest.Body)
}

func appendAutoModelToCatalog(body []byte) ([]byte, error) {
	return appendVirtualModelsToCatalog(body, []string{autoModelID})
}

func appendVirtualModelsToCatalog(body []byte, modelIDs []string) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	field, idField := "data", "id"
	if _, ok := envelope["models"]; ok {
		field, idField = "models", "slug"
	}
	if raw := bytes.TrimSpace(envelope[field]); len(raw) == 0 || raw[0] != '[' {
		return nil, fmt.Errorf("invalid model catalogue entries")
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(envelope[field], &entries); err != nil {
		return nil, err
	}
	entryIndex := make(map[string]int, len(entries))
	for i, raw := range entries {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		var id string
		if json.Unmarshal(item[idField], &id) == nil {
			entryIndex[id] = i
		}
	}
	for _, modelID := range modelIDs {
		if !isVirtualModelID(modelID) {
			continue
		}
		var item map[string]json.RawMessage
		index, exists := entryIndex[modelID]
		if exists {
			if json.Unmarshal(entries[index], &item) != nil {
				return nil, fmt.Errorf("invalid virtual model entry")
			}
		} else if field == "models" {
			generated, err := service.BuildCodexModelsManifest([]string{modelID})
			if err != nil {
				return nil, err
			}
			var catalog struct {
				Models []json.RawMessage `json:"models"`
			}
			if err := json.Unmarshal(generated, &catalog); err != nil || len(catalog.Models) != 1 || json.Unmarshal(catalog.Models[0], &item) != nil {
				return nil, fmt.Errorf("invalid virtual model manifest")
			}
		} else {
			item = map[string]json.RawMessage{
				"id":       json.RawMessage(fmt.Sprintf("%q", modelID)),
				"object":   json.RawMessage(`"model"`),
				"type":     json.RawMessage(`"model"`),
				"created":  json.RawMessage(`1704067200`),
				"owned_by": json.RawMessage(`"sub2api"`),
			}
		}
		if modelID == askModelID {
			item["display_name"] = json.RawMessage(`"Ask"`)
		} else {
			item["display_name"] = json.RawMessage(`"Auto"`)
		}
		item["input_modalities"] = json.RawMessage(`["text","image"]`)
		item["supports_image_detail_original"] = json.RawMessage(`false`)
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		if exists {
			entries[index] = encoded
		} else {
			entryIndex[modelID] = len(entries)
			entries = append(entries, encoded)
		}
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	envelope[field] = encoded
	return json.Marshal(envelope)
}

// Both discovery endpoints consume the same final catalogue, after group/platform
// selection and allowlist filtering. Preserve every field on the selected entry.
func writeModelsListResponse(c *gin.Context, models any) {
	response := gin.H{"object": "list", "data": models}
	body, err := json.Marshal(response)
	if err != nil {
		writeOpenAIModelsError(c, http.StatusInternalServerError, "api_error", "Failed to encode model catalogue")
		return
	}
	writeOpenAIModelsResponse(c, &service.OpenAIModelsResponse{Body: body})
}

func writeRetrievedModel(c *gin.Context, body []byte) {
	var catalog struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		writeOpenAIModelsError(c, http.StatusBadGateway, "upstream_error", "Invalid model catalogue")
		return
	}
	modelID := service.ModelAliasesFromContext(c.Request.Context()).Canonicalize(c.Param("model"))
	for _, raw := range catalog.Data {
		var model map[string]json.RawMessage
		if err := json.Unmarshal(raw, &model); err != nil {
			writeOpenAIModelsError(c, http.StatusBadGateway, "upstream_error", "Invalid model catalogue entry")
			return
		}
		var id string
		if err := json.Unmarshal(model["id"], &id); err != nil {
			writeOpenAIModelsError(c, http.StatusBadGateway, "upstream_error", "Invalid model catalogue ID")
			return
		}
		if id == modelID {
			c.Data(http.StatusOK, "application/json", raw)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
		"type": "invalid_request_error", "code": "model_not_found", "param": "model",
		"message": fmt.Sprintf("Model %q does not exist or is not available for this group", modelID),
	}})
}
