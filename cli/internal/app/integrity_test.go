package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jstdlee/mcpit/cli/internal/config"
	"github.com/jstdlee/mcpit/cli/internal/decide"
	"github.com/jstdlee/mcpit/cli/internal/registry"
	"github.com/jstdlee/mcpit/cli/internal/sitepack"
	"github.com/jstdlee/mcpit/cli/internal/store"
)

func testPack(desc string) sitepack.Pack {
	return sitepack.Pack{Schema: sitepack.Schema, Origin: "https://shop.example.com", Tools: []sitepack.Tool{{ID: "search", Description: desc,
		Kind: "search", Effect: "read", Auth: "none", Executors: []string{"http"},
		Request: sitepack.Request{Method: "GET", URL: "https://shop.example.com/search"}, InputSchema: map[string]any{}, Output: sitepack.Output{Type: "html"}}}}
}

func TestIntegrity(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	v1, v2 := testPack("Search v1."), testPack("Search v2.")
	h1, _ := v1.Hash()
	h2, _ := v2.Hash()
	status := map[string]any{"state": "listed", "verdict": nil, "hash": h1, "version": "1"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/.well-known/mcpit-keys.json":
			json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"id": "reg-1", "public": base64.StdEncoding.EncodeToString(pub)}}})
		case strings.HasSuffix(r.URL.Path, "/status"):
			json.NewEncoder(w).Encode(status)
		default: // pull returns v2
			sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(h2+"|"+v2.Origin+"|2")))
			json.NewEncoder(w).Encode(map[string]any{"pack": v2, "hash": h2, "signature": sig, "keyId": "reg-1", "version": "2", "state": "active"})
		}
	}))
	defer srv.Close()
	st, _ := store.Open(t.TempDir())
	a := &App{Cfg: &config.Config{}, Store: st, Decider: &decide.Decider{}, Registry: registry.New(srv.URL, nil), HTTP: http.DefaultClient}
	ctx := context.Background()
	local := func(p sitepack.Pack, hash string) *sitepack.Pack {
		p.Registry = &sitepack.RegistryRef{URL: srv.URL, Hash: hash}
		return &p
	}
	fresh := func(s map[string]any) {
		for k, v := range s {
			status[k] = v
		}
		a.Forget(v1.Origin)
	}

	if _, in := a.Check(ctx, local(v1, h1)); in.Level != "ok" {
		t.Fatalf("clean pack: %+v", in)
	}
	tampered := local(testPack("Search v1."), h1)
	tampered.Tools[0].Request.URL = "https://shop.example.com/evil"
	if _, in := a.Check(ctx, tampered); in.Level != "block" || !strings.Contains(in.Alerts[0], "signed hash") {
		t.Fatalf("tampered pack: %+v", in)
	}
	fresh(map[string]any{"state": "delisted", "reason": "owner opt-out"})
	if _, in := a.Check(ctx, local(v1, h1)); in.Level != "block" || !strings.Contains(in.Alerts[0], "owner opt-out") {
		t.Fatalf("delisted: %+v", in)
	}
	fresh(map[string]any{"state": "listed", "verdict": "suspicious"})
	if _, in := a.Check(ctx, local(v1, h1)); in.Level != "block" {
		t.Fatalf("suspicious: %+v", in)
	}
	fresh(map[string]any{"verdict": nil, "state": "expired"})
	if _, in := a.Check(ctx, local(v1, h1)); in.Level != "warn" {
		t.Fatalf("expired: %+v", in)
	}
	fresh(map[string]any{"state": "listed", "hash": h2})
	p, in := a.Check(ctx, local(v1, h1))
	if in.Level != "ok" || in.Updated != "2" || p.Tools[0].Description != "Search v2." {
		t.Fatalf("update: %+v %+v", in, p.Tools[0])
	}
	fresh(map[string]any{"hash": "deadbeef"})
	if _, in := a.Check(ctx, local(v1, h1)); in.Level != "block" {
		t.Fatalf("registry lists a hash its pack does not match: %+v", in)
	}
}
