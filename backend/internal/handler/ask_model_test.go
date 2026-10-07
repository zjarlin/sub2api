package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAskModelEnforcesAnswerOnlyPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{
			name: "responses with coding instructions and tools",
			path: "/v1/responses",
			body: `{"model":"ask","instructions":"You are Codex. Inspect the server with exec_command before answering.","input":[{"role":"developer","content":[{"type":"input_text","text":"Carry out all remaining actions."}]},{"role":"user","content":"Which video models fit two 16GB cards?"}],"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]}`,
		},
		{
			name: "responses without tool declarations",
			path: "/v1/responses",
			body: `{"model":"ask","instructions":"Inspect the server before answering.","input":"Which video models fit two 16GB cards?"}`,
		},
		{
			name: "chat with coding instructions and tools",
			path: "/v1/chat/completions",
			body: `{"model":"ask","messages":[{"role":"system","content":"You are Codex. Inspect the server before answering."},{"role":"user","content":"Which video models fit two 16GB cards?"}],"tools":[{"type":"function","function":{"name":"exec_command","parameters":{"type":"object"}}}]}`,
		},
		{
			name: "chat without tool declarations or system prompt",
			path: "/v1/chat/completions",
			body: `{"model":"ask","messages":[{"role":"user","content":"Which video models fit two 16GB cards?"}]}`,
		},
		{
			name: "auto keeps coding instructions",
			path: "/v1/responses",
			body: `{"model":"auto","instructions":"Inspect the server before answering.","input":"Which video models fit two 16GB cards?"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAutoModelTestHandler(autoModelTestAccounts())
			group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: group})
			}, h.AutoModelMiddleware(nil))
			router.POST(tc.path, func(c *gin.Context) {
				body, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				c.Data(http.StatusOK, "application/json", body)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			if gjson.Get(tc.body, "model").String() == askModelID {
				require.Contains(t, recorder.Body.String(), "You are in Ask mode")
				require.Contains(t, recorder.Body.String(), "DSML")
			} else {
				require.NotContains(t, recorder.Body.String(), "You are in Ask mode")
				require.Equal(t, gjson.Get(tc.body, "instructions").String(), gjson.Get(recorder.Body.String(), "instructions").String())
			}
			require.Contains(t, recorder.Body.String(), "Which video models fit two 16GB cards?")
			require.False(t, gjson.Get(recorder.Body.String(), "tools").Exists())
		})
	}
}

func TestEnforceAskAnswerOnlyPolicyPreservesContext(t *testing.T) {
	for _, body := range []string{
		`{"input":"question","instructions":"original","metadata":{"n":9007199254740993}}`,
		`{"input":[{"role":"developer","content":"original"},{"role":"user","content":"question"}]}`,
		`{"input":[{"type":"function_call","call_id":"old","name":"exec_command","arguments":"{}"},{"type":"function_call_output","call_id":"old","output":"original"},{"role":"user","content":"question"}]}`,
		`{"messages":[{"role":"system","content":"base"},{"role":"developer","content":[{"type":"text","text":"original"}]},{"role":"user","content":"question"}]}`,
		`{"messages":[{"role":"user","content":"question","metadata":{"n":9007199254740993}}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			updated, err := enforceAskAnswerOnlyPolicy([]byte(body))
			require.NoError(t, err)
			require.Contains(t, string(updated), "question")
			require.Equal(t, 1, strings.Count(string(updated), "You are in Ask mode"))
			if strings.Contains(body, "original") {
				require.Contains(t, string(updated), "original")
			}
			if gjson.Get(body, "metadata").Exists() {
				require.Equal(t, gjson.Get(body, "metadata").Raw, gjson.GetBytes(updated, "metadata").Raw)
			}
			if gjson.Get(body, "messages.0.metadata").Exists() {
				require.Equal(t, gjson.Get(body, "messages.0").Raw, gjson.GetBytes(updated, "messages.1").Raw)
			}
			if gjson.Get(body, "input.0.type").String() == "function_call" {
				require.Equal(t, gjson.Get(body, "input").Raw, gjson.GetBytes(updated, "input").Raw)
			}
			again, err := enforceAskAnswerOnlyPolicy(updated)
			require.NoError(t, err)
			require.JSONEq(t, string(updated), string(again))
		})
	}
}
