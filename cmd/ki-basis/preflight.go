package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"localaiserver/internal/agent"
	"localaiserver/internal/control"
)

type setupCheck struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message"`
}
type setupInspection struct {
	Checks []setupCheck `json:"checks"`
	Ready  bool         `json:"ready"`
	OS     string       `json:"os"`
	Arch   string       `json:"arch"`
	CPUs   int          `json:"cpus"`
}

// resolveSettings shares defaults between inspection and enrollment; empty fields
// never erase detected defaults. No directory or executable is created by a probe.
func resolveSettings(next, defaults agentSettings) agentSettings {
	if next.Binary == "" {
		next.Binary = defaults.Binary
	}
	if next.Engine == "" {
		next.Engine = defaults.Engine
	}
	if next.Models == "" {
		next.Models = defaults.Models
	}
	return next
}
func inspectSetup(ctx context.Context, settings agentSettings, bundle *control.Bundle, registered bool) setupInspection {
	out := setupInspection{Ready: true, OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU()}
	add := func(id, state, message string) {
		out.Checks = append(out.Checks, setupCheck{id, state, message})
		if state == "error" {
			out.Ready = false
		}
	}
	add("system", "ok", runtime.GOOS+" / "+runtime.GOARCH)
	if settings.Binary == "" || !filepath.IsAbs(settings.Binary) {
		add("runtime", "error", "Keine Laufzeit gefunden. llama.cpp-Paket importieren oder unter Erweitert auswählen.")
	} else {
		info, e := os.Stat(settings.Binary)
		if e != nil || !info.Mode().IsRegular() {
			add("runtime", "error", "Die ausgewählte Laufzeit ist nicht vorhanden.")
		} else {
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			command := exec.CommandContext(c, settings.Binary, "--version")
			command.Env = agent.RuntimeEnvironment(settings.Binary)
			// Version probes never expose raw runtime output in the setup API.
			e = command.Run()
			cancel()
			if e != nil {
				add("runtime", "error", "Die Laufzeit startet nicht. Paket für Betriebssystem, Architektur und Treiber prüfen.")
			} else {
				add("runtime", "ok", "llama-server kann gestartet werden.")
			}
		}
	}
	if agent.ValidateEngineURL(settings.Engine) != nil {
		add("port", "error", "Die lokale Modelladresse muss eine HTTP-Loopback-Adresse sein.")
	} else if !registered {
		u, _ := url.Parse(settings.Engine)
		address := u.Host
		if u.Port() == "" {
			address = net.JoinHostPort(u.Hostname(), "80")
		}
		listener, e := net.Listen("tcp", address)
		if e != nil {
			add("port", "error", "Der Modellserver-Port ist belegt. Unter Erweitert eine freie Adresse wählen.")
		} else {
			listener.Close()
			add("port", "ok", "Modellserver-Port ist frei.")
		}
	} else {
		add("port", "ok", "Adresse des angemeldeten Modellservers übernommen.")
	}
	if !filepath.IsAbs(settings.Models) {
		add("storage", "error", "Ein absoluter Speicherpfad ist erforderlich.")
	} else {
		parent := settings.Models
		for {
			info, e := os.Stat(parent)
			if e == nil {
				if !info.IsDir() {
					add("storage", "error", "Der Speicherpfad führt durch eine Datei.")
				} else {
					add("storage", "ok", "Speicherverzeichnis: "+settings.Models)
					gpu, vram, free := agent.HostResources(parent)
					if gpu != "" {
						add("gpu", "ok", fmt.Sprintf("Erkannt: %s (%.1f GB VRAM). Beschleunigung wird erst beim Modellstart bestätigt.", gpu, float64(vram)/1e9))
					} else {
						add("gpu", "warning", "Keine NVIDIA-GPU erkannt. CPU-Betrieb oder eine andere passende llama.cpp-Laufzeit verwenden.")
					}
					if free > 0 {
						if settings.ExistingModel == "" && free < 14700000000 {
							add("space", "error", fmt.Sprintf("Nur %.1f GB frei. Für das Standardmodell mindestens etwa 14,7 GB Speicher bereitstellen.", float64(free)/1e9))
						} else {
							add("space", "ok", fmt.Sprintf("%.1f GB freier Speicher.", float64(free)/1e9))
						}
					}
					probe, e := os.CreateTemp(parent, ".ki-write-check-")
					if e != nil {
						add("writable", "error", "Das Speicherverzeichnis ist nicht beschreibbar.")
					} else {
						probe.Close()
						os.Remove(probe.Name())
					}
				}
				break
			}
			if !os.IsNotExist(e) || filepath.Dir(parent) == parent {
				add("storage", "error", "Speicherverzeichnis ist nicht zugänglich.")
				break
			}
			parent = filepath.Dir(parent)
		}
	}
	if validateExistingModel(settings.ExistingModel) != nil {
		add("model", "error", "Die vorhandene Modelldatei wurde nicht gefunden.")
	}
	if _, e := os.Stat("/.dockerenv"); e == nil {
		add("persistence", "warning", "Container erkannt: Vor dem Abschalten dauerhaften Speicher oder eine Sicherung vorsehen.")
	}
	if bundle != nil {
		expiry, e := time.Parse(time.RFC3339, bundle.Expires)
		if bundle.Version != 1 || e != nil || !time.Now().Before(expiry) {
			add("connection", "error", "Die Verbindungsdatei ist ungültig oder abgelaufen. In der Zentrale neu erstellen.")
			return out
		}
		u, e := url.Parse(bundle.Gateway)
		roots := x509.NewCertPool()
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || !roots.AppendCertsFromPEM([]byte(bundle.CA)) {
			add("connection", "error", "Gateway-Adresse oder Zertifikat ungültig.")
			return out
		}
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		tr := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
		defer tr.CloseIdleConnections()
		req, _ := http.NewRequestWithContext(c, "GET", bundle.Gateway+"/healthz", nil)
		client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, e := client.Do(req)
		if e != nil {
			add("connection", "error", "Zentrale nicht sicher erreichbar. Adresse, Zertifikat und Netzwerkzugang prüfen.")
		} else {
			defer res.Body.Close()
			var health struct {
				Status string `json:"status"`
			}
			if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&health) != nil || health.Status != "ok" {
				add("connection", "error", "Die Adresse antwortet nicht als KI-Basis-Gateway.")
			} else {
				add("connection", "ok", "Zentrale erreichbar; Zertifikat geprüft.")
			}
		}
		if strings.EqualFold(u.Hostname(), "localhost") || net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback() {
			add("network", "warning", "Diese Adresse gilt auf dem Agent-Rechner. Bei getrennten Rechnern ist dafür ein bestehender Tunnel erforderlich.")
		}
	}
	return out
}
