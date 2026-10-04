// Package registry is the client for the mcpit registry: device keys, signed
// submits, pulls and reports.
package registry

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jstdlee/mcpit/cli/internal/sitepack"
)

// Key is the device key. The private key never leaves the machine.
type Key struct {
	ID      string             `json:"id"`
	Public  string             `json:"public"`  // base64 raw 32 bytes
	Private string             `json:"private"` // base64 seed 32 bytes
	priv    ed25519.PrivateKey `json:"-"`
}

func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "key:ed25519:" + hex.EncodeToString(sum[:])[:16]
}

func keyPath(dir string) string { return filepath.Join(dir, "key.json") }

// InitKey creates a key in dir, or returns the existing one.
func InitKey(dir string) (*Key, bool, error) {
	if k, err := LoadKey(dir); err == nil {
		return k, false, nil
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, false, err
	}
	k := &Key{ID: KeyID(pub), Public: base64.StdEncoding.EncodeToString(pub), Private: base64.StdEncoding.EncodeToString(priv.Seed()), priv: priv}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, err
	}
	b, _ := json.MarshalIndent(k, "", "  ")
	return k, true, os.WriteFile(keyPath(dir), b, 0o600)
}

func LoadKey(dir string) (*Key, error) {
	b, err := os.ReadFile(keyPath(dir))
	if err != nil {
		return nil, err
	}
	var k Key
	if err := json.Unmarshal(b, &k); err != nil {
		return nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(k.Private)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("bad key file")
	}
	k.priv = ed25519.NewKeyFromSeed(seed)
	return &k, nil
}

// SigningString is what the registry verifies: METHOD\nPATH\nTS\nsha256hex(body).
func SigningString(method, path, ts string, body []byte) string {
	sum := sha256.Sum256(body)
	return method + "\n" + path + "\n" + ts + "\n" + hex.EncodeToString(sum[:])
}

func (k *Key) Sign(msg string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(k.priv, []byte(msg)))
}

type Client struct {
	Base string
	Key  *Key
	HTTP *http.Client
}

