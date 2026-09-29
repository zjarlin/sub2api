package main

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type responseFormat struct {
	schema   *jsonschema.Schema
	contract string
}

func parseResponseFormat(raw json.RawMessage) (responseFormat, error) {
	var result responseFormat
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return result, nil
	}
	invalid := func() (responseFormat, error) {
		return result, problem(400, "invalid_response_format", "Invalid response_format type or JSON schema")
	}
	var format struct {
		Type       string `json:"type"`
		JSONSchema *struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Strict      *bool          `json:"strict"`
			Schema      map[string]any `json:"schema"`
		} `json:"json_schema"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&format) != nil {
		return invalid()
	}
	schema := map[string]any{"type": "object"}
	switch format.Type {
	case "text":
		if format.JSONSchema != nil {
			return invalid()
		}
		return result, nil
	case "json_object":
		if format.JSONSchema != nil {
			return invalid()
		}
	case "json_schema":
		if format.JSONSchema == nil || !functionName.MatchString(format.JSONSchema.Name) || format.JSONSchema.Schema == nil {
			return invalid()
		}
		schema = format.JSONSchema.Schema
	default:
		return invalid()
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(localSchemaLoader{})
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	if compiler.AddResource("https://vibex.invalid/response", schema) != nil {
		return invalid()
	}
	compiled, err := compiler.Compile("https://vibex.invalid/response")
	if err != nil {
		return invalid()
	}
	result.schema, result.contract = compiled, string(raw)
	return result, nil
}

func (f responseFormat) prompt(text string, tools bool) string {
	if f.schema == nil {
		return text
	}
	target := "Return the final answer as JSON without Markdown fences or extra text."
	if tools {
		target = "When answering, the content string inside the client function envelope must contain JSON matching this format. Keep the tool_calls envelope unchanged; this format does not constrain function calls."
	}
	return text + "\n" + target + " Response format: " + f.contract + "\n"
}

func (f responseFormat) validate(message map[string]any) error {
	if f.schema == nil || message["tool_calls"] != nil {
		return nil
	}
	text, ok := message["content"].(string)
	if content, pointer := message["content"].(*string); pointer && content != nil {
		text, ok = *content, true
	}
	var value any
	decoder := json.NewDecoder(bytes.NewBufferString(text))
	decoder.UseNumber()
	if !ok || decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || f.schema.Validate(value) != nil {
		return problem(502, "invalid_response_format", "VibeX reply does not match the requested response_format")
	}
	return nil
}
