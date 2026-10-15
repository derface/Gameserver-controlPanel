package mods

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
)

// Identifying installed jars by their hash: Modrinth tells which project and
// version a file is, and what the newest fitting version would be.

// Version is a Modrinth version of a project.
type Version struct {
	ID            string `json:"id"`
	ProjectID     string `json:"project_id"`
	VersionNumber string `json:"version_number"`
	Name          string `json:"name"`
	Files         []struct {
		Filename string            `json:"filename"`
		Hashes   map[string]string `json:"hashes"`
		Primary  bool              `json:"primary"`
	} `json:"files"`
}

func post(ctx context.Context, path string, body, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Modrinth nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return errors.New("Modrinth: zu viele Anfragen, bitte kurz warten")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Modrinth antwortet mit %s", resp.Status)
	}
	return json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 8<<20)).Decode(out)
}

// SHA1 of a file, as Modrinth indexes them.
func SHA1(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Identify returns the version for each known hash (unknown files are left out).
func Identify(ctx context.Context, hashes []string) (map[string]Version, error) {
	out := map[string]Version{}
	if len(hashes) == 0 {
		return out, nil
	}
	err := post(ctx, "/version_files", map[string]any{"hashes": hashes, "algorithm": "sha1"}, &out)
	return out, err
}

// Latest returns, per hash, the newest version of the same project for the
// loader and game version ("" = any).
func Latest(ctx context.Context, hashes []string, loader, gameVersion string) (map[string]Version, error) {
	out := map[string]Version{}
	if len(hashes) == 0 {
		return out, nil
	}
	body := map[string]any{"hashes": hashes, "algorithm": "sha1"}
	if loader != "" {
		body["loaders"] = []string{loader}
	}
	if gameVersion != "" {
		body["game_versions"] = []string{gameVersion}
	}
	err := post(ctx, "/version_files/update", body, &out)
	return out, err
}
