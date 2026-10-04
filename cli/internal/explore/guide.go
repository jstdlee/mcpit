package explore

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jstdlee/mcpit/cli/internal/sitepack"
)

// readGuide fetches what a site publishes for crawlers and agents: robots.txt,
// sitemap.xml, llms.txt / llms-full.txt, /.well-known/api-catalog, agent.json,
// ai-plugin.json and mcp.json. It returns the guide, the sitemap URLs, and endpoint
// candidates the guide names, with example parameter values to test them.
func (e *Explorer) readGuide(ctx context.Context) (*sitepack.Guide, []string, []*candidate) {
	g := &sitepack.Guide{}
	var sitemapURLs []string
	var cands []*candidate
	examples := map[string][]string{}

	if b := e.get(ctx, e.origin+"/robots.txt", ""); b != nil {
		g.Robots = &sitepack.GuideDoc{URL: e.origin + "/robots.txt", Text: clip(string(b), 3000)}
		for _, line := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "sitemap":
				g.Sitemaps = appendUnique(g.Sitemaps, v)
			case "llms":
				e.llmsHint = v
			}
		}
	}
	if len(g.Sitemaps) == 0 {
		g.Sitemaps = []string{e.origin + "/sitemap.xml"}
	}
	var found []string
	for _, sm := range g.Sitemaps {
		sitemapURLs = append(sitemapURLs, e.readSitemap(ctx, sm, 0)...)
	}
	if len(sitemapURLs) > 0 {
		found = g.Sitemaps
	}
	g.Sitemaps = found

	llmsURLs := []string{e.origin + "/llms.txt", e.origin + "/llms-full.txt"}
	if e.llmsHint != "" {
		llmsURLs = append([]string{e.abs(e.llmsHint)}, llmsURLs...)
	}
	for _, u := range llmsURLs {
		if b := e.getText(ctx, u); b != "" {
			g.LLMs = &sitepack.GuideDoc{URL: u, Text: clip(b, 8000)}
			cands = append(cands, e.fromGuideText(b, "llms", examples)...)
			break
		}
	}
	if b := e.get(ctx, e.origin+"/.well-known/agent.json", "json"); b != nil {
		g.AgentCard = &sitepack.GuideDoc{URL: e.origin + "/.well-known/agent.json", Text: clip(string(b), 4000)}
		cands = append(cands, e.fromGuideJSON(b, examples)...)
	}
	if b := e.get(ctx, e.origin+"/.well-known/ai-plugin.json", "json"); b != nil {
		g.AIPlugin = &sitepack.GuideDoc{URL: e.origin + "/.well-known/ai-plugin.json", Text: clip(string(b), 3000)}
		var ap struct {
			API struct {
				URL string `json:"url"`
			} `json:"api"`
		}
		if json.Unmarshal(b, &ap) == nil && ap.API.URL != "" {
			if spec := e.get(ctx, e.abs(ap.API.URL), "json"); spec != nil {
				cands = append(cands, e.fromOpenAPI(spec)...)
			}
		}
	}
	for _, p := range []string{"/.well-known/mcp.json", "/.well-known/mcp"} {
		if b := e.get(ctx, e.origin+p, "json"); b != nil {
			g.MCP = &sitepack.GuideDoc{URL: e.origin + p, Text: clip(string(b), 3000)}
			break
		}
	}
	if b := e.get(ctx, e.origin+"/.well-known/api-catalog", ""); b != nil {
		g.APICatalog = &sitepack.GuideDoc{URL: e.origin + "/.well-known/api-catalog", Text: clip(string(b), 3000)}
		// RFC 9727 linkset: follow service-desc links (OpenAPI or a JSON endpoint list).
		var ls struct {
			Linkset []map[string]json.RawMessage `json:"linkset"`
		}
		if json.Unmarshal(b, &ls) == nil {
			for _, entry := range ls.Linkset {
				var descs []struct {
					Href string `json:"href"`
				}
				json.Unmarshal(entry["service-desc"], &descs)
				for _, d := range descs {
					if spec := e.get(ctx, e.abs(d.Href), "json"); spec != nil {
						if cs := e.fromOpenAPI(spec); len(cs) > 0 {
							cands = append(cands, cs...)
						} else {
							cands = append(cands, e.fromGuideJSON(spec, examples)...)
						}
					}
				}
			}
		}
	}
	e.examples = examples
	if g.Robots == nil && g.LLMs == nil && g.AgentCard == nil && g.APICatalog == nil && g.AIPlugin == nil && g.MCP == nil && len(g.Sitemaps) == 0 {
		return nil, sitemapURLs, cands
	}
	return g, sitemapURLs, cands
}

