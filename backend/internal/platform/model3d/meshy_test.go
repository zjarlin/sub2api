package model3d

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMeshyTextTo3DCreateUsesV2PathAndNativeBody(t *testing.T) {
	var (
		gotPath   string
		gotMethod string
		gotAuth   string
		gotBody   map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotAuth = r.URL.Path, r.Method, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"meshy-task-1"}`))
	}))
	defer server.Close()

	provider := NewMeshyProvider(&MeshyConfig{APIKey: "msy_test", BaseURL: server.URL + "/openapi"}, 5)
	taskID, err := provider.Create(context.Background(), &CreateRequest{
		Mode:      ModeTextTo3D,
		Prompt:    "a red sports car",
		FaceLimit: 12000,
		Topology:  "quad",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if taskID != "meshy-task-1" {
		t.Fatalf("task id = %q", taskID)
	}
	if gotPath != "/openapi/v2/text-to-3d" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q", gotMethod)
	}
	if gotAuth != "Bearer msy_test" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotBody["mode"] != "preview" {
		t.Fatalf("mode = %v", gotBody["mode"])
	}
	if gotBody["prompt"] != "a red sports car" {
		t.Fatalf("prompt = %v", gotBody["prompt"])
	}
	if gotBody["topology"] != "quad" {
		t.Fatalf("topology = %v", gotBody["topology"])
	}
	if gotBody["target_polycount"] != float64(12000) {
		t.Fatalf("target_polycount = %v", gotBody["target_polycount"])
	}
	if gotBody["ai_model"] != "meshy-5" {
		t.Fatalf("ai_model = %v", gotBody["ai_model"])
	}
}

func TestMeshyOptionsOverrideDefaultsButEmptyValuesDoNot(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"result":"t"}`))
	}))
	defer server.Close()

	provider := NewMeshyProvider(&MeshyConfig{APIKey: "k", BaseURL: server.URL}, 5)
	_, err := provider.Create(context.Background(), &CreateRequest{
		Mode:   ModeTextTo3D,
		Prompt: "chair",
		Options: map[string]any{
			"art_style":       "sculpture",
			"negative_prompt": "   ",
			"should_remesh":   false,
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if gotBody["art_style"] != "sculpture" {
		t.Fatalf("art_style = %v", gotBody["art_style"])
	}
	if gotBody["should_remesh"] != false {
		t.Fatalf("explicit false must survive merge: %v", gotBody["should_remesh"])
	}
	if gotBody["symmetry_mode"] != "auto" {
		t.Fatalf("empty option must not clobber defaults: %v", gotBody["symmetry_mode"])
	}
}

func TestMeshyImageTo3DUsesV1Path(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"result":"img-task"}`))
	}))
	defer server.Close()
	provider := NewMeshyProvider(&MeshyConfig{APIKey: "k", BaseURL: server.URL + "/openapi"}, 5)
	if _, err := provider.Create(context.Background(), &CreateRequest{Mode: ModeImageTo3D, ImageURL: "https://example.com/a.png"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if gotPath != "/openapi/v1/image-to-3d" {
		t.Fatalf("path = %q", gotPath)
	}
}

func TestMeshyGetMapsStatusAssetsAndTimestamps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi/v2/text-to-3d/abc" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"id":"abc","type":"text-to-3d-preview","status":"SUCCEEDED","progress":100,
			"model_urls":{"obj":"https://cdn/obj.obj","glb":"https://cdn/model.glb","fbx":null},
			"thumbnail_url":"https://cdn/thumb.png","prompt":"chair",
			"created_at":1700000000000,"finished_at":1700000060000,"consumed_credits":20,
			"task_error":{"message":""}
		}`))
	}))
	defer server.Close()
	provider := NewMeshyProvider(&MeshyConfig{APIKey: "k", BaseURL: server.URL + "/openapi"}, 5)
	task, err := provider.GetWithMode(context.Background(), ModeTextTo3D, "abc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if task.Status != StatusSucceeded || task.ProviderStatus != "SUCCEEDED" {
		t.Fatalf("status = %q / %q", task.Status, task.ProviderStatus)
	}
	if task.CreatedAt != 1700000000 || task.FinishedAt != 1700000060 {
		t.Fatalf("timestamps = %d/%d", task.CreatedAt, task.FinishedAt)
	}
	if task.Credits != 20 {
		t.Fatalf("credits = %v", task.Credits)
	}
	if task.ThumbnailURL != "https://cdn/thumb.png" {
		t.Fatalf("thumbnail = %q", task.ThumbnailURL)
	}
	if len(task.Assets) != 2 || task.Assets[0].URL != "https://cdn/model.glb" || task.Assets[1].URL != "https://cdn/obj.obj" {
		t.Fatalf("assets = %+v", task.Assets)
	}
}

func TestMeshyGetImageTaskUsesV1PathAndFailureMessage(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"id":"x","status":"FAILED","progress":30,"task_error":{"message":"content flagged"}}`))
	}))
	defer server.Close()
	provider := NewMeshyProvider(&MeshyConfig{APIKey: "k", BaseURL: server.URL + "/openapi"}, 5)
	task, err := provider.GetWithMode(context.Background(), ModeImageTo3D, "x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if gotPath != "/openapi/v1/image-to-3d/x" {
		t.Fatalf("path = %q", gotPath)
	}
	if task.Status != StatusFailed || task.Error != "content flagged" {
		t.Fatalf("task = %+v", task)
	}
}

func TestMeshyCancelUsesDeleteOnResourcePath(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	provider := NewMeshyProvider(&MeshyConfig{APIKey: "k", BaseURL: server.URL + "/openapi"}, 5)
	if err := provider.CancelWithMode(context.Background(), ModeImageTo3D, "t1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/openapi/v1/image-to-3d/t1" {
		t.Fatalf("delete %s %s", gotMethod, gotPath)
	}
}

func TestMeshyBalanceAndErrorMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openapi/v1/balance" {
			_, _ = w.Write([]byte(`{"balance":42.5}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid api key"}`))
	}))
	defer server.Close()
	provider := NewMeshyProvider(&MeshyConfig{APIKey: "k", BaseURL: server.URL + "/openapi"}, 5)
	balance, err := provider.Balance(context.Background())
	if err != nil || balance != 42.5 {
		t.Fatalf("balance = %v err = %v", balance, err)
	}
	_, err = provider.Create(context.Background(), &CreateRequest{Mode: ModeTextTo3D, Prompt: "x"})
	upstream, ok := err.(*UpstreamError)
	if !ok {
		t.Fatalf("expected UpstreamError, got %T", err)
	}
	if upstream.StatusCode != http.StatusUnauthorized || upstream.Message != "invalid api key" {
		t.Fatalf("upstream = %+v", upstream)
	}
}

func TestMeshyRequiresAPIKey(t *testing.T) {
	provider := NewMeshyProvider(&MeshyConfig{}, 5)
	if _, err := provider.Create(context.Background(), &CreateRequest{Mode: ModeTextTo3D, Prompt: "x"}); err == nil {
		t.Fatal("expected unconfigured error")
	}
	if provider.Info().Configured {
		t.Fatal("provider must report unconfigured")
	}
}
