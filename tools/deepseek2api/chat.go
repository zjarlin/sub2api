package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tiktoken-go/tokenizer"
)

func newCompletionID() string {
	var random [12]byte
	_, _ = rand.Read(random[:])
	return "chatcmpl-" + hex.EncodeToString(random[:])
}

type chatResult struct {
	content     string
	reasoning   string
	totalTokens int
}

type chatOptions struct {
	thinking bool
	search   bool
	emit     func(content, reasoning string) error
}

func (a *adapter) completeChat(ctx context.Context, credential webCredential, prompt string, options chatOptions) (chatResult, error) {
	sessionID, err := a.upstream.createSession(ctx, credential)
	if err != nil {
		return chatResult{}, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		a.upstream.deleteSession(cleanupCtx, credential, sessionID)
	}()
	// 挑战只可使用一次；仅在上游明确拒绝 PoW 且尚未输出时换新挑战。
	for attempt := 0; attempt < 2; attempt++ {
		challenge, err := a.upstream.challenge(ctx, credential)
		if err != nil {
			return chatResult{}, err
		}
		proof, err := a.pow.solveChallenge(ctx, challenge)
		if err != nil {
			return chatResult{}, err
		}
		resp, err := a.upstream.completion(ctx, credential, sessionID, prompt, "default", options.thinking, options.search, proof)
		if err != nil {
			if errors.Is(err, errDeepSeekPowRejected) && attempt == 0 {
				continue
			}
			return chatResult{}, err
		}
		emitted := false
		emit := func(content, reasoning string) error {
			emitted = true
			if options.emit == nil {
				return nil
			}
			return options.emit(content, reasoning)
		}
		result, err := consumeDeepSeekStream(resp.Body, emit)
		resp.Body.Close()
		if !errors.Is(err, errDeepSeekPowRejected) || emitted || attempt == 1 {
			return result, err
		}
	}
	return chatResult{}, errDeepSeekPowRejected
}

func (a *adapter) chatWithRelogin(ctx context.Context, credential webCredential, prompt string, options chatOptions) (chatResult, error) {
	emitted := false
	if options.emit != nil {
		original := options.emit
		options.emit = func(content, reasoning string) error {
			emitted = true
			return original(content, reasoning)
		}
	}
	result, err := a.completeChat(ctx, credential, prompt, options)
	if !errors.Is(err, errDeepSeekUnauthorized) || emitted {
		return result, err
	}
	refreshed, err := a.renewCredential(ctx, credential)
	if err != nil {
		return chatResult{}, err
	}
	// 输出前只续登重试一次，避免重复正文或重复工具调用。
	result, err = a.completeChat(ctx, refreshed, prompt, options)
	if errors.Is(err, errDeepSeekUnauthorized) {
		return chatResult{}, a.rejectRefreshedCredential(refreshed)
	}
	return result, err
}

func (a *adapter) chat(w http.ResponseWriter, r *http.Request) {
	req, prompt, thinking, err := parseChat(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	if err := a.acquire(ctx); err != nil {
		writeError(w, apiError{503, "adapter_busy", "DeepSeek adapter is busy"})
		return
	}
	defer a.release()
	credential, err := a.account()
	if err != nil {
		writeError(w, err)
		return
	}
	codec, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		writeError(w, err)
		return
	}
	promptTokens, err := codec.Count(prompt)
	if err != nil {
		writeError(w, err)
		return
	}
	stream := &chatStream{w: w, id: newCompletionID(), created: time.Now().Unix(), model: req.Model, includeUsage: req.StreamOptions.IncludeUsage}
	options := chatOptions{thinking: thinking, search: req.search}
	var rawContent strings.Builder
	sentContent := 0
	if req.Stream {
		options.emit = func(content, reasoning string) error {
			if len(req.Tools) > 0 {
				rawContent.WriteString(content)
				end := safeToolPrefix(rawContent.String())
				content = rawContent.String()[sentContent:end]
				sentContent = end
			}
			return stream.text(content, reasoning)
		}
	}
	result, err := a.chatWithRelogin(ctx, credential, prompt, options)
	if err != nil {
		stream.fail(err)
		return
	}
	if result.totalTokens <= 0 {
		result.totalTokens, err = codec.Count(result.content + result.reasoning)
		if err != nil {
			stream.fail(err)
			return
		}
	}
	var calls []toolCall
	if len(req.Tools) > 0 {
		result.content, calls, err = req.policy.reply(result.content)
		if err != nil {
			stream.fail(err)
			return
		}
	}
	usage := map[string]int{"prompt_tokens": promptTokens, "completion_tokens": result.totalTokens, "total_tokens": promptTokens + result.totalTokens}
	finish := "stop"
	if len(calls) > 0 {
		finish = "tool_calls"
	}
	if !req.Stream {
		message := map[string]any{"role": "assistant", "content": result.content}
		if result.reasoning != "" {
			message["reasoning_content"] = result.reasoning
		}
		if len(calls) > 0 {
			message["tool_calls"] = calls
			if result.content == "" {
				message["content"] = nil
			}
		}
		writeJSON(w, 200, map[string]any{
			"id": stream.id, "object": "chat.completion", "created": stream.created, "model": req.Model,
			"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}, "usage": usage,
		})
		return
	}
	if len(req.Tools) > 0 {
		if err := stream.text(result.content[sentContent:], ""); err != nil {
			return
		}
	}
	for i, call := range calls {
		delta := map[string]any{"tool_calls": []any{map[string]any{"index": i, "id": call.ID, "type": call.Type, "function": call.Function}}}
		if err := stream.chunk(delta, nil, nil); err != nil {
			return
		}
	}
	if err := stream.chunk(map[string]any{}, finish, nil); err != nil {
		return
	}
	if req.StreamOptions.IncludeUsage {
		if err := stream.chunk(nil, nil, usage); err != nil {
			return
		}
	}
	_ = stream.done()
}
