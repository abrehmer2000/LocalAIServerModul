package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"localaiserver/internal/audit"
	"localaiserver/internal/config"
	"os"
	"path/filepath"
)

// admin-key is an explicit local operator action; existing application keys are never elevated.
func adminKeyCommand(args []string) error {
	flags := flag.NewFlagSet("admin-key", flag.ContinueOnError)
	path := flags.String("config", "config.local.json", "gateway configuration")
	out := flags.String("out", "", "new private credential JSON file (must not exist)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("--out required; new credentials are never printed")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	key := "kb_" + hex.EncodeToString(random[:])
	hash := sha256.Sum256([]byte(key))
	id := "admin-" + hex.EncodeToString(random[:4])
	cfg.Keys = append(cfg.Keys, config.Key{ID: id, SHA256: hex.EncodeToString(hash[:]), Admin: true})
	if err = cfg.Validate(); err != nil {
		return err
	}
	output, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer output.Close()
	if err = json.NewEncoder(output).Encode(map[string]string{"key": key, "id": id}); err != nil {
		return err
	}
	if err = output.Sync(); err != nil {
		return err
	}
	store, err := audit.Open(cfg.Database)
	if err != nil {
		return err
	}
	defer store.Close()
	if err = store.Append(context.Background(), audit.Event{Type: "admin.key_created", Actor: actor(), KeyID: id, Outcome: "configuration_update_requested"}); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	tmp, err := os.CreateTemp(filepath.Dir(*path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp.Name(), *path); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Administratorzugang gespeichert. Gateway neu starten. Zugangsdaten bleiben in der angegebenen privaten Datei.")
	return nil
}
