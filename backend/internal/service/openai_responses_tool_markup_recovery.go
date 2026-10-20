package service

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 上游偶尔把本该发起的工具调用写成一截纯文本标记，例如：
//
//	<|DSML|invoke name="exec"><|DSML|parameter name="input" string="true">pwd</|DSML|parameter></|DSML|invoke>
//
// 客户端于是只看到"说了要干、其实没干"的正文，表现为回答一点就结束（gpt-6 系列实测）。
// 这里把「完整、闭合、且确实属于本次请求已声明工具」的标记还原成标准 function_call /
// custom_tool_call，其余情况（示例、代码块、截断、未声明工具）保持原样，宁可少还原，
// 也不把正文当命令执行。

const (
	// 扣住的标记上限：长时间不闭合就整段放出，避免缓冲无限增长。
	toolMarkupPauseLimit = 64 << 10
	toolMarkupNamesKey   = "responses_tool_markup_names"
)

var toolMarkupOpeners = []string{"<|DSML|", "<|dsml|", "<DSML|", "<dsml|"}

// ---------------------------------------------------------------------------
// 工具声明
// ---------------------------------------------------------------------------

// responsesToolNames 记录本次请求里客户端可执行的工具，避免把正文里引用的
// 同类标记还原成调用。namespace 子工具同时登记短名。
type responsesToolNames struct {
	names  map[string]struct{}
	custom map[string]struct{} // 声明为 custom/freeform 的工具短名
}

func collectResponsesToolNames(body []byte) responsesToolNames {
	set := responsesToolNames{names: map[string]struct{}{}, custom: map[string]struct{}{}}
	var add func(tool gjson.Result)
	add = func(tool gjson.Result) {
		name := strings.TrimSpace(tool.Get("name").String())
		if name != "" {
			set.names[name] = struct{}{}
			set.names[autoContinueToolName(name)] = struct{}{}
			if tool.Get("type").String() == "custom" {
				set.custom[autoContinueToolName(name)] = struct{}{}
			}
		}
		if tool.Get("type").String() == "namespace" {
			for _, child := range tool.Get("tools").Array() {
				add(child)
			}
		}
	}
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		add(tool)
	}
	// Codex 会把附加工具放在 additional_tools 输入项里。
	for _, item := range gjson.GetBytes(body, "input").Array() {
		if item.Get("type").String() != "additional_tools" {
			continue
		}
		for _, tool := range item.Get("tools").Array() {
			add(tool)
		}
	}
	return set
}

func (set responsesToolNames) empty() bool { return len(set.names) == 0 }

func (set responsesToolNames) knows(name string) bool {
	if name == "" || set.names == nil {
		return false
	}
	if _, ok := set.names[name]; ok {
		return true
	}
	_, ok := set.names[autoContinueToolName(name)]
	return ok
}

func (set responsesToolNames) isCustom(name string) bool {
	_, ok := set.custom[autoContinueToolName(name)]
	return ok
}

// ---------------------------------------------------------------------------
// 解析
// ---------------------------------------------------------------------------

type recoveredToolCall struct {
	Name      string
	Arguments string // function_call 的 JSON 参数
	Input     string // custom_tool_call 的自由文本
	CallID    string
	ItemID    string
}

type toolMarkupParse struct {
	Calls     []recoveredToolCall
	Malformed bool // 出现标记但没还原出调用（工具未声明、缺参数等）
	Partial   bool // 调用块未闭合
}

