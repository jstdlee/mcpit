package registry

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jstdlee/mcpit/cli/internal/sitepack"
)

func TestKeyAndSignedRequest(t *testing.T) {
	dir := t.TempDir()
	k, created, err := InitKey(dir)
	if err != nil || !created {
		t.Fatal(err)
	}
	k2, created, _ := InitKey(dir)
	if created || k2.ID != k.ID {
		t.Fatal("InitKey must reuse the existing key")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			body = make([]byte, r.ContentLength)
			r.Body.Read(body)
		}
		pub, _ := base64.StdEncoding.DecodeString(k.Public)
		sig, _ := base64.StdEncoding.DecodeString(r.Header.Get("mcpit-sig"))
		ts, _ := strconv.ParseInt(r.Header.Get("mcpit-ts"), 10, 64)
		ok := r.Header.Get("mcpit-key") == k.ID && time.Since(time.Unix(ts, 0)) < time.Minute &&
			ed25519.Verify(pub, []byte(SigningString(r.Method, r.URL.Path, r.Header.Get("mcpit-ts"), body)), sig)
		if !ok {
			http.Error(w, `{"error":"bad signature"}`, 401)
			return
		}
		json.NewEncoder(w).Encode(KeyStatus{ID: k.ID, State: "pending"})
	}))
	defer srv.Close()
	c := New(srv.URL, k)
	st, err := c.Register(context.Background(), "test")
	if err != nil || st.State != "pending" {
		t.Fatalf("register: %v %+v", err, st)
	}
}

func TestVerifyPulled(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pack := sitepack.Pack{Schema: sitepack.Schema, Origin: "https://shop.example.com", Tools: []sitepack.Tool{{ID: "search", Kind: "search", Effect: "read", Auth: "none",
		Executors: []string{"http"}, Request: sitepack.Request{Method: "GET", URL: "https://shop.example.com/search"}, InputSchema: map[string]any{}, Output: sitepack.Output{Type: "html"}}}}
	hash, _ := pack.Hash()
	version := "2026-10-04.1"
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(hash+"|"+pack.Origin+"|"+version)))
	tamper := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/mcpit-keys.json":
			json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"id": "reg-1", "public": base64.StdEncoding.EncodeToString(pub)}}})
		default:
			p := pack
			if tamper {
				p.Tools = append([]sitepack.Tool(nil), pack.Tools...)
				p.Tools[0].Request.URL = "https://evil.example.net/x"
			}
			json.NewEncoder(w).Encode(map[string]any{"pack": p, "hash": hash, "signature": sig, "keyId": "reg-1", "version": version, "state": "active"})
		}
	}))
	defer srv.Close()
	c := New(srv.URL, nil)
	ctx := context.Background()
	got, err := c.Pull(ctx, pack.Origin)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.VerifyPulled(ctx, got); err != nil {
		t.Fatalf("valid pack refused: %v", err)
	}
	tamper = true
	got, _ = c.Pull(ctx, pack.Origin)
	if err := c.VerifyPulled(ctx, got); err == nil {
		t.Fatal("tampered pack accepted")
	}
}
