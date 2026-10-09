package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 裁决报告用已声明的异步追问卡片展示；没有该工具时保留可见文本，不制造阻塞确认。
func autoContinueReportItems(body []byte, decisions []*AutoContinueDecision, apiKeyID int64, secret string) []json.RawMessage {
	if len(decisions) == 0 {
		return nil
	}
	name, namespace := autoContinueFindReportTool(gjson.GetBytes(body, "tools"), "")
	if name == "" {
		for _, item := range gjson.GetBytes(body, "input").Array() {
			if item.Get("type").String() == "additional_tools" {
				name, namespace = autoContinueFindReportTool(item.Get("tools"), "")
				if name != "" {
					break
				}
			}
		}
	}
	if secret == "" {
		name = ""
	}
	var reports []json.RawMessage
	for _, decision := range decisions {
		// 直接修复不额外弹卡；有方案裁决时展示来源、方案和理由，并允许人纠正。
		if decision.Source != "arbiter" && decision.Source != "compaction" && len(decision.Selections) == 0 {
			continue
		}
		source := map[string]string{"design": "既定设计", "repair": "已定位缺陷的修复", "arbiter": "最高档裁决", "compaction": "自动上下文压缩"}[decision.Source]
		text := fmt.Sprintf("已采用以下方案并续跑：%s。依据：%s。决策来源：%s（%s）。记录编号：%s。需要调整时可在此反馈。", decision.Plan, decision.Reason, source, decision.Model, decision.ID)
		var item map[string]any
		if name != "" {
			arguments, _ := json.Marshal(map[string]any{"questions": []any{map[string]any{"title": text, "options": []string{"按此方案继续", "需要调整方案"}}}})
			item = map[string]any{"type": "function_call", "id": "fc_" + generateRequestID(), "call_id": autoContinueReportCallID(apiKeyID, secret, uuid.NewString()), "name": name, "arguments": string(arguments), "status": "completed"}
			if namespace != "" {
				item["namespace"] = namespace
			}
		} else {
			item = map[string]any{"type": "message", "id": "msg_" + generateRequestID(), "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}
		}
		encoded, _ := json.Marshal(item)
		reports = append(reports, encoded)
	}
	return reports
}

func autoContinueFindReportTool(tools gjson.Result, namespace string) (string, string) {
	for _, tool := range tools.Array() {
		if tool.Get("type").String() == "namespace" {
			if name, ns := autoContinueFindReportTool(tool.Get("tools"), tool.Get("name").String()); name != "" {
				return name, ns
			}
		}
		if autoContinueToolName(tool.Get("name").String()) == "request_user_input_async" {
			return tool.Get("name").String(), namespace
		}
	}
	return "", ""
}

func autoContinueAddReportJSON(response []byte, reports []json.RawMessage) []byte {
	if gjson.GetBytes(response, "status").String() != "completed" {
		return response
	}
	output := make([]json.RawMessage, 0)
	for _, item := range gjson.GetBytes(response, "output").Array() {
		output = append(output, json.RawMessage(item.Raw))
	}
	output = append(output, reports...)
	updated, err := sjson.SetBytes(response, "output", output)
	if err != nil {
		return response
	}
	return updated
}

// 流式执行工具即时透传，只在终态前补报告事件，保留真实响应 ID 与严格递增的序号。
func (w *autoContinueWriter) writeLive(data []byte) (int, error) {
	if !isEventStreamResponse(w.header) || len(w.reportItems) == 0 {
		return w.ResponseWriter.Write(data)
	}
	if w.livePending.Len()+len(data) > autoContinueBufferLimit {
		// 超大事件保留原始流并放弃报告注入，不能为一张卡片建立无界缓冲。
		w.reportItems = nil
		if _, err := w.ResponseWriter.Write(w.livePending.Bytes()); err != nil {
			return 0, err
		}
		w.livePending.Reset()
		return w.ResponseWriter.Write(data)
	}
	w.livePending.Write(data)
	for {
		pending := w.livePending.Bytes()
		boundary := bytes.Index(pending, []byte("\n\n"))
		if boundary < 0 {
			break
		}
		frame := append([]byte(nil), pending[:boundary+2]...)
		w.livePending.Next(boundary + 2)
		var before bytes.Buffer
		forEachOpenAISSEFrame(string(frame), func(kind string, payload []byte) {
			if seq := gjson.GetBytes(payload, "sequence_number"); seq.Exists() && seq.Int() > w.lastSequence {
				w.lastSequence = seq.Int()
			}
			if kind != "response.completed" && kind != "response.done" {
				return
			}
			response := gjson.GetBytes(payload, "response")
			if response.Get("status").String() != "completed" {
				return
			}
			sequence := gjson.GetBytes(payload, "sequence_number").Int()
			if sequence < w.lastSequence {
				sequence = w.lastSequence
			}
			index := len(response.Get("output").Array())
			reportFrames, nextSequence := autoContinueReportFrames(w.reportItems, index, sequence)
			before.Write(reportFrames)
			updated := autoContinueAddReportJSON([]byte(response.Raw), w.reportItems)
			payload, _ = sjson.SetRawBytes(payload, "response", updated)
			payload, _ = sjson.SetBytes(payload, "sequence_number", nextSequence)
			frame = []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", kind, payload))
			w.reportItems = nil
		})
		if before.Len() > 0 {
			if _, err := w.ResponseWriter.Write(before.Bytes()); err != nil {
				return 0, err
			}
		}
		if _, err := w.ResponseWriter.Write(frame); err != nil {
			return 0, err
		}
	}
	if len(w.reportItems) == 0 && w.livePending.Len() > 0 {
		if _, err := w.ResponseWriter.Write(w.livePending.Bytes()); err != nil {
			return 0, err
		}
		w.livePending.Reset()
	}
	return len(data), nil
}

func autoContinueReportFrames(reports []json.RawMessage, index int, sequence int64) ([]byte, int64) {
	var frames bytes.Buffer
	for _, report := range reports {
		for _, event := range autoContinueReportEvents(report, index) {
			event["sequence_number"] = sequence
			encoded, _ := json.Marshal(event)
			fmt.Fprintf(&frames, "event: %s\ndata: %s\n\n", event["type"], encoded)
			sequence++
		}
		index++
	}
	return frames.Bytes(), sequence
}

// 生成规范的工具参数或文本事件，既支持按 delta 读取的客户端，也支持终态恢复。
func autoContinueReportEvents(report json.RawMessage, index int) []map[string]any {
	item := gjson.ParseBytes(report)
	added, _ := sjson.SetBytes(report, "status", "in_progress")
	if item.Get("type").String() == "function_call" {
		added, _ = sjson.SetBytes(added, "arguments", "")
	} else {
		added, _ = sjson.SetBytes(added, "content", []any{})
	}
	events := []map[string]any{{"type": "response.output_item.added", "output_index": index, "item": json.RawMessage(added)}}
	identity := map[string]any{"item_id": item.Get("id").String(), "output_index": index}
	appendEvent := func(kind, field string, value any) {
		event := make(map[string]any)
		for key, data := range identity {
			event[key] = data
		}
		event["type"], event[field] = kind, value
		events = append(events, event)
	}
	if item.Get("type").String() == "function_call" {
		appendEvent("response.function_call_arguments.delta", "delta", item.Get("arguments").String())
		appendEvent("response.function_call_arguments.done", "arguments", item.Get("arguments").String())
	} else {
		identity["content_index"] = 0
		part := gjson.GetBytes(report, "content.0")
		empty, _ := sjson.SetBytes([]byte(part.Raw), "text", "")
		appendEvent("response.content_part.added", "part", json.RawMessage(empty))
		appendEvent("response.output_text.delta", "delta", part.Get("text").String())
		appendEvent("response.output_text.done", "text", part.Get("text").String())
		appendEvent("response.content_part.done", "part", json.RawMessage(part.Raw))
	}
	events = append(events, map[string]any{"type": "response.output_item.done", "output_index": index, "item": report})
	return events
}

// 网关生成的报告工具不在上游存储历史中；将其用户反馈转成正常消息，避免悬空工具结果。
func (s *OpenAIGatewayService) normalizeAutoContinueReportFeedback(c *gin.Context, body []byte) ([]byte, error) {
	if s.cfg == nil || c == nil {
		return body, nil
	}
	return autoContinueNormalizeReportFeedback(body, getAPIKeyIDFromContext(c), s.cfg.JWT.Secret)
}

func autoContinueNormalizeReportFeedback(body []byte, apiKeyID int64, secret string) ([]byte, error) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() || !strings.Contains(input.Raw, "call_auto_decision_report_") {
		return body, nil
	}
	var items []json.RawMessage
	for _, item := range input.Array() {
		if !autoContinueValidReportCallID(item.Get("call_id").String(), apiKeyID, secret) {
			items = append(items, json.RawMessage(item.Raw))
			continue
		}
		switch item.Get("type").String() {
		case "function_call":
		case "function_call_output":
			feedback, err := json.Marshal(map[string]any{"role": "user", "content": "用户对网关裁决报告的反馈：" + item.Get("output").String()})
			if err != nil {
				return nil, err
			}
			items = append(items, feedback)
		default:
			items = append(items, json.RawMessage(item.Raw))
		}
	}
	return sjson.SetBytes(body, "input", items)
}

// 用服务器密钥和 API Key 身份签名，不能凭工具结果的字符串前缀冒充人类纠正。
func autoContinueReportCallID(apiKeyID int64, secret, nonce string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "sub2api:auto-continue-report:%d:%s", apiKeyID, nonce)
	return "call_auto_decision_report_" + nonce + "_" + hex.EncodeToString(mac.Sum(nil)[:16])
}

func autoContinueValidReportCallID(callID string, apiKeyID int64, secret string) bool {
	if secret == "" || apiKeyID <= 0 || !strings.HasPrefix(callID, "call_auto_decision_report_") {
		return false
	}
	nonce, _, ok := strings.Cut(strings.TrimPrefix(callID, "call_auto_decision_report_"), "_")
	if !ok {
		return false
	}
	return hmac.Equal([]byte(callID), []byte(autoContinueReportCallID(apiKeyID, secret, nonce)))
}
