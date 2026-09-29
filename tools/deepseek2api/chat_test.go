package main

import (
	"strings"
	"testing"
)

func TestReadDeepSeekStreamIgnoresMetadataAfterBatch(t *testing.T) {
	stream := `event: ready
data: {"model_type":"default","request_message_id":1,"response_message_id":2}

event: update_session
data: {"updated_at":1770000000}

data: {"v":{"response":{"fragments":[{"type":"RESPONSE","content":""}],"status":"WIP"}}}

data: {"p":"response/fragments/-1/content","o":"APPEND","v":"Hello"}

event: update_session
data: {"updated_at":1770000001}

data: {"v":"!"}

data: {"p":"response","o":"BATCH","v":[{"p":"status","o":"SET","v":"FINISHED"},{"p":"accumulated_token_usage","v":3}]}

event: auto_tts_capability
data: {"status":"available"}

data: {"p":"response/status","o":"SET","v":"FINISHED"}

event: update_session
data: {"updated_at":1770000002}

event: title
data: {"content":"Greeting"}

event: close
data: {"click_behavior":"none","auto_resume":false}

`
	content, reasoning, tokens, err := readDeepSeekStream(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if content != "Hello!" || reasoning != "" || tokens != 3 {
		t.Fatalf("unexpected completion: content=%q reasoning=%q tokens=%d", content, reasoning, tokens)
	}
}

func TestReadDeepSeekStreamRejectsMalformedDelta(t *testing.T) {
	for _, event := range []string{
		`{"p":"response"}`,
		`{"o":"BATCH"}`,
		`{"v":`,
	} {
		t.Run(event, func(t *testing.T) {
			_, _, _, err := readDeepSeekStream(strings.NewReader("data: " + event + "\n\n"))
			if err == nil || !strings.Contains(err.Error(), "decode DeepSeek event") {
				t.Fatalf("expected a delta decoding error, got %v", err)
			}
		})
	}
}

func TestReadDeepSeekStreamSeparatesReasoningAndResponse(t *testing.T) {
	for _, fragments := range []string{
		`[{"id":2,"type":"RESPONSE","content":""}]`,
		`{"id":2,"type":"RESPONSE","content":""}`,
	} {
		t.Run(fragments, func(t *testing.T) {
			stream := `data: {"v":{"response":{"fragments":[{"id":1,"type":"THINK","content":"Let me think."}],"status":"WIP"}}}

data: {"p":"response/fragments","o":"APPEND","v":` + fragments + `}

data: {"p":"response/fragments/-1/content","v":"Hello"}

data: {"v":"!"}

data: {"p":"response","o":"BATCH","v":[{"p":"status","o":"SET","v":"FINISHED"},{"p":"accumulated_token_usage","v":10}]}

event: close
data: {"click_behavior":"none","auto_resume":false}

`
			content, reasoning, tokens, err := readDeepSeekStream(strings.NewReader(stream))
			if err != nil {
				t.Fatal(err)
			}
			if content != "Hello!" || reasoning != "Let me think." || tokens != 10 {
				t.Fatalf("unexpected completion: content=%q reasoning=%q tokens=%d", content, reasoning, tokens)
			}
		})
	}
}
