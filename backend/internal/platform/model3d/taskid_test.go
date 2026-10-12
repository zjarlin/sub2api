package model3d

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTaskHandleRoundTrip(t *testing.T) {
	secret := HandleSecret("server-secret")
	handle := TaskHandle{
		Provider:  "meshy",
		Mode:      ModeImageTo3D,
		TaskID:    "abc-123",
		UserID:    7,
		KeyID:     42,
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	token, err := EncodeTaskHandle(secret, handle)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Count(token, ".") != 1 {
		t.Fatalf("token shape = %q", token)
	}
	decoded, err := DecodeTaskHandle(secret, token, time.Now())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded != handle {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestTaskHandleRejectsTamperingAndForeignSecret(t *testing.T) {
	secret := HandleSecret("server-secret")
	token, err := EncodeTaskHandle(secret, TaskHandle{Provider: "tripo", Mode: ModeTextTo3D, TaskID: "t1", UserID: 1, KeyID: 2})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := DecodeTaskHandle(secret, token+"x", time.Now()); !errors.Is(err, ErrTaskMismatch) {
		t.Fatalf("tampered signature err = %v", err)
	}
	if _, err := DecodeTaskHandle(HandleSecret("other"), token, time.Now()); !errors.Is(err, ErrTaskMismatch) {
		t.Fatalf("foreign secret err = %v", err)
	}
	if _, err := DecodeTaskHandle(secret, "not-a-token", time.Now()); !errors.Is(err, ErrTaskMismatch) {
		t.Fatalf("malformed err = %v", err)
	}
}

func TestTaskHandleExpiry(t *testing.T) {
	secret := HandleSecret("server-secret")
	token, err := EncodeTaskHandle(secret, TaskHandle{
		Provider:  "meshy",
		Mode:      ModeTextTo3D,
		TaskID:    "t",
		ExpiresAt: time.Now().Add(-time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := DecodeTaskHandle(secret, token, time.Now()); !errors.Is(err, ErrTaskExpired) {
		t.Fatalf("err = %v", err)
	}
}

func TestTaskHandleRequiresSecret(t *testing.T) {
	if _, err := EncodeTaskHandle(nil, TaskHandle{Provider: "meshy", TaskID: "t"}); !errors.Is(err, ErrTaskMismatch) {
		t.Fatalf("encode err = %v", err)
	}
	if _, err := DecodeTaskHandle(nil, "a.b", time.Now()); !errors.Is(err, ErrTaskMismatch) {
		t.Fatalf("decode err = %v", err)
	}
}

func TestModeNormalize(t *testing.T) {
	cases := map[string]Mode{
		"text-to-3d":  ModeTextTo3D,
		"TEXT_TO_3D":  ModeTextTo3D,
		"image-to-3d": ModeImageTo3D,
		"img2model":   ModeImageTo3D,
		"":            "",
		"whatever":    "",
	}
	for input, want := range cases {
		if got := Mode(input).Normalize(); got != want {
			t.Fatalf("Mode(%q).Normalize() = %q, want %q", input, got, want)
		}
	}
	if !StatusSucceeded.Terminal() || !StatusFailed.Terminal() || StatusRunning.Terminal() {
		t.Fatal("terminal status classification is wrong")
	}
}