// parseToolMarkup 只解析闭合的 invoke 块；代码围栏内的内容视为示例，不解析。
func (set responsesToolNames) parseToolMarkup(text string) toolMarkupParse {
	var out toolMarkupParse
	for start := 0; start < len(text); {
		open, opener, ok := nextInvokeOpen(text, start)
		if !ok {
			break
		}
		if insideFence(text, open) {
			// 示例代码块：跳过这块，继续找后面的真实调用。
			start = open + len(opener)
			continue
		}
		rest := text[open+len(opener):]
		closeAttr := strings.Index(rest, ">")
		if closeAttr < 0 {
			out.Partial = true
			return out
		}
		end, closeTag, _ := nextInvokeClose(rest, closeAttr+1)
		if end < 0 {
			out.Partial = true
			return out
		}
		name := toolMarkupAttr(rest[:closeAttr], "name")
		body := rest[closeAttr+1 : end]
		start = open + len(opener) + end + len(closeTag)

		if !set.knows(name) {
			out.Malformed = true
			continue
		}
		call := recoveredToolCall{Name: name}
		if set.isCustom(name) {
			call.Input = toolMarkupInput(body)
		} else {
			args := map[string]string{}
			for _, param := range toolMarkupParams(body) {
				if param.name == "" {
					continue
				}
				args[param.name] = param.value
			}
			if len(args) == 0 {
				out.Malformed = true
				continue
			}
			encoded, err := json.Marshal(args)
			if err != nil {
				out.Malformed = true
				continue
			}
			call.Arguments = string(encoded)
		}
		out.Calls = append(out.Calls, call)
	}
	return out
}

type toolMarkupParam struct{ name, value string }

// toolMarkupParams 提取 <|DSML|parameter name=".." string="..">..</|DSML|parameter>。
func toolMarkupParams(body string) []toolMarkupParam {
	var out []toolMarkupParam
	for pos := 0; pos < len(body); {
		open, opener, ok := nextParameterOpen(body, pos)
		if !ok {
			return out
		}
		base := open + len(opener)
		rest := body[base:]
		closeAttr := strings.Index(rest, ">")
		if closeAttr < 0 {
			return out
		}
		end, closeTag, _ := nextParameterClose(rest, closeAttr+1)
		if end < 0 {
			return out
		}
		tagText := rest[:closeAttr]
		out = append(out, toolMarkupParam{
			name:  toolMarkupAttr(tagText, "name"),
			value: toolMarkupText(rest[closeAttr+1:end], attrIsString(tagText)),
		})
		pos = base + end + len(closeTag)
	}
	return out
}

// toolMarkupInput 还原 freeform 工具的整段输入；多个 parameter 按顺序拼接。
func toolMarkupInput(body string) string {
	params := toolMarkupParams(body)
	if len(params) == 0 {
		return toolMarkupText(body, false)
	}
	var b strings.Builder
	for _, param := range params {
		b.WriteString(param.value)
	}
	return b.String()
}

func attrIsString(tagText string) bool {
	return strings.EqualFold(toolMarkupAttr(tagText, "string"), "true")
}

func toolMarkupAttr(tagText, name string) string {
	at := strings.Index(tagText, name+"=")
	if at < 0 {
		return ""
	}
	rest := tagText[at+len(name)+1:]
	if len(rest) < 2 || (rest[0] != '"' && rest[0] != '\'') {
		return ""
	}
	if end := strings.IndexByte(rest[1:], rest[0]); end >= 0 {
		return rest[1 : 1+end]
	}
	return ""
}

// toolMarkupText 还原参数值：string="true" 时按 JSON 字符串转义规则解码。
func toolMarkupText(raw string, jsonString bool) string {
	if !jsonString {
		return raw
	}
	trimmed := strings.TrimSpace(raw)
	var decoded string
	if strings.HasPrefix(trimmed, `"`) && strings.HasSuffix(trimmed, `"`) && len(trimmed) >= 2 {
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			return decoded
		}
	}
	if err := json.Unmarshal([]byte(`"`+strings.NewReplacer(`"`, `\"`, "\n", `\n`).Replace(trimmed)+`"`), &decoded); err == nil {
		return decoded
	}
	return raw
}

func nextInvokeOpen(text string, from int) (int, string, bool) {
	return nextTagOpen(text, from, "invoke")
}

func nextParameterOpen(text string, from int) (int, string, bool) {
	return nextTagOpen(text, from, "parameter")
}

func nextTagOpen(text string, from int, tag string) (int, string, bool) {
	best, found := -1, ""
	for _, opener := range toolMarkupOpeners {
		candidate := opener + tag
		if at := strings.Index(text[clampIndex(from, len(text)):], candidate); at >= 0 {
			if best < 0 || at+from < best {
				best, found = at+from, candidate
			}
		}
	}
	return best, found, best >= 0
}

