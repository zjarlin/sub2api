package main

import (
	"errors"
	"strings"
	"testing"
)

func TestStreamLegacyPathsAndMultilineData(t *testing.T) {
	input := "data: {\n" + `data: "v":{"response":{"content":"","thinking_content":"","status":"WIP"}}}` + "\n\n" +
		`data: {"p":"response/thinking_content","o":"APPEND","v":"think"}` + "\n\n" +
		`data: {"p":"response/content","o":"APPEND","v":"hello"}` + "\n\n" +
		`data: {"v":" world"}` + "\n\n" +
		`data: {"p":"response/status","o":"SET","v":"FINISHED"}` + "\n\n" +
		`data: {"p":"response/accumulated_token_usage","v":8}` + "\n\n" + "data: [DONE]\n\n"
	var content, reasoning string
	result, err := consumeDeepSeekStream(strings.NewReader(input), func(c, r string) error { content += c; reasoning += r; return nil })
	if err != nil || content != "hello world" || reasoning != "think" || result.totalTokens != 8 {
		t.Fatalf("content=%q reasoning=%q result=%+v err=%v", content, reasoning, result, err)
	}
}

func TestStreamIndexedFragmentAndSet(t *testing.T) {
	input := `data: {"v":{"response":{"fragments":[{"type":"THINK","content":"think"},{"type":"RESPONSE","content":"hi"}],"status":"WIP"}}}` + "\n\n" +
		`data: {"p":"response/fragments/1/content","o":"SET","v":"hi there"}` + "\n\n" +
		`data: {"p":"response/status","o":"SET","v":"FINISHED"}` + "\n\n"
	var chunks []string
	result, err := consumeDeepSeekStream(strings.NewReader(input), func(c, r string) error { chunks = append(chunks, c); return nil })
	if err != nil || result.content != "hi there" || strings.Join(chunks, "|") != "hi| there" {
		t.Fatalf("result=%+v chunks=%q err=%v", result, chunks, err)
	}
}

func TestStreamRejectsFalseCompletionAndErrors(t *testing.T) {
	initial := `data: {"v":{"response":{"fragments":[{"type":"RESPONSE","content":"partial"}],"status":"WIP"}}}` + "\n\n"
	cases := []string{
		initial,
		initial + "data: [DONE]\n\n",
		initial + `data: {"p":"response/quasi_status","o":"SET","v":"FINISHED"}` + "\n\n",
		initial + "event: close\ndata: {}\n\n",
		initial + `data: {"p":"response/status","o":"SET","v":"FINISHED"}` + "\n",
		initial + `data: {"p":"response/fragments/9/content","o":"APPEND","v":"bad"}` + "\n\n",
		initial + "event: toast\ndata: {\"type\":\"error\",\"content\":\"private upstream diagnostic\"}\n\n",
		initial + "event: error\ndata: {}\n\n",
	}
	for i, input := range cases {
		if _, err := consumeDeepSeekStream(strings.NewReader(input), nil); err == nil {
			t.Fatalf("accepted invalid stream %d", i)
		}
	}
}

func TestStreamRejectsRewritesAndHonorsSinkFailure(t *testing.T) {
	input := `data: {"v":{"response":{"content":"original","status":"WIP"}}}` + "\n\n" +
		`data: {"p":"response/content","o":"SET","v":"rewritten"}` + "\n\n" +
		`data: {"p":"response/status","o":"SET","v":"FINISHED"}` + "\n\n"
	if _, err := consumeDeepSeekStream(strings.NewReader(input), func(string, string) error { return nil }); err == nil || !strings.Contains(err.Error(), "rewrote") {
		t.Fatalf("rewrite not rejected: %v", err)
	}
	closed := errors.New("downstream closed")
	if _, err := consumeDeepSeekStream(strings.NewReader(input), func(string, string) error { return closed }); !errors.Is(err, closed) {
		t.Fatalf("sink failure lost: %v", err)
	}
}
