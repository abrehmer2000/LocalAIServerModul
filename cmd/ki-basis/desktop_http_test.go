package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestDesktopTransportErrorsRemainFixedBrowserCodes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		want  string
	}{
		{"timeout", context.DeadlineExceeded, "desktop_operation_timed_out"},
		{"cancellation", context.Canceled, "desktop_operation_cancelled"},
		{"connection", errors.New("private diagnostic"), "desktop_gateway_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := desktopFixture(t, false)
			originalClient := app.client
			app.client = &http.Client{Transport: runtimeRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, &url.Error{Op: "Post", URL: "https://private-host.example/control?secret=private-value", Err: tc.cause}
			})}
			t.Cleanup(func() { app.client = originalClient })
			if !app.begin("testing", func(ctx context.Context) error {
				return app.call(ctx, "/v1/chat/completions", map[string]string{}, new(any))
			}) {
				t.Fatal("operation not accepted")
			}
			app.workers.Wait()
			response := desktopRequest(t, app, "GET", "/desktop/status", app.token, app.address)
			defer response.Body.Close()
			var status struct {
				Error string `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
				t.Fatal(err)
			}
			if status.Error != tc.want {
				t.Fatalf("browser error = %q; want %q", status.Error, tc.want)
			}
			if strings.Contains(status.Error, "private") {
				t.Fatal("transport diagnostic crossed browser boundary")
			}
		})
	}
}
