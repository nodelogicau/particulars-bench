package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Object is the part of a DKF object file the harness reads.
type Object struct {
	ID        string `yaml:"id"`
	Type      string `yaml:"type"`
	Subject   string `yaml:"subject"`
	Timestamp string `yaml:"timestamp"`
	Context   struct {
		Topics []string `yaml:"topics"`
	} `yaml:"context"`
	Retracted any `yaml:"retracted"`
}

// Time parses the object's timestamp; ok is false when it is absent or not
// RFC 3339.
func (o Object) Time() (t time.Time, ok bool) {
	t, err := time.Parse(time.RFC3339, o.Timestamp)
	return t, err == nil
}

// Objects reads every object file of one type directory ("claims",
// "particulars", "syntheses") laid out as dkf.yaml's format declares
// (workspace-layout): flat for dkf/0.1, sharded by month (and legacy/) for
// dkf/0.2. The layout is never inferred from the directory contents.
func Objects(dir, typeDir string) ([]Object, error) {
	format, _, err := ReadConfig(dir)
	if err != nil {
		return nil, err
	}
	var pattern string
	switch format {
	case "dkf/0.1":
		pattern = filepath.Join(dir, typeDir, "*.yaml")
	case "dkf/0.2":
		pattern = filepath.Join(dir, typeDir, "*", "*.yaml")
	default:
		return nil, fmt.Errorf("workspace %s: unsupported format %q", dir, format)
	}
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var out []Object
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var o Object
		if err := yaml.Unmarshal(b, &o); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if o.ID == "" {
			o.ID = strings.TrimSuffix(filepath.Base(p), ".yaml")
		}
		out = append(out, o)
	}
	return out, nil
}

// ObjectIDs returns the ids of one type directory's objects as a set.
func ObjectIDs(dir, typeDir string) (map[string]bool, error) {
	objs, err := Objects(dir, typeDir)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool, len(objs))
	for _, o := range objs {
		ids[o.ID] = true
	}
	return ids, nil
}

// Metrics summarises what an ingest wrote.
type Metrics struct {
	Claims          int `json:"claims"`
	RetractedClaims int `json:"retracted_claims"`
	Particulars     int `json:"particulars"`
	Topics          int `json:"topics"`
	Syntheses       int `json:"syntheses"`
	// OnSessionDate is the fraction of claims whose timestamp falls on the
	// date (UTC) of the session they were asserted in; nil when no claim
	// could be attributed to a session.
	OnSessionDate *float64 `json:"on_session_date"`
}

// ComputeMetrics reads the workspace's object files. claimSession maps a
// claim id to the date (YYYY-MM-DD, UTC) of the session during which it
// was written.
func ComputeMetrics(dir string, claimSession map[string]string) (Metrics, error) {
	var m Metrics
	claims, err := Objects(dir, "claims")
	if err != nil {
		return m, err
	}
	pars, err := Objects(dir, "particulars")
	if err != nil {
		return m, err
	}
	syns, err := Objects(dir, "syntheses")
	if err != nil {
		return m, err
	}
	m.Claims, m.Particulars, m.Syntheses = len(claims), len(pars), len(syns)
	topics := map[string]bool{}
	attributed, onDate := 0, 0
	for _, c := range claims {
		if c.Retracted != nil {
			m.RetractedClaims++
		}
		for _, t := range c.Context.Topics {
			topics[t] = true
		}
		day, ok := claimSession[c.ID]
		if !ok {
			continue
		}
		attributed++
		if t, ok := c.Time(); ok && t.UTC().Format(time.DateOnly) == day {
			onDate++
		}
	}
	for _, s := range syns {
		for _, t := range s.Context.Topics {
			topics[t] = true
		}
	}
	m.Topics = len(topics)
	if attributed > 0 {
		f := float64(onDate) / float64(attributed)
		m.OnSessionDate = &f
	}
	return m, nil
}
