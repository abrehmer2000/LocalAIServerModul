package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"localaiserver/internal/config"
)

const schemaFormat = `{"type":"json_schema","json_schema":{"name":"navigator","strict":true,"schema":{"type":"object","required":["answer"],"properties":{"answer":{"anyOf":[{"type":"string"},{"type":"null"}]}},"additionalProperties":false}}}`

func completion(content string) string {
	encoded, _ := json.Marshal(content)
	return `{"choices":[{"message":{"content":` + string(encoded) + `,"reasoning_content":"PRIVATE_REASONING"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":20}}`
}

func TestStructuredResponseEnforcement(t *testing.T) {
	for _, tt := range []struct {
		name, format, answer string
		status               int
	}{
		{"schema valid", schemaFormat, `{"answer":"ok"}`, 200},
		{"schema anyOf null", schemaFormat, `{"answer":null}`, 200},
		{"schema wrong type", schemaFormat, `{"answer":17}`, 200},
		{"schema missing", schemaFormat, `{}`, 200},
		{"schema extra", schemaFormat, `{"answer":"ok","extra":1}`, 200},
		{"object", `{"type":"json_object"}`, `{"ok":true}`, 200},
		{"array is not object", `{"type":"json_object"}`, `[]`, 200},
		{"malformed JSON", `{"type":"json_object"}`, `text`, 200},
		{"reasoning leaked", `{"type":"text"}`, `<think>PRIVATE_REASONING</think>answer`, 200},
		{"external schema", `{"type":"json_schema","json_schema":{"name":"bad","schema":{"$ref":"file:///etc/passwd"}}}`, `{}`, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]json.RawMessage
				json.NewDecoder(r.Body).Decode(&body)
				if !bytes.Contains(body["response_format"], []byte(`"type"`)) {
					t.Error("format not forwarded")
				}
				fmt.Fprint(w, completion(tt.answer))
			}))
			defer upstream.Close()
			visionNode := node("n", upstream.URL)
			visionNode.Vision = true
			g, err := New(cfg(visionNode), &memoryAudit{})
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			server := httptest.NewServer(g.Handler())
			defer server.Close()
			resp, err := request(server.URL, testKey, "u", `{"model":"chat","messages":[{"role":"user","content":"test"}],"response_format":`+tt.format+`}`)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tt.status {
				t.Fatalf("status=%d body=%s", resp.StatusCode, data)
			}
			if tt.status == 400 && calls != 0 {
				t.Fatal("invalid schema reached engine")
			}
			if tt.status != 200 && bytes.Contains(data, []byte("PRIVATE_REASONING")) {
				t.Fatal("error leaked contents")
			}
		})
	}
}

func TestEmbeddedImagesThinkingAndPrivacy(t *testing.T) {
	images := map[string][]byte{"png": []byte("\x89PNG\r\n\x1a\nfixture"), "jpeg": {0xff, 0xd8, 0xff, 0x00}, "gif": []byte("GIF89afixture"), "webp": []byte("RIFF1234WEBPfixture")}
	for mime, data := range images {
		for _, thinking := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/%v", mime, thinking), func(t *testing.T) {
				imageURL := "data:image/" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var payload map[string]any
					if json.NewDecoder(r.Body).Decode(&payload) != nil {
						t.Error("invalid request")
					}
					if payload["cache_prompt"] != false || payload["chat_template_kwargs"].(map[string]any)["enable_thinking"] != thinking {
						t.Error("thinking/cache not forwarded")
					}
					raw, _ := json.Marshal(payload["messages"])
					if !bytes.Contains(raw, []byte(imageURL)) {
						t.Error("image not forwarded")
					}
					if r.Header.Get("X-User-ID") != "" || payload["user"] != nil {
						t.Error("identity leaked")
					}
					fmt.Fprint(w, completion("image description"))
				}))
				defer upstream.Close()
				audit := &memoryAudit{}
				visionNode := node("n", upstream.URL)
				visionNode.Vision = true
				g, err := New(cfg(visionNode), audit)
				if err != nil {
					t.Fatal(err)
				}
				defer g.Close()
				server := httptest.NewServer(g.Handler())
				defer server.Close()
				body := fmt.Sprintf(`{"model":"chat","user":"u","messages":[{"role":"user","content":[{"type":"text","text":"PROMPT_SECRET"},{"type":"image_url","image_url":{"url":%q}}]}],"chat_template_kwargs":{"enable_thinking":%v},"cache_prompt":true}`, imageURL, thinking)
				resp, err := request(server.URL, testKey, "u", body)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != 200 {
					t.Fatal(resp.StatusCode)
				}
				audit.mu.Lock()
				raw, _ := json.Marshal(audit.events)
				audit.mu.Unlock()
				for _, secret := range []string{imageURL, "PROMPT_SECRET", "PRIVATE_REASONING", "image description"} {
					if bytes.Contains(raw, []byte(secret)) {
						t.Fatal("audit leaked contents")
					}
				}
			})
		}
	}
}

