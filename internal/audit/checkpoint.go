package audit

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type Checkpoint struct {
	Sequence  int64  `json:"sequence"`
	Hash      string `json:"head_hash"`
	Time      string `json:"time"`
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

func signingBytes(c Checkpoint) []byte { c.Signature = ""; raw, _ := json.Marshal(c); return raw }
func SigningKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		key, e := base64.StdEncoding.DecodeString(string(raw))
		if e != nil || len(key) != ed25519.PrivateKeySize {
			return nil, errors.New("invalid audit signing key")
		}
		return ed25519.PrivateKey(key), nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, err = f.WriteString(base64.StdEncoding.EncodeToString(key))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	return key, closeErr
}
func (s *Store) Checkpoint(ctx context.Context, key ed25519.PrivateKey) (Checkpoint, error) {
	head, err := s.Head(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	c := Checkpoint{Sequence: head.Sequence, Hash: head.Hash, Time: time.Now().UTC().Format(time.RFC3339Nano), PublicKey: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))}
	c.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, signingBytes(c)))
	return c, nil
}

// The trusted public key must come from independently protected configuration,
// never from the checkpoint being verified.
func (s *Store) VerifyCheckpoint(ctx context.Context, c Checkpoint, trusted ed25519.PublicKey) error {
	sig, err := base64.StdEncoding.DecodeString(c.Signature)
	if err != nil || c.PublicKey != base64.StdEncoding.EncodeToString(trusted) || !ed25519.Verify(trusted, signingBytes(c), sig) {
		return errors.New("invalid checkpoint signature")
	}
	found := c.Sequence == 0 && c.Hash == ""
	_, err = s.Verify(ctx, func(r Record) error {
		if r.Sequence == c.Sequence && r.Hash == c.Hash {
			found = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !found {
		return errors.New("audit rollback or checkpoint mismatch")
	}
	return nil
}
func WriteCheckpoint(path string, c Checkpoint) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(raw, 10)); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
