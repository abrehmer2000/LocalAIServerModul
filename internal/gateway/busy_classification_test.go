package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOnlyExplicitUpstreamBusyIsModelBusy(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"error":{"code":"model_busy"}}`, "model_busy"},
		{`{"error":{"message":"rate limit"}}`, "upstream_failure"},
	} {
		_, code := classifyUpstream(&http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(tc.body))})
		if code != tc.want {
			t.Fatalf("code=%s want %s", code, tc.want)
		}
	}
}

func TestIngressLimitDoesNotClaimModelBusy(t *testing.T) {
	g, err := New(cfg(node("n", "http://127.0.0.1:1")), &memoryAudit{})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for i := 0; i < cap(g.ingressSlots); i++ {
		g.ingressSlots <- struct{}{}
	}
	defer func() {
		for len(g.ingressSlots) > 0 {
			<-g.ingressSlots
		}
	}()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+testKey)
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), `"model_busy"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing request identity")
	}
}
