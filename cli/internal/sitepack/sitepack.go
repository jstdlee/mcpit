// Package sitepack defines the mcpit sitepack format (schema mcpit.sitepack/1):
// the shared, data-free description of the tools a website offers.
package sitepack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

const Schema = "mcpit.sitepack/1"

type Pack struct {
	Schema      string       `json:"schema"`
	Origin      string       `json:"origin"`
	Version     string       `json:"version,omitempty"`
	Fingerprint Fingerprint  `json:"fingerprint"`
	Tools       []Tool       `json:"tools"`
	Provenance  *Provenance  `json:"provenance,omitempty"`
	Registry    *RegistryRef `json:"registry,omitempty"`
}

type Fingerprint struct {
	Routes  []string `json:"routes,omitempty"`
	DOMHash string   `json:"domHash,omitempty"`
	APIHash string   `json:"apiHash,omitempty"`
	Variant string   `json:"variant,omitempty"`
}

type Tool struct {
	ID          string         `json:"id"`
	Rev         int            `json:"rev,omitempty"`
	Description string         `json:"description"`
	Kind        string         `json:"kind"`   // api | form | search | read | flow | native
	Effect      string         `json:"effect"` // read | write | payment | destructive
	Auth        string         `json:"auth"`   // none | session | token
	Executors   []string       `json:"executors"`
	Request     Request        `json:"request"`
	InputSchema map[string]any `json:"inputSchema"`
	Steps       []Step         `json:"steps,omitempty"`
	Output      Output         `json:"output"`
	Probe       *Probe         `json:"probe,omitempty"`
	Evidence    *Evidence      `json:"evidence,omitempty"`
}

type Request struct {
	Method      string            `json:"method"`
	URL         string            `json:"url"`
	Query       map[string]string `json:"query,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        any               `json:"body,omitempty"`
	ContentType string            `json:"contentType,omitempty"` // form | json
}

type Step struct {
	Kind    string `json:"kind"` // token
	URL     string `json:"url"`
	Extract string `json:"extract"` // e.g. input[name=csrf]
	As      string `json:"as"`
}

type Output struct {
	Type      string `json:"type"` // json | html | text
	ItemsPath string `json:"itemsPath,omitempty"`
}

type Probe struct {
	Args   map[string]any `json:"args"`
	Expect ProbeExpect    `json:"expect"`
}

type ProbeExpect struct {
	Status int `json:"status"`
}

type Evidence struct {
	Observed   int     `json:"observed"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source,omitempty"` // network | form | openapi | opensearch
}

type Provenance struct {
	Submitter string `json:"submitter,omitempty"`
	Client    string `json:"client,omitempty"`
	Decisions string `json:"decisions,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// RegistryRef is set on packs pulled from a registry.
type RegistryRef struct {
	URL       string `json:"url"`
	Hash      string `json:"hash"`
	Signature string `json:"signature,omitempty"`
	KeyID     string `json:"keyId,omitempty"`
	State     string `json:"state,omitempty"`
}

var (
	toolIDRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	kinds    = set("api", "form", "search", "read", "flow", "native")
	effects  = set("read", "write", "payment", "destructive")
	auths    = set("none", "session", "token")
	methods  = set("GET", "POST", "PUT", "PATCH", "DELETE")
)

func set(v ...string) map[string]bool {
	m := map[string]bool{}
	for _, s := range v {
		m[s] = true
	}
	return m
}

// Validate checks the structure. It does not judge safety; the scanner does that.
func (p *Pack) Validate() error {
	var errs []string
	if p.Schema != Schema {
		errs = append(errs, "schema must be "+Schema)
	}
	o, err := url.Parse(p.Origin)
	if err != nil || (o.Scheme != "https" && o.Scheme != "http") || o.Host == "" || o.Path != "" {
		errs = append(errs, "origin must be scheme://host with no path")
	}
	if len(p.Tools) == 0 {
		errs = append(errs, "at least one tool is required")
	}
	seen := map[string]bool{}
	for i, t := range p.Tools {
		at := fmt.Sprintf("tools[%d]", i)
		if !toolIDRe.MatchString(t.ID) {
			errs = append(errs, at+".id must match "+toolIDRe.String())
		}
		if seen[t.ID] {
			errs = append(errs, at+".id is a duplicate")
		}
		seen[t.ID] = true
		if !kinds[t.Kind] {
			errs = append(errs, at+".kind is invalid")
		}
		if !effects[t.Effect] {
			errs = append(errs, at+".effect is invalid")
		}
		if !auths[t.Auth] {
			errs = append(errs, at+".auth is invalid")
		}
		if !methods[t.Request.Method] {
			errs = append(errs, at+".request.method is invalid")
		}
		if t.Request.URL == "" {
			errs = append(errs, at+".request.url is required")
		}
		if len(t.Description) > 500 {
			errs = append(errs, at+".description is longer than 500 characters")
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (p *Pack) Tool(id string) *Tool {
	for i := range p.Tools {
		if p.Tools[i].ID == id {
			return &p.Tools[i]
		}
	}
	return nil
}

// Canonical returns the canonical JSON used for hashing: volatile fields
// (version, provenance, registry, tools[].evidence, tools[].rev) removed, tools
// sorted by id, object keys sorted, no spaces. The registry applies the same rule
// to the same JSON document, so both sides get the same hash.
func (p *Pack) Canonical() ([]byte, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return CanonicalPackJSON(raw)
}

// CanonicalPackJSON canonicalizes a raw sitepack JSON document.
func CanonicalPackJSON(raw []byte) ([]byte, error) {
	var v map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	delete(v, "version")
	delete(v, "provenance")
	delete(v, "registry")
	if tools, ok := v["tools"].([]any); ok {
		for _, t := range tools {
			if m, ok := t.(map[string]any); ok {
				delete(m, "evidence")
				delete(m, "rev")
			}
		}
		sort.SliceStable(tools, func(i, j int) bool { return toolKey(tools[i]) < toolKey(tools[j]) })
	}
	return CanonicalJSON(v)
}

func toolKey(t any) string {
	if m, ok := t.(map[string]any); ok {
		s, _ := m["id"].(string)
		return s
	}
	return ""
}

// HashJSON hashes a raw sitepack JSON document.
func HashJSON(raw []byte) (string, error) {
	b, err := CanonicalPackJSON(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Hash is sha256 of the canonical JSON, hex encoded.
func (p *Pack) Hash() (string, error) {
	b, err := p.Canonical()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// CanonicalJSON writes v with sorted object keys and no HTML escaping, so the
// bytes match JSON.stringify of the same sorted value in the registry.
func CanonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanon(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanon(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeScalar(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := writeCanon(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanon(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		return writeScalar(buf, x)
	}
	return nil
}

func writeScalar(buf *bytes.Buffer, v any) error {
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	buf.Truncate(buf.Len() - 1) // Encode adds a newline
	return nil
}

// Slug turns an origin into a file-safe name: https://www.example.com:8080 -> www.example.com_8080
func Slug(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return strings.Map(safe, origin)
	}
	s := u.Host
	if u.Scheme == "http" {
		s = "http_" + s
	}
	return strings.Map(safe, strings.ReplaceAll(s, ":", "_"))
}

func safe(r rune) rune {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
		return r
	}
	return '_'
}

// OriginOf returns scheme://host of a URL.
func OriginOf(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("not an http(s) URL: %s", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}
