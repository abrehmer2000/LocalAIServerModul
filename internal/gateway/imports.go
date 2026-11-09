package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type importLeaseStore interface {
	ImportLease(context.Context, string, string, time.Duration, bool) error
}

func (g *Gateway) importLease(w http.ResponseWriter, r *http.Request) {
	e, ok := g.identify(w, r)
	if !ok {
		return
	}
	store, ok := g.audit.(importLeaseStore)
	if !ok {
		g.reject(w, e, 503, "model_unavailable", "Import coordination unavailable")
		return
	}
	id := r.PathValue("id")
	if id == "" && r.Method == "POST" {
		id = newID()
	}
	if !requestPattern.MatchString(id) {
		g.reject(w, e, 400, "invalid_request", "Invalid lease identifier")
		return
	}
	ttl := 120 * time.Second
	if r.Method != "DELETE" {
		var in struct {
			Seconds int `json:"ttl_seconds"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil || in.Seconds < 1 || in.Seconds > 300 {
			g.reject(w, e, 400, "invalid_request", "Lease duration must be 1-300 seconds")
			return
		}
		ttl = time.Duration(in.Seconds) * time.Second
	}
	if err := store.ImportLease(r.Context(), id, e.KeyID, ttl, r.Method == "DELETE"); err != nil {
		g.reject(w, e, 403, "forbidden", "Lease operation rejected")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "ttl_seconds": int(ttl / time.Second), "released": r.Method == "DELETE"})
}
