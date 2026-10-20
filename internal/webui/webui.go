// Package webui serves a dependency-free status interface. Credentials stay in browser memory.
package webui

import (
	"embed"
	"encoding/json"
	"localaiserver/internal/catalog"
	"net/http"
)

//go:embed assets/*
var assets embed.FS

func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; form-action 'none'")
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "method not allowed", 405)
			return
		}
		if r.URL.Path == "/model-profile" {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == "GET" {
				_ = json.NewEncoder(w).Encode(catalog.Default())
			}
			return
		}
		file := ""
		contentType := ""
		switch r.URL.Path {
		case "/":
			file = "index.html"
			contentType = "text/html; charset=utf-8"
		case "/app.js":
			file = "app.js"
			contentType = "text/javascript; charset=utf-8"
		case "/style.css":
			file = "style.css"
			contentType = "text/css; charset=utf-8"
		default:
			http.NotFound(w, r)
			return
		}
		data, err := assets.ReadFile("assets/" + file)
		if err != nil {
			http.Error(w, "asset unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", contentType)
		if r.Method == "GET" {
			_, _ = w.Write(data)
		}
	})
}
