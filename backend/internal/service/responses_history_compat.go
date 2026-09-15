package service

import (
	"strconv"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Some Responses relays validate replayed output_text against the complete
// output schema. Clients may omit empty metadata when replaying streamed items.
func normalizeResponsesOutputTextMetadata(body []byte) ([]byte, error) {
	for i, item := range gjson.GetBytes(body, "input").Array() {
		if item.Get("role").String() != "assistant" || !item.Get("content").IsArray() {
			continue
		}
		for j, part := range item.Get("content").Array() {
			if part.Get("type").String() != "output_text" {
				continue
			}
			for _, field := range []string{"annotations", "logprobs"} {
				if value := part.Get(field); value.Exists() && value.Type != gjson.Null {
					continue
				}
				path := "input." + strconv.Itoa(i) + ".content." + strconv.Itoa(j) + "." + field
				var err error
				body, err = sjson.SetRawBytes(body, path, []byte("[]"))
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return body, nil
}
