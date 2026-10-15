package handler

import (
	"strings"
	"testing"
	"time"
)

func TestV0HandleIntegrity(t *testing.T) {
	original := v0ChatHandle{Chat: "chat_example", Account: 12, Key: 34, Group: 56, Expires: time.Now().Add(time.Hour).Unix(), Credential: "fingerprint"}
	token := signV0Handle(original, "test-secret")
	restored, ok := parseV0Handle(token, "test-secret")
	if !ok || restored != original {
		t.Fatal("valid handle did not round trip")
	}
	for _, invalid := range []string{"chat_example", token + "x", "x" + token, strings.Replace(token, ".", "..", 1)} {
		if _, ok := parseV0Handle(invalid, "test-secret"); ok {
			t.Fatal("accepted tampered handle")
		}
	}
	if _, ok := parseV0Handle(token, "other-secret"); ok {
		t.Fatal("accepted wrong signing key")
	}
	original.Expires = time.Now().Add(-time.Second).Unix()
	if _, ok := parseV0Handle(signV0Handle(original, "test-secret"), "test-secret"); ok {
		t.Fatal("accepted expired handle")
	}
}

func TestV0EndpointIsolation(t *testing.T) {
	for _, path := range []string{"chats", "hooks", "settings/preview-hosts", "usage/summary", "chats/token/../other", "chats/token/files/../../other"} {
		if _, _, ok := v0Endpoint("GET", path); ok {
			t.Fatalf("exposed privileged path %s", path)
		}
	}
	for _, path := range []string{"chats/token", "chats/token/files", "chats/token/files/download", "chats/token/preview", "chats/token/messages"} {
		if _, _, ok := v0Endpoint("GET", path); !ok {
			t.Fatalf("rejected supported path %s", path)
		}
	}
	if _, _, ok := v0Endpoint("POST", "chats"); !ok {
		t.Fatal("create unavailable")
	}
	if _, _, ok := v0Endpoint("POST", "chats/token/messages"); !ok {
		t.Fatal("followup unavailable")
	}
	if _, _, ok := v0Endpoint("DELETE", "chats/token"); ok {
		t.Fatal("unexpected destructive endpoint")
	}
}
