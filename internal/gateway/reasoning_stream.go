package gateway

import (
	"encoding/json"
	"strings"
)

// The pinned runtime emits reasoning_content. This bounded state machine also
// handles the explicit legacy wrapper even when its markers split across SSE
// chunks. Ordinary later quotations of the tag are preserved as answer text.
type reasoningStream struct {
	phase   int
	pending string
}

func (s *reasoningStream) filter(raw []byte) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(obj["choices"], &choices); err != nil {
		return nil, err
	}
	for _, choice := range choices {
		var delta map[string]json.RawMessage
		if json.Unmarshal(choice["delta"], &delta) != nil {
			continue
		}
		var content string
		if json.Unmarshal(delta["content"], &content) != nil || content == "" {
			continue
		}
		text := s.pending + content
		s.pending = ""
		reason := ""
		if s.phase == 0 {
			candidate := strings.TrimLeft(text, " \t\r\n")
			if candidate == "" { // emit harmless whitespace, keep looking for the first actual token
			} else if strings.HasPrefix("<think>", candidate) && len(candidate) < len("<think>") {
				s.pending = text
				text = ""
			} else if strings.HasPrefix(candidate, "<think>") {
				s.phase = 1
				text = strings.TrimPrefix(candidate, "<think>")
			} else {
				s.phase = 2
			}
		}
		if s.phase == 1 {
			if end := strings.Index(text, "</think>"); end >= 0 {
				reason = text[:end]
				text = text[end+len("</think>"):]
				s.phase = 2
			} else {
				keep := 0
				for n := 1; n < len("</think>") && n <= len(text); n++ {
					if strings.HasSuffix(text, "</think>"[:n]) {
						keep = n
					}
				}
				reason = text[:len(text)-keep]
				s.pending = text[len(text)-keep:]
				text = ""
			}
		}
		delta["content"], _ = json.Marshal(text)
		if reason != "" {
			delta["reasoning_content"], _ = json.Marshal(reason)
		}
		choice["delta"], _ = json.Marshal(delta)
	}
	obj["choices"], _ = json.Marshal(choices)
	return json.Marshal(obj)
}
