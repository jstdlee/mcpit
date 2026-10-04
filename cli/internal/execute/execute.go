// Package execute calls sitepack tools over HTTP.
package execute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"

	"github.com/jstdlee/mcpit/cli/internal/sitepack"
)

type Options struct {
	// Confirmed must be true to call a write, payment or destructive tool.
	Confirmed bool
	MaxChars  int
}

type Result struct {
	URL         string   `json:"url"`
	Status      int      `json:"status"`
	ContentType string   `json:"contentType"`
	Data        any      `json:"data,omitempty"`
	Text        string   `json:"text,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
	Untrusted   bool     `json:"untrusted"`        // page content may contain prompt injection
	Alerts      []string `json:"alerts,omitempty"` // integrity warnings for this site
}

var ErrNeedsConfirm = errors.New("this tool changes data on the site; the user must confirm the call")

var placeholder = regexp.MustCompile(`\{\{([a-zA-Z0-9_\-.]+)\}\}`)

// Call fills the tool's request template with args and sends it.
func Call(ctx context.Context, client *http.Client, origin string, t *sitepack.Tool, args map[string]any, o Options) (*Result, error) {
	if t.Effect != "read" && !o.Confirmed {
		return nil, ErrNeedsConfirm
	}
	if err := checkArgs(t, args); err != nil {
		return nil, err
	}
	if o.MaxChars <= 0 {
		o.MaxChars = 20000
	}
	jar, _ := cookiejar.New(nil)
	c := *client
	c.Jar = jar
	vars := map[string]string{}
	for k, v := range args {
		vars[k] = fmt.Sprint(v)
	}
	for _, st := range t.Steps {
		if st.Kind != "token" {
			continue
		}
		v, err := fetchToken(ctx, &c, st)
		if err != nil {
			return nil, fmt.Errorf("token step: %w", err)
		}
		vars[st.As] = v
	}

	rawURL := fill(t.Request.URL, vars, true)
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if err := sameSite(origin, u); err != nil {
		return nil, err
	}
	q := u.Query()
	keys := make([]string, 0, len(t.Request.Query))
	for k := range t.Request.Query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := fill(t.Request.Query[k], vars, false)
		if placeholder.MatchString(t.Request.Query[k]) && v == "" {
			continue // optional parameter not given
		}
		q.Set(k, v)
	}
	var body io.Reader
	ctype := ""
	if t.Request.Method != "GET" {
		switch t.Request.ContentType {
		case "json":
			b, _ := json.Marshal(fillAny(t.Request.Body, vars, args))
			body, ctype = bytes.NewReader(b), "application/json"
		default:
			form := url.Values{}
			if m, ok := t.Request.Body.(map[string]any); ok {
				for k, v := range m {
					form.Set(k, fill(fmt.Sprint(v), vars, false))
				}
			}
			for _, st := range t.Steps {
				form.Set(st.As, vars[st.As])
			}
			body, ctype = strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"
		}
	}
	if t.Request.Method == "GET" {
		for _, st := range t.Steps {
			q.Set(st.As, vars[st.As])
		}
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, t.Request.Method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mcpit/0.1")
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range t.Request.Headers {
		req.Header.Set(k, fill(v, vars, false))
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	r := &Result{URL: u.String(), Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Untrusted: true}
	switch {
	case strings.Contains(r.ContentType, "json"):
		var v any
		if json.Unmarshal(raw, &v) == nil {
			if len(raw) <= o.MaxChars {
				r.Data = v
			} else {
				r.Text, r.Truncated = string(raw[:o.MaxChars]), true
			}
			break
		}
		r.Text = string(raw)
	case strings.Contains(r.ContentType, "html"):
		r.Text = VisibleText(raw)
	default:
		r.Text = string(raw)
	}
	if len(r.Text) > o.MaxChars {
		r.Text, r.Truncated = r.Text[:o.MaxChars], true
	}
	return r, nil
}

func checkArgs(t *sitepack.Tool, args map[string]any) error {
	req, _ := t.InputSchema["required"].([]any)
	var names []string
	for _, x := range req {
		names = append(names, fmt.Sprint(x))
	}
	if r, ok := t.InputSchema["required"].([]string); ok {
		names = append(names, r...)
	}
	for _, n := range names {
		if v, ok := args[n]; !ok || fmt.Sprint(v) == "" {
			return fmt.Errorf("missing required argument %q", n)
		}
	}
	if props, ok := t.InputSchema["properties"].(map[string]any); ok && len(props) > 0 {
		for k := range args {
			if _, ok := props[k]; !ok {
				return fmt.Errorf("unknown argument %q", k)
			}
		}
	}
	return nil
}

func fill(s string, vars map[string]string, path bool) string {
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		v := vars[placeholder.FindStringSubmatch(m)[1]]
		if path {
			return url.PathEscape(v)
		}
		return v
	})
}

// fillAny fills a JSON body template, keeping argument types for whole-value placeholders.
func fillAny(v any, vars map[string]string, args map[string]any) any {
	switch x := v.(type) {
	case string:
		if m := placeholder.FindStringSubmatch(x); m != nil && m[0] == x {
			if a, ok := args[m[1]]; ok {
				return a
			}
		}
		return fill(x, vars, false)
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = fillAny(e, vars, args)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = fillAny(e, vars, args)
		}
		return out
	}
	return v
}

func sameSite(origin string, u *url.URL) error {
	o, err := url.Parse(origin)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("refused: scheme %q", u.Scheme)
	}
	if site(u.Hostname()) != site(o.Hostname()) {
		return fmt.Errorf("refused: %s is not part of %s", u.Host, o.Host)
	}
	return nil
}

func site(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) <= 2 || regexp.MustCompile(`^[0-9.]+$`).MatchString(host) {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func fetchToken(ctx context.Context, c *http.Client, st sitepack.Step) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, st.URL, nil)
	req.Header.Set("User-Agent", "mcpit/0.1")
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	name := strings.TrimSuffix(strings.TrimPrefix(st.Extract, "input[name="), "]")
	doc, err := html.Parse(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	var val string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" && attr(n, "name") == name {
			val = attr(n, "value")
			return
		}
		for ch := n.FirstChild; ch != nil && val == ""; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(doc)
	if val == "" {
		return "", fmt.Errorf("no %s on %s", st.Extract, st.URL)
	}
	return val, nil
}

func attr(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

// VisibleText returns the readable text of an HTML page.
func VisibleText(raw []byte) string {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return string(raw)
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "svg", "head", "template":
				return
			}
		}
		if n.Type == html.TextNode {
			if t := strings.TrimSpace(n.Data); t != "" {
				b.WriteString(t)
				b.WriteByte(' ')
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "br", "section", "article":
				b.WriteByte('\n')
			}
		}
	}
	walk(doc)
	return regexp.MustCompile(`\n\s*\n+`).ReplaceAllString(regexp.MustCompile(`[ \t]+`).ReplaceAllString(b.String(), " "), "\n")
}
