package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

type chatStream struct {
	w            http.ResponseWriter
	id           string
	created      int64
	model        string
	started      bool
	includeUsage bool
}

func (s *chatStream) send(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", data); err != nil {
		return err
	}
	return http.NewResponseController(s.w).Flush()
}

func (s *chatStream) start() error {
	if s.started {
		return nil
	}
	s.started = true
	s.w.Header().Set("Content-Type", "text/event-stream")
	s.w.Header().Set("Cache-Control", "no-cache")
	s.w.Header().Set("X-Accel-Buffering", "no")
	return s.chunk(map[string]any{"role": "assistant"}, nil, nil)
}

func (s *chatStream) chunk(delta map[string]any, finish any, usage map[string]int) error {
	if err := s.start(); err != nil {
		return err
	}
	choices := []any{}
	if delta != nil {
		choices = append(choices, map[string]any{"index": 0, "delta": delta, "finish_reason": finish})
	}
	value := map[string]any{"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model, "choices": choices}
	if usage != nil {
		value["usage"] = usage
	} else if s.includeUsage {
		value["usage"] = nil
	}
	return s.send(value)
}

func (s *chatStream) text(content, reasoning string) error {
	if content == "" && reasoning == "" {
		return nil
	}
	delta := map[string]any{}
	if reasoning != "" {
		delta["reasoning_content"] = reasoning
	}
	if content != "" {
		delta["content"] = content
	}
	return s.chunk(delta, nil, nil)
}

func (s *chatStream) fail(err error) {
	if !s.started {
		writeError(s.w, err)
		return
	}
	public := apiError{502, "upstream_error", "DeepSeek web stream failed before completion"}
	var api apiError
	if errors.As(err, &api) {
		public = api
	}
	// 已开始的响应只能发流内错误，不能再写 JSON HTTP 响应或成功结束标记。
	_ = s.send(map[string]any{"error": map[string]string{"type": "upstream_error", "code": public.code, "message": public.message}})
}

func (s *chatStream) done() error {
	if _, err := io.WriteString(s.w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	return http.NewResponseController(s.w).Flush()
}
