package translate

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestTranslationFailureReason(t *testing.T) {
	err := &url.Error{Op: "Post", URL: "https://user:secret@example.test?q=private", Err: errors.New("timeout token=private-key input-text")}
	got := failureReason(err, []string{"input-text"})
	for _, secret := range []string{"private-key", "input-text", "example.test", "user:secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("leaked %s: %s", secret, got)
		}
	}
	if !strings.Contains(got, "timeout") {
		t.Fatal(got)
	}
	if failureReason(nil, nil) != "" {
		t.Fatal("success has failure reason")
	}
	if len([]rune(failureReason(errors.New(strings.Repeat("错", 500)), nil))) != 301 {
		t.Fatal("unbounded reason")
	}
}

func TestTranslationFailureReasonRedactsEmbeddedCredentials(t *testing.T) {
	reason := failureReason(errors.New("upstream timeout at https://user:password@example.test?q=secret-query Authorization: Bearer secret-token api_key=secret-key"), nil)
	for _, secret := range []string{"password", "example.test", "secret-query", "secret-token", "secret-key"} {
		if strings.Contains(reason, secret) {
			t.Fatalf("leaked %s: %s", secret, reason)
		}
	}
	if !strings.Contains(reason, "upstream timeout") {
		t.Fatal(reason)
	}
}

func TestTranslationChainPreservesEveryFailure(t *testing.T) {
	failing := func(name, reason string) Translator {
		return healthTestTranslator{name: name, run: func(context.Context, *TranslateRequest) (*TranslateResponse, error) {
			return nil, errors.New(reason)
		}}
	}
	a := &Aggregator{translators: []Translator{failing("hymt", "upstream timeout"), failing("baidu", "quota exceeded")}}
	for i := 0; i < 3; i++ {
		_, err := a.Translate(context.Background(), healthRequest())
		var chain *ChainError
		if !errors.As(err, &chain) || len(chain.Attempts) != 2 {
			t.Fatalf("missing attempts: %v", err)
		}
		if chain.Attempts[0].Provider != "hymt" || chain.Attempts[0].Reason != "upstream timeout" || chain.Attempts[1].Reason != "quota exceeded" {
			t.Fatal(chain.Attempts)
		}
	}
	_, err := a.Translate(context.Background(), healthRequest())
	var chain *ChainError
	if !errors.As(err, &chain) {
		t.Fatal(err)
	}
	for _, attempt := range chain.Attempts {
		if attempt.Status != "cooldown" || attempt.Reason == "" {
			t.Fatal(attempt)
		}
	}
	if (&ChainError{}).Error() != "translate: no providers configured" {
		t.Fatal("missing configuration reason")
	}
}
