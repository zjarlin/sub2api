package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// debugEnabled 由 CGW_DEBUG=1 打开，仅用于排障日志，不记录提示词正文。
var debugEnabled = os.Getenv("CGW_DEBUG") == "1"

func debugf(format string, args ...any) {
	if debugEnabled {
		log.Printf("chatgpt-web: "+format, args...)
	}
}

const maxRequestBody = 2 << 20

// promptGenerator 抽象浏览器侧，便于测试注入假实现。
type promptGenerator interface {
	ask(ctx context.Context, prompt string) (string, error)
}

type apiError struct {
	status  int
	code    string
	message string
}

func (e apiError) Error() string { return e.message }

type adapter struct {
	key        string
	models     []string
	generator  promptGenerator
	answerWait time.Duration
	slots      chan struct{}
}

func newAdapter(key string, models []string, generator promptGenerator, answerWait time.Duration) (*adapter, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("ChatGPT web adapter key is required")
	}
	if len(models) == 0 {
		return nil, errors.New("at least one ChatGPT web model ID is required")
	}
	if generator == nil {
		return nil, errors.New("ChatGPT web browser is required")
	}
	return &adapter{key: key, models: models, generator: generator, answerWait: answerWait, slots: make(chan struct{}, 4)}, nil
}

func randomID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(buffer)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	var public apiError
	if !errors.As(err, &public) {
		public = apiError{status: 502, code: "upstream_error", message: "ChatGPT web request failed"}
	}
	writeJSON(w, public.status, map[string]any{"error": map[string]string{
		"type": "upstream_error", "code": public.code, "message": public.message,
	}})
}

func (a *adapter) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(a.key)) != 1 {
			writeError(w, apiError{status: 401, code: "invalid_api_key", message: "Invalid adapter API key"})
			return
		}
		next(w, r)
	}
}

func (a *adapter) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /v1/models", a.auth(a.modelsHandler))
	mux.HandleFunc("POST /v1/chat/completions", a.auth(a.chatCompletions))
	mux.HandleFunc("POST /v1/responses", a.auth(a.responses))
	return mux
}

func (a *adapter) modelsHandler(w http.ResponseWriter, r *http.Request) {
	items := make([]map[string]any, 0, len(a.models))
	for _, model := range a.models {
		items = append(items, map[string]any{"id": model, "object": "model", "created": 0, "owned_by": "openai-web"})
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": items})
}

func (a *adapter) acquire(ctx context.Context) error {
	select {
	case a.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *adapter) release() { <-a.slots }

func decodeOne(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected extra JSON")
	}
	return nil
}

// answer 把统一提示交给浏览器并返回助手文本。
func (a *adapter) answer(ctx context.Context, prompt string) (string, error) {
	if err := a.acquire(ctx); err != nil {
		return "", apiError{status: 499, code: "client_closed_request", message: "Request was cancelled"}
	}
	defer a.release()
	callContext, cancel := context.WithTimeout(ctx, a.answerWait+45*time.Second)
	defer cancel()
	text, err := a.generator.ask(callContext, prompt)
	if err != nil {
		return "", apiError{status: 502, code: "upstream_unavailable", message: "ChatGPT web upstream failed: " + err.Error()}
	}
	if strings.TrimSpace(text) == "" {
		return "", apiError{status: 502, code: "empty_answer", message: "ChatGPT web upstream returned an empty answer"}
	}
	return text, nil
}

func (a *adapter) chatCompletions(w http.ResponseWriter, r *http.Request) {
	var body chatRequest
	if err := decodeOne(w, r, &body); err != nil {
		writeError(w, apiError{status: 400, code: "invalid_request", message: "Malformed JSON request body"})
		return
	}
	request, err := toChat(body, false, a.models)
	if err != nil {
		debugf("chat toChat error: %v", err)
		writeError(w, err)
		return
	}
	prompt, err := buildPrompt(request.messages)
	if err != nil {
		debugf("chat buildPrompt error: %v", err)
		writeError(w, err)
		return
	}
	text, err := a.answer(r.Context(), prompt)
	if err != nil {
		debugf("chat answer error: %v", err)
		writeError(w, err)
		return
	}
	debugf("chat ok len=%d stream=%v", len(text), request.stream)
	id := "chatcmpl-" + randomID()
	created := time.Now().Unix()
	if !request.stream {
		writeJSON(w, 200, map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": request.model,
			"choices": []map[string]any{{
				"index": 0, "message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop",
			}},
			"usage": estimatedUsage(prompt, text),
		})
		return
	}
	stream := newSSE(w)
	defer stream.done()
	base := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": request.model}
	if !stream.send("", mergeMaps(base, map[string]any{
		"choices": []map[string]any{{"index": 0, "delta": map[string]string{"role": "assistant", "content": text}, "finish_reason": nil}},
	})) {
		return
	}
	if !stream.send("", mergeMaps(base, map[string]any{
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	})) {
		return
	}
	if body.StreamOptions.IncludeUsage {
		stream.send("", mergeMaps(base, map[string]any{
			"choices": []any{}, "usage": estimatedUsage(prompt, text),
		}))
	}
}

func (a *adapter) responses(w http.ResponseWriter, r *http.Request) {
	var body chatRequest
	if err := decodeOne(w, r, &body); err != nil {
		writeError(w, apiError{status: 400, code: "invalid_request", message: "Malformed JSON request body"})
		return
	}
	request, err := toChat(body, true, a.models)
	if err != nil {
		debugf("responses toChat error: %v", err)
		writeError(w, err)
		return
	}
	prompt, err := buildPrompt(request.messages)
	if err != nil {
		debugf("responses buildPrompt error: %v", err)
		writeError(w, err)
		return
	}
	text, err := a.answer(r.Context(), prompt)
	if err != nil {
		debugf("responses answer error: %v", err)
		writeError(w, err)
		return
	}
	debugf("responses ok len=%d stream=%v", len(text), request.stream)
	response := buildResponse(text, request.model, prompt)
	if !request.stream {
		writeJSON(w, 200, response)
		return
	}
	stream := newSSE(w)
	defer stream.doneForResponses()
	sequence := 0
	for _, event := range responseEvents(response) {
		payload := map[string]any{"type": event.Name, "sequence_number": sequence}
		for key, value := range event.Payload {
			payload[key] = value
		}
		sequence++
		if !stream.send(event.Name, payload) {
			return
		}
	}
}

func mergeMaps(base, extra map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

type sseStream struct {
	writer  http.ResponseWriter
	flusher http.Flusher
}

func newSSE(w http.ResponseWriter) *sseStream {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	return &sseStream{writer: w, flusher: flusher}
}

func (s *sseStream) send(event string, payload any) bool {
	data, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	var frame strings.Builder
	if event != "" {
		frame.WriteString("event: ")
		frame.WriteString(event)
		frame.WriteByte('\n')
	}
	frame.WriteString("data: ")
	frame.Write(data)
	frame.WriteString("\n\n")
	if _, err := io.WriteString(s.writer, frame.String()); err != nil {
		return false
	}
	if s.flusher != nil {
		s.flusher.Flush()
	}
	return true
}

// done 结束 Chat Completions 流（[DONE] 终止符）。
func (s *sseStream) done() {
	if _, err := io.WriteString(s.writer, "data: [DONE]\n\n"); err == nil && s.flusher != nil {
		s.flusher.Flush()
	}
}

// doneForResponses 结束 Responses 流（以 response.completed 收尾，无需 [DONE]）。
func (s *sseStream) doneForResponses() {
	if s.flusher != nil {
		s.flusher.Flush()
	}
}