func nextInvokeClose(rest string, from int) (int, string, bool) {
	return nextTagClose(rest, from, "invoke")
}

func nextParameterClose(rest string, from int) (int, string, bool) {
	return nextTagClose(rest, from, "parameter")
}

func nextTagClose(rest string, from int, tag string) (int, string, bool) {
	best, found := -1, ""
	for _, opener := range toolMarkupOpeners {
		candidate := "</" + opener[1:] + tag + ">"
		if at := strings.Index(rest[clampIndex(from, len(rest)):], candidate); at >= 0 {
			if best < 0 || at+from < best {
				best, found = at+from, candidate
			}
		}
	}
	return best, found, best >= 0
}

func clampIndex(index, length int) int {
	if index < 0 {
		return 0
	}
	if index > length {
		return length
	}
	return index
}

// insideFence 报告位置是否落在 ``` 代码围栏内（奇偶计数）。
func insideFence(text string, pos int) bool {
	return strings.Count(text[:clampIndex(pos, len(text))], "```")%2 == 1
}

// stripToolMarkup 去掉已还原的调用块与空外壳，保留其余正文。
func stripToolMarkup(text string) string {
	var b strings.Builder
	for pos := 0; pos < len(text); {
		open, opener, ok := nextInvokeOpen(text, pos)
		if !ok {
			b.WriteString(stripMarkupWrappers(text[pos:]))
			break
		}
		b.WriteString(stripMarkupWrappers(text[pos:open]))
		base := open + len(opener)
		rest := text[base:]
		closeAttr := strings.Index(rest, ">")
		if closeAttr < 0 {
			break
		}
		end, closeTag, _ := nextInvokeClose(rest, closeAttr+1)
		if end < 0 {
			break
		}
		pos = base + end + len(closeTag)
	}
	return strings.TrimRight(b.String(), " \t\r\n")
}

// stripMarkupWrappers 去掉不含内容的 calls/tool_calls 外壳标记。
func stripMarkupWrappers(text string) string {
	if !strings.Contains(text, "calls>") {
		return text
	}
	for _, opener := range toolMarkupOpeners {
		for _, name := range []string{"tool_calls", "calls"} {
			text = strings.ReplaceAll(text, "<"+opener[1:]+name+">", "")
			text = strings.ReplaceAll(text, "</"+opener[1:]+name+">", "")
		}
	}
	return text
}

// ---------------------------------------------------------------------------
// 非流式
// ---------------------------------------------------------------------------

// recoverResponsesToolMarkupBody 把 message 正文里的调用标记提成标准调用，
// 并按原顺序重排 output。只改 output，不动 id / usage / created_at。
func recoverResponsesToolMarkupBody(body []byte, set responsesToolNames) ([]byte, bool) {
	if set.empty() || !gjson.ValidBytes(body) || !bytes.Contains(body, []byte("invoke")) {
		return body, false
	}
	output := gjson.GetBytes(body, "output")
	if !output.IsArray() {
		return body, false
	}
	var rebuilt []any
	changed := false
	for _, item := range output.Array() {
		if item.Get("type").String() != "message" {
			rebuilt = append(rebuilt, json.RawMessage(item.Raw))
			continue
		}
		var kept, appended []map[string]any
		itemChanged := false
		for _, part := range item.Get("content").Array() {
			parsed := set.parseToolMarkup(part.Get("text").String())
			if len(parsed.Calls) == 0 || parsed.Partial || parsed.Malformed {
				kept = append(kept, objectFromRaw(part.Raw))
				continue
			}
			itemChanged = true
			changed = true
			if cleaned := stripToolMarkup(part.Get("text").String()); cleaned != "" {
				next := objectFromRaw(part.Raw)
				next["text"] = cleaned
				kept = append(kept, next)
			}
			for _, call := range parsed.Calls {
				appended = append(appended, callObject(call))
			}
		}
		if !itemChanged {
			rebuilt = append(rebuilt, json.RawMessage(item.Raw))
			continue
		}
		if len(kept) > 0 {
			updated := objectFromRaw(item.Raw)
			updated["content"] = kept
			rebuilt = append(rebuilt, updated)
		}
		for _, call := range appended {
			rebuilt = append(rebuilt, call)
		}
	}
	if !changed {
		return body, false
	}
	next, err := sjson.SetBytes(body, "output", rebuilt)
	if err != nil {
		return body, false
	}
	return next, true
}

