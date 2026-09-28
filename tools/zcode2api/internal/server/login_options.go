package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"glm-zcode-2api/internal/credential"
)

type loginOptions struct {
	Plan     string `json:"plan"`
	Provider string `json:"provider"`
}

type loginOptionsKey struct{}

// 套餐选项只在创建会话时读取，随后固定在该会话内。
func (s *Server) loginOptionsHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/login/sessions" {
			next.ServeHTTP(w, r)
			return
		}
		s.auth(func(w http.ResponseWriter, r *http.Request) {
			options := loginOptions{Plan: s.cfg.Upstream.Plan, Provider: s.cfg.Upstream.OAuthProvider}
			var input loginOptions
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
				writeError(w, http.StatusBadRequest, "invalid_request_error", "Invalid ZCode login options", nil)
				return
			}
			if input.Plan != "" {
				options.Plan = input.Plan
			}
			if input.Provider != "" {
				options.Provider = input.Provider
			}
			if (options.Plan != credential.PlanCoding && options.Plan != credential.PlanStart) || (options.Provider != "zai" && options.Provider != "bigmodel") {
				writeError(w, http.StatusBadRequest, "invalid_request_error", "Unsupported ZCode plan or account provider", nil)
				return
			}
			ctx := context.WithValue(r.Context(), loginOptionsKey{}, options)
			next.ServeHTTP(w, r.WithContext(ctx))
		})(w, r)
	})
}
