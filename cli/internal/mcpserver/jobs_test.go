package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jstdlee/mcpit/cli/internal/app"
	"github.com/jstdlee/mcpit/cli/internal/config"
	"github.com/jstdlee/mcpit/cli/internal/decide"
	"github.com/jstdlee/mcpit/cli/internal/explore"
	"github.com/jstdlee/mcpit/cli/internal/fixture"
	"github.com/jstdlee/mcpit/cli/internal/registry"
	"github.com/jstdlee/mcpit/cli/internal/store"
)

func TestExploreJob(t *testing.T) {
	srv := httptest.NewServer(fixture.New())
	defer srv.Close()
	st, _ := store.Open(t.TempDir())
	a := &app.App{Cfg: &config.Config{}, Store: st, Decider: &decide.Decider{}, Registry: registry.New("http://127.0.0.1:1", nil), HTTP: http.DefaultClient, Offline: true}
	js := newJobs(a)
	start := time.Now()
	j, err := js.start(srv.URL+"/", explore.Options{NoBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("start must return at once")
	}
	if again, _ := js.start(srv.URL+"/", explore.Options{NoBrowser: true}); again != j && j.running() {
		t.Fatal("a running job must be reused")
	}
	select {
	case <-j.done:
	case <-time.After(30 * time.Second):
		t.Fatal("job did not end")
	}
	v := js.get(srv.URL).view()
	if v["status"] != "done" || len(v["tools"].([]map[string]any)) == 0 {
		t.Fatalf("view: %v", v)
	}
}
