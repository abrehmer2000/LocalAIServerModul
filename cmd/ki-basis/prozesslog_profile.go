package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"localaiserver/internal/config"
	"os"
	"path/filepath"
)

// Write a reviewable replacement; never modify an existing installation in place.
func prozesslogProfileCommand(args []string) error {
	flags := flag.NewFlagSet("prozesslog-profile", flag.ContinueOnError)
	source := flags.String("config", "config.local.json", "existing gateway configuration")
	output := flags.String("output", "config.prozesslog.local.json", "new configuration (must not exist)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	c, err := config.Load(*source)
	if err != nil {
		return err
	}
	if c.TimeoutSeconds < config.DefaultTimeoutSeconds {
		c.TimeoutSeconds = config.DefaultTimeoutSeconds
	}
	if c.TimeoutSeconds < c.MinimumTimeout() {
		c.TimeoutSeconds = c.MinimumTimeout()
	}
	if c.MaxBodyBytes < config.MaxRequestBytes {
		c.MaxBodyBytes = config.MaxRequestBytes
	}
	if c.MaxOutputTokens < config.DefaultOutputTokens {
		c.MaxOutputTokens = config.DefaultOutputTokens
	}
	for i := range c.Nodes {
		c.Nodes[i].EngineProtocol = "llama.cpp"
		c.Nodes[i].MaxBodyBytes = c.MaxBodyBytes
	}
	if err = c.Validate(); err != nil {
		return err
	}
	if filepath.Clean(*output) == filepath.Clean(*source) {
		return errors.New("output must differ from source")
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("cannot create profile; output must not exist")
	}
	_, err = f.Write(append(data, '\n'))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Fprintln(os.Stdout, "Prozesslog-Profil erstellt. Vor dem Neustart prüfen: Engine-Kontext je Slot, reale Slotzahl, Vision-Projektor und Proxy-Zeitlimit. Knoten-Kapazitäten bleiben unverändert.")
	return nil
}
