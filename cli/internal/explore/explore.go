// Package explore turns a website into a sitepack. It reads declared specs, then
// visits the given page (and, on request, its entry pages) with a headless browser over the Chrome DevTools
// Protocol, captures the DOM and the network log, and lets the decision model
// sort requests, forms, parameters and effects.
package explore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jstdlee/mcpit/cli/internal/decide"
	"github.com/jstdlee/mcpit/cli/internal/execute"
	"github.com/jstdlee/mcpit/cli/internal/sitepack"
)

type Options struct {
	Depth      int // page levels: 1 = the URL only (default), 2 = also the entry pages it links to
	MaxPages   int
	ChromePath string
	NoBrowser  bool // declared specs and static HTML only
	Verify     bool
	Progress   func(string)
	Dump       string // write the raw capture (pages, forms, requests, kinds) to this JSON file
}

type Explorer struct {
	D          *decide.Decider
	Opts       Options
	ProbeText  string
	HTTP       *http.Client
	origin     string
	robots     *Robots // robots.txt, RFC 9309
	robotsTxt  string
	llmsHint   string
	examples   map[string][]string // parameter examples from the guide
	sitemap    []string
	feedItems  []feedItem
	altArgs    map[string][]map[string]any // more example arguments per tool id
	patterns   []*Pattern                  // path families of the site (items or sections)
	declTpls   []string                    // URL templates from declared specs
	linkPool   []string                    // level-2 entry and item links of the start page (visited or not)
	linkText   map[string]string           // link text per URL (for link.next)
	navPrefix  map[string]bool             // first segments linked from nav, header, footer or aside: site sections
	cardPrefix map[string]bool             // one-segment links inside repeated cards (/owner avatars)
}

