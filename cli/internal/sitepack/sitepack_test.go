package sitepack

import (
	"encoding/json"
	"testing"
)

func pack() *Pack {
	return &Pack{Schema: Schema, Origin: "https://shop.example.com", Tools: []Tool{
		{ID: "search", Description: "Search <products> & more", Kind: "search", Effect: "read", Auth: "none", Executors: []string{"http"},
			Request: Request{Method: "GET", URL: "https://shop.example.com/search", Query: map[string]string{"q": "{{q}}"}},
			InputSchema: map[string]any{"type": "object", "required": []string{"q"}}, Output: Output{Type: "html"}},
		{ID: "a_detail", Description: "Detail", Kind: "api", Effect: "read", Auth: "none", Executors: []string{"http"},
			Request: Request{Method: "GET", URL: "https://shop.example.com/api/p/{{id}}"}, InputSchema: map[string]any{"type": "object"}, Output: Output{Type: "json"}},
	}}
}

func TestHashIgnoresVolatileFieldsAndOrder(t *testing.T) {
	a := pack()
	h1, err := a.Hash()
	if err != nil {
		t.Fatal(err)
	}
	b := pack()
	b.Tools[0], b.Tools[1] = b.Tools[1], b.Tools[0]
	b.Version = "2026-10-04.1"
	b.Provenance = &Provenance{Submitter: "key:ed25519:abc"}
	b.Tools[0].Evidence = &Evidence{Observed: 3, Confidence: 0.9}
	b.Tools[1].Rev = 7
	h2, _ := b.Hash()
	if h1 != h2 {
		t.Fatalf("hash changed with volatile fields: %s vs %s", h1, h2)
	}
	b.Tools[0].Request.URL = "https://shop.example.com/api/p2/{{id}}"
	if h3, _ := b.Hash(); h3 == h1 {
		t.Fatal("hash did not change with the request URL")
	}
}

// The registry (TypeScript) must produce the same canonical bytes.
func TestCanonicalBytes(t *testing.T) {
	raw := []byte(`{"tools":[{"id":"b","rev":2,"x":1.5,"s":"<&>"},{"id":"a","evidence":{"c":1}}],"origin":"https://e.com","schema":"mcpit.sitepack/1","version":"v","provenance":{}}`)
	got, err := CanonicalPackJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"origin":"https://e.com","schema":"mcpit.sitepack/1","tools":[{"id":"a"},{"id":"b","s":"<&>","x":1.5}]}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// Hash of the struct form equals the hash of its JSON form.
	p := pack()
	b, _ := json.Marshal(p)
	h1, _ := p.Hash()
	h2, _ := HashJSON(b)
	if h1 != h2 {
		t.Fatal("struct and raw hashes differ")
	}
}

func TestValidate(t *testing.T) {
	p := pack()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Tools[1].ID = "search"
	p.Tools[0].Effect = "maybe"
	p.Origin = "https://e.com/path"
	err := p.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"duplicate", "effect", "origin"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{"https://www.example.com": "www.example.com", "http://127.0.0.1:7810": "http_127.0.0.1_7810"}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%s)=%s want %s", in, got, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
