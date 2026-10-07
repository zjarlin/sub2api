package service

import (
	"context"
	"errors"
)

type AutoModelRouteObservation struct {
	RunID           string               `json:"run_id"`
	Revision        int64                `json:"-"`
	Candidates      []AutoModelCandidate `json:"candidates,omitempty"`
	PlanID          string               `json:"plan_id,omitempty"`
	RequestID       string               `json:"request_id"`
	SessionID       string               `json:"session_id"`
	TurnID          string               `json:"turn_id"`
	RequestedModel  string               `json:"requested_model"`
	SelectedModel   string               `json:"selected_model"`
	ResolvedModel   string               `json:"resolved_model,omitempty"`
	AttemptedModels []string             `json:"attempted_models"`
	State           string               `json:"state"`
	StartedAt       int64                `json:"started_at"`
	UpdatedAt       int64                `json:"updated_at"`
	Operation       *VerticalOperation   `json:"operation,omitempty"`
}

type VerticalOperation struct {
	Kind           string          `json:"kind"`
	Provider       string          `json:"provider,omitempty"`
	TaskID         string          `json:"task_id,omitempty"`
	SourceLanguage string          `json:"source_language,omitempty"`
	TargetLanguage string          `json:"target_language,omitempty"`
	Artifacts      []RouteArtifact `json:"artifacts,omitempty"`
}

type RouteArtifact struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

type AutoModelCandidate struct {
	Model    string   `json:"model"`
	Platform string   `json:"platform"`
	Aliases  []string `json:"aliases,omitempty"`
	Eligible bool     `json:"eligible"`
	Order    int      `json:"order,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

type AutoModelRouteStore interface {
	SaveAutoModelRoute(context.Context, int64, AutoModelRouteObservation) error
	ListAutoModelRoutes(context.Context, int64, string, string) ([]AutoModelRouteObservation, error)
}

func (s *GatewayService) AutoModelRouteStore() (AutoModelRouteStore, error) {
	if s != nil {
		if store, ok := s.usageLogRepo.(AutoModelRouteStore); ok {
			return store, nil
		}
	}
	return nil, errors.New("auto route observations are unavailable")
}