func New(d *decide.Decider, o Options) *Explorer {
	if o.Depth < 1 || o.Depth > 2 {
		o.Depth = 1
	}
	if o.MaxPages <= 0 {
		o.MaxPages = 15
	}
	return &Explorer{D: d, Opts: o, ProbeText: "test", HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (e *Explorer) say(f string, a ...any) {
	if e.Opts.Progress != nil {
		e.Opts.Progress(fmt.Sprintf(f, a...))
	}
}

// Run explores seed and returns a sitepack.
func (e *Explorer) Run(ctx context.Context, seed string) (*sitepack.Pack, error) {
	origin, err := sitepack.OriginOf(seed)
	if err != nil {
		return nil, err
	}
	e.origin = origin
	pack := &sitepack.Pack{Schema: sitepack.Schema, Origin: origin}

	e.say("reading declared specs and the site guide")
	cands := e.declared(ctx)
	guide, sitemapURLs, guideCands := e.readGuide(ctx)
	e.sitemap = sitemapURLs
	if guide != nil {
		e.say("guide: robots=%t llms=%t agent=%t catalog=%t sitemap=%d urls, %d endpoints named", guide.Robots != nil, guide.LLMs != nil, guide.AgentCard != nil, guide.APICatalog != nil, len(sitemapURLs), len(guideCands))
	}
	cands = append(cands, guideCands...)

	var pages []*PageResult
	if !e.Opts.NoBrowser {
		pages, err = e.crawl(ctx, seed)
		if err != nil && len(pages) == 0 {
			if len(cands) == 0 {
				return nil, err
			}
			e.say("crawl failed (%v); keeping %d tools from published specs", err, len(cands))
		}
	}

	e.markBlocked(ctx, pages)
	var all []string
	for _, p := range pages {
		if !p.Blocked {
			all = append(all, p.URL)
		}
	}
	e.patterns = e.detectPatterns(ctx, append(append(all, e.linkPool...), e.sitemap...))
	for i, p := range e.patterns {
		if i == 10 {
			e.say("  ... %d more path families", len(e.patterns)-10)
			break
		}
		e.say("pattern %s: %d pages, %s", p.Template, p.Count, p.Kind)
	}
	for _, c := range cands {
		if c.source == "openapi" || c.source == "guide" {
			e.declTpls = append(e.declTpls, c.tool.Request.URL)
		}
	}

	// Requests: rules settle assets; the decision model sorts the rest, one batch per page.
	var apis []*NetEntry
	type dumpReq struct {
		*NetEntry
		Kind string `json:"kind"`
	}
	type dumpPage struct {
		URL     string     `json:"url"`
		Title   string     `json:"title"`
		DOMSize int        `json:"domSize"`
		Links   []string   `json:"links"`
		LinkInf []LinkInfo `json:"linkInfo"`
		Forms   []Form     `json:"forms"`
		Search  []string   `json:"searchInputs"`
		Net     []dumpReq  `json:"requests"`
	}
	var dump []dumpPage
	for _, p := range pages {
		kinds := e.classifyRequests(ctx, p.URL, p.Net)
		dp := dumpPage{URL: p.URL, Title: p.Title, DOMSize: p.DOMSize, Links: p.Links, LinkInf: p.LinkInfo, Forms: p.Forms, Search: p.Search}
		for _, n := range p.Net {
			dp.Net = append(dp.Net, dumpReq{n, kinds[n]})
		}
		dump = append(dump, dp)
		for _, n := range p.Net {
			if kinds[n] == "api" && e.sameSite(n.URL) && n.Status < 400 {
				apis = append(apis, n)
			}
		}
	}
	cands = append(cands, e.fromNetwork(ctx, apis)...)
	if e.Opts.Dump != "" {
		if b, err := json.MarshalIndent(dump, "", "  "); err == nil {
			os.WriteFile(e.Opts.Dump, b, 0o644)
		}
	}

	// Forms from the DOM tree.
	var forms []Form
	seenForm := map[string]bool{}
	for _, p := range pages {
		if p.Blocked {
			continue // forms on a browser-check page belong to the check, not the site
		}
		for _, f := range p.Forms {
			key := f.Method + " " + stripQuery(f.Action) + " " + fieldNames(f.Fields)
			if !seenForm[key] && e.sameSite(f.Action) {
				seenForm[key] = true
				forms = append(forms, f)
			}
		}
	}
	formKinds := e.classifyForms(ctx, forms)
	for i, f := range forms {
		if c := e.fromForm(f, formKinds[i]); c != nil {
			cands = append(cands, c)
		}
	}

	cands = append(cands, e.fromPageShapes(pages, e.sitemap)...)
	cands = dedupe(cands)
	e.applyExamples(cands)
	cands = e.decideUseful(ctx, cands)
	e.decideParamRoles(ctx, cands)
	e.decideEffects(ctx, cands)

	ids := map[string]int{}
	e.altArgs = map[string][]map[string]any{}
	for _, c := range cands {
		c.finish(e.ProbeText)
		t := c.tool
		t.ID = safeID(t.ID)
		base := t.ID
		if n := ids[base]; n > 0 {
			t.ID = fmt.Sprintf("%s_%d", base, n+1)
		}
		ids[base]++
		if alts := c.altProbes(); len(alts) > 0 {
			e.altArgs[t.ID] = alts
		}
		pack.Tools = append(pack.Tools, t)
	}

	if e.Opts.Verify {
		pack.Tools = e.verify(ctx, origin, pack.Tools)
	}
	pack.Fingerprint = fingerprint(pages, pack.Tools)
	pack.Pages = buildPages(pages, e.linkPool, e.linkText, e.sitemap, e.feedItems, e.patterns)
	if guide != nil {
		for _, p := range pages {
			if p.MetaDesc != "" && guide.Description == "" {
				guide.Description = p.MetaDesc
				break
			}
		}
		pack.Guide = guide
	}
	pack.Provenance = &sitepack.Provenance{Client: "mcpit/0.1", Decisions: "rules+" + e.D.Model(), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if len(pack.Tools) == 0 && pack.Guide == nil {
		return pack, fmt.Errorf("no tools found on %s", origin)
	}
	return pack, pack.Validate()
}

// crawl visits the URL and sorts its links; with Depth 2 it also visits the entry pages.
func (e *Explorer) crawl(ctx context.Context, seed string) ([]*PageResult, error) {
	cap := NewCapturer(ctx, e.Opts.ChromePath)
	defer cap.Close()
	cap.ProbeValues = e.probeValues()
	if g, ok := e.geoHint(); ok {
		cap.Geo = g
	}
	if e.D.Available() {
		cap.PickButtons = e.pickButtons
	} else {
		cap.PickButtons = func(_ context.Context, _ string, bs []Button) []string {
			var out []string
			for _, b := range bs {
				switch ruleButtonKind(b) {
				case "tab", "location", "more":
					out = append(out, b.Sel)
				}
			}
			return out
		}
	}
	var pages []*PageResult
	var firstErr error
	visit := func(level int, u, why string) *PageResult {
		if len(pages) >= e.Opts.MaxPages || ctx.Err() != nil || !e.allowed(u) {
			return nil
		}
		e.say("level %d%s: %s", level, why, u)
		p, err := cap.Visit(ctx, u, true)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			e.say("  failed: %v", err)
			return nil
		}
		pages = append(pages, p)
		return p
	}
	// Level 1: the URL itself.
	start := visit(1, seed, "")
	// The links of the URL (and the sitemap) are sorted by link.kind for the site map: entry
	// pages, item families and list pages. By default (Depth 1) none of them is opened: a big
	// site (Hugging Face) has hundreds of entries, and each one is a separate explore.
	// With Depth 2, entry pages are visited, then one item sample; list pages never.
	var infos []LinkInfo
	if start != nil {
		infos = start.LinkInfo
		if len(infos) == 0 {
			for _, l := range start.Links {
				infos = append(infos, LinkInfo{URL: l, Region: "main"})
			}
		}
	}
	plan := e.planLinks(ctx, seed, infos, e.sitemap)
	e.linkPool = append(e.linkPool, plan.Entries...)
	e.linkPool = append(e.linkPool, plan.AllItem...)
	e.linkText = plan.Text
	e.say("links: %d entry pages, %d item families, %d list pages, %d sitemap URLs listed only", len(plan.Entries), len(plan.Items), len(plan.Pages), len(plan.Listed))
	if e.Opts.Depth < 2 {
		return pages, firstErr
	}
	entries := plan.Entries
	if budget := e.Opts.MaxPages - len(pages); len(entries) > budget && budget > 0 {
		entries = e.rankLinks(ctx, entries)[:budget]
	}
	for _, u := range entries {
		visit(2, u, " entry")
	}
	// One item sample shows the detail page and its API (/api/products/{id}).
	if len(plan.Items) > 0 {
		if len(pages) < e.Opts.MaxPages {
			visit(2, plan.Items[0], " item sample")
		}
		if n := len(plan.Items) - 1; n > 0 {
			e.say("level 2: %d more item families skipped", n)
		}
	}
	return pages, firstErr
}

// candidate is a tool under construction.
type candidate struct {
	tool          sitepack.Tool
	params        []*param
	observed      int
	effectFact    string
	formKind      string
	sampleBody    string
	samplePreview string
	source        string
	key           string
}

type param struct {
	name     string
	in       string // query | path | body | form
	values   []string
	role     string
	roleFact string
	required bool
	options  []string
	numeric  bool
	variable bool // the guide shows it as a placeholder (name=...)
}

func (c *candidate) param(name string) *param {
	for _, p := range c.params {
		if p.name == name {
			return p
		}
	}
	p := &param{name: name}
	c.params = append(c.params, p)
	return p
}

func (c *candidate) paramNames() []string {
	var n []string
	for _, p := range c.params {
		n = append(n, p.name)
	}
	return n
}

// finish turns parameters with roles into the request template and input schema.
func (c *candidate) finish(probe string) {
	props := map[string]any{}
	var required []string
	query := map[string]string{}
	body := map[string]any{}
	probeArgs := map[string]any{}
	for _, p := range c.params {
		switch p.role {
		case "tracking":
			continue
		case "token":
			// Tokens are fetched at call time by a token step (forms) or dropped (APIs).
			continue
		case "const":
			if len(p.values) > 0 {
				setParam(p, query, body, p.values[0])
			}
			continue
		}
		prop := map[string]any{"type": "string"}
		if p.numeric {
			prop["type"] = "integer"
		}
		if len(p.options) > 0 {
			prop["enum"] = p.options
		}
		if p.role == "page" {
			prop["description"] = "Page number or offset."
		}
		if p.role == "query" {
			prop["description"] = "Search text."
		}
		props[p.name] = prop
		if p.required || p.role == "query" || p.in == "path" {
			required = append(required, p.name)
		}
		setParam(p, query, body, "{{"+p.name+"}}")
		if len(p.values) > 0 {
			v := p.values[0]
			if p.role == "query" {
				v = probe
			}
			if p.numeric {
				if n, err := strconv.Atoi(v); err == nil {
					probeArgs[p.name] = n
					continue
				}
			}
			probeArgs[p.name] = v
		}
	}
	sort.Strings(required)
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	c.tool.InputSchema = schema
	if len(query) > 0 {
		c.tool.Request.Query = query
	}
	if len(body) > 0 && c.tool.Request.Body == nil {
		c.tool.Request.Body = body
	}
	if c.tool.Effect == "read" && len(probeArgs) >= len(required) {
		c.tool.Probe = &sitepack.Probe{Args: probeArgs, Expect: sitepack.ProbeExpect{Status: 200}}
	}
	conf := 0.6
	if c.observed > 1 {
		conf = 0.8
	}
	if c.source == "openapi" || c.source == "opensearch" {
		conf = 0.9
	}
	c.tool.Evidence = &sitepack.Evidence{Observed: c.observed, Confidence: conf, Source: c.source}
	if c.tool.Description == "" {
		c.tool.Description = describe(c)
	}
}

// altProbes returns up to 3 other argument sets from the observed values of path parameters.
func (c *candidate) altProbes() []map[string]any {
	if c.tool.Probe == nil {
		return nil
	}
	var out []map[string]any
	for i := 1; i < 4; i++ {
		alt := map[string]any{}
		changed := false
		for k, v := range c.tool.Probe.Args {
			alt[k] = v
		}
		for _, p := range c.params {
			if p.in == "path" && i < len(p.values) {
				alt[p.name], changed = p.values[i], true
			}
		}
		if changed {
			out = append(out, alt)
		}
	}
	return out
}

func setParam(p *param, query map[string]string, body map[string]any, v string) {
	switch p.in {
	case "body", "form":
		body[p.name] = v
	case "path", "graphql":
		// path: in the URL template; graphql: in the fixed body template
	default:
		query[p.name] = v
	}
}

// fromNetwork groups API requests by method + path template.
func (e *Explorer) fromNetwork(ctx context.Context, entries []*NetEntry) []*candidate {
	// Literal item names in API URLs (/api/v1/crates/serde) become template parameters:
	// first by declared templates, then by item patterns among the observed URLs.
	var apiURLs []string
	for _, n := range entries {
		apiURLs = append(apiURLs, n.URL)
	}
	apiPats := e.detectPatterns(ctx, apiURLs)
	for _, p := range apiPats {
		if p.Count >= 3 {
			p.Kind = "items" // the registry gate quarantines 3+ literal endpoints as one repeating family
		}
	}
	groups := map[string]*candidate{}
	var order []string
	for _, n := range entries {
		u, err := url.Parse(n.URL)
		if err != nil {
			continue
		}
		path, pathParams := e.templateAPIPath(u, apiPats)
		op := graphqlOp(n.Body)
		key := n.Method + " " + u.Scheme + "://" + u.Host + path + " " + op
		c := groups[key]
		if c == nil {
			c = &candidate{source: "network", key: key}
			id := toolID(n.Method, path, op)
			if id == "search" {
				id = "search_api" // "search" is kept for the site's own search page
			}
			c.tool = sitepack.Tool{
				ID:        id,
				Kind:      "api",
				Auth:      "none",
				Executors: []string{"http"},
				Request:   sitepack.Request{Method: n.Method, URL: u.Scheme + "://" + u.Host + braces(path)},
				Output:    outputOf(n),
			}
			if n.Method == "GET" || n.Method == "HEAD" {
				c.effectFact = "read" // fact: safe method
			}
			if op != "" {
				c.tool.Description = "GraphQL operation " + op + "."
			}
			groups[key] = c
			order = append(order, key)
		}
		c.observed++
		if c.samplePreview == "" {
			c.samplePreview = n.Preview
		}
		for _, pp := range pathParams {
			p := c.param(pp.name)
			p.in, p.roleFact = "path", "id"
			p.values = appendUnique(p.values, pp.value)
		}
		for name, vals := range u.Query() {
			p := c.param(name)
			p.in = "query"
			for _, v := range vals {
				p.values = appendUnique(p.values, v)
			}
		}
		if n.Method != "GET" && n.Body != "" {
			c.sampleBody = n.Body
			if strings.HasPrefix(strings.TrimSpace(n.Body), "{") {
				c.tool.Request.ContentType = "json"
				var b map[string]any
				if json.Unmarshal([]byte(n.Body), &b) == nil {
					if op != "" {
						// Keep the GraphQL document fixed; expose its variables.
						vars, _ := b["variables"].(map[string]any)
						tpl := map[string]any{"query": b["query"], "operationName": op}
						tv := map[string]any{}
						for k, v := range vars {
							p := c.param(k)
							p.in = "graphql"
							p.values = appendUnique(p.values, fmt.Sprint(v))
							tv[k] = "{{" + k + "}}"
						}
						tpl["variables"] = tv
						c.tool.Request.Body = tpl
					} else {
						for k, v := range b {
							p := c.param(k)
							p.in = "body"
							p.values = appendUnique(p.values, fmt.Sprint(v))
						}
					}
				}
			}
		}
	}
	var out []*candidate
	for _, k := range order {
		c := groups[k]
		for _, p := range c.params {
			p.numeric = allNumeric(p.values)
			if p.in == "query" && c.observed > 1 && len(p.values) > 0 {
				p.required = true
			}
		}
		out = append(out, c)
	}
	return out
}

// fromForm builds a tool from a DOM form. Login and signup forms never become tools.
func (e *Explorer) fromForm(f Form, kind string) *candidate {
	if kind == "login" || kind == "signup" || kind == "settings" {
		return nil
	}
	if !hasUserInput(f) {
		return nil // fact: no field a user fills in (buttons, display radios, hidden only)
	}
	u, err := url.Parse(f.Action)
	if err != nil {
		return nil
	}
	method := f.Method
	if method != "POST" {
		method = "GET"
	}
	c := &candidate{source: "form", formKind: kind, observed: 1}
	c.tool = sitepack.Tool{
		ID:        formToolID(kind, u.Path),
		Kind:      "form",
		Auth:      "none",
		Executors: []string{"http"},
		Request:   sitepack.Request{Method: method, URL: u.Scheme + "://" + u.Host + u.Path},
		Output:    sitepack.Output{Type: "html"},
	}
	if kind == "search" {
		c.tool.Kind = "search"
	}
	if method == "POST" {
		c.tool.Request.ContentType = "form"
	} else {
		c.effectFact = "read"
	}
	for name, vals := range u.Query() {
		p := c.param(name)
		p.in, p.roleFact = "query", "const"
		p.values = vals
	}
	for _, fd := range f.Fields {
		if fd.Name == "" {
			continue
		}
		p := c.param(fd.Name)
		p.in = "query"
		if method == "POST" {
			p.in = "form"
		}
		p.required = fd.Required
		p.options = fd.Options
		if fd.Type == "hidden" {
			if looksLikeToken(fd.Name, fd.Value) {
				p.roleFact = "token"
				c.tool.Steps = append(c.tool.Steps, sitepack.Step{Kind: "token", URL: f.Page, Extract: "input[name=" + fd.Name + "]", As: fd.Name})
			} else {
				p.roleFact = "const"
				p.values = []string{fd.Value}
			}
			continue
		}
		if fd.Type == "password" {
			return nil
		}
		if kind == "search" && (fd.Type == "search" || fd.Type == "text") && len(c.paramsWithRole("query")) == 0 {
			p.roleFact = "query"
		} else {
			p.roleFact = "filter" // a visible field is user input (fact)
		}
		if len(fd.Options) > 0 {
			p.values = fd.Options[:1]
		}
	}
	if len(c.tool.Steps) > 0 {
		c.tool.Executors = []string{"http"}
		if body := c.tool.Request.Body; body == nil {
			c.tool.Request.Body = nil
		}
	}
	return c
}

func (c *candidate) paramsWithRole(role string) []*param {
	var out []*param
	for _, p := range c.params {
		if p.roleFact == role || p.role == role {
			out = append(out, p)
		}
	}
	return out
}

// hasUserInput: the form has a text-like field or a select; radio/checkbox-only forms
// that post back to the same page are display preferences.
func hasUserInput(f Form) bool {
	choices := 0
	for _, fd := range f.Fields {
		switch fd.Type {
		case "hidden", "submit", "button", "reset", "image":
		case "radio", "checkbox":
			choices++
		default:
			return true
		}
	}
	return choices > 0 && stripQuery(f.Action) != stripQuery(f.Page)
}

func looksLikeToken(name, value string) bool {
	n := strings.ToLower(name)
	if regexp.MustCompile(`csrf|token|nonce|authenticity|xsrf|__requestverification`).MatchString(n) {
		return true
	}
	return len(value) >= 24 && regexp.MustCompile(`^[A-Za-z0-9+/=_\-.]+$`).MatchString(value)
}

func dedupe(cands []*candidate) []*candidate {
	seen := map[string]*candidate{}
	var out []*candidate
	for _, c := range cands {
		k := c.tool.Request.Method + " " + regexp.MustCompile(`\{\{[^}]+\}\}`).ReplaceAllString(c.tool.Request.URL, "{{}}") + " " + graphqlOpFromBody(c.tool.Request.Body)
		if prev, ok := seen[k]; ok {
			// Prefer declared and network sources over forms for the same endpoint,
			// and keep the parameters that only the other source saw.
			winner, loser := prev, c
			if rank(c.source) > rank(prev.source) {
				winner, loser = c, prev
			}
			for _, lp := range loser.params {
				wp := winner.param(lp.name)
				if wp.in == "" {
					*wp = *lp
				} else {
					if len(wp.options) == 0 {
						wp.options = lp.options
					}
					if len(wp.values) == 0 {
						wp.values = lp.values // observed values give declared tools a test call
					}
					wp.variable = wp.variable || lp.variable
				}
			}
			if winner.tool.Description == "" || strings.HasPrefix(winner.tool.Description, "GET ") {
				if loser.tool.Description != "" {
					winner.tool.Description = loser.tool.Description
				}
			}
			winner.observed += loser.observed
			if winner.formKind == "" {
				winner.formKind = loser.formKind
			}
			*prev = *winner
			continue
		}
		seen[k] = c
		out = append(out, c)
	}
	return out
}

func rank(src string) int {
	return map[string]int{"page": 0, "form": 1, "guide": 2, "network": 3, "opensearch": 4, "openapi": 5}[src]
}

func graphqlOpFromBody(b any) string {
	if m, ok := b.(map[string]any); ok {
		if s, ok := m["operationName"].(string); ok {
			return s
		}
	}
	return ""
}

// verify runs read probes twice (4 tools at a time, at most 20 tools) and keeps tools that answer.
func (e *Explorer) verify(ctx context.Context, origin string, tools []sitepack.Tool) []sitepack.Tool {
	out := append([]sitepack.Tool(nil), tools...)
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	n := 0
	for i := range out {
		t := &out[i]
		if t.Probe == nil || t.Effect != "read" {
			continue
		}
		if n >= 20 {
			break
		}
		n++
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ok := 0
			var preview string
			for k := 0; k < 2; k++ {
				r, err := execute.Call(ctx, e.HTTP, origin, t, t.Probe.Args, execute.Options{NoBrowser: true})
				if err == nil && r.Status == t.Probe.Expect.Status {
					ok++
					preview = resultPreview(r)
				}
			}
			// Some sites challenge single pages: try other example values before giving up.
			for _, alt := range e.altArgs[t.ID] {
				if ok > 0 && e.isData(ctx, t, preview) {
					break
				}
				if r, err := execute.Call(ctx, e.HTTP, origin, t, alt, execute.Options{NoBrowser: true}); err == nil && r.Status == t.Probe.Expect.Status {
					ok, preview = 1, resultPreview(r)
					t.Probe.Args = alt
				}
			}
			// A 200 can still be a browser check or an error page: decision point resp.data.
			via := "http"
			challenged := false
			if ok > 0 && !e.isData(ctx, t, preview) && !e.Opts.NoBrowser {
				// Sites often challenge a client right after a burst of requests: wait once, retry.
				mu.Lock()
				e.say("verify %s: browser check; waiting 30 s before one retry", t.ID)
				mu.Unlock()
				select {
				case <-time.After(30 * time.Second):
				case <-ctx.Done():
				}
				if r, err := execute.Call(ctx, e.HTTP, origin, t, t.Probe.Args, execute.Options{NoBrowser: true}); err == nil && r.Status == t.Probe.Expect.Status {
					preview = resultPreview(r)
				}
			}
			if ok > 0 && !e.isData(ctx, t, preview) {
				ok, challenged = 0, true
				if t.Request.Method == "GET" && !e.Opts.NoBrowser {
					hl := *t
					hl.Executors = []string{"headless"}
					if r, err := execute.Call(ctx, e.HTTP, origin, &hl, t.Probe.Args, execute.Options{}); err == nil && e.isData(ctx, t, resultPreview(r)) {
						t.Executors = []string{"http", "headless"} // HTTP first, Chrome after a browser check
						ok, via, challenged = 1, "headless browser (plain HTTP got a browser check)", false
					}
				}
			}
			if ok == 0 && challenged {
				via = "http (browser check; probe kept so the registry can test from its own network)"
			}
			if t.Evidence == nil {
				t.Evidence = &sitepack.Evidence{}
			}
			switch {
			case ok > 0 && strings.HasPrefix(via, "headless"):
				t.Evidence.Verified = "headless"
			case ok > 0:
				t.Evidence.Verified = "http"
			case challenged:
				t.Evidence.Verified = "challenged"
			default:
				t.Evidence.Verified = "failed"
			}
			mu.Lock()
			e.say("verify %s: %d/2 via %s", t.ID, ok, via)
			mu.Unlock()
			if ok == 0 {
				if !challenged {
					t.Probe = nil // the endpoint itself failed
				}
				if t.Evidence != nil {
					t.Evidence.Confidence = 0.3
				}
			}
		}()
	}
	wg.Wait()
	return out
}

// markBlocked flags browser-check, captcha and error pages: a rule fact on the title and
// first text, and the decision point resp.data for the rest.
func (e *Explorer) markBlocked(ctx context.Context, pages []*PageResult) {
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, p := range pages {
		head := p.Title + " " + clip(p.Text, 300)
		if blockedPage.MatchString(head) {
			p.Blocked = true
		} else if e.D.Available() && strings.TrimSpace(p.Text) != "" {
			wg.Add(1)
			go func(p *PageResult) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				ans, err := e.D.Ask(ctx, "resp.data", map[string]any{"request": "GET " + p.URL, "response": p.Title + " — " + p.Text},
					map[string]decide.Question{"x": decide.Noul("This response is the real content or data the request asked for, not an error, captcha, login wall or browser check page.")},
					map[string]string{"x": p.URL})
				if err == nil && ans["x"].Noul < 0.5 {
					p.Blocked = true
				}
			}(p)
		}
	}
	wg.Wait()
	n := 0
	for _, p := range pages {
		if p.Blocked {
			n++
		}
	}
	if n > 0 {
		e.say("%d of %d pages were browser checks or error pages; their forms are ignored", n, len(pages))
	}
}

