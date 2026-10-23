package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type deepSeekFragment struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type deepSeekDelta struct {
	path      string
	operation string
	fragments []deepSeekFragment
	status    string
	usage     int
	content   string
	reasoning string
}

func patchText(target *string, operation string, value json.RawMessage) error {
	var text string
	if string(value) == "null" {
		return errors.New("null DeepSeek text patch")
	}
	if err := json.Unmarshal(value, &text); err != nil {
		return err
	}
	if operation == "APPEND" {
		*target += text
	} else {
		*target = text
	}
	return nil
}

func (d *deepSeekDelta) applyValue(path, operation string, value json.RawMessage) error {
	switch path {
	case "response":
		var snapshot struct {
			Fragments []deepSeekFragment `json:"fragments"`
			Status    string             `json:"status"`
			Usage     int                `json:"accumulated_token_usage"`
			Content   string             `json:"content"`
			Reasoning string             `json:"thinking_content"`
		}
		if operation != "SET" {
			return errors.New("unsupported DeepSeek response operation")
		}
		if err := json.Unmarshal(value, &snapshot); err != nil {
			return err
		}
		d.fragments, d.status, d.usage = snapshot.Fragments, snapshot.Status, snapshot.Usage
		d.content, d.reasoning = snapshot.Content, snapshot.Reasoning
		return nil
	case "response/fragments":
		var fragments []deepSeekFragment
		if err := json.Unmarshal(value, &fragments); err != nil {
			var fragment deepSeekFragment
			if err := json.Unmarshal(value, &fragment); err != nil || fragment.Type == "" {
				return errors.New("invalid DeepSeek fragments")
			}
			fragments = []deepSeekFragment{fragment}
		}
		if operation == "APPEND" {
			d.fragments = append(d.fragments, fragments...)
		} else {
			d.fragments = fragments
		}
		return nil
	case "response/content":
		return patchText(&d.content, operation, value)
	case "response/thinking_content":
		return patchText(&d.reasoning, operation, value)
	case "response/status":
		return json.Unmarshal(value, &d.status)
	case "response/accumulated_token_usage":
		return json.Unmarshal(value, &d.usage)
	}
	if !strings.HasPrefix(path, "response/fragments/") || !strings.HasSuffix(path, "/content") {
		return nil
	}
	index, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(path, "response/fragments/"), "/content"))
	if index == -1 {
		index = len(d.fragments) - 1
	}
	if err != nil || index < 0 || index >= len(d.fragments) {
		return errors.New("invalid DeepSeek fragment index")
	}
	return patchText(&d.fragments[index].Content, operation, value)
}

