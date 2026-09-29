package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type chatRequest struct {
	Model             string          `json:"model"`
	Messages          []chatMessage   `json:"messages"`
	Tools             []functionTool  `json:"tools"`
	ToolChoice        json.RawMessage `json:"tool_choice"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls"`
	ResponseFormat    json.RawMessage `json:"response_format"`
	policy            toolPolicy
	format            responseFormat
	images            []chatImage
	Stream            bool `json:"stream"`
	StreamOptions     struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

func (a *adapter) ensureProject(ctx context.Context, c credential, model string) (credential, error) {
	created := c.AppID == ""
	if c.AppID == "" {
		var app struct {
			AppID string `json:"app_id"`
			App   *struct {
				AppID string `json:"app_id"`
			} `json:"app"`
		}
		body := map[string]any{"name": "Sub2API VibeX", "llm_provider_id": model, "app_type": "web"}
		if err := a.call(ctx, c, "POST", "/vc/api/apps", body, &app); err != nil {
			return c, err
		}
		c.AppID = app.AppID
		if c.AppID == "" && app.App != nil {
			c.AppID = app.App.AppID
		}
		if c.AppID == "" {
			return c, problem(502, "invalid_upstream_response", "VibeX did not return a project ID")
		}
		a.mu.Lock()
		if a.credential.UID != c.UID {
			a.mu.Unlock()
			return c, problem(409, "account_changed", "VibeX account changed; retry the request")
		}
		updated := a.credential
		updated.AppID = c.AppID
		if err := a.save(updated); err != nil {
			a.mu.Unlock()
			return c, err
		}
		a.credential = updated
		a.mu.Unlock()
	}
	path := "/vc/api/apps/" + url.PathEscape(c.AppID)
	startCtx, stopStart := context.WithTimeout(ctx, 90*time.Second)
	defer stopStart()
	startRequested := false
	currentProvider := ""
	for {
		var state struct {
			LLMProviderID string `json:"llm_provider_id"`
			Live          *struct {
				Status string `json:"status"`
			} `json:"live"`
			Cached string `json:"status_cached"`
		}
		if err := a.call(startCtx, c, "GET", path, nil, &state); err != nil {
			return c, err
		}
		status := state.Cached
		if state.Live != nil {
			status = state.Live.Status
		}
		if status == "running" {
			currentProvider = state.LLMProviderID
			break
		}
		if !startRequested {
			if err := a.call(startCtx, c, "POST", path+"/start", nil, nil); err != nil {
				return c, err
			}
			startRequested = true
		} else if status == "" || status == "exited" || status == "failed" {
			return c, problem(502, "project_not_running", "VibeX project failed to start")
		}
		select {
		case <-startCtx.Done():
			return c, problem(504, "project_start_timeout", "VibeX project did not become ready")
		case <-time.After(time.Second):
		}
	}
	if created || currentProvider != model {
		if err := a.call(ctx, c, "PATCH", path+"/llm-provider", map[string]string{"provider_id": model}, nil); err != nil {
			return c, err
		}
		if err := a.call(ctx, c, "POST", path+"/ensure-llm-settings", nil, nil); err != nil {
			return c, err
		}
	}
	return c, nil
}

func sendWS(ctx context.Context, conn *websocket.Conn, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

func readWS(ctx context.Context, conn *websocket.Conn) (map[string]json.RawMessage, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var event map[string]json.RawMessage
	if json.Unmarshal(data, &event) != nil {
		return nil, problem(502, "invalid_upstream_event", "Invalid VibeX event")
	}
	return event, nil
}

func eventType(e map[string]json.RawMessage) string {
	var t string
	_ = json.Unmarshal(e["type"], &t)
	return t
}

func eventError(e map[string]json.RawMessage) error {
	var code, message string
	_ = json.Unmarshal(e["code"], &code)
	_ = json.Unmarshal(e["message"], &message)
	switch code {
	case "FREE_LLM_QUOTA_EXCEEDED", "FREE_LLM_GLOBAL_BUDGET_EXCEEDED":
		return problem(429, "quota_exceeded", "VibeX quota is exhausted; retry after reset")
	case "BALANCE_INSUFFICIENT", "INSUFFICIENT_BALANCE", "PAID_BALANCE_INSUFFICIENT":
		return problem(402, "insufficient_balance", "VibeX wallet balance is insufficient")
	case "TOKEN_INVALID", "TOKEN_MISSION":
		return problem(401, "vibex_login_required", "RunningHub login expired; sign in again")
	case "SOURCE_SESSION_ACTIVE", "SOURCE_TURN_RUNNING", "INIT_ROOT_BUSY":
		return problem(409, "project_busy", "VibeX project has an active turn; retry after it finishes")
	default:
		if strings.Contains(message, "工作区已切换") {
			return problem(409, "workspace_changed", "VibeX workspace changed; reconnect and retry")
		}
		return problem(502, "generation_failed", "VibeX generation failed; check login, quota and balance")
	}
}

func (a *adapter) connect(ctx context.Context, c credential) (*websocket.Conn, string, error) {
	var app map[string]json.RawMessage
	if err := a.call(ctx, c, "GET", "/vc/api/apps/"+url.PathEscape(c.AppID), nil, &app); err != nil {
		return nil, "", err
	}
	meta := map[string]json.RawMessage{}
	for _, key := range []string{"app_id", "name", "app_type", "image", "host_ip", "host_ports", "created_at", "pocketbase_url", "flow", "enabled_capabilities"} {
		if value, ok := app[key]; ok {
			meta[key] = value
		}
	}
	u, err := url.Parse(a.origin)
	if err != nil {
		return nil, "", err
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/app-ws/" + url.PathEscape(c.AppID) + "/ws"
	wsHeaders := http.Header{}
	wsHeaders.Set("Cookie", "Rh-Accesstoken="+c.Token)
	wsHeaders.Set("Origin", a.origin)
	conn, err := a.dialProject(ctx, u.String(), wsHeaders)
	if err != nil {
		return nil, "", err
	}
	conn.SetReadLimit(4 << 20)
	// 首次连接必须初始化项目根目录；仅 init 会收到 ready，但无法接受提示词。
	if err := sendWS(ctx, conn, map[string]any{"type": "init_root", "cwd": "/workspace/app", "meta": meta, "replay": false}); err != nil {
		conn.CloseNow()
		return nil, "", err
	}
	var sessionID string
	ready, idle, sessionSeen := false, false, false
	for {
		e, err := readWS(ctx, conn)
		if err != nil {
			conn.CloseNow()
			return nil, "", err
		}
		switch eventType(e) {
		case "error":
			conn.CloseNow()
			return nil, "", eventError(e)
		case "run_status":
			if string(e["active"]) == "true" {
				conn.CloseNow()
				return nil, "", problem(409, "project_busy", "VibeX project has an active turn")
			}
			idle = string(e["active"]) == "false"
		case "session_info":
			_ = json.Unmarshal(e["session_id"], &sessionID)
			sessionSeen = true
		case "ready":
			ready = true
		}
		if ready && idle && sessionSeen {
			return conn, sessionID, nil
		}
	}
}

// 仅重试提示词发送前的握手，不重试初始化、会话创建或生成。
func (a *adapter) dialProject(ctx context.Context, address string, headers http.Header) (*websocket.Conn, error) {
	for attempt := 0; attempt < 3; attempt++ {
		conn, response, err := websocket.Dial(ctx, address, &websocket.DialOptions{HTTPClient: a.client, HTTPHeader: headers})
		if err == nil {
			return conn, nil
		}
		status := 0
		if response != nil {
			status = response.StatusCode
			if response.Body != nil {
				response.Body.Close()
			}
		}
		switch status {
		case 401, 403:
			return nil, problem(401, "vibex_login_required", "RunningHub login expired; sign in again")
		case 402:
			return nil, problem(402, "insufficient_balance", "VibeX wallet balance is insufficient")
		case 429:
			return nil, problem(429, "quota_exceeded", "VibeX quota is exhausted; retry after reset")
		}
		if attempt == 2 || status != 0 && status < 500 {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 300 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, problem(502, "websocket_unavailable", "Unable to connect to the VibeX project")
}

type tokenUsage struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
	Total      int `json:"total_tokens"`
}

func generate(ctx context.Context, conn *websocket.Conn, previousSession, prompt string, buffered bool, emit func(string) error) (tokenUsage, error) {
	var usage tokenUsage
	// 读取取消会关闭 WebSocket；先由请求监听器发送上游取消，再关闭连接。
	readCtx, stopRead := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer stopRead()
	if err := sendWS(ctx, conn, map[string]string{"type": "new_session"}); err != nil {
		return usage, err
	}
	// 新会话确认可以是 null，实际 ID 在提示词启动后才分配。
	for {
		e, err := readWS(readCtx, conn)
		if err != nil {
			return usage, err
		}
		if eventType(e) == "error" {
			return usage, eventError(e)
		}
		if eventType(e) == "session_info" {
			var id string
			_ = json.Unmarshal(e["session_id"], &id)
			if id != previousSession || previousSession == "" {
				break
			}
		}
	}
	if err := sendWS(ctx, conn, map[string]string{"type": "prompt", "text": prompt, "permission_mode": "dontAsk"}); err != nil {
		return usage, err
	}
	pingCtx, stopPing := context.WithCancel(readCtx)
	defer stopPing()
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-ticker.C:
				if sendWS(pingCtx, conn, map[string]string{"type": "ping"}) != nil {
					return
				}
			}
		}
	}()
	var streamed string
	var finalText string
	var textSeen bool
	for {
		e, err := readWS(readCtx, conn)
		if err != nil {
			return usage, err
		}
		switch eventType(e) {
		case "error", "source_turn_waiting":
			return usage, eventError(e)
		case "done":
			if !textSeen {
				return usage, problem(502, "empty_response", "VibeX returned no text")
			}
			if buffered {
				if finalText == "" {
					finalText = streamed
				}
				return usage, emit(finalText)
			}
			return usage, nil
		case "claude_event":
			var event struct {
				Type                string `json:"type"`
				IsError             bool   `json:"is_error"`
				IsAPIError          bool   `json:"isApiErrorMessage"`
				BalanceInsufficient bool   `json:"balance_insufficient"`
				Usage               struct {
					Input      int `json:"input_tokens"`
					Output     int `json:"output_tokens"`
					CacheRead  int `json:"cache_read_input_tokens"`
					CacheWrite int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
				Event struct {
					Type  string `json:"type"`
					Delta struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"delta"`
				} `json:"event"`
				Message struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(e["event"], &event) != nil {
				return usage, problem(502, "invalid_upstream_event", "Invalid VibeX generation event")
			}
			if event.BalanceInsufficient {
				return usage, problem(402, "insufficient_balance", "VibeX wallet balance is insufficient")
			}
			if event.IsError || event.IsAPIError {
				return usage, problem(502, "generation_failed", "VibeX generation failed")
			}
			if event.Type == "result" {
				usage.Prompt = event.Usage.Input + event.Usage.CacheRead + event.Usage.CacheWrite
				usage.Completion = event.Usage.Output
				usage.Total = usage.Prompt + usage.Completion
			}
			if event.Type == "stream_event" && event.Event.Type == "content_block_delta" && event.Event.Delta.Type == "text_delta" {
				text := event.Event.Delta.Text
				streamed += text
				if len(streamed) > 4<<20 {
					return usage, problem(502, "response_too_large", "VibeX response exceeded the size limit")
				}
				textSeen = textSeen || text != ""
				if !buffered {
					if err := emit(text); err != nil {
						return usage, err
					}
				}
			}
			if event.Type == "assistant" {
				var final strings.Builder
				for _, part := range event.Message.Content {
					if part.Type == "text" {
						final.WriteString(part.Text)
					}
				}
				text := final.String()
				if len(text) > 4<<20 {
					return usage, problem(502, "response_too_large", "VibeX response exceeded the size limit")
				}
				if text == "" {
					continue
				}
				if !strings.HasPrefix(text, streamed) {
					return usage, problem(502, "stream_mismatch", "VibeX revised its streamed response")
				}
				finalText = text
				if !buffered {
					if err := emit(text[len(streamed):]); err != nil {
						return usage, err
					}
				}
				textSeen = true
				streamed = ""
			}
		}
	}
}

func (a *adapter) chat(w http.ResponseWriter, r *http.Request) {
	request, prompt, err := decodeChat(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	if err := a.acquire(ctx); err != nil {
		writeError(w, err)
		return
	}
	defer func() { <-a.slot }()
	c, err := a.snapshot()
	if err != nil {
		writeError(w, err)
		return
	}
	providers, err := a.providers(ctx, c)
	if err != nil {
		writeError(w, err)
		return
	}
	found := false
	for _, p := range providers {
		if p.ID == request.Model {
			found = true
			break
		}
	}
	if !found {
		writeError(w, problem(400, "model_not_found", "Select a model from the VibeX account model list"))
		return
	}
	c, err = a.ensureProject(ctx, c, request.Model)
	if err != nil {
		writeError(w, err)
		return
	}
	prompt, err = a.uploadImages(ctx, c, request.images, prompt)
	if err != nil {
		writeError(w, err)
		return
	}
	initCtx, initCancel := context.WithTimeout(ctx, 90*time.Second)
	conn, previousSession, err := a.connect(initCtx, c)
	initCancel()
	if err != nil {
		writeError(w, err)
		return
	}
	defer conn.CloseNow()
	watchCtx, stopWatching := context.WithCancel(context.Background())
	defer stopWatching()
	go func() {
		select {
		case <-watchCtx.Done():
			return
		case <-ctx.Done():
			cancelCtx, cancelTurn := context.WithTimeout(context.Background(), 3*time.Second)
			_ = sendWS(cancelCtx, conn, map[string]string{"type": "cancel"})
			cancelTurn()
			_ = conn.CloseNow()
		}
	}()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		writeError(w, err)
		return
	}
	id, created := "chatcmpl-"+hex.EncodeToString(random[:]), time.Now().Unix()
	var text strings.Builder
	started := false
	sse := func(value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return err
		}
		return http.NewResponseController(w).Flush()
	}
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": request.Model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	}
	emit := func(value string) error {
		if text.Len()+len(value) > 4<<20 {
			return problem(502, "response_too_large", "VibeX response exceeded the size limit")
		}
		text.WriteString(value)
		if !request.Stream || request.policy.enabled() || request.format.schema != nil || value == "" {
			return nil
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Accel-Buffering", "no")
			started = true
			if err := sse(chunk(map[string]any{"role": "assistant"}, nil)); err != nil {
				return err
			}
		}
		return sse(chunk(map[string]any{"content": value}, nil))
	}
	usage, err := generate(ctx, conn, previousSession, prompt, request.policy.enabled() || request.format.schema != nil || len(request.images) > 0, emit)
	message, finish := map[string]any{"role": "assistant", "content": text.String()}, "stop"
	if err == nil && request.policy.enabled() {
		message, finish, err = request.policy.reply(text.String())
	}
	if err == nil {
		err = request.format.validate(message)
	}
	if err != nil {
		// 断开前取消上游，后续连接还会检查项目是否仍有运行中的任务。
		cancelCtx, cancelTurn := context.WithTimeout(context.Background(), 3*time.Second)
		_ = sendWS(cancelCtx, conn, map[string]string{"type": "cancel"})
		cancelTurn()
		if started {
			_ = sse(errorValue(err))
			return
		}
		if !errors.Is(err, context.Canceled) {
			writeError(w, err)
		}
		return
	}
	if request.Stream {
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Accel-Buffering", "no")
			if sse(chunk(map[string]any{"role": "assistant"}, nil)) != nil {
				return
			}
		}
		if request.policy.enabled() || request.format.schema != nil {
			if content, ok := message["content"].(string); ok && content != "" {
				if sse(chunk(map[string]any{"content": content}, nil)) != nil {
					return
				}
			}
			if content, ok := message["content"].(*string); ok && content != nil && *content != "" {
				if sse(chunk(map[string]any{"content": *content}, nil)) != nil {
					return
				}
			}
			if calls, ok := message["tool_calls"].([]toolCall); ok {
				for i, call := range calls {
					delta := map[string]any{"tool_calls": []any{map[string]any{"index": i, "id": call.ID, "type": call.Type, "function": call.Function}}}
					if sse(chunk(delta, nil)) != nil {
						return
					}
				}
			}
		}
		_ = sse(chunk(map[string]any{}, finish))
		if request.StreamOptions.IncludeUsage {
			_ = sse(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": request.Model, "choices": []any{}, "usage": usage})
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		_ = http.NewResponseController(w).Flush()
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "object": "chat.completion", "created": created, "model": request.Model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}, "usage": usage})
}