func resultPreview(r *execute.Result) string {
	if r == nil {
		return ""
	}
	if r.Data != nil {
		b, _ := json.Marshal(r.Data)
		return clip(string(b), 1200)
	}
	return clip(strings.Join(strings.Fields(r.Text), " "), 1200)
}

var blockedPage = regexp.MustCompile(`(?i)(enable javascript|javascript is disabled|just a moment|checking (if|your) (the site|browser)|captcha|access denied|client challenge|are you a robot|not found|404)`)

// isData decides resp.data: is this the real content, not an error, captcha or browser check?
func (e *Explorer) isData(ctx context.Context, t *sitepack.Tool, preview string) bool {
	if strings.TrimSpace(preview) == "" {
		return false
	}
	if e.D.Available() {
		ans, err := e.D.Ask(ctx, "resp.data", map[string]any{"request": t.Request.Method + " " + t.Request.URL, "response": preview},
			map[string]decide.Question{"x": decide.Noul("This response is the real content or data the request asked for, not an error, captcha, login wall or browser check page.")},
			map[string]string{"x": t.ID})
		if err == nil {
			return ans["x"].Noul >= 0.5
		}
	}
	return !blockedPage.MatchString(clip(preview, 400))
}

func fingerprint(pages []*PageResult, tools []sitepack.Tool) sitepack.Fingerprint {
	var routes []string
	h := sha256.New()
	for _, p := range pages {
		if u, err := url.Parse(p.URL); err == nil {
			routes = append(routes, templateOf(u.Path))
		}
		fmt.Fprintf(h, "%s|%d|%d\n", templateOf(p.URL), len(p.Forms), p.DOMSize/100)
	}
	a := sha256.New()
	for _, t := range tools {
		fmt.Fprintf(a, "%s %s\n", t.Request.Method, t.Request.URL)
	}
	sort.Strings(routes)
	return sitepack.Fingerprint{Routes: uniq(routes), DOMHash: hex.EncodeToString(h.Sum(nil))[:16], APIHash: hex.EncodeToString(a.Sum(nil))[:16]}
}

