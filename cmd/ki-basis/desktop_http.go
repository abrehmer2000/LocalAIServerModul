package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"localaiserver/internal/webui"
)

func desktopJSON(w http.ResponseWriter, status int, value any) { writeLocalJSON(w, status, value) }
func desktopError(w http.ResponseWriter, status int, code string) {
	desktopJSON(w, status, map[string]string{"error": code})
}
func desktopDecode(w http.ResponseWriter, r *http.Request, value any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		desktopError(w, 415, "desktop_json_required")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		desktopError(w, 400, "desktop_invalid_request")
		return false
	}
	return true
}
func (d *desktopApp) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /desktop/session", func(w http.ResponseWriter, r *http.Request) {
		// Only the launch capability can establish a cookie. Existing cookies cannot
		// bootstrap a fresh session or replace another browser's authentication.
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Desktop-Token")), []byte(d.token)) != 1 {
			desktopError(w, 401, "desktop_session_required")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: d.cookieName, Value: d.token, Path: "/desktop/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		desktopJSON(w, 200, map[string]bool{"authenticated": true})
	})
	mux.HandleFunc("POST /desktop/access", d.exportApplicationAccess)
	mux.HandleFunc("GET /desktop/status", func(w http.ResponseWriter, r *http.Request) { desktopJSON(w, 200, d.status()) })
	mux.HandleFunc("POST /desktop/prepare", func(w http.ResponseWriter, r *http.Request) {
		if !d.begin("runtime", d.prepare) {
			desktopError(w, 409, "desktop_operation_in_progress")
			return
		}
		desktopJSON(w, 202, map[string]bool{"accepted": true})
	})
	mux.HandleFunc("POST /desktop/model", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action string `json:"action"`
		}
		if !desktopDecode(w, r, &in) {
			return
		}
		if in.Action == "" {
			in.Action = "start"
		}
		if in.Action != "start" && in.Action != "download" && in.Action != "stop" {
			desktopError(w, 400, "desktop_invalid_action")
			return
		}
		if !d.begin("model", func(ctx context.Context) error { return d.modelAction(ctx, in.Action) }) {
			desktopError(w, 409, "desktop_operation_in_progress")
			return
		}
		desktopJSON(w, 202, map[string]bool{"accepted": true})
	})
	mux.HandleFunc("POST /desktop/test", func(w http.ResponseWriter, r *http.Request) {
		if !d.begin("testing", d.testAnswer) {
			desktopError(w, 409, "desktop_operation_in_progress")
			return
		}
		desktopJSON(w, 202, map[string]bool{"accepted": true})
	})
	mux.HandleFunc("POST /desktop/cancel", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		cancel := d.operationCancel
		d.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		ctx, stop := context.WithTimeout(r.Context(), 5*time.Second)
		defer stop()
		d.agent.mu.Lock()
		connected := d.agent.runner != nil
		d.agent.mu.Unlock()
		if connected {
			if err := d.modelAction(ctx, "stop"); err != nil {
				desktopError(w, 502, "desktop_model_stop_failed")
				return
			}
		}
		desktopJSON(w, 200, map[string]bool{"cancelled": true})
	})
	mux.HandleFunc("POST /desktop/quit", func(w http.ResponseWriter, r *http.Request) {
		if d.service {
			desktopError(w, 409, "desktop_stop_service_with_system_manager")
			return
		}
		desktopJSON(w, 200, map[string]bool{"stopping": true})
		go func() { time.Sleep(100 * time.Millisecond); d.cancel() }()
	})
	mux.Handle("/", webui.DesktopHandler())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		if r.Host != strings.TrimPrefix(d.address, "http://") {
			desktopError(w, 403, "desktop_host_rejected")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != d.address {
			desktopError(w, 403, "desktop_origin_rejected")
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
			desktopError(w, 403, "desktop_origin_rejected")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/desktop/") {
			supplied := r.Header.Get("X-Desktop-Token")
			if supplied == "" {
				if cookie, err := r.Cookie(d.cookieName); err == nil {
					supplied = cookie.Value
				}
			}
			if subtle.ConstantTimeCompare([]byte(supplied), []byte(d.token)) != 1 {
				desktopError(w, 401, "desktop_session_required")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func desktopOperationError(err error, fallback string) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return errors.New(fallback)
}
