package handler

type userCreateAccountRequest struct {
	Shared             bool           `json:"shared"`
	Name               string         `json:"name" binding:"required"`
	Notes              *string        `json:"notes"`
	Platform           string         `json:"platform" binding:"required"`
	Type               string         `json:"type" binding:"required,oneof=oauth setup-token apikey upstream bedrock service_account"`
	Credentials        map[string]any `json:"credentials" binding:"required"`
	Extra              map[string]any `json:"extra"`
	Concurrency        int            `json:"concurrency"`
	LoadFactor         *int           `json:"load_factor"`
	Priority           int            `json:"priority"`
	RateMultiplier     *float64       `json:"rate_multiplier"`
	GroupIDs           []int64        `json:"group_ids"`
	ExpiresAt          *int64         `json:"expires_at"`
	AutoPauseOnExpired *bool          `json:"auto_pause_on_expired"`
}

type userUpdateAccountRequest struct {
	Shared             *bool          `json:"shared"`
	Name               string         `json:"name"`
	Notes              *string        `json:"notes"`
	Type               string         `json:"type" binding:"omitempty,oneof=oauth setup-token apikey upstream bedrock service_account"`
	Credentials        map[string]any `json:"credentials"`
	Extra              map[string]any `json:"extra"`
	Concurrency        *int           `json:"concurrency"`
	LoadFactor         *int           `json:"load_factor"`
	Priority           *int           `json:"priority"`
	RateMultiplier     *float64       `json:"rate_multiplier"`
	Status             string         `json:"status" binding:"omitempty,oneof=active inactive error"`
	GroupIDs           *[]int64       `json:"group_ids"`
	ExpiresAt          *int64         `json:"expires_at"`
	AutoPauseOnExpired *bool          `json:"auto_pause_on_expired"`
}

type userTestAccountRequest struct {
	ModelID string `json:"model_id"`
	Prompt  string `json:"prompt"`
	Mode    string `json:"mode"`
}