// --- declared specs ---

func (e *Explorer) declared(ctx context.Context) []*candidate {
	e.readRobots(ctx)
	var out []*candidate
	for _, p := range []string{"/openapi.json", "/swagger.json", "/api-docs", "/v3/api-docs", "/.well-known/openapi.json", "/api/openapi.json"} {
		if b := e.get(ctx, e.origin+p, "json"); b != nil {
			if cs := e.fromOpenAPI(b); len(cs) > 0 {
				e.say("openapi: %s (%d operations)", p, len(cs))
				out = append(out, cs...)
				break
			}
		}
	}
	if home := e.get(ctx, e.origin+"/", ""); home != nil {
		if m := regexp.MustCompile(`<link[^>]+type=["']application/opensearchdescription\+xml["'][^>]*>`).Find(home); m != nil {
			if h := regexp.MustCompile(`href=["']([^"']+)["']`).FindSubmatch(m); h != nil {
				if c := e.fromOpenSearch(ctx, e.abs(string(h[1]))); c != nil {
					e.say("opensearch: %s", string(h[1]))
					out = append(out, c)
				}
			}
		}
	}
	return out
}

// readRobots fetches and parses robots.txt (RFC 9309) once.
func (e *Explorer) readRobots(ctx context.Context) {
	if e.robotsTxt != "" || e.robots != nil {
		return
	}
	b := e.get(ctx, e.origin+"/robots.txt", "")
	if b == nil {
		return
	}
	e.robotsTxt = string(b)
	e.robots = ParseRobots(e.robotsTxt)
}