func objectFromRaw(raw string) map[string]any {
	out := map[string]any{}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		return map[string]any{}
	}
	return out
}

func callObject(call recoveredToolCall) map[string]any {
	assignCallIDs(&call)
	if call.Input != "" {
		return map[string]any{"type": "custom_tool_call", "id": call.ItemID, "call_id": call.CallID, "name": call.Name, "input": call.Input, "status": "completed"}
	}
	return map[string]any{"type": "function_call", "id": call.ItemID, "call_id": call.CallID, "name": call.Name, "arguments": call.Arguments, "status": "completed"}
}

func assignCallIDs(call *recoveredToolCall) {
	if call.CallID == "" {
		call.CallID = randomID("call_")
	}
	if call.ItemID == "" {
		call.ItemID = randomID("item_")
	}
}

func randomID(prefix string) string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return prefix + "recovered"
	}
	return prefix + hex.EncodeToString(raw[:])
}

// ---------------------------------------------------------------------------
// 流式：标记可能被拆到多帧，先把"可能是标记开头"的尾巴扣住，确认后再放出。
// ---------------------------------------------------------------------------

type toolMarkupStreamPart struct {
	OutputIndex int
	ContentIdx  int
	ItemID      string
	Sequence    int64
}

type toolMarkupRecoverer struct {
	mu      sync.Mutex
	set     responsesToolNames
	pending string
	part    toolMarkupStreamPart
	fenced  bool // 已进入 ``` 代码围栏（围栏内的标记是示例）
	done    bool
}

func newToolMarkupRecoverer(set responsesToolNames) *toolMarkupRecoverer {
	if set.empty() {
		return nil
	}
	return &toolMarkupRecoverer{set: set}
}

// Feed 追加文本增量并返回可以安全下发的正文。
func (r *toolMarkupRecoverer) Feed(delta string, part toolMarkupStreamPart) string {
	if r == nil || delta == "" {
		return delta
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return delta
	}
	if r.pending == "" {
		keep, inMarkup := scanToolMarkupPrefix(delta)
		if keep == len(delta) && !inMarkup {
			// 没有可疑开头：整段可以下发，同时维护代码围栏奇偶。
			if strings.Count(delta, "```")%2 == 1 {
				r.fenced = !r.fenced
			}
			return delta
		}
		if r.fenced {
			return delta // 围栏内的标记是示例，原样透传
		}
		// 出现完整或半个 opener：先扣住，等后续帧判定是不是真调用。
		r.part = part
		r.pending = delta[keep:]
		return delta[:keep]
	}
	r.pending += delta
	if len(r.pending) > toolMarkupPauseLimit {
		// 长时间不闭合：整段放出，避免无限堆积。
		if _, _, ok := r.completeLocked(); ok {
			return ""
		}
		flushed := r.pending
		r.pending = ""
		return flushed
	}
	return ""
}

// Complete 在缓冲已构成完整调用时取出调用与剩余正文。
func (r *toolMarkupRecoverer) Complete() (string, []recoveredToolCall, bool) {
	if r == nil {
		return "", nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	text, calls, ok := r.completeLocked()
	if !ok {
		return "", nil, false
	}
	r.pending = ""
	return text, calls, true
}

func (r *toolMarkupRecoverer) completeLocked() (string, []recoveredToolCall, bool) {
	if r.done || r.pending == "" {
		return "", nil, false
	}
	parsed := r.set.parseToolMarkup(r.pending)
	if len(parsed.Calls) == 0 || parsed.Partial || parsed.Malformed {
		return "", nil, false
	}
	return stripToolMarkup(r.pending), parsed.Calls, true
}

// Buffered 报告当前被扣住、尚未下发的文本。
func (r *toolMarkupRecoverer) Buffered() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending
}

