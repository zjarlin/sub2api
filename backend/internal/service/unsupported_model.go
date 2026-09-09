package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

const (
	UnsupportedModelsExtraKey       = "unsupported_models"
	unsupportedModelKeyMaxBytes     = 512
	unsupportedModelMessageMaxBytes = 1024
	upstreamUnsupportedModelReason  = "upstream_model_unsupported"
)

type UnsupportedModelObservation struct {
	DetectedAt time.Time `json:"detected_at"`
	StatusCode int       `json:"status_code"`
	Reason     string    `json:"reason"`
	Message    string    `json:"message,omitempty"`
}

func normalizeUnsupportedModelKey(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if len(model) > unsupportedModelKeyMaxBytes {
		return ""
	}
	return model
}

func unsupportedModelKeyForAccount(account *Account, requestedModel string) string {
	if account == nil {
		return ""
	}
	if account.IsOpenAIPassthroughEnabled() {
		return normalizeUnsupportedModelKey(requestedModel)
	}
	return normalizeUnsupportedModelKey(account.GetMappedModel(requestedModel))
}

func observedUnsupportedModelKey(account *Account, model string) string {
	model = strings.TrimSpace(model)
	if account == nil || model == "" || account.IsOpenAIPassthroughEnabled() {
		return normalizeUnsupportedModelKey(model)
	}
	for _, mapped := range account.GetModelMapping() {
		if strings.EqualFold(strings.TrimSpace(mapped), model) {
			return normalizeUnsupportedModelKey(model)
		}
	}
	return normalizeUnsupportedModelKey(account.GetMappedModel(model))
}

func (a *Account) IsModelKnownUnsupported(requestedModel string) bool {
	model := unsupportedModelKeyForAccount(a, requestedModel)
	if model == "" || a.Extra == nil {
		return false
	}
	models, ok := a.Extra[UnsupportedModelsExtraKey].(map[string]any)
	if !ok {
		return false
	}
	_, unsupported := models[model]
	return unsupported
}

func (a *Account) hasKnownUnsupportedModels() bool {
	if a == nil || a.Extra == nil {
		return false
	}
	models, ok := a.Extra[UnsupportedModelsExtraKey].(map[string]any)
	return ok && len(models) > 0
}

func (a *Account) rememberUnsupportedModel(model string, observation UnsupportedModelObservation) {
	model = normalizeUnsupportedModelKey(model)
	if a == nil || model == "" {
		return
	}
	if a.Extra == nil {
		a.Extra = make(map[string]any)
	}
	models, _ := a.Extra[UnsupportedModelsExtraKey].(map[string]any)
	if models == nil {
		models = make(map[string]any)
		a.Extra[UnsupportedModelsExtraKey] = models
	}
	models[model] = observation
}

func isDeterministicUnsupportedModelError(statusCode int, body []byte) bool {
	if statusCode != 400 && statusCode != 404 && statusCode != 422 {
		return false
	}
	for _, path := range []string{
		"error.code", "error.type", "error.message",
		"response.error.code", "response.error.type", "response.error.message",
		"code", "type", "message",
	} {
		value := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, path).String()))
		switch value {
		case "model_not_found", "model_not_available", "unsupported_model", "invalid_model":
			return true
		}
		if isExplicitOpenAIModelAvailabilityMessage(value) {
			return true
		}
	}
	return !gjson.ValidBytes(body) && isExplicitOpenAIModelAvailabilityMessage(string(body))
}

func (s *RateLimitService) persistUnsupportedModel(
	ctx context.Context,
	account *Account,
	model string,
	statusCode int,
	reason string,
	responseBody []byte,
) bool {
	repo, ok := s.accountRepo.(AccountUnsupportedModelRepository)
	model = normalizeUnsupportedModelKey(model)
	if !ok || model == "" {
		return false
	}
	observation := UnsupportedModelObservation{
		DetectedAt: time.Now().UTC(),
		StatusCode: statusCode,
		Reason:     reason,
		Message:    truncateString(extractUpstreamErrorMessage(responseBody), unsupportedModelMessageMaxBytes),
	}
	if err := repo.SetUnsupportedModel(ctx, account.ID, model, observation); err != nil {
		slog.Warn("persist_unsupported_model_failed", "account_id", account.ID, "model", model, "error", err)
		return false
	}
	account.rememberUnsupportedModel(model, observation)
	slog.Info("unsupported_model_persisted", "account_id", account.ID, "model", model, "status_code", statusCode, "reason", reason)
	return true
}
