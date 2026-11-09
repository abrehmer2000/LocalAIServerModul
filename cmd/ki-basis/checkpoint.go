package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"localaiserver/internal/audit"
	"localaiserver/internal/config"
	"os"
	"time"
)

func checkpointCommand(args []string) error {
	flags := flag.NewFlagSet("audit-checkpoint", flag.ContinueOnError)
	cfgPath := flags.String("config", "config.local.json", "gateway configuration")
	keyPath := flags.String("signing-key", "", "private signing key file (created if missing)")
	output := flags.String("output", "", "independently protected checkpoint file")
	verify := flags.Bool("verify", false, "verify saved checkpoint instead of writing")
	trusted := flags.String("public-key", "", "trusted public key file (base64)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *output == "" {
		return errors.New("checkpoint output required")
	}
	c, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	s, err := audit.Open(c.Database)
	if err != nil {
		return err
	}
	defer s.Close()
	if *verify {
		raw, err := os.ReadFile(*output)
		if err != nil {
			return err
		}
		var checkpoint audit.Checkpoint
		if json.Unmarshal(raw, &checkpoint) != nil {
			return errors.New("invalid checkpoint file")
		}
		pub, err := os.ReadFile(*trusted)
		if err != nil {
			return err
		}
		decoded, err := base64.StdEncoding.DecodeString(string(pub))
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return errors.New("invalid trusted key")
		}
		return s.VerifyCheckpoint(context.Background(), checkpoint, ed25519.PublicKey(decoded))
	}
	if *keyPath == "" {
		return errors.New("signing key path required")
	}
	key, err := audit.SigningKey(*keyPath)
	if err != nil {
		return err
	}
	cp, err := s.Checkpoint(context.Background(), key)
	if err != nil {
		return err
	}
	return audit.WriteCheckpoint(*output, cp)
}

func runCheckpointAnchors(ctx context.Context, s *audit.Store, c config.Config, onFailure func()) error {
	if c.AuditCheckpoint == "" {
		return nil
	}
	key, err := audit.SigningKey(c.AuditSigningKey)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(c.AuditCheckpoint)
	if err != nil {
		return errors.New("initialize and protect an external checkpoint before enabling anchors")
	}
	var checkpoint audit.Checkpoint
	if json.Unmarshal(raw, &checkpoint) != nil {
		return errors.New("invalid checkpoint")
	}
	if err = s.VerifyCheckpoint(ctx, checkpoint, key.Public().(ed25519.PublicKey)); err != nil {
		return err
	}
	go func() {
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				work, cancel := context.WithTimeout(ctx, 5*time.Second)
				cp, err := s.Checkpoint(work, key)
				cancel()
				if err == nil {
					err = audit.WriteCheckpoint(c.AuditCheckpoint, cp)
				}
				if err != nil && ctx.Err() == nil {
					onFailure()
					return
				}
			}
		}
	}()
	return nil
}
