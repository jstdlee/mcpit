package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jstdlee/mcpit/cli/internal/app"
	"github.com/jstdlee/mcpit/cli/internal/config"
	"github.com/jstdlee/mcpit/cli/internal/decide"
	"github.com/jstdlee/mcpit/cli/internal/fixture"
	"github.com/jstdlee/mcpit/cli/internal/registry"
	"github.com/jstdlee/mcpit/cli/internal/sitepack"
	"github.com/jstdlee/mcpit/cli/internal/store"
)

func TestMCPTools(t *testing.T) {
	site := fixture.New()
	srv := httptest.NewServer(site)
	defer srv.Close()
	st, _ := store.Open(t.TempDir())
	p := &sitepack.Pack{Schema: sitepack.Schema, Origin: srv.URL, Tools: []sitepack.Tool{
		{ID: "search_api", Description: "Search products.", Kind: "api", Effect: "read", Auth: "none", Executors: []string{"http"},
			Request:     sitepack.Request{Method: "GET", URL: srv.URL + "/api/search", Query: map[string]string{"q": "{{q}}"}},
			InputSchema: map[string]any{"type": "object", "required": []string{"q"}, "properties": map[string]any{"q": map[string]any{"type": "string"}}}, Output: sitepack.Output{Type: "json"}},
		{ID: "contact", Description: "Contact form.", Kind: "form", Effect: "write", Auth: "none", Executors: []string{"http"},
			Request:     sitepack.Request{Method: "POST", URL: srv.URL + "/contact", ContentType: "form", Body: map[string]any{"message": "{{message}}"}},
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string"}}}, Output: sitepack.Output{Type: "html"}},
	}}
	if err := st.Save(p); err != nil {
		t.Fatal(err)
	}
	a := &app.App{Cfg: &config.Config{}, Store: st, Decider: &decide.Decider{}, Registry: registry.New("http://127.0.0.1:1", nil), HTTP: http.DefaultClient, Offline: true}

	ctx := context.Background()
	ct, stt := mcp.NewInMemoryTransports()
	server := New(a)
	ss, err := server.Connect(ctx, stt, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	for _, n := range []string{"mcpit_find", "mcpit_tools", "mcpit_call", "mcpit_explore", "mcpit_submit", "mcpit_report"} {
		if !names[n] {
			t.Errorf("missing tool %s", n)
		}
	}

	call := func(name string, args map[string]any) string {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res.Content[0].(*mcp.TextContent).Text
	}
	if out := call("mcpit_find", map[string]any{"site": srv.URL}); !strings.Contains(out, `"source": "local"`) || !strings.Contains(out, "search_api") {
		t.Errorf("find: %s", out)
	}
	if out := call("mcpit_call", map[string]any{"site": srv.URL, "tool": "search_api", "args": map[string]any{"q": "lamp"}}); !strings.Contains(out, "Desk lamp") {
		t.Errorf("call: %s", out)
	}
	// The test client has no elicitation support, so a write tool must be refused.
	if out := call("mcpit_call", map[string]any{"site": srv.URL, "tool": "contact", "args": map[string]any{"message": "hi"}}); !strings.Contains(out, "Refused") {
		t.Errorf("write call not refused: %s", out)
	}
	if site.Contacts != 0 {
		t.Error("write tool ran without confirmation")
	}
	if out := call("mcpit_find", map[string]any{"site": "https://unknown.example.org"}); !strings.Contains(out, "mcpit_explore") {
		t.Errorf("unknown site should suggest explore: %s", out)
	}
}
