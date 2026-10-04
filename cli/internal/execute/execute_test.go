package execute

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jstdlee/mcpit/cli/internal/fixture"
	"github.com/jstdlee/mcpit/cli/internal/sitepack"
)

func TestCallReadAndWrite(t *testing.T) {
	site := fixture.New()
	srv := httptest.NewServer(site)
	defer srv.Close()
	ctx := context.Background()

	search := &sitepack.Tool{ID: "search_api", Effect: "read", Request: sitepack.Request{Method: "GET", URL: srv.URL + "/api/search",
		Query: map[string]string{"q": "{{q}}", "page": "{{page}}"}},
		InputSchema: map[string]any{"type": "object", "required": []any{"q"}, "properties": map[string]any{"q": map[string]any{}, "page": map[string]any{}}}}
	r, err := Call(ctx, srv.Client(), srv.URL, search, map[string]any{"q": "lamp"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	res := r.Data.(map[string]any)["results"].([]any)
	if r.Status != 200 || len(res) != 3 || !r.Untrusted {
		t.Fatalf("unexpected result %+v", r)
	}
	if _, err := Call(ctx, srv.Client(), srv.URL, search, map[string]any{}, Options{}); err == nil || !strings.Contains(err.Error(), "q") {
		t.Fatalf("missing required arg not refused: %v", err)
	}
	if _, err := Call(ctx, srv.Client(), srv.URL, search, map[string]any{"q": "x", "evil": 1}, Options{}); err == nil {
		t.Fatal("unknown argument not refused")
	}

	byID := &sitepack.Tool{ID: "p", Effect: "read", Request: sitepack.Request{Method: "GET", URL: srv.URL + "/api/products/{{id}}"},
		InputSchema: map[string]any{"type": "object", "required": []string{"id"}, "properties": map[string]any{"id": map[string]any{}}}}
	r, err = Call(ctx, srv.Client(), srv.URL, byID, map[string]any{"id": 981}, Options{})
	if err != nil || r.Data.(map[string]any)["title"] != "Desk lamp" {
		t.Fatalf("path param call: %v %+v", err, r)
	}

	contact := &sitepack.Tool{ID: "contact", Effect: "write", Request: sitepack.Request{Method: "POST", URL: srv.URL + "/contact", ContentType: "form",
		Body: map[string]any{"email": "{{email}}", "message": "{{message}}"}},
		Steps:       []sitepack.Step{{Kind: "token", URL: srv.URL + "/contact", Extract: "input[name=csrf_token]", As: "csrf_token"}},
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"email": map[string]any{}, "message": map[string]any{}}}}
	args := map[string]any{"email": "a@example.com", "message": "hi"}
	if _, err := Call(ctx, srv.Client(), srv.URL, contact, args, Options{}); !errors.Is(err, ErrNeedsConfirm) {
		t.Fatalf("write without confirm: %v", err)
	}
	if site.Contacts != 0 {
		t.Fatal("form was posted without confirmation")
	}
	r, err = Call(ctx, srv.Client(), srv.URL, contact, args, Options{Confirmed: true})
	if err != nil || r.Status != 200 || site.Contacts != 1 {
		t.Fatalf("confirmed write with token step failed: %v %+v contacts=%d", err, r, site.Contacts)
	}
}

func TestRefusesOtherSites(t *testing.T) {
	tool := &sitepack.Tool{ID: "x", Effect: "read", Request: sitepack.Request{Method: "GET", URL: "https://evil.example.net/collect"}, InputSchema: map[string]any{}}
	_, err := Call(context.Background(), http.DefaultClient, "https://shop.example.com", tool, nil, Options{})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("off-site request not refused: %v", err)
	}
}

func TestVisibleText(t *testing.T) {
	got := VisibleText([]byte(`<html><head><title>x</title><script>evil()</script></head><body><h1>Hello</h1><p>World <b>now</b></p></body></html>`))
	if strings.Contains(got, "evil") || !strings.Contains(got, "Hello") || !strings.Contains(got, "World now") {
		t.Fatalf("bad text: %q", got)
	}
}
