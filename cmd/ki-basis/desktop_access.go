package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"localaiserver/internal/catalog"
	"localaiserver/internal/config"
	"localaiserver/internal/event"
)

const desktopApplicationKeyID = "desktop-application"
const desktopApplicationFile = "application-access.json"

type desktopApplicationAccess struct {
	BaseURL string `json:"base_url"`
	User    string `json:"user"`
	Model   string `json:"model"`
	Key     string `json:"key"`
	CAPEM   string `json:"ca_pem"`
}

const desktopInitializationFile = ".desktop-initialization.json"

type desktopInitialization struct {
	Version int    `json:"version"`
	Key     string `json:"key"`
}

// A private intent record precedes configuration creation. Interrupted first
// installs can finish provisioning without treating every later restart as new.
func beginDesktopInitialization(dir string) error {
	marker := filepath.Join(dir, desktopInitializationFile)
	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return errors.New("desktop_initialization_unavailable")
	}
	central := filepath.Join(dir, "central")
	for _, path := range []string{filepath.Join(central, "config.json"), filepath.Join(central, "gateway.db")} {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return errors.New("desktop_initialization_unavailable")
		}
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return errors.New("desktop_initialization_unavailable")
	}
	raw, _ := json.Marshal(desktopInitialization{Version: 1, Key: "kb_" + hex.EncodeToString(random[:])})
	if err := writePrivate(marker, raw); err != nil {
		return errors.New("desktop_initialization_unavailable")
	}
	return nil
}

func desktopApplicationKeyMatches(keys []config.Key, key string) bool {
	hash := sha256.Sum256([]byte(key))
	for _, candidate := range keys {
		if candidate.ID == desktopApplicationKeyID && !candidate.Admin && candidate.SHA256 == hex.EncodeToString(hash[:]) && len(candidate.Models) == 1 && candidate.Models[0] == catalog.DefaultAlias {
			return true
		}
	}
	return false
}

// A durable key set outranks the intent record: a key removed after the first
// commit stays revoked even if a crash left the initialization marker behind.
func (d *desktopApp) provisionApplicationAccess(activeKeys []config.Key) error {
	marker := filepath.Join(d.dir, desktopInitializationFile)
	raw, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return errors.New("desktop_initialization_unavailable")
	}
	var pending desktopInitialization
	if json.Unmarshal(raw, &pending) != nil || pending.Version != 1 || len(pending.Key) != 67 {
		return errors.New("desktop_initialization_invalid")
	}
	if activeKeys != nil {
		if desktopApplicationKeyMatches(activeKeys, pending.Key) {
			// The credential file is written before the durable commit, so a marker
			// left after commit requires no credential rewriting or replacement.
			var saved struct {
				Key string `json:"key"`
			}
			credential, readErr := os.ReadFile(filepath.Join(d.dir, "central", desktopApplicationFile))
			if readErr != nil || json.Unmarshal(credential, &saved) != nil || saved.Key != pending.Key {
				return errors.New("desktop_application_credentials_unavailable")
			}
		}
		if err = os.Remove(marker); err != nil {
			return errors.New("desktop_initialization_unavailable")
		}
		return nil
	}
	hash := sha256.Sum256([]byte(pending.Key))
	key := config.Key{ID: desktopApplicationKeyID, SHA256: hex.EncodeToString(hash[:]), Models: []string{catalog.DefaultAlias}}
	found := false
	for _, existing := range d.config.Keys {
		if existing.ID == desktopApplicationKeyID {
			if !desktopApplicationKeyMatches([]config.Key{existing}, pending.Key) {
				return errors.New("desktop_initialization_invalid")
			}
			found = true
		}
	}
	if !found {
		d.config.Keys = append(d.config.Keys, key)
	}
	raw, _ = json.Marshal(map[string]string{"key": pending.Key})
	credentialPath := filepath.Join(d.dir, "central", desktopApplicationFile)
	if existing, readErr := os.ReadFile(credentialPath); readErr == nil {
		var saved struct {
			Key string `json:"key"`
		}
		if json.Unmarshal(existing, &saved) != nil || saved.Key != pending.Key {
			return errors.New("desktop_initialization_invalid")
		}
	} else if !os.IsNotExist(readErr) {
		return errors.New("desktop_application_credentials_unavailable")
	} else if err = writePrivate(credentialPath, raw); err != nil {
		return errors.New("desktop_application_credentials_unavailable")
	}
	raw, err = json.MarshalIndent(d.config, "", "  ")
	if err != nil {
		return errors.New("desktop_application_credentials_unavailable")
	}
	if err = writePrivate(filepath.Join(d.dir, "central", "config.json"), raw); err != nil {
		return errors.New("desktop_application_credentials_unavailable")
	}
	if err = d.store.StoreKeys(d.ctx, d.config.Keys, event.Event{Actor: "desktop-setup", KeyID: desktopApplicationKeyID}); err != nil {
		return errors.New("desktop_application_credentials_unavailable")
	}
	if err = os.Remove(marker); err != nil {
		return errors.New("desktop_initialization_unavailable")
	}
	return nil
}

func (d *desktopApp) applicationAccess(ctx context.Context) (desktopApplicationAccess, error) {
	var result desktopApplicationAccess
	raw, err := os.ReadFile(filepath.Join(d.dir, "central", desktopApplicationFile))
	if err != nil {
		return result, errors.New("desktop_application_credentials_unavailable")
	}
	var saved struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(raw, &saved) != nil || len(saved.Key) != 67 {
		return result, errors.New("desktop_application_credentials_unavailable")
	}
	keys, err := d.store.LoadKeys(ctx)
	if err != nil {
		return result, errors.New("desktop_application_credentials_unavailable")
	}
	if keys == nil {
		keys = d.config.Keys
	}
	if !desktopApplicationKeyMatches(keys, saved.Key) {
		return result, errors.New("desktop_application_credentials_unavailable")
	}
	ca, err := os.ReadFile(d.config.ControlCACert)
	if err != nil {
		return result, errors.New("desktop_application_credentials_unavailable")
	}
	if err = d.store.Append(ctx, event.Event{Type: "desktop.application_access_exported", Actor: "desktop-setup", KeyID: desktopApplicationKeyID, Outcome: "completed"}); err != nil {
		return result, errors.New("desktop_audit_unavailable")
	}
	return desktopApplicationAccess{BaseURL: d.gatewayURL + "/v1", User: "desktop-client", Model: catalog.DefaultAlias, Key: saved.Key, CAPEM: string(ca)}, nil
}
func (d *desktopApp) exportApplicationAccess(w http.ResponseWriter, r *http.Request) {
	access, err := d.applicationAccess(r.Context())
	if err != nil {
		desktopError(w, 409, err.Error())
		return
	}
	desktopJSON(w, 200, access)
}