func (e *Explorer) allowed(raw string) bool {
	return e.robots.Allowed("mcpit", raw)
}

func (e *Explorer) get(ctx context.Context, u, want string) []byte {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "mcpit/0.1 (+https://mcpit-registry.jstdlee.workers.dev)")
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	if want == "json" && !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return b
}

func (e *Explorer) abs(ref string) string {
	base, _ := url.Parse(e.origin + "/")
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return base.ResolveReference(r).String()
}

func (e *Explorer) fromOpenAPI(b []byte) []*candidate {
	var doc struct {
		OpenAPI string `json:"openapi"`
		Swagger string `json:"swagger"`
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
		BasePath string                                `json:"basePath"`
		Paths    map[string]map[string]json.RawMessage `json:"paths"`
	}
	if json.Unmarshal(b, &doc) != nil || (doc.OpenAPI == "" && doc.Swagger == "") {
		return nil
	}
	base := e.origin + doc.BasePath
	if len(doc.Servers) > 0 {
		s := doc.Servers[0].URL
		if strings.HasPrefix(s, "/") {
			base = e.origin + strings.TrimSuffix(s, "/")
		} else if strings.HasPrefix(s, "http") {
			base = strings.TrimSuffix(s, "/")
		}
	}
	var paths []string
	for p := range doc.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []*candidate
	for _, p := range paths {
		for _, m := range []string{"get", "post", "put", "patch", "delete"} {
			raw, ok := doc.Paths[p][m]
			if !ok {
				continue
			}
			var op struct {
				OperationID string `json:"operationId"`
				Summary     string `json:"summary"`
				Parameters  []struct {
					Name     string `json:"name"`
					In       string `json:"in"`
					Required bool   `json:"required"`
					Schema   struct {
						Type string   `json:"type"`
						Enum []string `json:"enum"`
					} `json:"schema"`
				} `json:"parameters"`
			}
			json.Unmarshal(raw, &op)
			method := strings.ToUpper(m)
			c := &candidate{source: "openapi", observed: 1}
			id := snake(op.OperationID)
			if id == "" {
				id = toolID(method, p, "")
			}
			c.tool = sitepack.Tool{ID: id, Kind: "api", Auth: "none", Executors: []string{"http"},
				Description: clip(op.Summary, 300),
				Request:     sitepack.Request{Method: method, URL: base + braces(p)}, Output: sitepack.Output{Type: "json"}}
			if method == "GET" {
				c.effectFact = "read"
			} else if method == "DELETE" {
				c.effectFact = "destructive"
			} else {
				c.tool.Request.ContentType = "json"
			}
			for _, pr := range op.Parameters {
				if pr.In != "query" && pr.In != "path" {
					continue
				}
				pp := c.param(pr.Name)
				pp.in, pp.required, pp.options = pr.In, pr.Required, pr.Schema.Enum
				pp.numeric = pr.Schema.Type == "integer" || pr.Schema.Type == "number"
				if pr.In == "path" {
					pp.roleFact = "id"
				}
			}
			out = append(out, c)
			if len(out) >= 40 {
				return out
			}
		}
	}
	return out
}

