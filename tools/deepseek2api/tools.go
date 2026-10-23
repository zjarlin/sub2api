package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type functionTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		Parameters  map[string]any `json:"parameters,omitempty"`
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

func (localSchemaLoader) Load(string) (any, error) {
	return nil, errors.New("external tool schema references are unsupported")
}

var functionName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func parseToolPolicy(req chatRequest) (toolPolicy, error) {
	p := toolPolicy{choice: "none", parallel: true, schemas: map[string]*jsonschema.Schema{}}
	invalid := func() (toolPolicy, error) {
		return p, apiError{400, "invalid_tools", "Invalid function tools, schemas or tool choice"}
	}
	if len(req.Tools) > 128 {
		return invalid()
	}
	for _, tool := range req.Tools {
		name := tool.Function.Name
		if tool.Type != "function" || !functionName.MatchString(name) || p.schemas[name] != nil {
			return invalid()
		}
		schema := tool.Function.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		compiler := jsonschema.NewCompiler()
		compiler.UseLoader(localSchemaLoader{})
		compiler.DefaultDraft(jsonschema.Draft2020)
		compiler.AssertFormat()
		if err := compiler.AddResource("https://tools.invalid/parameters", schema); err != nil {
			return invalid()
		}
		compiled, err := compiler.Compile("https://tools.invalid/parameters")
		if err != nil {
			return invalid()
		}
		p.schemas[name] = compiled
	}
	if len(req.Tools) > 0 {
		p.choice = "auto"
	}
	if len(req.ToolChoice) > 0 && string(req.ToolChoice) != "null" {
		var choice string
		if json.Unmarshal(req.ToolChoice, &choice) == nil {
			if choice != "none" && choice != "auto" && choice != "required" {
				return invalid()
			}
			p.choice = choice
		} else {
			var selected struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if json.Unmarshal(req.ToolChoice, &selected) != nil || selected.Type != "function" || p.schemas[selected.Function.Name] == nil {
				return invalid()
			}
			p.choice, p.forced = "required", selected.Function.Name
		}
	}
	if p.choice != "none" && len(req.Tools) == 0 {
		return invalid()
	}
	if req.ParallelToolCalls != nil {
		p.parallel = *req.ParallelToolCalls
	}
	return p, nil
}

// 网页模型使用 DSML 描述客户端函数；执行权始终留给调用方。
func (p toolPolicy) prompt(tools []functionTool) string {
	if len(tools) == 0 {
		return ""
	}
	if p.choice == "none" {
		return "\nClient tools are disabled for this turn. Answer as text without tool calls.\n"
	}
	contract, _ := json.Marshal(map[string]any{"tools": tools, "tool_choice": p.choice, "selected_function": p.forced, "parallel_tool_calls": p.parallel})
	return `
Client function protocol: request only the declared functions; the client executes them and returns role=tool results. Never invent tool results.
For calls, output this DSML form without code fences:
<｜DSML｜ calls><｜DSML｜ invoke name="function_name"><｜DSML｜ parameter name="argument" string="true">string value</｜DSML｜ parameter></｜DSML｜ invoke></｜DSML｜ calls>
Use string="false" with a JSON value for numbers, booleans, arrays, objects and null. Preserve string whitespace. All tags must close. Parameters must satisfy the function JSON Schema.
For parallel calls, include multiple invoke elements in one calls element. required means at least one call; selected_function means exactly that function once; parallel_tool_calls=false permits at most one call. Otherwise answer in plain text.
Contract:
` + string(contract) + "\n"
}

var dsmlTag = regexp.MustCompile(`<(/?)(?:｜｜DSML｜｜|｜DSML｜)\s+(/?)(calls|tool_calls|invoke|parameter)\b([^>]*)>`)
var dsmlMarkers = []string{"<｜DSML｜", "</｜DSML｜", "<｜｜DSML｜｜", "</｜｜DSML｜｜"}

type dsmlCursor struct {
	text string
	pos  int
}
type dsmlElement struct {
	kind       string
	closed     bool
	attrs      map[string]string
	start, end int
}

func (c *dsmlCursor) next() (dsmlElement, error) {
	match := dsmlTag.FindStringSubmatchIndex(c.text[c.pos:])
	if match == nil {
		return dsmlElement{}, errors.New("incomplete DSML tool call")
	}
	sub := func(i int) string { return c.text[c.pos+match[2*i] : c.pos+match[2*i+1]] }
	e := dsmlElement{start: c.pos + match[0], end: c.pos + match[1], closed: sub(1) != "" || sub(2) != "", kind: sub(3), attrs: map[string]string{}}
	if e.kind == "tool_calls" {
		e.kind = "calls"
	}
	attrs := sub(4)
	if strings.TrimSpace(attrs) != "" {
		decoder := xml.NewDecoder(strings.NewReader("<tag" + attrs + "/>"))
		token, err := decoder.Token()
		if err != nil {
			return e, err
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			return e, errors.New("invalid DSML attributes")
		}
		for _, attr := range start.Attr {
			if _, duplicate := e.attrs[attr.Name.Local]; duplicate || attr.Name.Space != "" {
				return e, errors.New("duplicate DSML attribute")
			}
			e.attrs[attr.Name.Local] = attr.Value
		}
	}
	c.pos = e.end
	return e, nil
}

func (c *dsmlCursor) structural() (dsmlElement, error) {
	before := c.pos
	e, err := c.next()
	if err != nil {
		return e, err
	}
	if strings.TrimSpace(c.text[before:e.start]) != "" {
		return e, errors.New("unexpected text within DSML tool call")
	}
	if e.closed && len(e.attrs) != 0 {
		return e, errors.New("attributes on DSML closing tag")
	}
	return e, nil
}