func New(base string, key *Key) *Client {
	return &Client{Base: base, Key: key, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string { return fmt.Sprintf("registry: HTTP %d: %s", e.Status, e.Msg) }

func (c *Client) do(ctx context.Context, method, path string, in, out any, signed bool) error {
	var body []byte
	if in != nil {
		body, _ = json.Marshal(in)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "mcpit/0.1")
	if signed {
		if c.Key == nil {
			return errors.New("no device key: run `mcpit key init`")
		}
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		u, _ := url.Parse(c.Base + path)
		req.Header.Set("mcpit-key", c.Key.ID)
		req.Header.Set("mcpit-ts", ts)
		req.Header.Set("mcpit-sig", c.Key.Sign(SigningString(method, u.Path, ts, body)))
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = string(raw)
		}
		return &APIError{resp.StatusCode, e.Error}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type KeyStatus struct {
	ID    string `json:"id"`
	State string `json:"state"` // pending | approved | rejected | revoked
	Name  string `json:"name,omitempty"`
}

// Register sends the public key. The request is signed, which proves possession.
func (c *Client) Register(ctx context.Context, name string) (*KeyStatus, error) {
	var out KeyStatus
	err := c.do(ctx, "POST", "/v1/keys", map[string]string{"publicKey": c.Key.Public, "name": name}, &out, true)
	return &out, err
}

func (c *Client) KeyStatus(ctx context.Context) (*KeyStatus, error) {
	var out KeyStatus
	err := c.do(ctx, "GET", "/v1/keys/me", nil, &out, true)
	return &out, err
}

type Submission struct {
	ID       string           `json:"id"`
	State    string           `json:"state"`
	Hash     string           `json:"hash"`
	Origin   string           `json:"origin"`
	Outcome  string           `json:"outcome,omitempty"`
	Reason   string           `json:"reason,omitempty"`
	Tools    []map[string]any `json:"tools,omitempty"`
	Existing bool             `json:"existing,omitempty"`
}

func (c *Client) Submit(ctx context.Context, p *sitepack.Pack) (*Submission, error) {
	var out Submission
	err := c.do(ctx, "POST", "/v1/submissions", map[string]any{"pack": p}, &out, true)
	return &out, err
}

func (c *Client) Status(ctx context.Context, id string) (*Submission, error) {
	var out Submission
	err := c.do(ctx, "GET", "/v1/submissions/"+url.PathEscape(id), nil, &out, false)
	return &out, err
}

type Pulled struct {
	Pack      sitepack.Pack   `json:"pack"`
	RawPack   json.RawMessage `json:"-"`
	Hash      string          `json:"hash"`
	Signature string          `json:"signature"`
	KeyID     string          `json:"keyId"`
	State     string          `json:"state"`
	Version   string          `json:"version"`
}

var ErrNotFound = errors.New("site not in the registry")

func (c *Client) Pull(ctx context.Context, origin string) (*Pulled, error) {
	var raw struct {
		Pack json.RawMessage `json:"pack"`
	}
	var out Pulled
	err := c.do(ctx, "GET", "/v1/sites/"+url.PathEscape(origin), nil, &rawAndOut{&raw, &out}, false)
	out.RawPack = raw.Pack
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 404 {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// VerifyPulled checks the registry signature over hash|origin|version with the
// registry public keys from /.well-known/mcpit-keys.json.
func (c *Client) VerifyPulled(ctx context.Context, p *Pulled) error {
	var keys struct {
		Keys []struct {
			ID     string `json:"id"`
			Public string `json:"public"`
		} `json:"keys"`
	}
	if err := c.do(ctx, "GET", "/.well-known/mcpit-keys.json", nil, &keys, false); err != nil {
		return err
	}
	for _, k := range keys.Keys {
		if k.ID != p.KeyID {
			continue
		}
		pub, err := base64.StdEncoding.DecodeString(k.Public)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return errors.New("bad registry key")
		}
		sig, err := base64.StdEncoding.DecodeString(p.Signature)
		if err != nil {
			return err
		}
		msg := p.Hash + "|" + p.Pack.Origin + "|" + p.Version
		if !ed25519.Verify(pub, []byte(msg), sig) {
			return errors.New("registry signature does not match")
		}
		h, err := sitepack.HashJSON(p.RawPack)
		if err != nil {
			return err
		}
		if h != p.Hash {
			return fmt.Errorf("pack hash %s does not match signed hash %s", h, p.Hash)
		}
		return nil
	}
	return fmt.Errorf("unknown registry key %s", p.KeyID)
}

type Report struct {
	Origin string `json:"origin"`
	Tool   string `json:"tool"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

// Report sends an anonymous counter: site, tool, ok/fail, error class. No parameters.
func (c *Client) Report(ctx context.Context, r Report) error {
	return c.do(ctx, "POST", "/v1/reports", r, nil, false)
}

// rawAndOut decodes one response into two targets.
type rawAndOut struct{ a, b any }

func (r *rawAndOut) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, r.a); err != nil {
		return err
	}
	return json.Unmarshal(b, r.b)
}

// SiteStatus is what the registry says about a site right now.
type SiteStatus struct {
	Origin     string `json:"origin"`
	State      string `json:"state"`   // listed | delisted | expired
	Verdict    string `json:"verdict"` // good | suspicious | bad | ""
	Version    string `json:"version"`
	Hash       string `json:"hash"`
	VerifiedAt string `json:"verifiedAt"`
	Reason     string `json:"reason"`
}

func (c *Client) SiteStatus(ctx context.Context, origin string) (*SiteStatus, error) {
	var out SiteStatus
	err := c.do(ctx, "GET", "/v1/sites/"+url.PathEscape(origin)+"/status", nil, &out, false)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 404 {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}