func (d *deepSeekDelta) apply(raw json.RawMessage, prefix string, depth int) error {
	if depth > 16 {
		return errors.New("DeepSeek patch nesting exceeds limit")
	}
	var event struct {
		Path      *string         `json:"p"`
		Operation *string         `json:"o"`
		Value     json.RawMessage `json:"v"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return err
	}
	if len(event.Value) == 0 {
		if event.Path != nil || event.Operation != nil {
			return errors.New("DeepSeek delta is missing a value")
		}
		// 元数据不改变路径和操作游标，避免误继承上一条 BATCH。
		return nil
	}
	if event.Path != nil {
		d.path = *event.Path
	}
	if event.Operation != nil {
		d.operation = *event.Operation
	}
	path := strings.Trim(prefix+"/"+d.path, "/ ")
	if d.operation == "BATCH" {
		var batch []json.RawMessage
		if err := json.Unmarshal(event.Value, &batch); err != nil {
			return err
		}
		child := *d
		child.path, child.operation = "", "SET"
		for _, item := range batch {
			if err := child.apply(item, path, depth+1); err != nil {
				return err
			}
		}
		d.fragments, d.status, d.usage = child.fragments, child.status, child.usage
		d.content, d.reasoning = child.content, child.reasoning
		return nil
	}
	if d.operation != "SET" && d.operation != "APPEND" {
		return errors.New("unsupported DeepSeek patch operation")
	}
	if path == "" {
		var snapshot struct {
			Response json.RawMessage `json:"response"`
		}
		if err := json.Unmarshal(event.Value, &snapshot); err != nil {
			return err
		}
		if len(snapshot.Response) > 0 {
			return d.applyValue("response", "SET", snapshot.Response)
		}
		return nil
	}
	return d.applyValue(path, d.operation, event.Value)
}

func (d *deepSeekDelta) result() chatResult {
	if d.fragments == nil {
		return chatResult{content: d.content, reasoning: d.reasoning, totalTokens: d.usage}
	}
	var content, reasoning strings.Builder
	for _, fragment := range d.fragments {
		switch fragment.Type {
		case "RESPONSE":
			content.WriteString(fragment.Content)
		case "THINK":
			reasoning.WriteString(fragment.Content)
		}
	}
	return chatResult{content: content.String(), reasoning: reasoning.String(), totalTokens: d.usage}
}

func readDeepSeekStream(body io.Reader) (string, string, int, error) {
	result, err := consumeDeepSeekStream(body, nil)
	return result.content, result.reasoning, result.totalTokens, err
}

// 空行才结束一个 SSE 帧，多行 data 必须合并后再解析 JSON。
func consumeDeepSeekStream(body io.Reader, emit func(content, reasoning string) error) (chatResult, error) {
	limited := &io.LimitedReader{R: body, N: 32 << 20}
	reader := bufio.NewReader(limited)
	for {
		prefix, err := reader.Peek(1)
		if err != nil || !strings.ContainsRune(" \t\r\n", rune(prefix[0])) {
			break
		}
		_, _ = reader.ReadByte()
	}
	if prefix, _ := reader.Peek(1); len(prefix) > 0 && prefix[0] == '{' {
		var envelope upstreamEnvelope
		if err := json.NewDecoder(io.LimitReader(reader, 2<<20)).Decode(&envelope); err != nil {
			return chatResult{}, fmt.Errorf("decode DeepSeek response: %w", err)
		}
		if err := envelope.responseError(); err != nil {
			return chatResult{}, err
		}
		return chatResult{}, errors.New("DeepSeek returned JSON instead of a completion stream")
	}
	delta := &deepSeekDelta{operation: "SET"}
	var previous chatResult
	process := func(event, data string) error {
		var envelope upstreamEnvelope
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			return fmt.Errorf("decode DeepSeek event: %w", err)
		}
		if err := envelope.responseError(); err != nil {
			return err
		}
		if event == "error" || event == "toast" {
			var notice struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(data), &notice)
			if event == "error" || notice.Type == "error" {
				return errors.New("DeepSeek reported a stream error")
			}
			return nil
		}
		if event != "" && event != "message" {
			return nil
		}
		if err := delta.apply(json.RawMessage(data), "", 0); err != nil {
			return fmt.Errorf("decode DeepSeek event: %w", err)
		}
		current := delta.result()
		if emit == nil {
			return nil
		}
		// SSE 已发送的文字不能撤回；覆盖已发前缀时明确失败，不能重复或静默截断。
		if !strings.HasPrefix(current.content, previous.content) || !strings.HasPrefix(current.reasoning, previous.reasoning) {
			return errors.New("DeepSeek rewrote previously streamed text")
		}
		content, reasoning := current.content[len(previous.content):], current.reasoning[len(previous.reasoning):]
		previous = current
		if content == "" && reasoning == "" {
			return nil
		}
		return emit(content, reasoning)
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	var event string
	var lines []string
	flush := func() (bool, error) {
		data := strings.Join(lines, "\n")
		name := event
		event, lines = "", nil
		if strings.TrimSpace(data) == "[DONE]" {
			return true, nil
		}
		if strings.TrimSpace(data) == "" {
			return false, nil
		}
		return false, process(name, data)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			done, err := flush()
			if err != nil {
				return chatResult{}, err
			}
			if done {
				break
			}
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			lines = append(lines, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return chatResult{}, err
	}
	if limited.N == 0 {
		return chatResult{}, errors.New("DeepSeek response exceeds limit")
	}
	// EOF 前没有空行的半帧不能当作已完成消息。
	if len(lines) != 0 || delta.status != "FINISHED" {
		return chatResult{}, errors.New("DeepSeek response ended without completion")
	}
	return delta.result(), nil
}
