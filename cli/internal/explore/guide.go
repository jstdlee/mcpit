package explore

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"

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

	// robots.txt (RFC 9309): sitemaps and the non-standard "Llms:" pointer.
	e.readRobots(ctx)
	if e.robots != nil {
		g.Robots = &sitepack.GuideDoc{URL: e.origin + "/robots.txt", Text: clip(e.robotsTxt, 3000)}
		g.Sitemaps = append(g.Sitemaps, e.robots.Sitemaps...)
		e.llmsHint = e.robots.Extra["llms"]
	}
	// sitemaps.org protocol: urlset, sitemap index, gzip.
	if len(g.Sitemaps) == 0 {
		g.Sitemaps = []string{e.origin + "/sitemap.xml", e.origin + "/sitemap_index.xml"}
	}
	var found []string
	for _, sm := range g.Sitemaps {
		if urls := e.readSitemap(ctx, sm, 0); len(urls) > 0 {
			sitemapURLs = append(sitemapURLs, urls...)
			found = append(found, sm)
		}
	}
	g.Sitemaps = found

	// Home page head: meta, canonical, Open Graph, JSON-LD (schema.org), feed links.
	feedLinks := e.readHead(ctx, g, &cands)
	// RSS 2.0 / Atom feeds: announced links first, then common paths.
	for _, fu := range append(feedLinks, e.origin+"/rss.xml", e.origin+"/feed.xml", e.origin+"/atom.xml", e.origin+"/feed", e.origin+"/index.xml") {
		if len(g.Feeds) >= 3 {
			break
		}
		if f, items := e.readFeed(ctx, fu); f != nil && !hasFeed(g.Feeds, f.URL) {
			g.Feeds = append(g.Feeds, *f)
			e.feedItems = append(e.feedItems, items...)
		}
	}
	// RFC 9116 security.txt.
	if t := e.getText(ctx, e.origin+"/.well-known/security.txt"); strings.Contains(strings.ToLower(t), "contact:") {
		g.SecurityTxt = &sitepack.GuideDoc{URL: e.origin + "/.well-known/security.txt", Text: clip(t, 2000)}
	}

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
	if g.Robots == nil && g.LLMs == nil && g.AgentCard == nil && g.APICatalog == nil && g.AIPlugin == nil && g.MCP == nil &&
		g.JSONLD == nil && g.SecurityTxt == nil && len(g.Meta) == 0 && len(g.Feeds) == 0 && len(g.Sitemaps) == 0 {
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
	if len(b) > 2 && b[0] == 0x1f && b[1] == 0x8b { // sitemap.xml.gz
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil
		}
		b, _ = io.ReadAll(io.LimitReader(zr, 50<<20))
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
func buildPages(pages []*PageResult, sitemap []string, feed []feedItem) []sitepack.Page {
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
	for i, it := range feed {
		if i >= 50 {
			break
		}
		add(it.URL, it.Title, "feed")
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

// readHead reads the home page <head>: meta description, canonical, lang, Open Graph and
// Twitter tags, schema.org JSON-LD (a WebSite SearchAction becomes a search tool), feed
// links and the llms link. It returns the announced feed URLs.
func (e *Explorer) readHead(ctx context.Context, g *sitepack.Guide, cands *[]*candidate) []string {
	b := e.get(ctx, e.origin+"/", "")
	if b == nil {
		return nil
	}
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	meta := map[string]string{}
	var feeds []string
	var jsonld []string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "html":
				if l := attr(n, "lang"); l != "" {
					meta["lang"] = l
				}
			case "meta":
				key := attr(n, "name")
				if key == "" {
					key = attr(n, "property")
				}
				key = strings.ToLower(key)
				if c := attr(n, "content"); c != "" && (key == "description" || key == "keywords" || strings.HasPrefix(key, "og:") || strings.HasPrefix(key, "twitter:")) {
					if len(meta) < 30 {
						meta[key] = clip(c, 300)
					}
				}
			case "link":
				rel := strings.ToLower(attr(n, "rel"))
				href := attr(n, "href")
				switch {
				case rel == "canonical":
					meta["canonical"] = clip(e.abs(href), 300)
				case rel == "alternate" && (strings.Contains(attr(n, "type"), "rss") || strings.Contains(attr(n, "type"), "atom")):
					if u := e.abs(href); e.sameSite(u) {
						feeds = append(feeds, u)
					}
				case rel == "llms" && e.llmsHint == "":
					e.llmsHint = href
				}
			case "script":
				if strings.Contains(attr(n, "type"), "ld+json") && n.FirstChild != nil {
					jsonld = append(jsonld, strings.TrimSpace(n.FirstChild.Data))
				}
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(doc)
	if len(meta) > 0 {
		g.Meta = meta
		g.Description = meta["description"]
	}
	if len(jsonld) > 0 {
		all := strings.Join(jsonld, "\n")
		g.JSONLD = &sitepack.GuideDoc{URL: e.origin + "/", Text: clip(all, 6000)}
		for _, block := range jsonld {
			if c := e.searchActionTool(block); c != nil {
				*cands = append(*cands, c)
			}
		}
	}
	return feeds
}

// searchActionTool turns a schema.org WebSite potentialAction SearchAction into a tool.
func (e *Explorer) searchActionTool(block string) *candidate {
	var v any
	if json.Unmarshal([]byte(block), &v) != nil {
		return nil
	}
	var tpl string
	var find func(x any)
	find = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			if typ, _ := t["@type"].(string); typ == "SearchAction" {
				switch tg := t["target"].(type) {
				case string:
					tpl = tg
				case map[string]any:
					tpl, _ = tg["urlTemplate"].(string)
				}
			}
			for _, y := range t {
				if tpl == "" {
					find(y)
				}
			}
		case []any:
			for _, y := range t {
				if tpl == "" {
					find(y)
				}
			}
		}
	}
	find(v)
	if tpl == "" || !e.sameSite(tpl) {
		return nil
	}
	u, err := url.Parse(strings.NewReplacer("{", "MCPITL", "}", "MCPITR").Replace(tpl))
	if err != nil {
		return nil
	}
	c := &candidate{source: "guide", observed: 1, effectFact: "read", formKind: "search"}
	c.tool = sitepack.Tool{ID: "search", Kind: "search", Auth: "none", Executors: []string{"http"},
		Description: "Search the site (schema.org SearchAction).",
		Request:     sitepack.Request{Method: "GET", URL: u.Scheme + "://" + u.Host + u.Path}, Output: sitepack.Output{Type: "html"}}
	for name, vals := range u.Query() {
		p := c.param(name)
		p.in = "query"
		if len(vals) > 0 && strings.HasPrefix(vals[0], "MCPITL") {
			p.roleFact = "query"
			p.values = []string{e.ProbeText}
		} else if len(vals) > 0 {
			p.roleFact, p.values = "const", vals
		}
	}
	return c
}

type rssDoc struct {
	XMLName xml.Name
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title string `xml:"title"`
			Link  string `xml:"link"`
		} `xml:"item"`
	} `xml:"channel"`
	// Atom
	Title   string `xml:"title"`
	Entries []struct {
		Title string `xml:"title"`
		Links []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

type feedItem struct{ URL, Title string }

// readFeed parses an RSS 2.0 or Atom feed.
func (e *Explorer) readFeed(ctx context.Context, u string) (*sitepack.Feed, []feedItem) {
	b := e.get(ctx, u, "")
	if b == nil {
		return nil, nil
	}
	var d rssDoc
	if xml.Unmarshal(b, &d) != nil {
		return nil, nil
	}
	var items []feedItem
	switch d.XMLName.Local {
	case "rss", "RDF":
		for _, it := range d.Channel.Items {
			if l := strings.TrimSpace(it.Link); e.sameOrigin(l) {
				items = append(items, feedItem{l, strings.TrimSpace(it.Title)})
			}
		}
		return &sitepack.Feed{URL: u, Title: clip(strings.TrimSpace(d.Channel.Title), 120), Items: len(items)}, items
	case "feed":
		for _, en := range d.Entries {
			for _, l := range en.Links {
				if (l.Rel == "" || l.Rel == "alternate") && e.sameOrigin(strings.TrimSpace(l.Href)) {
					items = append(items, feedItem{strings.TrimSpace(l.Href), strings.TrimSpace(en.Title)})
					break
				}
			}
		}
		return &sitepack.Feed{URL: u, Title: clip(strings.TrimSpace(d.Title), 120), Items: len(items)}, items
	}
	return nil, nil
}

func hasFeed(fs []sitepack.Feed, u string) bool {
	for _, f := range fs {
		if f.URL == u {
			return true
		}
	}
	return false
}

func attr(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

// fromPageShapes turns families of pages that share a shape (/project/httpx/,
// /project/requests/ …) into read tools: GET /project/{name}/ returns the page text.
// Members come from crawled pages and sitemap URLs.
func (e *Explorer) fromPageShapes(pages []*PageResult, sitemap []string) []*candidate {
	type member struct {
		segs  []string
		path  string
		title string
		slash bool
	}
	groups := map[string][]member{}
	seen := map[string]bool{}
	add := func(raw, title string) {
		u, err := url.Parse(raw)
		if err != nil || u.RawQuery != "" || !e.sameOrigin(raw) || !isPageURL(raw) || seen[u.Path] {
			return
		}
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segs) < 2 || segs[0] == "" {
			return
		}
		seen[u.Path] = true
		slash := strings.HasSuffix(u.Path, "/")
		key := fmt.Sprintf("%s/%d/%t", segs[0], len(segs), slash)
		groups[key] = append(groups[key], member{segs, u.Path, title, slash})
	}
	for _, p := range pages {
		add(p.URL, p.Title)
	}
	for _, s := range sitemap {
		add(s, "")
	}
	keys := make([]string, 0, len(groups))
	for k, g := range groups {
		if len(g) >= 2 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return len(groups[keys[i]]) > len(groups[keys[j]]) })
	var out []*candidate
	for _, k := range keys {
		if len(out) >= 6 {
			break
		}
		g := groups[k]
		first := g[0]
		var vary []int
		for i := 1; i < len(first.segs); i++ {
			for _, m := range g[1:] {
				if m.segs[i] != first.segs[i] {
					vary = append(vary, i)
					break
				}
			}
		}
		if len(vary) == 0 || len(vary) > 2 {
			continue
		}
		tpl := append([]string(nil), first.segs...)
		c := &candidate{source: "page", observed: len(g), effectFact: "read"}
		var names []string
		for n, i := range vary {
			vals := []string{}
			for _, m := range g {
				vals = appendUnique(vals, m.segs[i])
			}
			name := "name"
			if allNumeric(vals) {
				name = "id"
			}
			if n > 0 {
				name += "2"
			}
			names = append(names, name)
			tpl[i] = "{{" + name + "}}"
			p := c.param(name)
			p.in, p.roleFact, p.required, p.values = "path", "id", true, vals[:1]
		}
		path := "/" + strings.Join(tpl, "/")
		if first.slash {
			path += "/"
		}
		example := first.path
		desc := fmt.Sprintf("Read a %s page by %s (example: %s", first.segs[0], strings.Join(names, " and "), example)
		if first.title != "" {
			desc += "; title: " + clip(first.title, 60)
		}
		desc += "); returns the page text."
		c.tool = sitepack.Tool{ID: safeID(first.segs[0] + "_page"), Kind: "read", Auth: "none", Executors: []string{"http"},
			Description: clip(desc, 300), Request: sitepack.Request{Method: "GET", URL: e.origin + path}, Output: sitepack.Output{Type: "html"}}
		out = append(out, c)
	}
	return out
}
