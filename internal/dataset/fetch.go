// Package dataset fetches, verifies, parses, and samples LongMemEval.
package dataset

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// File is one pinned dataset file.
type File struct {
	Name   string
	SHA256 string
	URL    string
}

// Repository and Revision pin the Hugging Face dataset the files come from:
// the cleaned release of LongMemEval (MIT), at an exact commit.
const (
	Repository = "xiaowu0162/longmemeval-cleaned"
	Revision   = "98d7416c24c778c2fee6e6f3006e7a073259d48f"
)

func hf(name string) string {
	return "https://huggingface.co/datasets/" + Repository + "/resolve/" + Revision + "/" + name
}

// Variants maps a variant name to its pinned file. The SHA-256s are the
// Git LFS object ids published for Revision.
var Variants = map[string]File{
	"oracle": {
		Name:   "longmemeval_oracle.json",
		SHA256: "821a2034d219ab45846873dd14c14f12cfe7776e73527a483f9dac095d38620c",
		URL:    hf("longmemeval_oracle.json"),
	},
	"s": {
		Name:   "longmemeval_s_cleaned.json",
		SHA256: "d6f21ea9d60a0d56f34a05b609c79c88a451d2ae03597821ea3d5a9678c3a442",
		URL:    hf("longmemeval_s_cleaned.json"),
	},
}

// VariantNames lists the known variants, sorted.
func VariantNames() []string {
	names := make([]string, 0, len(Variants))
	for n := range Variants {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// CacheDir is the root of everything the harness keeps between runs:
// PARTICULARS_BENCH_CACHE if set, else <user cache dir>/particulars-bench.
func CacheDir() (string, error) {
	if d := os.Getenv("PARTICULARS_BENCH_CACHE"); d != "" {
		return d, nil
	}
	d, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "particulars-bench"), nil
}

// ChecksumError reports a dataset file that does not match its pinned hash.
type ChecksumError struct {
	Path, Expected, Actual string
}

func (e *ChecksumError) Error() string {
	return fmt.Sprintf("%s: checksum mismatch: expected sha256 %s, got %s (delete the file to download it again)", e.Path, e.Expected, e.Actual)
}

// Ensure returns the path of the variant's file in the cache, downloading it
// on first use, and verifies it against the pinned checksum.
func Ensure(variant string, log io.Writer) (path string, f File, err error) {
	f, ok := Variants[variant]
	if !ok {
		return "", f, fmt.Errorf("unknown dataset variant %q (known: %v)", variant, VariantNames())
	}
	root, err := CacheDir()
	if err != nil {
		return "", f, err
	}
	dir := filepath.Join(root, "longmemeval", "data")
	path = filepath.Join(dir, f.Name)
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		if err := download(f, dir, path, log); err != nil {
			return "", f, err
		}
	}
	sum, err := fileSHA256(path)
	if err != nil {
		return "", f, err
	}
	if sum != f.SHA256 {
		return "", f, &ChecksumError{Path: path, Expected: f.SHA256, Actual: sum}
	}
	return path, f, nil
}

func download(f File, dir, path string, log io.Writer) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	fmt.Fprintf(log, "downloading %s to %s\n", f.URL, path)
	resp, err := http.Get(f.URL)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: %s", f.URL, resp.Status)
	}
	tmp, err := os.CreateTemp(dir, f.Name+".*.part")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != f.SHA256 {
		return &ChecksumError{Path: f.URL, Expected: f.SHA256, Actual: sum}
	}
	return os.Rename(tmp.Name(), path)
}

func fileSHA256(path string) (string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = fh.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
