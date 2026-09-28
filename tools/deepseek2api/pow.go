package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

type challenge struct {
	Algorithm  string `json:"algorithm"`
	Challenge  string `json:"challenge"`
	Salt       string `json:"salt"`
	Signature  string `json:"signature"`
	Difficulty int64  `json:"difficulty"`
	ExpireAt   int64  `json:"expire_at"`
	TargetPath string `json:"target_path"`
}

type powSolver struct {
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	stack    string
	malloc   string
	solve    string
	nextID   atomic.Uint64
}

func newPowSolver(ctx context.Context, wasm []byte) (*powSolver, error) {
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))
	compiled, err := runtime.CompileModule(ctx, wasm)
	if err != nil {
		_ = runtime.Close(ctx)
		return nil, fmt.Errorf("compile DeepSeek PoW module: %w", err)
	}
	defs := compiled.ExportedFunctions()
	stack := matchExport(defs, "__wbindgen_add_to_stack_pointer", []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32})
	malloc := matchExport(defs, "__wbindgen_malloc", []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32})
	if malloc == "" {
		malloc = matchPrefix(defs, "__wbindgen_export_", []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32})
	}
	params := []api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeF64}
	solve := matchExport(defs, "wasm_solve", params, nil)
	if solve == "" {
		solve = matchUnique(defs, params, nil)
	}
	if stack == "" || malloc == "" || solve == "" {
		_ = runtime.Close(ctx)
		return nil, errors.New("DeepSeek PoW module has unsupported exports")
	}
	return &powSolver{runtime: runtime, compiled: compiled, stack: stack, malloc: malloc, solve: solve}, nil
}

func sameTypes(a, b []api.ValueType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasSignature(def api.FunctionDefinition, params, results []api.ValueType) bool {
	return sameTypes(def.ParamTypes(), params) && sameTypes(def.ResultTypes(), results)
}

func matchExport(defs map[string]api.FunctionDefinition, name string, params, results []api.ValueType) string {
	if def, ok := defs[name]; ok && hasSignature(def, params, results) {
		return name
	}
	return ""
}

func matchPrefix(defs map[string]api.FunctionDefinition, prefix string, params, results []api.ValueType) string {
	var found string
	for name, def := range defs {
		if strings.HasPrefix(name, prefix) && hasSignature(def, params, results) {
			if found != "" {
				return ""
			}
			found = name
		}
	}
	return found
}

func matchUnique(defs map[string]api.FunctionDefinition, params, results []api.ValueType) string {
	var found string
	for name, def := range defs {
		if hasSignature(def, params, results) {
			if found != "" {
				return ""
			}
			found = name
		}
	}
	return found
}

func writeWasmString(ctx context.Context, mod api.Module, malloc api.Function, value string) (uint64, uint64, error) {
	if len(value) > 1<<20 {
		return 0, 0, errors.New("PoW input exceeds limit")
	}
	ptr, err := malloc.Call(ctx, uint64(len(value)), 1)
	if err != nil || len(ptr) != 1 {
		return 0, 0, errors.New("PoW memory allocation failed")
	}
	if !mod.Memory().Write(uint32(ptr[0]), []byte(value)) {
		return 0, 0, errors.New("PoW memory write failed")
	}
	return ptr[0], uint64(len(value)), nil
}

func (p *powSolver) solveChallenge(ctx context.Context, input challenge) (string, error) {
	if input.Algorithm != "DeepSeekHashV1" || input.Difficulty <= 0 || input.Difficulty > 10_000_000 {
		return "", errors.New("unsupported DeepSeek PoW challenge")
	}
	name := "pow-" + strconv.FormatUint(p.nextID.Add(1), 10)
	mod, err := p.runtime.InstantiateModule(ctx, p.compiled, wazero.NewModuleConfig().WithName(name))
	if err != nil {
		return "", fmt.Errorf("instantiate PoW module: %w", err)
	}
	defer mod.Close(ctx)
	stack, malloc, solve := mod.ExportedFunction(p.stack), mod.ExportedFunction(p.malloc), mod.ExportedFunction(p.solve)
	if mod.Memory() == nil || stack == nil || malloc == nil || solve == nil {
		return "", errors.New("PoW module is incomplete")
	}
	ret, err := stack.Call(ctx, 0xfffffff0)
	if err != nil || len(ret) != 1 {
		return "", errors.New("PoW stack allocation failed")
	}
	ptr, length, err := writeWasmString(ctx, mod, malloc, input.Challenge)
	if err != nil {
		return "", err
	}
	prefix := input.Salt + "_" + strconv.FormatInt(input.ExpireAt, 10) + "_"
	prefixPtr, prefixLen, err := writeWasmString(ctx, mod, malloc, prefix)
	if err != nil {
		return "", err
	}
	_, err = solve.Call(ctx, ret[0], ptr, length, prefixPtr, prefixLen, math.Float64bits(float64(input.Difficulty)))
	if err != nil {
		return "", fmt.Errorf("solve PoW: %w", err)
	}
	status, ok := mod.Memory().ReadUint32Le(uint32(ret[0]))
	if !ok || status == 0 {
		return "", errors.New("DeepSeek PoW has no solution")
	}
	answer, ok := mod.Memory().ReadFloat64Le(uint32(ret[0]) + 8)
	if !ok || math.IsNaN(answer) || answer < 0 {
		return "", errors.New("DeepSeek PoW returned an invalid answer")
	}
	payload := map[string]any{
		"algorithm":   input.Algorithm,
		"challenge":   input.Challenge,
		"salt":        input.Salt,
		"answer":      int64(answer),
		"signature":   input.Signature,
		"target_path": input.TargetPath,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