func (e *Explorer) fromOpenSearch(ctx context.Context, u string) *candidate {
	b := e.get(ctx, u, "")
	if b == nil {
		return nil
	}
	m := regexp.MustCompile(`<Url[^>]+type=["']text/html["'][^>]+template=["']([^"']+)["']`).FindSubmatch(b)
	if m == nil {
		m = regexp.MustCompile(`<Url[^>]+template=["']([^"']+)["']`).FindSubmatch(b)
	}
	if m == nil {
		return nil
	}
	tpl := strings.ReplaceAll(string(m[1]), "&amp;", "&")
	pu, err := url.Parse(strings.ReplaceAll(tpl, "{searchTerms}", "MCPITQ"))
	if err != nil {
		return nil
	}
	c := &candidate{source: "opensearch", observed: 1, effectFact: "read", formKind: "search"}
	c.tool = sitepack.Tool{ID: "search", Kind: "search", Auth: "none", Executors: []string{"http"},
		Description: "Search the site (OpenSearch).",
		Request:     sitepack.Request{Method: "GET", URL: pu.Scheme + "://" + pu.Host + pu.Path}, Output: sitepack.Output{Type: "html"}}
	for name, vals := range pu.Query() {
		p := c.param(name)
		p.in = "query"
		if len(vals) > 0 && vals[0] == "MCPITQ" {
			p.roleFact = "query"
			p.values = []string{e.ProbeText}
		} else if len(vals) > 0 && !strings.Contains(vals[0], "{") {
			p.roleFact, p.values = "const", vals
		} else {
			p.roleFact = "tracking"
		}
	}
	return c
}

