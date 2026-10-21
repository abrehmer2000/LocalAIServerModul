package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"localaiserver/internal/catalog"
	"localaiserver/internal/config"
	"localaiserver/internal/event"
	"localaiserver/internal/management"
	"net/http"
	"net/url"
	"time"
)

// Bundle contains a short-lived enrollment secret, never an API key or CA private key.
type Bundle struct {
	Version int    `json:"version"`
	Gateway string `json:"gateway"`
	CA      string `json:"ca"`
	Token   string `json:"token"`
	Expires string `json:"expires_at"`
	NodeID  string `json:"node_id"`
}

func randomID(prefix string) string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b[:])
}

// AdminHandler must be wrapped in gateway.Admin; it never authenticates ordinary API keys itself.
func (s *Service) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/overview", s.overview)
	mux.HandleFunc("POST /admin/enroll", s.enroll)
	mux.HandleFunc("POST /admin/nodes/{id}/test", s.testNode)
	mux.HandleFunc("POST /admin/nodes/{id}/action", s.action)
	mux.HandleFunc("POST /admin/nodes/{id}/revoke", s.revoke)
	mux.HandleFunc("GET /admin/audit", s.recentAudit)
	mux.HandleFunc("GET /admin/backup", s.backup)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if event.FromContext(r.Context()).Actor == "" {
			deny(w, 403)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Service) overview(w http.ResponseWriter, r *http.Request) {
	devices, err := s.store.Devices(r.Context())
	if err != nil {
		deny(w, 503)
		return
	}
	type device struct {
		ID         string            `json:"id"`
		Model      string            `json:"model"`
		Revision   string            `json:"revision"`
		Connection string            `json:"connection"`
		Revoked    bool              `json:"revoked"`
		Report     management.Report `json:"report"`
		Plan       *management.Plan  `json:"plan,omitempty"`
	}
	out := make([]device, 0, len(devices))
	s.mu.Lock()
	reports := make(map[string]management.Report, len(s.reports))
	for k, v := range s.reports {
		reports[k] = v
	}
	s.mu.Unlock()
	for _, d := range devices {
		p, _ := s.store.Plan(r.Context(), d.Node.ID)
		out = append(out, device{ID: d.Node.ID, Model: d.Node.Model, Revision: d.Node.Revision, Connection: d.Node.Connection, Revoked: d.Revoked, Report: reports[d.Node.ID], Plan: p})
	}
	e := event.FromContext(r.Context())
	e.Type = "admin.overview_viewed"
	e.Status = 200
	if s.store.Append(r.Context(), e) != nil {
		deny(w, 503)
		return
	}
	respond(w, 200, map[string]any{"devices": out, "nodes": s.pool.Snapshot(), "catalog": []any{catalog.Default()}})
}
func (s *Service) enroll(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID       string `json:"id"`
		Gateway  string `json:"gateway"`
		Model    string `json:"model"`
		Revision string `json:"revision"`
		Capacity int    `json:"capacity"`
	}
	if decode(w, r, &in) != nil {
		deny(w, 400)
		return
	}
	u, err := url.Parse(in.Gateway)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		deny(w, 400)
		return
	}
	// Enrollment files may only name the gateway certificate's actual host.
	if s.gatewayCert.VerifyHostname(u.Hostname()) != nil {
		respond(w, 400, map[string]string{"error": "gateway_certificate_host_mismatch"})
		return
	}
	if in.Model == "" {
		in.Model = catalog.DefaultAlias
	}
	if in.Revision == "" {
		in.Revision = "catalog-gemma4-q4_0-v1"
	}
	if in.Capacity == 0 {
		in.Capacity = 1
	}
	n := config.Node{ID: in.ID, URL: "https://agent.invalid", Model: in.Model, UpstreamModel: in.Model, Revision: in.Revision, Capacity: in.Capacity, Connection: "outbound"}
	if config.ValidateNode(n) != nil {
		deny(w, 400)
		return
	}
	if s.pool.CheckDescriptor(n) != nil {
		respond(w, 409, map[string]string{"error": "node_or_revision_conflict"})
		return
	}
	token := randomID("jt_")
	expiry := time.Now().Add(10 * time.Minute)
	e := event.FromContext(r.Context())
	if s.store.IssueJoin(r.Context(), TokenHash(token), n, expiry, e.Actor) != nil {
		respond(w, 409, map[string]string{"error": "enrollment_not_created"})
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="connection.json"`)
	respond(w, 201, Bundle{1, in.Gateway, string(s.authority.PEM), token, expiry.UTC().Format(time.RFC3339), n.ID})
}
func (s *Service) action(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
		GPU    string `json:"gpu"`
	}
	if decode(w, r, &in) != nil || (in.Action != "download" && in.Action != "start" && in.Action != "stop") || (in.GPU != "" && in.GPU != "auto" && in.GPU != "cpu") {
		deny(w, 400)
		return
	}
	id := r.PathValue("id")
	devices, err := s.store.Devices(r.Context())
	if err != nil {
		deny(w, 503)
		return
	}
	var n config.Node
	for _, d := range devices {
		if d.Node.ID == id && !d.Revoked {
			n = d.Node
		}
	}
	if n.ID == "" {
		deny(w, 404)
		return
	}
	if n.Connection != "outbound" {
		respond(w, 409, map[string]string{"error": "outbound_agent_required"})
		return
	}
	p := management.Plan{Generation: randomID("op_"), Action: in.Action, GPU: in.GPU}
	if p.GPU == "" {
		p.GPU = "auto"
	}
	if in.Action != "stop" {
		if n.Model != catalog.DefaultAlias {
			respond(w, 409, map[string]string{"error": "model_not_in_catalog"})
			return
		}
		m, err := s.store.PinnedModel(r.Context(), n.Model)
		if err != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			m, err = catalog.Resolve(ctx, &http.Client{Timeout: 20 * time.Second})
			if err != nil {
				respond(w, 502, map[string]string{"error": err.Error()})
				return
			}
			m, err = s.store.PinModel(r.Context(), m)
			if err != nil {
				deny(w, 503)
				return
			}
		}
		if n.ModelSHA256 != "" && n.ModelSHA256 != m.SHA256 {
			respond(w, 409, map[string]string{"error": "model_hash_conflict"})
			return
		}
		p.Model = m
	}
	e := event.FromContext(r.Context())
	if s.store.SetPlan(r.Context(), id, p, e.Actor, e.KeyID, e.UserID) != nil {
		deny(w, 503)
		return
	}
	respond(w, 202, p)
}
func (s *Service) revoke(w http.ResponseWriter, r *http.Request) {
	e := event.FromContext(r.Context())
	id := r.PathValue("id")
	if s.store.Revoke(r.Context(), id, e.Actor) != nil {
		deny(w, 409)
		return
	}
	s.broker.CancelNode(id)
	s.pool.Heartbeat(id, false, 0)
	respond(w, 200, map[string]bool{"revoked": true})
}
func (s *Service) recentAudit(w http.ResponseWriter, r *http.Request) {
	e := event.FromContext(r.Context())
	e.Type = "admin.audit_viewed"
	e.Status = 200
	if s.store.Append(r.Context(), e) != nil {
		deny(w, 503)
		return
	}
	records, err := s.store.Recent(r.Context(), 100)
	if err != nil {
		deny(w, 503)
		return
	}
	respond(w, 200, records)
}

// ValidateReport accepts bounded metadata only; arbitrary engine text is never stored.
func validReport(r management.Report) bool {
	states := map[string]bool{"": true, "idle": true, "external": true, "downloading": true, "verifying": true, "downloaded": true, "starting": true, "running": true, "stopped": true, "error": true}
	errors := map[string]bool{"": true, "download_failed": true, "disk_full": true, "hash_mismatch": true, "engine_missing": true, "engine_failed": true, "external_engine_running": true, "invalid_model": true, "model_not_downloaded": true}
	return states[r.State] && errors[r.Error] && len(r.GPU) <= 512 && r.VRAMBytes <= 1<<50 && r.FreeBytes <= 1<<60 && len(r.OS) < 32 && len(r.Arch) < 32 && r.CPUs >= 0 && r.CPUs <= 65536 && len(r.Generation) < 128 && r.Downloaded >= 0 && r.Total >= 0 && (r.SHA256 == "" || len(r.SHA256) == 64)
}
