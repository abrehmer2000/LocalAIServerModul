package catalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestResolveRequiresImmutableCommitSizeAndHash(t *testing.T) {
	for _, valid := range []bool{true, false} {
		sha := strings.Repeat("a", 64)
		if !valid {
			sha = ""
		}
		payload, _ := json.Marshal(map[string]any{"sha": strings.Repeat("b", 40), "siblings": []any{map[string]any{"rfilename": ModelFile, "size": 123, "lfs": map[string]any{"sha256": sha}}}})
		c := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(payload)))}, nil
		})}
		m, err := Resolve(context.Background(), c)
		if valid && (err != nil || !ValidModel(m)) {
			t.Fatal("valid metadata rejected", err)
		}
		if !valid && err == nil {
			t.Fatal("unverifiable model accepted")
		}
	}
}
