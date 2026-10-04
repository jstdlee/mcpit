package explore

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/jstdlee/mcpit/cli/internal/decide"
	"github.com/jstdlee/mcpit/cli/internal/fixture"
)

func chrome(t *testing.T) string {
	if p := os.Getenv("MCPIT_CHROME"); p != "" {
		return p
	}
	for _, n := range []string{"google-chrome", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	t.Skip("no Chrome/Chromium found")
	return ""
}

// TestExploreFixture runs the whole explorer (CDP capture, rule fallback for
// decisions) against the fixture shop.
func TestExploreFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("browser test")
	}
	path := chrome(t)
	site := fixture.New()
	srv := httptest.NewServer(site)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e := New(&decide.Decider{}, Options{Depth: 2, MaxPages: 10, ChromePath: path, Verify: true, Progress: func(s string) { t.Log(s) }})
	p, err := e.Run(ctx, srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, tool := range p.Tools {
		got[tool.ID] = tool.Effect
		t.Logf("%s %s %s", tool.ID, tool.Effect, tool.Description)
	}
	want := map[string]string{"search": "read", "search_api": "read", "product_filters": "read", "products": "read",
		"products_by_id": "read", "recommendations": "read", "contact_contact": "write"}
	for id, eff := range want {
		if got[id] != eff {
			t.Errorf("tool %s: effect %q, want %q", id, got[id], eff)
		}
	}
	for id := range got {
		if _, ok := want[id]; !ok {
			t.Errorf("unexpected tool %s (asset, tracking, login or admin leaked)", id)
		}
	}
	if site.Contacts != 0 {
		t.Error("explore posted the contact form")
	}
	if site.Hits["GET /admin"] != 0 {
		t.Error("explore visited a robots-disallowed page")
	}
	contact := p.Tool("contact_contact")
	if contact == nil || len(contact.Steps) != 1 || contact.Steps[0].As != "csrf_token" || contact.Probe != nil {
		t.Errorf("contact tool needs one token step and no probe: %+v", contact)
	}
	search := p.Tool("search")
	if search == nil || search.Probe == nil {
		t.Fatal("search tool needs a probe")
	}
	if props := search.InputSchema["properties"].(map[string]any); props["category"] == nil {
		t.Error("search form's category select was not merged into the OpenSearch tool")
	}
}

func TestRuleFallbacks(t *testing.T) {
	cases := []struct {
		n    NetEntry
		want string
	}{
		{NetEntry{URL: "https://www.google-analytics.com/g/collect", Type: "Ping"}, "tracking"},
		{NetEntry{URL: "https://shop.example.com/api/search?q=x", MIME: "application/json", Type: "Fetch"}, "api"},
		{NetEntry{URL: "https://shop.example.com/locales/en.json", MIME: "application/json", Type: "Fetch"}, "asset"},
		{NetEntry{URL: "https://shop.example.com/api/session/refresh", MIME: "application/json", Type: "XHR"}, "auth"},
	}
	for _, c := range cases {
		if got := ruleReqKind(&c.n); got != c.want {
			t.Errorf("%s: %s want %s", c.n.URL, got, c.want)
		}
	}
	if k := ruleFormKind(Form{Fields: []Field{{Name: "email"}, {Name: "password", Type: "password"}}}); k != "login" {
		t.Errorf("login form: %s", k)
	}
}

func TestTemplates(t *testing.T) {
	if p, ps := templatePath("/api/products/981/reviews"); p != "/api/products/{id}/reviews" || len(ps) != 1 {
		t.Errorf("templatePath: %s %v", p, ps)
	}
	if id := toolID("GET", "/api/v2/products/{id}", ""); id != "products_by_id" {
		t.Errorf("toolID: %s", id)
	}
	if id := toolID("POST", "/cart/add", ""); id != "post_cart_add" {
		t.Errorf("toolID: %s", id)
	}
}
