//go:build unit

package admin

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDoubaoProfileMultipartUpload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var uploads int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploads++
		require.Equal(t, "/internal/profiles/alice", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, `{"cookies":{"sessionid":"fixture-only"}}`, string(body))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"profile":"alice"}`))
	}))
	defer upstream.Close()
	service.SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, DesktopURL: upstream.URL, DesktopKey: "private-key"})
	t.Cleanup(func() { service.SetBuiltinAdapterConfig(nil) })
	router := gin.New()
	router.POST("/profiles", (&AccountHandler{}).UploadDoubaoSessionProfile)

	for _, tc := range []struct {
		name    string
		profile string
		file    string
		status  int
	}{
		{"valid", " alice ", `{"cookies":{"sessionid":"fixture-only"}}`, 201},
		{"reserved", "default", "{}", 400},
		{"path", "../alice", "{}", 400},
		{"oversize", "alice", strings.Repeat("x", maxDoubaoSessionSnapshotBytes+1), 400},
		{"body_limit", "alice", strings.Repeat("x", maxDoubaoSessionSnapshotBytes+(1<<20)+1), 400},
		{"empty", "alice", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			require.NoError(t, writer.WriteField("profile", tc.profile))
			part, err := writer.CreateFormFile("session", "session.json")
			require.NoError(t, err)
			_, err = io.WriteString(part, tc.file)
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			request := httptest.NewRequest(http.MethodPost, "/profiles", &body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			require.NotContains(t, recorder.Body.String(), "fixture-only")
			if tc.status == 201 {
				require.Equal(t, "alice", gjson.Get(recorder.Body.String(), "data.name").String())
			}
		})
	}
	require.Equal(t, 1, uploads)
	request := httptest.NewRequest(http.MethodPost, "/profiles", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
