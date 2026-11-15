package webui

import "net/http"

// DesktopHandler serves the local first-run experience; API authentication is
// enforced by the desktop runner before any operation or status is exposed.
func DesktopHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			serveAgentAsset(w, r, "desktop.html", "text/html; charset=utf-8")
		case "/desktop.js":
			serveAgentAsset(w, r, "desktop.js", "text/javascript; charset=utf-8")
		case "/desktop.css":
			serveAgentAsset(w, r, "desktop.css", "text/css; charset=utf-8")
		default:
			http.NotFound(w, r)
		}
	})
}
