package model3d

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTripoTextToModelCreate(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"tripo-1"}}`))
	}))
	defer server.Close()

	provider := NewTripoProvider(&TripoConfig{APIKey: "tsk_test", BaseURL: server.URL + "/v2/openapi"}, 5)
	taskID, err := provider.Create(context.Background(), &CreateRequest{
		Mode:           ModeTextTo3D,
		Prompt:         "small robot",
		Model:          "v3.0-20250812",
		FaceLimit:      8000,
		NegativePrompt: "blurry",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if taskID != "tripo-1" {
		t.Fatalf("task id = %q", taskID)
	}
	if gotPath != "/v2/openapi/task" || gotAuth != "Bearer tsk_test" {
		t.Fatalf("path/auth = %q/%q", gotPath, gotAuth)
	}
	if gotBody["type"] != "text_to_model" || gotBody["prompt"] != "small robot" {
		t.Fatalf("body = %+v", gotBody)
	}
	if gotBody["model_version"] != "v3.0-20250812" {
		t.Fatalf("model_version = %v", gotBody["model_version"])
	}
	if gotBody["face_limit"] != float64(8000) {
		t.Fatalf("face_limit = %v", gotBody["face_limit"])
	}
}

func TestTripoImageToModelUsesURLWithoutUpload(t *testing.T) {
	var paths []string
	var uploadCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/v2/openapi/upload" {
			uploadCalled = true
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if file, ok := body["file"].(map[string]any); ok {
			if file["url"] != "https://example.com/input.png" {
				t.Errorf("file = %+v", file)
			}
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"tripo-img"}}`))
	}))
	defer server.Close()

	provider := NewTripoProvider(&TripoConfig{APIKey: "k", BaseURL: server.URL + "/v2/openapi"}, 5)
	if _, err := provider.Create(context.Background(), &CreateRequest{Mode: ModeImageTo3D, ImageURL: "https://example.com/input.png"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if uploadCalled {
		t.Fatal("URL input must not trigger an upload")
	}
	if len(paths) != 1 || paths[0] != "/v2/openapi/task" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestTripoImageToModelUploadsInlineBase64(t *testing.T) {
	var (
		gotTokenField string
		gotFilename   string
		gotFileBytes  []byte
		gotTaskFile   map[string]any
	)
	pngBytes := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x01, 0x02}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/openapi/upload":
			mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mediaType != "multipart/form-data" {
				t.Fatalf("content type = %q", r.Header.Get("Content-Type"))
			}
			reader := multipart.NewReader(r.Body, params["boundary"])
			form, err := reader.ReadForm(1 << 20)
			if err != nil {
				t.Fatalf("read form: %v", err)
			}
			for field, files := range form.File {
				gotTokenField = field
				gotFilename = files[0].Filename
				file, _ := files[0].Open()
				gotFileBytes, _ = readAllLimited(file, 1<<20)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"image_token":"tok-123"}}`))
		case "/v2/openapi/task":
			_ = json.NewDecoder(r.Body).Decode(&gotTaskFile)
			_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"tripo-inline"}}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := NewTripoProvider(&TripoConfig{APIKey: "k", BaseURL: server.URL + "/v2/openapi"}, 5)
	inline := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	taskID, err := provider.Create(context.Background(), &CreateRequest{Mode: ModeImageTo3D, ImageBase64: inline})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if taskID != "tripo-inline" {
		t.Fatalf("task id = %q", taskID)
	}
	if gotTokenField != "file" || gotFilename != "image.png" {
		t.Fatalf("upload field/filename = %q/%q", gotTokenField, gotFilename)
	}
	if string(gotFileBytes) != string(pngBytes) {
		t.Fatalf("uploaded bytes = %v", gotFileBytes)
	}
	file, _ := gotTaskFile["file"].(map[string]any)
	if file == nil || file["file_token"] != "tok-123" {
		t.Fatalf("task file = %+v", gotTaskFile["file"])
	}
}

func TestTripoGetMapsOutputAndStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/openapi/task/t1" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{
			"task_id":"t1","type":"image_to_model","status":"success","progress":100,
			"create_time":1700000000,
			"output":{"model":"https://cdn/model.glb","pbr_model":"https://cdn/pbr.glb","rendered_image":"https://cdn/render.png"}
		}}`))
	}))
	defer server.Close()
	provider := NewTripoProvider(&TripoConfig{APIKey: "k", BaseURL: server.URL + "/v2/openapi"}, 5)
	task, err := provider.Get(context.Background(), "t1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if task.Status != StatusSucceeded || task.Mode != ModeImageTo3D {
		t.Fatalf("task = %+v", task)
	}
	if task.ThumbnailURL != "https://cdn/render.png" {
		t.Fatalf("thumbnail = %q", task.ThumbnailURL)
	}
	if len(task.Assets) != 2 || task.Assets[0].URL != "https://cdn/model.glb" {
		t.Fatalf("assets = %+v", task.Assets)
	}
}

func TestTripoEnvelopeErrorSurfacesCodeAndMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":2010,"message":"Insufficient balance"}`))
	}))
	defer server.Close()
	provider := NewTripoProvider(&TripoConfig{APIKey: "k", BaseURL: server.URL}, 5)
	_, err := provider.Create(context.Background(), &CreateRequest{Mode: ModeTextTo3D, Prompt: "x"})
	upstream, ok := err.(*UpstreamError)
	if !ok {
		t.Fatalf("expected UpstreamError, got %T (%v)", err, err)
	}
	if upstream.Code != "2010" || upstream.Message != "Insufficient balance" {
		t.Fatalf("upstream = %+v", upstream)
	}
}

func TestTripoCancelAndBalance(t *testing.T) {
	var cancelPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/openapi/user/balance" {
			_, _ = w.Write([]byte(`{"code":0,"data":{"balance":12.5,"frozen":1}}`))
			return
		}
		cancelPath = r.URL.Path
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer server.Close()
	provider := NewTripoProvider(&TripoConfig{APIKey: "k", BaseURL: server.URL + "/v2/openapi"}, 5)
	if err := provider.Cancel(context.Background(), "t9"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelPath != "/v2/openapi/task/t9/cancel" {
		t.Fatalf("cancel path = %q", cancelPath)
	}
	balance, err := provider.Balance(context.Background())
	if err != nil || balance != 12.5 {
		t.Fatalf("balance = %v err = %v", balance, err)
	}
}
