package gateway

import (
	"context"
	"encoding/json"
	"io"
	"localaiserver/internal/config"
	"localaiserver/internal/event"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
)

var userPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`)
var requestPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,32}$`)

// StrictPaths must wrap the outermost ServeMux: ServeMux redirects before
// dispatching handlers, including when it is only used for management routes.
func StrictPaths(next http.Handler, rejects ...func(http.ResponseWriter, *http.Request)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path.Clean(r.URL.Path) != r.URL.Path || strings.Contains(r.URL.EscapedPath(), "%") {
			if len(rejects) > 0 {
				rejects[0](w, r)
			} else {
				apiError(w, 400, "invalid_request", "Non-canonical path")
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

type keyStore struct {
	sync.RWMutex
	keys []config.Key
}

func (g *Gateway) keySnapshot() []config.Key {
	g.keys.RLock()
	defer g.keys.RUnlock()
	return append([]config.Key(nil), g.keys.keys...)
}
func (g *Gateway) allowed(key, model string) bool {
	for _, k := range g.keySnapshot() {
		if k.ID == key {
			return k.Allows(model)
		}
	}
	return false
}

// ReplaceKeys validates first, audits intent, then atomically publishes the
// complete set. Existing requests keep their already authorized reservation.
type persistentKeys interface {
	LoadKeys(context.Context) ([]config.Key, error)
	StoreKeys(context.Context, []config.Key, event.Event) error
}

func (g *Gateway) ReplaceKeys(keys []config.Key, e event.Event) error {
	g.keyMutation.Lock()
	defer g.keyMutation.Unlock()
	c := g.cfg
	c.Keys = keys
	if err := c.Validate(); err != nil {
		return err
	}
	e.Type = "keys.reloaded"
	e.Outcome = "completed"
	if store, ok := g.audit.(persistentKeys); ok {
		if err := store.StoreKeys(context.Background(), keys, e); err != nil {
			return err
		}
	} else if err := g.append(e); err != nil {
		return err
	}
	g.keys.Lock()
	g.keys.keys = append([]config.Key(nil), keys...)
	g.keys.Unlock()
	return nil
}
func (g *Gateway) keyAdmin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" {
		apiError(w, 405, "invalid_request", "PUT required")
		return
	}
	var body struct {
		Keys []config.Key `json:"keys"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil || d.Decode(new(any)) != io.EOF || len(body.Keys) == 0 {
		apiError(w, 400, "invalid_request", "Invalid key set")
		return
	}
	if err := g.ReplaceKeys(body.Keys, event.FromContext(r.Context())); err != nil {
		apiError(w, 400, "invalid_request", "Key replacement rejected")
		return
	}
	writeJSON(w, 200, map[string]bool{"reloaded": true})
}

func (g *Gateway) ingress(next http.Handler) http.Handler {
	return g.GuardPaths(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		select {
		case g.ingressSlots <- struct{}{}:
			defer func() { <-g.ingressSlots }()
		default:
			g.EarlyReject(w, r, 503, "model_unavailable", "Gateway request concurrency limit reached")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (g *Gateway) EarlyReject(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	e, ok := g.identify(w, r)
	if ok {
		g.reject(w, e, status, code, message)
	}
}
func (g *Gateway) GuardPaths(next http.Handler) http.Handler {
	return StrictPaths(next, func(w http.ResponseWriter, r *http.Request) {
		g.EarlyReject(w, r, 400, "invalid_request", "Non-canonical path")
	})
}
