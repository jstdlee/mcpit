package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jstdlee/mcpit/cli/internal/registry"
	"github.com/jstdlee/mcpit/cli/internal/sitepack"
)

// Integrity is the result of checking a registry pack before use.
type Integrity struct {
	Level     string   `json:"level"` // ok | warn | block
	Alerts    []string `json:"alerts,omitempty"`
	Updated   string   `json:"updated,omitempty"` // new version pulled
	Verdict   string   `json:"verdict,omitempty"`
	State     string   `json:"state,omitempty"`
	CheckedAt string   `json:"checkedAt,omitempty"`
}

// ErrBlocked means the pack must not be used without the user's explicit confirmation.
var ErrBlocked = errors.New("mcpit blocked this site")

// BlockedError carries the alerts for the agent and the user.
type BlockedError struct{ Integrity *Integrity }

func (e *BlockedError) Error() string { return fmt.Sprintf("%v: %v", ErrBlocked, e.Integrity.Alerts) }
func (e *BlockedError) Unwrap() error { return ErrBlocked }

const statusTTL = 10 * time.Minute

// Check verifies a pack before use. Local packs (not from the registry) pass. For
// registry packs it checks the local hash against the signed hash, then the registry
// status (cached 10 min): de-listed, "bad" or "suspicious" sites are blocked, expired
// ones warn, and a newer signed version is pulled and replaces the local copy.
func (a *App) Check(ctx context.Context, p *sitepack.Pack) (*sitepack.Pack, *Integrity) {
	in := &Integrity{Level: "ok"}
	if p.Registry == nil {
		// A pack the user explored: only warn when the registry flags the site.
		if !a.Offline {
			if st, err := a.status(ctx, p.Origin); err == nil {
				switch {
				case st.State == "delisted":
					in.Level, in.Alerts = "warn", []string{fmt.Sprintf("the registry de-listed %s%s; this local pack is your own exploration", p.Origin, reason(st.Reason))}
				case st.Verdict == "bad" || st.Verdict == "suspicious":
					in.Level, in.Alerts = "warn", []string{fmt.Sprintf("the registry marked %s as %s; this local pack is your own exploration", p.Origin, st.Verdict)}
				}
			}
		}
		return p, in
	}
	warn := func(s string) {
		if in.Level == "ok" {
			in.Level = "warn"
		}
		in.Alerts = append(in.Alerts, s)
	}
	block := func(s string) { in.Level = "block"; in.Alerts = append(in.Alerts, s) }

	if h, err := p.Hash(); err != nil || h != p.Registry.Hash {
		block(fmt.Sprintf("the local copy of %s does not match its signed hash (local %.12s, signed %.12s): it changed after it was pulled", p.Origin, h, p.Registry.Hash))
		return p, in
	}
	if a.Offline {
		return p, in
	}
	st, err := a.status(ctx, p.Origin)
	switch {
	case errors.Is(err, registry.ErrNotFound):
		warn(fmt.Sprintf("%s is no longer in the registry; the local copy may be out of date", p.Origin))
		return p, in
	case err != nil:
		warn("could not reach the registry to check this site: " + err.Error())
		return p, in
	}
	in.Verdict, in.State, in.CheckedAt = st.Verdict, st.State, time.Now().UTC().Format(time.RFC3339)
	switch {
	case st.State == "delisted":
		block(fmt.Sprintf("%s was de-listed by the registry moderator%s", p.Origin, reason(st.Reason)))
	case st.Verdict == "bad":
		block(fmt.Sprintf("the registry marked %s as bad (blocked)", p.Origin))
	case st.Verdict == "suspicious":
		block(fmt.Sprintf("the registry marked %s as suspicious", p.Origin))
	}
	if in.Level == "block" {
		return p, in
	}
	if st.State == "expired" {
		warn(fmt.Sprintf("the registry version of %s expired: it was not re-verified in time", p.Origin))
	}
	if st.Hash != "" && st.Hash != p.Registry.Hash {
		np, err := a.Pull(ctx, p.Origin)
		if err != nil {
			block(fmt.Sprintf("the registry lists a different version of %s (hash %.12s) and it failed verification: %v", p.Origin, st.Hash, err))
			return p, in
		}
		if np.Registry == nil || np.Registry.Hash != st.Hash {
			block(fmt.Sprintf("the pulled version of %s does not match the hash the registry lists", p.Origin))
			return p, in
		}
		in.Updated = np.Version
		return np, in
	}
	return p, in
}

func reason(r string) string {
	if r == "" {
		return ""
	}
	return ": " + r
}

// status reads the registry status with a small file cache, so short CLI runs and many
// MCP calls do not ask the registry every time.
func (a *App) status(ctx context.Context, origin string) (*registry.SiteStatus, error) {
	path := filepath.Join(a.Store.Dir, "status", sitepack.Slug(origin)+".json")
	var cached struct {
		At     time.Time            `json:"at"`
		Status *registry.SiteStatus `json:"status"`
		Gone   bool                 `json:"gone"`
	}
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &cached) == nil && time.Since(cached.At) < statusTTL {
		if cached.Gone {
			return nil, registry.ErrNotFound
		}
		if cached.Status != nil {
			return cached.Status, nil
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := a.Registry.SiteStatus(cctx, origin)
	if err != nil && !errors.Is(err, registry.ErrNotFound) {
		return nil, err
	}
	cached.At, cached.Status, cached.Gone = time.Now(), st, errors.Is(err, registry.ErrNotFound)
	os.MkdirAll(filepath.Dir(path), 0o755)
	if b, e := json.Marshal(cached); e == nil {
		os.WriteFile(path, b, 0o644)
	}
	return st, err
}

// Forget drops the cached status of a site (after a pull or a moderator change).
func (a *App) Forget(origin string) {
	os.Remove(filepath.Join(a.Store.Dir, "status", sitepack.Slug(origin)+".json"))
}