type urlset struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
	Sitemaps []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
}

func (e *Explorer) readSitemap(ctx context.Context, u string, depth int) []string {
	b := e.get(ctx, u, "")
	if b == nil {
		return nil
	}
	var us urlset
	if xml.Unmarshal(b, &us) != nil {
		return nil
	}
	var out []string
	for _, x := range us.URLs {
		if loc := strings.TrimSpace(x.Loc); e.sameOrigin(loc) {
			out = append(out, loc)
		}
		if len(out) >= 500 {
			return out
		}
	}
	if depth == 0 {
		for i, x := range us.Sitemaps {
			if i >= 5 {
				break
			}
			out = append(out, e.readSitemap(ctx, strings.TrimSpace(x.Loc), 1)...)
		}
	}
	return out
}

func (e *Explorer) getText(ctx context.Context, u string) string {
	b := e.get(ctx, u, "")
	s := string(b)
	if b == nil || strings.Contains(strings.ToLower(s[:min(len(s), 200)]), "<html") {
		return "" // SPA fallback page, not a text file
	}
	return s
}

var (
	endpointRe = regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE)\s+(/[A-Za-z0-9_\-./{}:]*)(\?[^\s#)'"` + "`" + `]*)?([^\n]*)`)
	exampleRe  = regexp.MustCompile(`[?&]([A-Za-z_][A-Za-z0-9_]*)=([^&\s)'"` + "`" + `]+)`)
)

// fromGuideText reads endpoint lines such as "GET /api/search?q=...  # by name or code"
// and example queries such as "?q=510123" or "?lat=1.35&lng=103.8".
func (e *Explorer) fromGuideText(text, source string, examples map[string][]string) []*candidate {
	for _, m := range exampleRe.FindAllStringSubmatch(text, -1) {
		if v, _ := url.QueryUnescape(m[2]); v != "" && !strings.Contains(v, "...") && !strings.Contains(v, "{") {
			examples[m[1]] = appendUnique(examples[m[1]], v)
		}
	}
	var out []*candidate
	for _, m := range endpointRe.FindAllStringSubmatch(text, -1) {
		desc := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(m[4]), "#-—:"))
		out = append(out, e.guideCandidate(m[1], m[2], strings.TrimPrefix(m[3], "?"), desc, source))
	}
	return out
}