// --- helpers ---

func (e *Explorer) sameOrigin(raw string) bool {
	o, err := sitepack.OriginOf(raw)
	return err == nil && o == e.origin
}

// sameSite accepts the origin host and its subdomains or parent (api.example.com for www.example.com).
func (e *Explorer) sameSite(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	o, _ := url.Parse(e.origin)
	return siteOf(u.Hostname()) == siteOf(o.Hostname())
}

func siteOf(host string) string {
	if regexp.MustCompile(`^[0-9.]+$`).MatchString(host) || host == "localhost" {
		return host
	}
	parts := strings.Split(host, ".")
	if len(parts) <= 2 {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

var numSeg = regexp.MustCompile(`^(\d+|[0-9a-fA-F]{16,}|[0-9a-fA-F-]{36})$`)

type pathParam struct{ name, value string }

// templatePath turns /product/123 into /product/{id}.
func templatePath(p string) (string, []pathParam) {
	segs := strings.Split(p, "/")
	var params []pathParam
	for i, s := range segs {
		if numSeg.MatchString(s) {
			name := "id"
			if len(params) > 0 {
				name = fmt.Sprintf("id%d", len(params)+1)
			}
			params = append(params, pathParam{name, s})
			segs[i] = "{" + name + "}"
		}
	}
	return strings.Join(segs, "/"), params
}

func templateOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	p, _ := templatePath(u.Path)
	var keys []string
	for k := range u.Query() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return u.Host + p + "?" + strings.Join(keys, "&")
}

// isPageURL skips files that the guide reader already handles (txt, xml, json, .well-known).
func isPageURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	p := strings.ToLower(u.Path)
	if strings.HasPrefix(p, "/.well-known/") {
		return false
	}
	for _, ext := range []string{".txt", ".xml", ".json", ".pdf", ".zip", ".png", ".jpg", ".svg", ".css", ".js"} {
		if strings.HasSuffix(p, ext) {
			return false
		}
	}
	return true
}

// probeValues: "test", plus a numeric code seen in the guide or the sitemap (such as a
// stop code or a postal code), so inputs that expect numbers also fire their requests.
func (e *Explorer) probeValues() []string {
	vals := []string{e.ProbeText}
	num := regexp.MustCompile(`^\d{3,8}$`)
	for _, list := range e.examples {
		for _, v := range list {
			if num.MatchString(v) && len(vals) < 3 {
				vals = appendUnique(vals, v)
			}
		}
	}
	for _, s := range e.sitemap {
		if u, err := url.Parse(s); err == nil {
			for _, seg := range strings.Split(u.Path, "/") {
				if num.MatchString(seg) && len(vals) < 3 {
					vals = appendUnique(vals, seg)
				}
			}
		}
	}
	return vals
}

// shapeOf keeps the first path segment and the segment count: /people/a/lists/b and
// /people/c/lists/d share a shape, so only two pages of it are visited.
func shapeOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) <= 1 {
		return templateOf(raw)
	}
	var keys []string
	for k := range u.Query() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%s/%s/%d?%s", u.Host, segs[0], len(segs), strings.Join(keys, "&"))
}

