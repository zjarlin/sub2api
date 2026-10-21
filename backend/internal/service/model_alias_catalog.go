package service

import (
	"encoding/json"
	"slices"
)

// 对最终可见目录做精确归一和去重；保留选中条目的能力字段，不合并不兼容能力。
func (p *ModelAliasPolicy) CanonicalizeCatalog(body []byte) ([]byte, error) {
	if p == nil || len(p.Groups) == 0 {
		return body, nil
	}
	var catalog map[string]json.RawMessage
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, err
	}
	for _, field := range []string{"data", "models"} {
		raw, ok := catalog[field]
		if !ok {
			continue
		}
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, err
		}
		out := make([]map[string]json.RawMessage, 0, len(entries))
		seen := map[string]int{}
		preferred := map[string]int{}
		for _, entry := range entries {
			idField := "id"
			if _, ok := entry["slug"]; ok {
				idField = "slug"
			}
			var id string
			if err := json.Unmarshal(entry[idField], &id); err != nil {
				return nil, err
			}
			canonical := p.RootID(id)
			rank := slices.Index(p.IDs(canonical), id)
			if rank < 0 {
				rank = 4096
			}
			if _, exists := seen[canonical]; exists && rank >= preferred[canonical] {
				continue
			}
			entry[idField], _ = json.Marshal(canonical)
			for _, nameField := range []string{"display_name", "name"} {
				var name string
				if json.Unmarshal(entry[nameField], &name) == nil && name == id {
					entry[nameField], _ = json.Marshal(canonical)
				}
			}
			if pos, exists := seen[canonical]; exists {
				out[pos] = entry
			} else {
				seen[canonical] = len(out)
				out = append(out, entry)
			}
			preferred[canonical] = rank
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			return nil, err
		}
		catalog[field] = encoded
	}
	return json.Marshal(catalog)
}
