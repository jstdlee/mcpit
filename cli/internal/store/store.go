// Package store keeps sitepacks and the decision log on the local machine.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jstdlee/mcpit/cli/internal/sitepack"
)

type Store struct {
	Dir string
	mu  sync.Mutex
}

var ErrNotFound = errors.New("no sitepack for this site in the local store")

// Default returns the store under $MCPIT_HOME or the OS data directory.
func Default() (*Store, error) {
	dir := os.Getenv("MCPIT_HOME")
	if dir == "" {
		base, err := dataDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(base, "mcpit")
	}
	return Open(dir)
}

func dataDir() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d, nil
	}
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "packs"), 0o755); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

func (s *Store) path(origin string) string {
	return filepath.Join(s.Dir, "packs", sitepack.Slug(origin)+".json")
}

func (s *Store) Save(p *sitepack.Pack) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("invalid sitepack: %w", err)
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path(p.Origin) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(p.Origin))
}

// Load accepts an origin, a full URL or a host name.
func (s *Store) Load(site string) (*sitepack.Pack, error) {
	origin := normalize(site)
	b, err := os.ReadFile(s.path(origin))
	if errors.Is(err, os.ErrNotExist) && !strings.Contains(site, "://") {
		b, err = os.ReadFile(s.path("http://" + site))
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var p sitepack.Pack
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func normalize(site string) string {
	if !strings.Contains(site, "://") {
		site = "https://" + site
	}
	if o, err := sitepack.OriginOf(site); err == nil {
		return o
	}
	return site
}

func (s *Store) Remove(site string) error {
	err := os.Remove(s.path(normalize(site)))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

func (s *Store) List() ([]*sitepack.Pack, error) {
	files, err := filepath.Glob(filepath.Join(s.Dir, "packs", "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []*sitepack.Pack
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var p sitepack.Pack
		if json.Unmarshal(b, &p) == nil {
			out = append(out, &p)
		}
	}
	return out, nil
}

// Decision is one entry of the decision log.
type Decision struct {
	Time   string             `json:"time"`
	Point  string             `json:"point"`
	Model  string             `json:"model"`
	Answer string             `json:"answer"`
	Probs  map[string]float64 `json:"probs,omitempty"`
	Action string             `json:"action"`
	Subject string            `json:"subject"`
}

func (s *Store) LogDecision(d Decision) {
	if d.Time == "" {
		d.Time = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(d)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(s.Dir, "decisions.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}