func braces(p string) string {
	return regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`).ReplaceAllString(p, "{{$1}}")
}

func toolID(method, path, op string) string {
	if op != "" {
		return snake(op)
	}
	var parts []string
	segs := strings.Split(strings.TrimSuffix(path, "/"), "/")
	for _, s := range segs {
		s = strings.ToLower(s)
		if s == "" || s == "api" || regexp.MustCompile(`^v\d+$`).MatchString(s) || strings.HasPrefix(s, "{") {
			continue
		}
		parts = append(parts, s)
	}
	if last := segs[len(segs)-1]; strings.HasPrefix(last, "{") && len(parts) > 0 {
		parts = append(parts, "by", strings.Trim(last, "{}"))
	}
	id := snake(strings.Join(parts, "_"))
	if id == "" {
		id = "root"
	}
	if method != "GET" {
		id = strings.ToLower(method) + "_" + id
	}
	if !regexp.MustCompile(`^[a-z]`).MatchString(id) {
		id = "t_" + id
	}
	return clip(id, 60)
}

// safeID makes any name a valid tool id: ^[a-z][a-z0-9_]{0,63}$.
func safeID(s string) string {
	s = snake(s)
	if s == "" {
		s = "tool"
	}
	if s[0] < 'a' || s[0] > 'z' {
		s = "t_" + s
	}
	if len(s) > 58 { // leave room for a _N suffix
		s = strings.TrimRight(s[:58], "_")
	}
	return s
}

func formToolID(kind, path string) string {
	switch kind {
	case "search":
		return "search"
	case "filter":
		return clip(snake("filter_"+strings.Trim(path, "/")), 60)
	}
	return clip(snake(kind+"_"+strings.Trim(path, "/")), 60)
}

func snake(s string) string {
	s = regexp.MustCompile(`([a-z0-9])([A-Z])`).ReplaceAllString(s, "${1}_${2}")
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

func graphqlOp(body string) string {
	if !strings.Contains(body, "operationName") {
		return ""
	}
	var b struct {
		OperationName string `json:"operationName"`
	}
	json.Unmarshal([]byte(body), &b)
	return b.OperationName
}

func outputOf(n *NetEntry) sitepack.Output {
	if !strings.Contains(n.MIME, "json") {
		return sitepack.Output{Type: "text"}
	}
	o := sitepack.Output{Type: "json"}
	var v any
	if json.Unmarshal([]byte(n.Preview), &v) == nil {
		o.ItemsPath = itemsPath(v, "$")
	}
	return o
}

func itemsPath(v any, at string) string {
	switch x := v.(type) {
	case []any:
		return at + "[*]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, ok := x[k].([]any); ok {
				return at + "." + k + "[*]"
			}
		}
		for _, k := range keys {
			if m, ok := x[k].(map[string]any); ok {
				if p := itemsPath(m, at+"."+k); p != "" {
					return p
				}
			}
		}
	}
	return ""
}

func describe(c *candidate) string {
	t := c.tool
	var b strings.Builder
	switch {
	case c.formKind == "search" || t.Kind == "search":
		b.WriteString("Search the site")
	case c.formKind != "":
		b.WriteString(strings.ToUpper(c.formKind[:1]) + c.formKind[1:] + " form")
	default:
		path := t.Request.URL
		if u, err := url.Parse(t.Request.URL); err == nil {
			path = u.Path
		}
		b.WriteString(t.Request.Method + " " + strings.ReplaceAll(strings.ReplaceAll(path, "{{", "{"), "}}", "}"))
	}
	var names []string
	for _, p := range c.params {
		if p.role != "token" && p.role != "tracking" && p.role != "const" {
			names = append(names, p.name)
		}
	}
	if len(names) > 0 {
		b.WriteString(" (params: " + strings.Join(names, ", ") + ")")
	}
	if t.Output.Type == "json" {
		b.WriteString("; returns JSON")
		if t.Output.ItemsPath != "" {
			b.WriteString(" items at " + t.Output.ItemsPath)
		}
	}
	b.WriteString(".")
	return clip(b.String(), 300)
}

func stripQuery(s string) string    { return strings.SplitN(s, "?", 2)[0] }
func stripFragment(s string) string { return strings.SplitN(s, "#", 2)[0] }

func fieldNames(fs []Field) string {
	var n []string
	for _, f := range fs {
		n = append(n, f.Name)
	}
	sort.Strings(n)
	return strings.Join(n, ",")
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	if len(s) >= 8 {
		return s
	}
	return append(s, v)
}

func allNumeric(vs []string) bool {
	if len(vs) == 0 {
		return false
	}
	for _, v := range vs {
		if _, err := strconv.Atoi(v); err != nil {
			return false
		}
	}
	return true
}

func uniq(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// templateAPIPath maps a literal API path onto a declared template when one matches,
// else onto an item pattern among observed API URLs, else numeric/hex ids only.
func (e *Explorer) templateAPIPath(u *url.URL, pats []*Pattern) (string, []pathParam) {
	host := u.Scheme + "://" + u.Host
	for _, tpl := range e.declTpls {
		if !strings.HasPrefix(tpl, host) || !strings.Contains(tpl, "{{") {
			continue
		}
		tp := strings.TrimPrefix(tpl, host)
		if templateRe(tp).MatchString(u.Path) {
			var params []pathParam
			tsegs, segs := strings.Split(tp, "/"), strings.Split(u.Path, "/")
			for i := range tsegs {
				if m := regexp.MustCompile(`^\{\{([A-Za-z0-9_]+)\}\}$`).FindStringSubmatch(tsegs[i]); m != nil && i < len(segs) {
					params = append(params, pathParam{m[1], segs[i]})
				}
			}
			return regexp.MustCompile(`\{\{([A-Za-z0-9_]+)\}\}`).ReplaceAllString(tp, "{$1}"), params
		}
	}
	if p := matchPattern(pats, u.String()); p != nil && p.Kind == "items" {
		segs := strings.Split(u.Path, "/")
		var params []pathParam
		// shapeKey trims the leading slash, so segment Pos sits at index Pos+1 here
		for n, pos := range p.Positions {
			if i := pos + 1; i < len(segs) {
				params = append(params, pathParam{p.Params[n], segs[i]})
				segs[i] = "{" + p.Params[n] + "}"
			}
		}
		if len(params) > 0 {
			return strings.Join(segs, "/"), params
		}
	}
	// A deeper URL under a site item family: /{owner}/{name}/funding_links.
	if e.sameOrigin(u.String()) {
		if p := underItems(e.patterns, u); p != nil {
			segs := strings.Split(u.Path, "/")
			var params []pathParam
			for k, pos := range p.Positions {
				params = append(params, pathParam{p.Params[k], segs[pos+1]})
				segs[pos+1] = "{" + p.Params[k] + "}"
			}
			return strings.Join(segs, "/"), params
		}
	}
	return templatePath(u.Path)
}
