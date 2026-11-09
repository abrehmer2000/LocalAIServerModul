package gateway

import (
	"encoding/json"
	"fmt"
	"localaiserver/internal/config"
	"localaiserver/internal/event"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestV2ProtocolIdentityPathsAndPreservation(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, completion("Äöü „Zitat“ – \\u1234"))
	}))
	defer up.Close()
	g, err := New(cfg(node("n", up.URL)), &memoryAudit{})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	body := `{"model":"chat","messages":[{"role":"user","content":"\u00e4"}]}`
	for _, tt := range []struct {
		user, path string
		want       int
	}{{"", "/v1/chat/completions", 200}, {"a@b", "/v1/chat/completions", 200}, {"-x", "/v1/chat/completions", 400}, {"a:b", "/v1/chat/completions", 400}, {strings.Repeat("a", 65), "/v1/chat/completions", 400}, {"u", "/v1/../v1/chat/completions", 400}, {"u", "/v1/chat/completions/", 400}} {
		r := httptest.NewRequest("POST", tt.path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+testKey)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Request-ID", "client-123")
		if tt.user != "" {
			r.Header.Set("X-User-ID", tt.user)
		}
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Fatalf("user=%q path=%s got=%d", tt.user, tt.path, w.Code)
		}
		if w.Code == 200 {
			if w.Header().Get("X-Request-ID") != "client-123" {
				t.Fatal("client ID lost")
			}
			var data map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &data)
			if data["id"] == nil || data["model"] != "chat" {
				t.Fatal("missing completion metadata")
			}
		}
	}
	before := calls.Load()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"chat","messages":[{"role":"user","content":"\ud800"}]}`))
	r.Header.Set("Authorization", "Bearer "+testKey)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 400 || calls.Load() != before {
		t.Fatal("invalid Unicode repaired or sent upstream")
	}
}

func TestV2PermanentAndTransientCodesSurviveAdapter(t *testing.T) {
	for _, tt := range []struct {
		code         string
		status, want int
	}{{"context_length_exceeded", 400, 400}, {"vision_not_supported", 422, 422}, {"model_loading", 503, 503}, {"model_unavailable", 503, 503}, {"timeout", 504, 504}} {
		t.Run(tt.code, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprintf(w, `{"error":{"code":%q,"message":"PRIVATE_PROMPT"}}`, tt.code)
			}))
			defer up.Close()
			a := &memoryAudit{}
			g, err := New(cfg(node("n", up.URL)), a)
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"chat","messages":[{"role":"user","content":"test"}]}`))
			r.Header.Set("Authorization", "Bearer "+testKey)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			g.Handler().ServeHTTP(w, r)
			if w.Code != tt.want || !strings.Contains(w.Body.String(), tt.code) || strings.Contains(w.Body.String(), "PRIVATE_PROMPT") {
				t.Fatal(w.Code, w.Body.String())
			}
			if retryable(tt.code) && w.Header().Get("Retry-After") == "" {
				t.Fatal("missing retry header")
			}
			if got := a.events[len(a.events)-1]; got.Status != w.Code || got.Outcome != tt.code {
				t.Fatal("audit differs from response", got)
			}
		})
	}
}

func TestV2TruncatedJSONIsUnchangedAndDoesNotQuarantine(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Replace(completion(`{"answer":`), `"stop"`, `"length"`, 1))
	}))
	defer up.Close()
	a := &memoryAudit{}
	g, err := New(cfg(node("n", up.URL)), a)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"chat","messages":[{"role":"user","content":"test"}],"response_format":{"type":"json_object"}}`))
		r.Header.Set("Authorization", "Bearer "+testKey)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, r)
		var data struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				Finish string `json:"finish_reason"`
			} `json:"choices"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &data)
		if w.Code != 200 || len(data.Choices) != 1 || data.Choices[0].Message.Content != `{"answer":` || data.Choices[0].Finish != "length" {
			t.Fatal(w.Body.String())
		}
	}
}

func TestV2TextModelRejectsImageWithoutBackendCall(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("image reached text engine") }))
	defer up.Close()
	g, err := New(cfg(node("n", up.URL)), &memoryAudit{})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	body := `{"model":"chat","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]}]}`
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testKey)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "vision_not_supported") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestV2KeyRightsAndRotation(t *testing.T) {
	c := cfg(node("n", "http://127.0.0.1:1"))
	c.Keys[0].Models = []string{}
	g, err := New(c, &memoryAudit{})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"chat","messages":[{"role":"user","content":"test"}]}`))
	r.Header.Set("Authorization", "Bearer "+testKey)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing model right admitted")
	}
	keys := []config.Key{c.Keys[0]}
	keys[0].Models = []string{"chat"}
	if err = g.ReplaceKeys(keys, event.Event{}); err != nil {
		t.Fatal(err)
	}
	if !g.allowed(keys[0].ID, "chat") {
		t.Fatal("key reload not visible")
	}
}
