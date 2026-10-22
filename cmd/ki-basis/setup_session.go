package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type setupSession struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// Reopening a desktop package should open the existing UI, not run another
// agent against the same identity and race for its model process.
func reuseSetupSession(dir string) (string, bool) {
	raw, e := os.ReadFile(filepath.Join(dir, ".ui-session.json"))
	if e != nil {
		return "", false
	}
	var session setupSession
	if json.Unmarshal(raw, &session) != nil || len(session.Token) != 64 {
		return "", false
	}
	u, e := url.Parse(session.URL)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", session.URL+"/setup/status", nil)
	req.Header.Set("X-Setup-Token", session.Token)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	res, e := client.Do(req)
	if e != nil {
		return "", false
	}
	defer res.Body.Close()
	var status struct {
		Directory string `json:"data_directory"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&status) != nil || status.Directory != dir {
		return "", false
	}
	return strings.TrimRight(session.URL, "/") + "/#" + session.Token, true
}
