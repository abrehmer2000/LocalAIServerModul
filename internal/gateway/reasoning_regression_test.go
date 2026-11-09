package gateway

import (
	"encoding/json"
	"localaiserver/internal/event"
	"strings"
	"testing"
)

func TestLeadingWhitespaceDoesNotExposeReasoning(t *testing.T) {
	for _, prefix := range []string{"", " \n\t"} {
		var e event.Event
		body, err := prepareCompletion([]byte(completion(prefix+"<think>private</think>answer")), chatRequest{}, "m", "id", &e)
		if err != nil {
			t.Fatal(err)
		}
		var obj struct {
			Choices []struct {
				Message struct {
					Content   string `json:"content"`
					Reasoning string `json:"reasoning_content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(body, &obj); err != nil {
			t.Fatal(err)
		}
		if obj.Choices[0].Message.Content != "answer" || obj.Choices[0].Message.Reasoning != "private" {
			t.Fatalf("unexpected split: %s", body)
		}
	}
}

func TestStreamingWhitespaceAndSplitReasoningMarker(t *testing.T) {
	for _, chunks := range [][]string{{" \n", "<th", "ink>private</thi", "nk>answer"}, {" \n<think>private</think>answer"}} {
		var s reasoningStream
		var answer, reasoning strings.Builder
		for _, chunk := range chunks {
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": chunk}}}})
			out, err := s.filter(raw)
			if err != nil {
				t.Fatal(err)
			}
			var obj struct {
				Choices []struct {
					Delta map[string]string `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(out, &obj); err != nil {
				t.Fatal(err)
			}
			answer.WriteString(obj.Choices[0].Delta["content"])
			reasoning.WriteString(obj.Choices[0].Delta["reasoning_content"])
		}
		if strings.TrimSpace(answer.String()) != "answer" || reasoning.String() != "private" {
			t.Fatalf("answer=%q reasoning=%q", answer.String(), reasoning.String())
		}
	}
}
