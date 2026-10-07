package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// 回复保持原来的 Responses/Chat 协议，路由卡片从独立观测接口读取。
func writeVerticalReply(c *gin.Context, body []byte, model, text string, images []map[string]any) {
	stream := gjson.GetBytes(body, "stream").Bool()
	id := "resp_" + uuid.NewString()
	created := time.Now().Unix()
	if strings.HasSuffix(c.Request.URL.Path, "/chat/completions") {
		writeVerticalChatReply(c, stream, id, created, model, text)
		return
	}
	itemID := "msg_" + uuid.NewString()
	part := &apicompat.ResponsesContentPart{Type: "output_text", Text: text}
	item := &apicompat.ResponsesOutput{Type: "message", ID: itemID, Status: "completed", Role: "assistant", Content: []apicompat.ResponsesContentPart{*part}}
	response := map[string]any{
		"id": id, "object": "response", "created_at": created, "model": model,
		"status": "completed", "error": nil, "incomplete_details": nil,
	}
	output := []any{item}
	for _, image := range images {
		image["id"] = "ig_" + uuid.NewString()
		output = append(output, image)
	}
	response["output"] = output
	if !stream {
		c.JSON(http.StatusOK, response)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	sequence := 0
	emit := func(kind string, value any) bool {
		encoded, err := json.Marshal(value)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", kind, encoded); err != nil {
			return false
		}
		c.Writer.Flush()
		return true
	}
	event := func(kind string, value apicompat.ResponsesStreamEvent) bool {
		value.Type = kind
		value.SequenceNumber = sequence
		sequence++
		return emit(kind, value)
	}
	initial := &apicompat.ResponsesResponse{ID: id, Object: "response", CreatedAt: created, Model: model, Status: "in_progress", Output: []apicompat.ResponsesOutput{}}
	if !event("response.created", apicompat.ResponsesStreamEvent{Response: initial}) ||
		!event("response.in_progress", apicompat.ResponsesStreamEvent{Response: initial}) {
		return
	}
	added := &apicompat.ResponsesOutput{Type: "message", ID: itemID, Status: "in_progress", Role: "assistant", Content: []apicompat.ResponsesContentPart{}}
	empty := &apicompat.ResponsesContentPart{Type: "output_text", Text: ""}
	for _, value := range []apicompat.ResponsesStreamEvent{
		{Type: "response.output_item.added", Item: added},
		{Type: "response.content_part.added", ItemID: itemID, Part: empty},
		{Type: "response.output_text.delta", ItemID: itemID, Delta: text},
		{Type: "response.output_text.done", ItemID: itemID, Text: text},
		{Type: "response.content_part.done", ItemID: itemID, Part: part},
		{Type: "response.output_item.done", Item: item},
	} {
		if !event(value.Type, value) {
			return
		}
	}
	for index, image := range images {
		for _, kind := range []string{"response.output_item.added", "response.output_item.done"} {
			if !emit(kind, map[string]any{"type": kind, "sequence_number": sequence, "output_index": index + 1, "item": image}) {
				return
			}
			sequence++
		}
	}
	emit("response.completed", map[string]any{"type": "response.completed", "sequence_number": sequence, "response": response})
}

func writeVerticalChatReply(c *gin.Context, stream bool, id string, created int64, model, text string) {
	if !stream {
		c.JSON(http.StatusOK, gin.H{"id": id, "object": "chat.completion", "created": created, "model": model,
			"choices": []gin.H{{"index": 0, "message": gin.H{"role": "assistant", "content": text}, "finish_reason": "stop"}}})
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	for _, choice := range []gin.H{
		{"index": 0, "delta": gin.H{"role": "assistant", "content": text}, "finish_reason": nil},
		{"index": 0, "delta": gin.H{}, "finish_reason": "stop"},
	} {
		value, err := json.Marshal(gin.H{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []gin.H{choice}})
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", value); err != nil {
			return
		}
		c.Writer.Flush()
	}
	_, _ = c.Writer.WriteString("data: [DONE]\n\n")
	c.Writer.Flush()
}
