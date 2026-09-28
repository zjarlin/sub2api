package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type functionTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		Parameters  map[string]any `json:"parameters"`
		Strict      *bool          `json:"strict,omitempty"`
	} `json:"function"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type toolPolicy struct {
	choice   string
	forced   string
	parallel bool
	schemas  map[string]*jsonschema.Schema
}

type localSchemaLoader struct{}

func (localSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema references are unsupported")
}

var functionName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func parseToolPolicy(r chatRequest) (toolPolicy, error) {
	p := toolPolicy{choice: "none", parallel: true, schemas: map[string]*jsonschema.Schema{}}
	invalid := func() (toolPolicy, error) {
		return p, problem(400, "invalid_tools", "Invalid function tools, schemas or tool choice")
	}
	if len(r.Tools) > 128 {
		return invalid()
	}
	for i, tool := range r.Tools {
		if tool.Type != "function" || !functionName.MatchString(tool.Function.Name) || p.schemas[tool.Function.Name] != nil {
			return invalid()
		}
		schema := tool.Function.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object"}
			r.Tools[i].Function.Parameters = schema
		}
		compiler := jsonschema.NewCompiler()
		compiler.UseLoader(localSchemaLoader{})
		compiler.DefaultDraft(jsonschema.Draft2020)
		compiler.AssertFormat()
		if compiler.AddResource("https://vibex.invalid/parameters", schema) != nil {
			return invalid()
		}
		compiled, err := compiler.Compile("https://vibex.invalid/parameters")
		if err != nil {
			return invalid()
		}
		p.schemas[tool.Function.Name] = compiled
	}
	if len(r.Tools) > 0 {
		p.choice = "auto"
	}
	if len(r.ToolChoice) > 0 && string(r.ToolChoice) != "null" {
		var choice string
		if json.Unmarshal(r.ToolChoice, &choice) == nil {
			if choice != "auto" && choice != "none" && choice != "required" {
				return invalid()
			}
			p.choice = choice
		} else {
			var choice struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if json.Unmarshal(r.ToolChoice, &choice) != nil || choice.Type != "function" || p.schemas[choice.Function.Name] == nil {
				return invalid()
			}
			p.choice, p.forced = "required", choice.Function.Name
		}
	}
	if p.choice != "none" && len(r.Tools) == 0 {
		return invalid()
	}
	if r.ParallelToolCalls != nil {
		p.parallel = *r.ParallelToolCalls
	}
	return p, nil
}

func (p toolPolicy) enabled() bool { return p.choice != "none" }

func (p toolPolicy) prompt(text string, tools []functionTool) string {
	if !p.enabled() {
		return text + "\nReturn only the final answer as text. Do not call client functions.\n"
	}
	choice := p.choice
	if p.forced != "" {
		choice = p.forced
	}
	contract, _ := json.Marshal(map[string]any{"tools": tools, "tool_choice": choice, "parallel_tool_calls": p.parallel})
	return text + `
Client function protocol: only describe calls; the client executes them and sends role=tool results. Never use project tools to execute client functions. Return exactly one JSON object without fences or other text: {"content":null,"tool_calls":[{"name":"function_name","arguments":{"key":"value"}}]} when calling, or {"content":"final answer","tool_calls":[]} when answering. Arguments must match the provided schema. required must call at least one function. A selected function must be called exactly once. parallel_tool_calls=false permits at most one call. Only use results present in the conversation.
This output protocol also applies after tool results. Contract:
` + string(contract)
}

func (p toolPolicy) reply(text string) (map[string]any, string, error) {
	invalid := func() (map[string]any, string, error) {
		return nil, "", problem(502, "invalid_tool_response", "VibeX reply does not match the requested tool policy or argument schema")
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```json\n") && strings.HasSuffix(text, "\n```") {
		text = strings.TrimSuffix(strings.TrimPrefix(text, "```json\n"), "\n```")
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &raw) != nil || len(raw) != 2 || raw["content"] == nil || raw["tool_calls"] == nil {
		return invalid()
	}
	var content *string
	var calls []struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw["tool_calls"]))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if json.Unmarshal(raw["content"], &content) != nil || decoder.Decode(&calls) != nil || string(raw["tool_calls"]) == "null" || len(calls) > 128 {
		return invalid()
	}
	if (!p.parallel || p.forced != "") && len(calls) > 1 || p.choice == "required" && len(calls) == 0 {
		return invalid()
	}
	message := map[string]any{"role": "assistant", "content": content}
	if len(calls) == 0 {
		if content == nil {
			return invalid()
		}
		return message, "stop", nil
	}
	result := make([]toolCall, 0, len(calls))
	for _, call := range calls {
		schema := p.schemas[call.Name]
		if schema == nil || p.forced != "" && call.Name != p.forced || call.Arguments == nil || schema.Validate(call.Arguments) != nil {
			return invalid()
		}
		args, _ := json.Marshal(call.Arguments)
		item := toolCall{ID: "call_" + randomID(), Type: "function"}
		item.Function.Name, item.Function.Arguments = call.Name, string(args)
		result = append(result, item)
	}
	message["tool_calls"] = result
	return message, "tool_calls", nil
}