// Flush 放出全部未下发内容（能还原就还原，否则原样）。
func (r *toolMarkupRecoverer) Flush() (string, []recoveredToolCall) {
	if r == nil {
		return "", nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done || r.pending == "" {
		return "", nil
	}
	r.done = true
	pending := r.pending
	r.pending = ""
	parsed := r.set.parseToolMarkup(pending)
	if len(parsed.Calls) == 0 || parsed.Partial || parsed.Malformed {
		return pending, nil
	}
	return stripToolMarkup(pending), parsed.Calls
}

// scanToolMarkupPrefix 返回需要扣住的起始下标，以及是否已出现完整 opener。
// 无可疑开头时返回 keep=len(text)。
func scanToolMarkupPrefix(text string) (keep int, inMarkup bool) {
	for i := 0; i < len(text); i++ {
		rest := text[i:]
		for _, opener := range toolMarkupOpeners {
			if strings.HasPrefix(rest, opener) {
				return i, true
			}
			if len(rest) < len(opener) && strings.HasPrefix(opener, rest) {
				return i, false
			}
		}
	}
	return len(text), false
}

// ---------------------------------------------------------------------------
// SSE 事件
// ---------------------------------------------------------------------------

// rewriteToolCallSSE 按帧处理文本增量：能还原的调用替换成标准事件，
// 尚未确认的尾巴先扣住，确认不是标记后原样放出。
func rewriteToolCallSSE(frame []byte, eventType string, calls *[]recoveredToolCall, prompt *toolMarkupRecoverer) ([]byte, bool) {
	if prompt == nil || eventType != "response.output_text.delta" {
		return frame, false
	}
	delta := gjson.GetBytes(frame, "delta").String()
	if delta == "" {
		return frame, false
	}
	part := toolMarkupStreamPart{
		OutputIndex: int(gjson.GetBytes(frame, "output_index").Int()),
		ContentIdx:  int(gjson.GetBytes(frame, "content_index").Int()),
		ItemID:      gjson.GetBytes(frame, "item_id").String(),
		Sequence:    gjson.GetBytes(frame, "sequence_number").Int(),
	}
	released := prompt.Feed(delta, part)
	if text, taken, ok := prompt.Complete(); ok {
		*calls = append(*calls, taken...)
		var b strings.Builder
		if text != "" {
			b.WriteString(buildTextDeltaSSE(text, part))
		}
		for i := range taken {
			b.WriteString(buildToolCallSSE(&taken[i], part.Sequence+int64(i)*2+1))
		}
		return []byte(b.String()), true
	}
	if released == "" {
		return nil, true // 整帧被扣住，等后续帧补齐
	}
	if released == delta {
		return frame, false
	}
	return []byte(buildTextDeltaSSE(released, part)), true
}

// flushToolMarkupSSE 在非文本事件或流结束时放出剩余缓冲。
func flushToolMarkupSSE(prompt *toolMarkupRecoverer, calls *[]recoveredToolCall, sequence int64) []byte {
	if prompt == nil || prompt.Buffered() == "" {
		return nil
	}
	text, taken := prompt.Flush()
	*calls = append(*calls, taken...)
	var b strings.Builder
	if text != "" {
		b.WriteString(buildTextDeltaSSE(text, toolMarkupStreamPart{Sequence: sequence}))
	}
	for i := range taken {
		b.WriteString(buildToolCallSSE(&taken[i], sequence+int64(i)*2+1))
	}
	return []byte(b.String())
}

// buildToolCallSSE 生成客户端可直接执行的标准调用事件（参数已完整）。
func buildToolCallSSE(call *recoveredToolCall, sequence int64) string {
	assignCallIDs(call)
	added, done := map[string]any{}, map[string]any{}
	if call.Input != "" {
		added = map[string]any{"type": "response.output_item.added", "sequence_number": sequence, "output_index": 0, "item": map[string]any{"type": "custom_tool_call", "id": call.ItemID, "call_id": call.CallID, "name": call.Name, "input": ""}}
		done = map[string]any{"type": "response.output_item.done", "sequence_number": sequence + 1, "output_index": 0, "item": map[string]any{"type": "custom_tool_call", "id": call.ItemID, "call_id": call.CallID, "name": call.Name, "input": call.Input, "status": "completed"}}
	} else {
		added = map[string]any{"type": "response.output_item.added", "sequence_number": sequence, "output_index": 0, "item": map[string]any{"type": "function_call", "id": call.ItemID, "call_id": call.CallID, "name": call.Name, "arguments": ""}}
		done = map[string]any{"type": "response.output_item.done", "sequence_number": sequence + 1, "output_index": 0, "item": map[string]any{"type": "function_call", "id": call.ItemID, "call_id": call.CallID, "name": call.Name, "arguments": call.Arguments, "status": "completed"}}
	}
	var b strings.Builder
	for _, event := range []map[string]any{added, done} {
		encoded, err := json.Marshal(event)
		if err != nil {
			continue
		}
		b.WriteString("event: ")
		b.WriteString(event["type"].(string))
		b.WriteString("\ndata: ")
		b.Write(encoded)
		b.WriteString("\n\n")
	}
	return b.String()
}

func buildTextDeltaSSE(text string, part toolMarkupStreamPart) string {
	event := map[string]any{
		"type":            "response.output_text.delta",
		"sequence_number": part.Sequence,
		"item_id":         part.ItemID,
		"output_index":    part.OutputIndex,
		"content_index":   part.ContentIdx,
		"delta":           text,
	}
	encoded, _ := json.Marshal(event)
	return "event: response.output_text.delta\ndata: " + string(encoded) + "\n\n"
}

// reconcileTerminalOutput 让终态响应与已下发的调用一致：output 里的标记同样还原，
// 流中已还原但终态缺失的调用补到末尾，避免客户端历史与增量不一致。
func reconcileTerminalOutput(payload string, set responsesToolNames, calls []recoveredToolCall) string {
	response := gjson.Get(payload, "response")
	if !response.IsObject() || !response.Get("output").IsArray() {
		return payload
	}
	next := payload
	if recovered, changed := recoverResponsesToolMarkupBody([]byte(response.Raw), set); changed {
		if patched, err := sjson.SetRaw(payload, "response", string(recovered)); err == nil {
			next = patched
		}
	}
	if len(calls) == 0 {
		return next
	}
	existing := map[string]struct{}{}
	var merged []any
	for _, item := range gjson.Get(next, "response.output").Array() {
		merged = append(merged, json.RawMessage(item.Raw))
		if id := item.Get("call_id").String(); id != "" {
			existing[id] = struct{}{}
		}
	}
	added := 0
	for _, call := range calls {
		if _, ok := existing[call.CallID]; ok {
			continue
		}
		merged = append(merged, callObject(call))
		added++
	}
	if added == 0 {
		return next
	}
	patched, err := sjson.SetBytes([]byte(next), "response.output", merged)
	if err != nil {
		return next
	}
	return string(patched)
}

// ---------------------------------------------------------------------------
// 出站接管
// ---------------------------------------------------------------------------

// attachToolMarkupRecovery 在 Responses 出站前接管响应体：流式按帧还原，
// 非流式在读取完整响应后一次还原（见 recoverToolMarkupJSONBody）。
func attachToolMarkupRecovery(c *gin.Context, resp *http.Response, requestBody []byte, maxLineSize int) {
	if resp == nil || c == nil || GetOpenAIClientTransport(c) == OpenAIClientTransportWS {
		return
	}
	set := collectResponsesToolNames(requestBody)
	if set.empty() {
		return
	}
	c.Set(toolMarkupNamesKey, set)
	if isEventStreamResponse(resp.Header) && resp.Body != nil {
		resp.Body = newToolMarkupStreamBody(resp.Body, set, maxLineSize)
	}
}

func toolMarkupNames(c *gin.Context) responsesToolNames {
	if c == nil {
		return responsesToolNames{}
	}
	value, ok := c.Get(toolMarkupNamesKey)
	if !ok {
		return responsesToolNames{}
	}
	set, _ := value.(responsesToolNames)
	return set
}

// recoverToolMarkupJSONBody 处理非流式响应体。
func recoverToolMarkupJSONBody(c *gin.Context, body []byte) ([]byte, bool) {
	set := toolMarkupNames(c)
	if set.empty() {
		return body, false
	}
	return recoverResponsesToolMarkupBody(body, set)
}

// newToolMarkupStreamBody 在 SSE 出站前把正文里的调用标记还原成标准调用事件。
// 与 newResponsesClientToolStreamBody 同构：按帧处理，逐帧下发。
func newToolMarkupStreamBody(source io.ReadCloser, set responsesToolNames, maxLineSize int) io.ReadCloser {
	reader, writer := io.Pipe()
	go transformToolMarkupStream(source, writer, set, maxLineSize)
	return &toolMarkupStreamBody{PipeReader: reader, source: source}
}

type toolMarkupStreamBody struct {
	*io.PipeReader
	source io.ReadCloser
}

func (b *toolMarkupStreamBody) Close() error {
	_ = b.source.Close()
	return b.PipeReader.Close()
}

func transformToolMarkupStream(source io.ReadCloser, destination *io.PipeWriter, set responsesToolNames, maxLineSize int) {
	defer func() { _ = source.Close() }()
	if maxLineSize <= 0 {
		maxLineSize = defaultMaxLineSize
	}
	scanner := bufio.NewScanner(source)
	scanBuf := getSSEScannerBuf64K()
	defer putSSEScannerBuf64K(scanBuf)
	scanner.Buffer(scanBuf[:0], maxLineSize)

	recoverer := newToolMarkupRecoverer(set)
	var calls []recoveredToolCall
	emit := func(text string) error {
		if text == "" {
			return nil
		}
		_, err := destination.Write([]byte(text))
		return err
	}
	frame := make([]string, 0, 4)
	flushFrame := func() error {
		if len(frame) == 0 {
			return nil
		}
		lines := frame
		frame = make([]string, 0, 4)
		eventType, payload := "", ""
		for _, line := range lines {
			if typ, ok := extractOpenAISSEEventLine(line); ok {
				eventType = typ
			}
			if data, ok := extractOpenAISSEDataLine(line); ok {
				payload = data
			}
		}
		if payload == "" || !gjson.Valid(payload) {
			return emit(strings.Join(lines, "\n") + "\n\n")
		}
		if eventType == "" {
			eventType = strings.TrimSpace(gjson.Get(payload, "type").String())
		}
		if eventType == "response.output_text.delta" {
			next, replaced := rewriteToolCallSSE([]byte(payload), eventType, &calls, recoverer)
			if !replaced {
				return emit(strings.Join(lines, "\n") + "\n\n")
			}
			return emit(string(next))
		}
		// 其它事件：先把扣住的正文/调用放出去，保持"正文 → 调用 → 终态"的顺序。
		if flushed := flushToolMarkupSSE(recoverer, &calls, gjson.Get(payload, "sequence_number").Int()-1); len(flushed) > 0 {
			if err := emit(string(flushed)); err != nil {
				return err
			}
		}
		if eventType == "response.completed" || eventType == "response.done" || eventType == "response.incomplete" {
			return emit("event: " + eventType + "\ndata: " + reconcileTerminalOutput(payload, set, calls) + "\n\n")
		}
		return emit(strings.Join(lines, "\n") + "\n\n")
	}

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if err := flushFrame(); err != nil {
				_ = destination.CloseWithError(err)
				return
			}
			continue
		}
		// 上一帧缺空行却又来新的 data 行：先收口，避免两帧被拼在一起。
		if _, isData := extractOpenAISSEDataLine(line); isData && frameHasData(frame) {
			if err := flushFrame(); err != nil {
				_ = destination.CloseWithError(err)
				return
			}
		}
		frame = append(frame, line)
	}
	if err := flushFrame(); err != nil {
		_ = destination.CloseWithError(err)
		return
	}
	if flushed := flushToolMarkupSSE(recoverer, &calls, 0); len(flushed) > 0 {
		if err := emit(string(flushed)); err != nil {
			_ = destination.CloseWithError(err)
			return
		}
	}
	if err := scanner.Err(); err != nil {
		_ = destination.CloseWithError(err)
		return
	}
	_ = destination.Close()
}

func frameHasData(lines []string) bool {
	for _, line := range lines {
		if _, ok := extractOpenAISSEDataLine(line); ok {
			return true
		}
	}
	return false
}