// fromGuideJSON walks any JSON (agent card, endpoint catalog) for objects with a
// method and a url/path, plus query examples inside strings.
func (e *Explorer) fromGuideJSON(b []byte, examples map[string][]string) []*candidate {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return nil
	}
	var out []*candidate
	var walk func(x any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			method, _ := t["method"].(string)
			path := firstString(t, "url", "path", "endpoint", "href")
			if method != "" && path != "" {
				desc := firstString(t, "description", "summary", "title")
				u := path
				if pu, err := url.Parse(path); err == nil && pu.Host != "" {
					if !e.sameSite(path) {
						goto next
					}
					u = pu.Path
					if pu.RawQuery != "" {
						u += "?" + pu.RawQuery
					}
				}
				p, q, _ := strings.Cut(u, "?")
				out = append(out, e.guideCandidate(strings.ToUpper(method), p, q, desc, "agent"))
			}
		next:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k])
			}
		case []any:
			for _, y := range t {
				walk(y)
			}
		case string:
			for _, m := range exampleRe.FindAllStringSubmatch(t, -1) {
				if val, _ := url.QueryUnescape(m[2]); val != "" && !strings.Contains(val, "...") {
					examples[m[1]] = appendUnique(examples[m[1]], val)
				}
			}
		}
	}
	walk(v)
	return out
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func (e *Explorer) guideCandidate(method, path, query, desc, source string) *candidate {
	path = strings.TrimSuffix(path, ".")
	tpl, pathParams := templatePath(path)
	tpl = regexp.MustCompile(`:([A-Za-z_][A-Za-z0-9_]*)`).ReplaceAllString(tpl, "{$1}") // /stops/:id -> /stops/{id}
	c := &candidate{source: "guide", observed: 1}
	c.tool = sitepack.Tool{
		ID:          toolID(method, tpl, ""),
		Kind:        "api",
		Auth:        "none",
		Executors:   []string{"http"},
		Description: clip(desc, 300),
		Request:     sitepack.Request{Method: method, URL: e.origin + braces(tpl)},
		Output:      sitepack.Output{Type: "json"},
	}
	if method == "GET" {
		c.effectFact = "read"
	} else {
		c.tool.Request.ContentType = "json"
	}
	for _, m := range regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`).FindAllStringSubmatch(tpl, -1) {
		p := c.param(m[1])
		p.in, p.roleFact = "path", "id"
	}
	for _, pp := range pathParams {
		c.param(pp.name).values = []string{pp.value}
	}
	for _, kv := range strings.Split(query, "&") {
		name, val, _ := strings.Cut(kv, "=")
		if name == "" {
			continue
		}
		p := c.param(name)
		p.in = "query"
		if val != "" && !strings.Contains(val, "...") && !strings.Contains(val, "{") {
			p.values = appendUnique(p.values, val)
		} else {
			p.variable = true
		}
	}
	return c
}

// applyExamples gives guide parameters the example values the guide shows, so the
// test calls use realistic input (a postal code, real coordinates).
func (e *Explorer) applyExamples(cands []*candidate) {
	for _, c := range cands {
		for _, p := range c.params {
			if ex := e.examples[p.name]; len(ex) > 0 && len(p.values) == 0 {
				p.values = append(p.values, ex...)
			}
			if len(p.values) == 0 && regexp.MustCompile(`^(q|query|search|keyword|term|text)$`).MatchString(p.name) {
				p.values = []string{e.ProbeText}
			}
			p.numeric = p.numeric || (allNumeric(p.values) && len(p.values) > 0 && p.in != "query")
		}
	}
}

// geoHint returns example coordinates from the guide, if any.
func (e *Explorer) geoHint() ([2]float64, bool) {
	lat := firstFloat(e.examples["lat"], e.examples["latitude"])
	lng := firstFloat(e.examples["lng"], e.examples["lon"], e.examples["longitude"])
	if lat == nil || lng == nil {
		return [2]float64{}, false
	}
	return [2]float64{*lat, *lng}, true
}

func firstFloat(lists ...[]string) *float64 {
	for _, l := range lists {
		for _, v := range l {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return &f
			}
		}
	}
	return nil
}

// buildPages makes the site map: crawled pages with their heading or title, then
// sitemap URLs. The category is the first path segment.
func buildPages(pages []*PageResult, sitemap []string) []sitepack.Page {
	// On single-page apps every page shares one <title>; such a title says nothing.
	count := map[string]int{}
	for _, p := range pages {
		count[p.Title]++
	}
	generic := func(t string) bool { return len(pages) > 2 && count[t]*2 > len(pages) }
	seen := map[string]bool{}
	var out []sitepack.Page
	add := func(raw, title, source string) {
		u, err := url.Parse(raw)
		if err != nil || !isPageURL(raw) {
			return
		}
		path := u.Path
		if path == "" {
			path = "/"
		}
		if u.RawQuery != "" {
			path += "?" + u.RawQuery
		}
		if seen[path] || len(out) >= 500 {
			return
		}
		seen[path] = true
		tpl, _ := templatePath(u.Path)
		cat := strings.SplitN(strings.Trim(tpl, "/"), "/", 2)[0]
		switch {
		case cat == "":
			cat = "home"
		case strings.HasPrefix(cat, "{"):
			cat = "detail" // /77021 -> one item page
		}
		if title == "" {
			title = titleFromPath(u.Path)
		}
		out = append(out, sitepack.Page{Path: path, Title: clip(title, 120), Category: cat, Source: source})
	}
	for _, p := range pages {
		title := p.Title
		if title == "" || generic(title) {
			if p.Heading != "" && !generic(p.Heading) && p.Heading != p.Title {
				title = p.Heading
			} else if u, _ := url.Parse(p.URL); u != nil && strings.Trim(u.Path, "/") != "" {
				title = titleFromPath(u.Path)
			}
		}
		add(p.URL, title, "crawl")
	}
	for _, s := range sitemap {
		add(s, "", "sitemap")
	}
	return out
}

func titleFromPath(p string) string {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	last := segs[len(segs)-1]
	if last == "" {
		return "Home"
	}
	last = strings.NewReplacer("-", " ", "_", " ").Replace(last)
	return strings.ToUpper(last[:1]) + last[1:]
}
