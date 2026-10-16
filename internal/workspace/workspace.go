// Package workspace creates DKF workspaces, fingerprints them, and reads their
// object files — everything the harness does to a workspace without an
// implementation.
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/nodelogicau/particulars-bench/internal/spec"
)

// Harness is the source.harness written into every workspace's defaults.
const Harness = "longmemeval-bench"

// Author is the source.author default: the person whose chats are replayed.
const Author = "user"

type config struct {
	Format    string `yaml:"format"`
	Workspace struct {
		ID string `yaml:"id"`
	} `yaml:"workspace"`
	Defaults struct {
		Scope  string `yaml:"scope"`
		Source struct {
			Author  string `yaml:"author"`
			Harness string `yaml:"harness"`
		} `yaml:"source"`
	} `yaml:"defaults"`
}

// Create makes dir (which must not exist or be empty) a DKF workspace by
// writing dkf.yaml per workspace-config, and returns the workspace id. No
// implementation is involved.
func Create(dir string) (id string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(entries) > 0 {
		return "", fmt.Errorf("workspace directory %s is not empty", dir)
	}
	u, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	var c config
	c.Format = spec.DKFFormat
	c.Workspace.ID = u.String()
	c.Defaults.Scope = "personal"
	c.Defaults.Source.Author = Author
	c.Defaults.Source.Harness = Harness
	b, err := yaml.Marshal(&c)
	if err != nil {
		return "", err
	}
	return c.Workspace.ID, os.WriteFile(filepath.Join(dir, "dkf.yaml"), b, 0o644)
}

// ReadConfig returns the format and id declared by dir's dkf.yaml.
func ReadConfig(dir string) (format, id string, err error) {
	b, err := os.ReadFile(filepath.Join(dir, "dkf.yaml"))
	if err != nil {
		return "", "", err
	}
	var c config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return "", "", fmt.Errorf("dkf.yaml: %w", err)
	}
	return c.Format, c.Workspace.ID, nil
}

// FileHash is one entry of a manifest.
type FileHash struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Hash lists every regular file under dir with its SHA-256, sorted by path.
func Hash(dir string) ([]FileHash, error) {
	var out []FileHash
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		sum, err := hashFile(p)
		if err != nil {
			return err
		}
		out = append(out, FileHash{Path: filepath.ToSlash(rel), SHA256: sum})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

// Matches reports whether dir holds exactly the files listed, byte for byte.
func Matches(dir string, want []FileHash) (bool, error) {
	got, err := Hash(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if len(got) != len(want) {
		return false, nil
	}
	for i := range got {
		if got[i] != want[i] {
			return false, nil
		}
	}
	return true, nil
}

// Copy copies the tree at src to dst, which must not exist.
func Copy(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// WriteJSON writes v as indented JSON to path atomically.
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