func (c *dsmlCursor) arguments() (map[string]any, error) {
	args := map[string]any{}
	for {
		e, err := c.structural()
		if err != nil {
			return nil, err
		}
		if e.kind == "invoke" && e.closed {
			return args, nil
		}
		if e.kind != "parameter" || e.closed || e.attrs["name"] == "" || len(e.attrs) > 2 {
			return nil, errors.New("invalid DSML parameter")
		}
		for key := range e.attrs {
			if key != "name" && key != "string" {
				return nil, errors.New("unknown DSML parameter attribute")
			}
		}
		name := e.attrs["name"]
		if _, duplicate := args[name]; duplicate {
			return nil, errors.New("duplicate DSML parameter")
		}
		end, err := c.next()
		if err != nil || end.kind != "parameter" || !end.closed || len(end.attrs) != 0 {
			return nil, errors.New("unclosed DSML parameter")
		}
		raw := c.text[e.end:end.start]
		switch e.attrs["string"] {
		case "true":
			args[name] = raw
		case "", "false":
			var value any
			decoder := json.NewDecoder(strings.NewReader(raw))
			decoder.UseNumber()
			if !json.Valid([]byte(raw)) || decoder.Decode(&value) != nil {
				return nil, errors.New("invalid DSML JSON parameter")
			}
			args[name] = value
		default:
			return nil, errors.New("invalid DSML string flag")
		}
	}
}

func (p toolPolicy) parseBlock(c *dsmlCursor) ([]toolCall, error) {
	var calls []toolCall
	for {
		e, err := c.structural()
		if err != nil {
			return nil, err
		}
		if e.kind == "calls" && e.closed && len(calls) > 0 {
			return calls, nil
		}
		name := e.attrs["name"]
		if e.kind != "invoke" || e.closed || len(e.attrs) != 1 || p.schemas[name] == nil || p.choice == "none" || (p.forced != "" && p.forced != name) || len(calls) >= 128 {
			return nil, errors.New("undeclared or disallowed DSML function")
		}
		args, err := c.arguments()
		if err != nil {
			return nil, err
		}
		if err := p.schemas[name].Validate(args); err != nil {
			return nil, errors.New("DSML arguments do not match function schema")
		}
		encoded, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		call := toolCall{ID: "call_" + strings.TrimPrefix(newCompletionID(), "chatcmpl-"), Type: "function"}
		call.Function.Name, call.Function.Arguments = name, string(encoded)
		calls = append(calls, call)
	}
}

func (p toolPolicy) reply(text string) (string, []toolCall, error) {
	invalid := func() (string, []toolCall, error) {
		return "", nil, apiError{502, "invalid_tool_response", "DeepSeek returned an incomplete or invalid tool call"}
	}
	var plain strings.Builder
	var calls []toolCall
	cursor := dsmlCursor{text: text}
	for cursor.pos < len(text) {
		start := safeToolPrefix(text[cursor.pos:])
		// 流结束后普通的末尾比较符号可以放行，已成形的 DSML 前缀仍须完整闭合。
		if len(text)-cursor.pos-start < len("<｜DS") {
			start = len(text) - cursor.pos
		}
		plain.WriteString(text[cursor.pos : cursor.pos+start])
		cursor.pos += start
		if cursor.pos == len(text) {
			break
		}
		before := cursor.pos
		e, err := cursor.next()
		if err != nil || e.start != before || e.kind != "calls" || e.closed || len(e.attrs) != 0 {
			return invalid()
		}
		block, err := p.parseBlock(&cursor)
		if err != nil {
			return invalid()
		}
		calls = append(calls, block...)
		if len(calls) > 128 {
			return invalid()
		}
	}
	if (p.choice == "required" && len(calls) == 0) || ((!p.parallel || p.forced != "") && len(calls) > 1) {
		return invalid()
	}
	return plain.String(), calls, nil
}

// 扣留完整标记及尾部未定形前缀，防止分块边界把 DSML 泄漏成正文。
func safeToolPrefix(text string) int {
	end := len(text)
	for _, marker := range dsmlMarkers {
		if index := strings.Index(text, marker); index >= 0 && index < end {
			end = index
		}
		for length := 1; length < len(marker) && length <= len(text); length++ {
			if strings.HasSuffix(text, marker[:length]) && len(text)-length < end {
				end = len(text) - length
			}
		}
	}
	return end
}

func renderToolCalls(calls []toolCall) (string, error) {
	var out strings.Builder
	out.WriteString("<｜DSML｜ calls>\n")
	for _, call := range calls {
		var args map[string]any
		decoder := json.NewDecoder(bytes.NewBufferString(call.Function.Arguments))
		decoder.UseNumber()
		if decoder.Decode(&args) != nil || args == nil || !json.Valid([]byte(call.Function.Arguments)) {
			return "", errors.New("invalid historical tool arguments")
		}
		fmt.Fprintf(&out, "<｜DSML｜ invoke name=%q>\n", call.Function.Name)
		keys := make([]string, 0, len(args))
		for key := range args {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			var name bytes.Buffer
			if err := xml.EscapeText(&name, []byte(key)); err != nil {
				return "", err
			}
			// JSON 转义保留字符串空白，也避免参数值中的标签破坏历史边界。
			value, err := json.Marshal(args[key])
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&out, "<｜DSML｜ parameter name=\"%s\" string=\"false\">%s</｜DSML｜ parameter>\n", name.String(), value)
		}
		out.WriteString("</｜DSML｜ invoke>\n")
	}
	out.WriteString("</｜DSML｜ calls>")
	return out.String(), nil
}
