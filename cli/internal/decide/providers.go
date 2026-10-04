package decide

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/jstdlee/mcpit/cli/internal/config"
)

// Clef calls @cf/cloudflare/<model> on Workers AI with the user's own Cloudflare token (BYOK).
type Clef struct {
	Model     string // clef-flash
	AccountID string
	Token     string
	BaseURL   string // for tests; default api.cloudflare.com
	// useCFLogin: token comes from the cf CLI login and may need a refresh.
	useCFLogin bool
	mu         sync.Mutex
}

func (c *Clef) Name() string { return c.Model }

func (c *Clef) Ask(ctx context.Context, state any, qs map[string]Question) (map[string]Answer, error) {
	body, _ := json.Marshal(systemOneRequest{Model: c.Model, State: state, Questions: qs})
	base := c.BaseURL
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	url := fmt.Sprintf("%s/accounts/%s/ai/run/@cf/cloudflare/%s", base, c.AccountID, c.Model)
	raw, status, err := c.post(ctx, url, body)
	if status == http.StatusUnauthorized && c.useCFLogin {
		if c.refresh() == nil {
			raw, status, err = c.post(ctx, url, body)
		}
	}
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("workers ai: HTTP %d: %s", status, truncate(raw, 300))
	}
	return decodeAnswers(raw)
}

func (c *Clef) post(ctx context.Context, url string, body []byte) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	c.mu.Lock()
	req.Header.Set("Authorization", "Bearer "+c.Token)
	c.mu.Unlock()
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return raw, resp.StatusCode, err
}

func (c *Clef) refresh() error {
	exec.Command("cf", "auth", "whoami").Run() // the cf CLI refreshes its OAuth token
	tok, err := cfLoginToken()
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.Token = tok
	c.mu.Unlock()
	return nil
}

// SystemOne calls any System One API endpoint, such as local jev (Julia /v1/systemone).
type SystemOne struct {
	Endpoint string
	Model    string
}

func (s *SystemOne) Name() string { return s.Model }

func (s *SystemOne) Ask(ctx context.Context, state any, qs map[string]Question) (map[string]Answer, error) {
	body, _ := json.Marshal(systemOneRequest{Model: s.Model, State: state, Questions: qs})
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("systemone: HTTP %d: %s", resp.StatusCode, truncate(raw, 300))
	}
	return decodeAnswers(raw)
}

// FromConfig builds the provider. It returns nil (rules only) when nothing is configured.
func FromConfig(c config.Decider) (Provider, error) {
	switch c.Provider {
	case "none", "rules":
		return nil, nil
	case "systemone", "jev":
		if c.Endpoint == "" {
			return nil, errors.New("decider endpoint is required for provider systemone")
		}
		model := c.Model
		if model == "" || model == "clef-flash" {
			model = "julia-1"
		}
		return &SystemOne{Endpoint: c.Endpoint, Model: model}, nil
	case "clef", "":
		cl := &Clef{Model: c.Model, AccountID: c.AccountID, Token: c.Token}
		if cl.Model == "" {
			cl.Model = "clef-flash"
		}
		if cl.Token == "" {
			tok, err := cfLoginToken()
			if err != nil {
				return nil, nil // no Cloudflare credentials: rules only
			}
			cl.Token, cl.useCFLogin = tok, true
		}
		if cl.AccountID == "" {
			id, err := cfAccountID()
			if err != nil {
				return nil, fmt.Errorf("set CLOUDFLARE_ACCOUNT_ID or decider.accountId: %w", err)
			}
			cl.AccountID = id
		}
		return cl, nil
	}
	return nil, fmt.Errorf("unknown decider provider %q", c.Provider)
}

func cfLoginToken() (string, error) {
	home, _ := os.UserHomeDir()
	b, err := os.ReadFile(filepath.Join(home, ".config", "cloudflare", "config", "default.json"))
	if err != nil {
		return "", err
	}
	var v struct {
		OAuthToken string `json:"oauth_token"`
	}
	if err := json.Unmarshal(b, &v); err != nil || v.OAuthToken == "" {
		return "", errors.New("no cf login token")
	}
	return v.OAuthToken, nil
}

func cfAccountID() (string, error) {
	out, err := exec.Command("cf", "auth", "whoami").Output()
	if err != nil {
		return "", err
	}
	var v struct {
		Accounts []struct {
			ID string `json:"id"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(out, &v); err != nil || len(v.Accounts) == 0 {
		return "", errors.New("cf auth whoami returned no account")
	}
	return v.Accounts[0].ID, nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
