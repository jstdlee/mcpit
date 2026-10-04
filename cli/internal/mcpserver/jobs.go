package mcpserver

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/jstdlee/mcpit/cli/internal/app"
	"github.com/jstdlee/mcpit/cli/internal/explore"
	"github.com/jstdlee/mcpit/cli/internal/sitepack"
)

// Explore jobs run in the MCP server process, so a slow explore never hits an agent's
// tool-call timeout. One job per site at a time.
type jobs struct {
	a  *app.App
	mu sync.Mutex
	m  map[string]*job
}

type job struct {
	mu       sync.Mutex
	origin   string
	started  time.Time
	ended    time.Time
	progress []string
	pack     *sitepack.Pack
	err      error
	done     chan struct{}
}

func newJobs(a *app.App) *jobs { return &jobs{a: a, m: map[string]*job{}} }

func (js *jobs) start(url string, o explore.Options) (*job, error) {
	if !strings.Contains(url, "://") {
		url = "https://" + url
	}
	origin, err := sitepack.OriginOf(url)
	if err != nil {
		return nil, err
	}
	js.mu.Lock()
	defer js.mu.Unlock()
	if j := js.m[origin]; j != nil && j.running() {
		return j, nil
	}
	j := &job{origin: origin, started: time.Now(), done: make(chan struct{})}
	js.m[origin] = j
	o.Progress = func(s string) {
		j.mu.Lock()
		j.progress = append(j.progress, s)
		j.mu.Unlock()
	}
	go func() {
		defer close(j.done)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		p, err := js.a.Explore(ctx, url, o)
		j.mu.Lock()
		j.pack, j.err, j.ended = p, err, time.Now()
		j.mu.Unlock()
	}()
	return j, nil
}

func (js *jobs) get(site string) *job {
	if !strings.Contains(site, "://") {
		site = "https://" + site
	}
	origin, err := sitepack.OriginOf(site)
	if err != nil {
		return nil
	}
	js.mu.Lock()
	defer js.mu.Unlock()
	if j := js.m[origin]; j != nil {
		return j
	}
	// http:// sites (local tests)
	return js.m[strings.Replace(origin, "https://", "http://", 1)]
}

func (j *job) running() bool {
	select {
	case <-j.done:
		return false
	default:
		return true
	}
}

func (j *job) view() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	v := map[string]any{"origin": j.origin}
	last := j.progress
	if len(last) > 5 {
		last = last[len(last)-5:]
	}
	switch {
	case j.running():
		v["status"] = "running"
		v["seconds"] = int(time.Since(j.started).Seconds())
		v["progress"] = last
		v["next"] = "Call mcpit_explore_status with this site in about 20 seconds (or pass wait=true)."
	case j.err != nil && (j.pack == nil || len(j.pack.Tools) == 0):
		v["status"] = "failed"
		v["error"] = j.err.Error()
	default:
		v["status"] = "done"
		v["seconds"] = int(j.ended.Sub(j.started).Seconds())
		v["tools"] = summary(j.pack)
		v["pages"] = len(j.pack.Pages)
		v["next"] = "Call a tool with mcpit_call. Offer mcpit_submit to share the pack (ask the user first)."
		if j.err != nil {
			v["warning"] = j.err.Error()
		}
	}
	return v
}