func TestRejectInvalidImages(t *testing.T) {
	for _, url := range []string{"https://example.org/private.png", "data:image/svg+xml;base64,AAAA", "data:image/png;base64,%%%", "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not png"))} {
		if validateImage(url) == nil {
			t.Fatalf("accepted %s", url)
		}
	}
	data := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, (8<<20)-8)...)
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	if err := validateImage(url); err != nil {
		t.Fatal(err)
	}
	url = "data:image/png;base64," + base64.StdEncoding.EncodeToString(append(data, 0))
	if validateImage(url) == nil {
		t.Fatal("oversized image accepted")
	}
}

func TestLargeImageBodyAndLongNavigator(t *testing.T) {
	image := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, (8<<20)-8)...)
	body := `{"model":"chat","messages":[{"role":"user","content":[{"type":"text","text":"` + strings.Repeat("n", 70000) + `"},{"type":"image_url","image_url":{"url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(image) + `"}}]}]}`
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	c := cfg()
	c.MaxBodyBytes = config.MaxRequestBytes
	if _, err := decodeRequest(httptest.NewRecorder(), r, c); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"chat","messages":[{"role":"user","content":"`+strings.Repeat("n", config.MaxRequestBytes)+`"}]}`))
	if _, err := decodeRequest(httptest.NewRecorder(), r, c); err == nil {
		t.Fatal("oversized body accepted")
	}
}

func TestBackendStatusAndRetryAfter(t *testing.T) {
	for _, status := range []int{400, 413, 415, 422, 429, 503, 500, 401, 302} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(status)
				fmt.Fprint(w, "PRIVATE_ENGINE_ERROR")
			}))
			defer up.Close()
			g, err := New(cfg(node("n", up.URL)), &memoryAudit{})
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			server := httptest.NewServer(g.Handler())
			defer server.Close()
			resp, err := request(server.URL, testKey, "u", strings.Replace(chat, `"stream":true`, `"stream":false`, 1))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			want := status
			if status == 500 || status == 401 || status == 302 {
				want = 502
			}
			if resp.StatusCode != want {
				t.Fatalf("got %d want %d", resp.StatusCode, want)
			}
			if (status == 429 || status == 503) && resp.Header.Get("Retry-After") != "30" {
				t.Fatal("retry not bounded")
			}
			data, _ := io.ReadAll(resp.Body)
			if bytes.Contains(data, []byte("PRIVATE_ENGINE_ERROR")) {
				t.Fatal("engine error leaked")
			}
		})
	}
	for input, want := range map[string]string{"": "1", "garbage": "1", "-1": "1", "0": "1", "7": "7", "999": "30"} {
		if got := retryAfter(input); got != want {
			t.Fatalf("retry %s=%s", input, got)
		}
	}
}

// This deliberately crosses the former 30-second response-header deadline.
func TestNonStreamingResponseAfterThirtySeconds(t *testing.T) {
	if testing.Short() {
		t.Skip("31-second inference deadline regression")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-time.After(31 * time.Second):
			fmt.Fprint(w, completion("ok"))
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	c := cfg(node("n", upstream.URL))
	c.TimeoutSeconds = config.DefaultTimeoutSeconds
	g, err := New(c, &memoryAudit{})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	server := httptest.NewServer(g.Handler())
	defer server.Close()
	resp, err := request(server.URL, testKey, "u", strings.Replace(chat, `"stream":true`, `"stream":false`, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
}

func TestFourSimultaneousRequestsAndCapacityRecovery(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		select {
		case <-release:
			fmt.Fprint(w, completion("ok"))
		case <-r.Context().Done():
		}
	}))
	defer up.Close()
	n := node("n", up.URL)
	n.Vision = true
	n.Capacity = 4
	g, err := New(cfg(n), &memoryAudit{})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	server := httptest.NewServer(g.Handler())
	defer server.Close()
	body := strings.Replace(chat, `"stream":true`, `"stream":false`, 1)
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			resp, err := request(server.URL, testKey, "u", body)
			if err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 {
					err = fmt.Errorf("status %d", resp.StatusCode)
				}
			}
			results <- err
		}()
	}
	// Always unblock handlers, including on a failed assertion.
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("four requests did not reach engine together")
		}
	}
	resp, err := request(server.URL, testKey, "u", body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 || resp.Header.Get("Retry-After") != "1" {
		t.Fatal("uncontrolled overflow")
	}
	close(release)
	released = true
	for i := 0; i < 4; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	resp, err = request(server.URL, testKey, "u", body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("capacity not recovered")
	}
}

func TestTimeoutWhileReadingNonStreamingResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, `{"choices":[`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	c := cfg(node("n", upstream.URL))
	c.TimeoutSeconds = 1
	g, err := New(c, &memoryAudit{})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	server := httptest.NewServer(g.Handler())
	defer server.Close()
	resp, err := request(server.URL, testKey, "u", strings.Replace(chat, `"stream":true`, `"stream":false`, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 504 {
		t.Fatalf("timeout became %d", resp.StatusCode)
	}
}
