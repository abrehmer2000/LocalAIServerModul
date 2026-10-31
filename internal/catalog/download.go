package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"localaiserver/internal/management"
	"net/http"
	"regexp"
)

const ModelFile = "gemma-4-26B_q4_0-it.gguf"

var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

func ValidModel(m management.Model) bool {
	return m.ID == DefaultAlias && m.Repository == Default().Repository && m.Filename == ModelFile && commitPattern.MatchString(m.Commit) && hashPattern.MatchString(m.SHA256) && m.Size > 0 && m.Size < 100<<30
}
func DownloadURL(m management.Model) string {
	return "https://huggingface.co/" + m.Repository + "/resolve/" + m.Commit + "/" + m.Filename
}

// Resolve pins an approved catalog entry to its immutable upstream commit and LFS hash.
func Resolve(ctx context.Context, client *http.Client) (management.Model, error) {
	m := management.Model{ID: DefaultAlias, Repository: Default().Repository, Filename: ModelFile}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://huggingface.co/api/models/"+m.Repository+"?blobs=true", nil)
	resp, err := client.Do(req)
	if err != nil {
		return m, errors.New("catalog_unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return m, errors.New("catalog_access_required")
	}
	var meta struct {
		SHA      string `json:"sha"`
		Siblings []struct {
			Filename string `json:"rfilename"`
			Size     int64  `json:"size"`
			LFS      struct {
				SHA  string `json:"sha256"`
				Size int64  `json:"size"`
			} `json:"lfs"`
		} `json:"siblings"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&meta) != nil {
		return m, errors.New("catalog_invalid")
	}
	m.Commit = meta.SHA
	for _, f := range meta.Siblings {
		if f.Filename == m.Filename {
			m.SHA256 = f.LFS.SHA
			m.Size = f.Size
			if m.Size == 0 {
				m.Size = f.LFS.Size
			}
		}
	}
	if !ValidModel(m) {
		return m, errors.New("catalog_missing_integrity")
	}
	return m, nil
}
