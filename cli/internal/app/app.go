// Package app joins the store, the registry, the explorer, the executor and the
// decision model. The CLI and the MCP server both use it.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jstdlee/mcpit/cli/internal/config"
	"github.com/jstdlee/mcpit/cli/internal/decide"
	"github.com/jstdlee/mcpit/cli/internal/execute"
	"github.com/jstdlee/mcpit/cli/internal/explore"
	"github.com/jstdlee/mcpit/cli/internal/registry"
	"github.com/jstdlee/mcpit/cli/internal/sitepack"
	"github.com/jstdlee/mcpit/cli/internal/store"
)

type App struct {
	Cfg      *config.Config
	Store    *store.Store
	Decider  *decide.Decider
	Registry *registry.Client
	HTTP     *http.Client
	Offline  bool // never contact the registry
}

func New() (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	st, err := store.Default()
	if err != nil {
		return nil, err
	}
	a := &App{Cfg: cfg, Store: st, HTTP: &http.Client{Timeout: 30 * time.Second}}
	p, err := decide.FromConfig(cfg.Decider)
	if err != nil {
		return nil, err
	}
	a.Decider = &decide.Decider{P: p, Log: func(point, subject, model string, ans decide.Answer) {
		st.LogDecision(store.Decision{Point: point, Model: model, Subject: subject, Answer: answerText(ans), Probs: ans.Probabilities})
	}}
	key, _ := registry.LoadKey(config.Dir())
	a.Registry = registry.New(strings.TrimSuffix(cfg.Registry, "/"), key)
	return a, nil
}

func answerText(a decide.Answer) string {
	switch a.Type {
	case "noul":
		return fmt.Sprintf("%.2f", a.Noul)
	case "choice":
		return a.Choice
	case "score":
		return fmt.Sprintf("%.2f", a.Score)
	}
	return ""
}

// Source says which rung of the lookup ladder answered.
type Source string

const (
	FromLocal    Source = "local"
	FromRegistry Source = "registry"
)

// Pack returns the sitepack for a site: local store first, then the registry.
func (a *App) Pack(ctx context.Context, site string) (*sitepack.Pack, Source, error) {
	p, err := a.Store.Load(site)
	if err == nil {
		return p, FromLocal, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, "", err
	}
	if a.Offline {
		return nil, "", fmt.Errorf("%w; run mcpit_explore on a page of the site", store.ErrNotFound)
	}
	pulled, err := a.Pull(ctx, site)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return nil, "", fmt.Errorf("%w; run mcpit_explore on a page of the site", store.ErrNotFound)
		}
		return nil, "", fmt.Errorf("local store has no pack and registry failed: %w", err)
	}
	return pulled, FromRegistry, nil
}

// Pull fetches the active version, checks its signature and saves it locally.
func (a *App) Pull(ctx context.Context, site string) (*sitepack.Pack, error) {
	origin := site
	if !strings.Contains(origin, "://") {
		origin = "https://" + origin
	}
	origin, err := sitepack.OriginOf(origin)
	if err != nil {
		return nil, err
	}
	pulled, err := a.Registry.Pull(ctx, origin)
	if err != nil {
		return nil, err
	}
	if err := a.Registry.VerifyPulled(ctx, pulled); err != nil {
		return nil, fmt.Errorf("refused registry pack: %w", err)
	}
	p := pulled.Pack
	p.Version = pulled.Version
	p.Registry = &sitepack.RegistryRef{URL: a.Registry.Base, Hash: pulled.Hash, Signature: pulled.Signature, KeyID: pulled.KeyID, State: pulled.State}
	if err := a.Store.Save(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (a *App) Explore(ctx context.Context, url string, o explore.Options) (*sitepack.Pack, error) {
	e := explore.New(a.Decider, o)
	p, err := e.Run(ctx, url)
	if err != nil {
		return p, err
	}
	// Keep a pulled registry version's tools that exploration did not find again.
	if old, err := a.Store.Load(p.Origin); err == nil {
		for _, t := range old.Tools {
			if p.Tool(t.ID) == nil {
				p.Tools = append(p.Tools, t)
			}
		}
	}
	return p, a.Store.Save(p)
}

// Call runs one tool and reports an anonymous ok/fail counter for registry packs.
func (a *App) Call(ctx context.Context, site, toolID string, args map[string]any, confirmed bool) (*execute.Result, error) {
	p, _, err := a.Pack(ctx, site)
	if err != nil {
		return nil, err
	}
	t := p.Tool(toolID)
	if t == nil {
		return nil, fmt.Errorf("site %s has no tool %q", p.Origin, toolID)
	}
	if args == nil {
		args = map[string]any{}
	}
	r, err := execute.Call(ctx, a.HTTP, p.Origin, t, args, execute.Options{Confirmed: confirmed})
	if p.Registry != nil && !a.Offline && !errors.Is(err, execute.ErrNeedsConfirm) {
		ok := err == nil && r.Status < 400
		rep := registry.Report{Origin: p.Origin, Tool: t.ID, OK: ok}
		if !ok {
			rep.Error = "http"
			if err != nil {
				rep.Error = "network"
			}
		}
		go a.Registry.Report(context.Background(), rep)
	}
	return r, err
}

// Pick uses the decision point tool.pick to choose the tool that fits a task.
func (a *App) Pick(ctx context.Context, p *sitepack.Pack, task string) (string, float64, error) {
	if len(p.Tools) == 0 {
		return "", 0, errors.New("no tools")
	}
	if !a.Decider.Available() {
		return "", 0, decide.ErrNoModel
	}
	opts := map[string]string{}
	for _, t := range p.Tools {
		opts[t.ID] = t.Description
	}
	ans, err := a.Decider.Ask(ctx, "tool.pick", map[string]any{"site": p.Origin, "task": task},
		map[string]decide.Question{"pick": {Type: "choice", Instructions: "Which site tool fits the task best?", Criteria: opts}},
		map[string]string{"pick": task})
	if err != nil {
		return "", 0, err
	}
	x := ans["pick"]
	return x.Choice, x.Probabilities[x.Choice], nil
}

// Sites lists local packs whose origin contains q.
func (a *App) Sites(q string) ([]*sitepack.Pack, error) {
	all, err := a.Store.List()
	if err != nil {
		return nil, err
	}
	var out []*sitepack.Pack
	for _, p := range all {
		if q == "" || strings.Contains(strings.ToLower(p.Origin), strings.ToLower(q)) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Origin < out[j].Origin })
	return out, nil
}

func (a *App) Submit(ctx context.Context, site string) (*registry.Submission, error) {
	p, err := a.Store.Load(site)
	if err != nil {
		return nil, err
	}
	if a.Registry.Key == nil {
		return nil, errors.New("no device key: run `mcpit key init`, then wait for moderator approval")
	}
	c := *p
	c.Registry = nil
	if c.Provenance == nil {
		c.Provenance = &sitepack.Provenance{}
	}
	c.Provenance.Submitter = a.Registry.Key.ID
	return a.Registry.Submit(ctx, &c)
}
