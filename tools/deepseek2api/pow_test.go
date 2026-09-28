package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

func TestActualDeepSeekWASM(t *testing.T) {
	path := os.Getenv("DEEPSEEK_WEB_TEST_WASM_FILE")
	if path == "" {
		t.Skip("set DEEPSEEK_WEB_TEST_WASM_FILE to verify the current upstream PoW module")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	solver, err := newPowSolver(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	defer solver.runtime.Close(ctx)
	input := challenge{
		Algorithm: "DeepSeekHashV1", Challenge: "7ffc9d19b6eed96a6fca68f8ffe30ee61035d4959e4180f187bf85b356016a96",
		Salt: "3bde54628ea8413fee87", Signature: "signature", Difficulty: 144000,
		ExpireAt: 1775380966945, TargetPath: completionPath,
	}
	encoded, err := solver.solveChallenge(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Answer int64 `json:"answer"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.Answer < 0 {
		t.Fatalf("invalid PoW result: %s: %v", data, err)
	}
}
