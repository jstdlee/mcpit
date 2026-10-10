package explore

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jstdlee/mcpit/cli/internal/decide"
)

// Cases follow RFC 9309 sections 2.2.2 and 2.2.3.
func TestRobotsRFC9309(t *testing.T) {
	r := ParseRobots(`# comment
User-Agent: *
Disallow: /private/
Allow: /private/open
Disallow: /*.pdf$

user-agent: mcpit
user-agent: otherbot
disallow: /no-mcpit
allow: /no-mcpit/yes

User-agent: otherbot
Disallow: /also

Sitemap: https://e.com/sitemap.xml
Llms: https://e.com/llms.txt
`)
	cases := []struct {
		agent, url string
		want       bool
	}{
		{"somebot", "https://e.com/private/x", false},
		{"somebot", "https://e.com/private/open", true},
		{"somebot", "https://e.com/a/b.pdf", false},
		{"somebot", "https://e.com/a/b.pdf?x=1", true}, // $ anchors the end
		{"mcpit", "https://e.com/private/x", true},     // named group replaces *
		{"mcpit", "https://e.com/no-mcpit", false},
		{"mcpit", "https://e.com/no-mcpit/yes", true}, // longest match wins
		{"otherbot", "https://e.com/also", false},     // groups for one agent join
	}
	for _, c := range cases {
		if got := r.Allowed(c.agent, c.url); got != c.want {
			t.Errorf("%s %s: %v want %v", c.agent, c.url, got, c.want)
		}
	}
	if len(r.Sitemaps) != 1 || r.Extra["llms"] != "https://e.com/llms.txt" {
		t.Errorf("sitemaps %v extra %v", r.Sitemaps, r.Extra)
	}
	if !ParseRobots("User-agent: *\nDisallow:\n").Allowed("x", "https://e.com/") {
		t.Error("empty disallow must allow everything")
	}
}

func TestGuideStandards(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "User-agent: *\nAllow: /\nSitemap: %s/sitemap.xml.gz\n", srv.URL)
	})
	mux.HandleFunc("/sitemap.xml.gz", func(w http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		zw := gzip.NewWriter(&b)
		fmt.Fprintf(zw, `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>%s/a</loc></url><url><loc>%s/b</loc></url><url><loc>https://other.example/c</loc></url></urlset>`, srv.URL, srv.URL)
		zw.Close()
		w.Write(b.Bytes())
	})
	mux.HandleFunc("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>News</title><item><title>First post</title><link>%s/news/1</link></item><item><title>Ext</title><link>https://other.example/x</link></item></channel></rss>`, srv.URL)
	})
	mux.HandleFunc("/atom.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<feed xmlns="http://www.w3.org/2005/Atom"><title>Blog</title><entry><title>Hello</title><link rel="alternate" href="%s/blog/hello"/></entry></feed>`, srv.URL)
	})
	mux.HandleFunc("/.well-known/security.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "Contact: mailto:security@example.com\nExpires: 2030-01-01T00:00:00Z\n")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `<!doctype html><html lang="en"><head><title>Shop</title>
<meta name="description" content="A test shop."><meta property="og:title" content="Shop OG">
<link rel="canonical" href="/"><link rel="alternate" type="application/rss+xml" href="/feed.xml">
<script type="application/ld+json">{"@context":"https://schema.org","@type":"WebSite","url":"%s/","potentialAction":{"@type":"SearchAction","target":{"@type":"EntryPoint","urlTemplate":"%s/find?term={search_term_string}"},"query-input":"required name=search_term_string"}}</script>
</head><body></body></html>`, srv.URL, srv.URL)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	e := New(&decide.Decider{}, Options{})
	e.origin = srv.URL
	g, sitemap, cands := e.readGuide(context.Background())
	if g == nil {
		t.Fatal("no guide")
	}
	if len(sitemap) != 2 {
		t.Errorf("gzip sitemap: %v", sitemap)
	}
	if g.Meta["og:title"] != "Shop OG" || g.Meta["lang"] != "en" || g.Description != "A test shop." || g.Meta["canonical"] == "" {
		t.Errorf("meta: %v", g.Meta)
	}
	if g.JSONLD == nil || g.SecurityTxt == nil {
		t.Error("JSON-LD or security.txt missing")
	}
	if len(g.Feeds) != 2 || g.Feeds[0].Title != "News" || g.Feeds[0].Items != 1 {
		t.Errorf("feeds: %+v", g.Feeds)
	}
	var search *candidate
	for _, c := range cands {
		if c.tool.ID == "search" {
			search = c
		}
	}
	if search == nil || search.param("term").roleFact != "query" {
		t.Fatalf("SearchAction tool missing: %+v", cands)
	}
	pages := buildPages(nil, nil, nil, sitemap, e.feedItems, nil)
	if len(pages) != 4 || pages[2].Title != "First post" || pages[2].Category != "news" {
		t.Errorf("pages: %+v", pages)
	}
}

func TestPageShapes(t *testing.T) {
	e := New(&decide.Decider{}, Options{})
	e.origin = "https://pypi.example"
	pages := []*PageResult{{URL: "https://pypi.example/project/httpx/", Title: "httpx · PyPI"}, {URL: "https://pypi.example/help/"}}
	sitemap := []string{"https://pypi.example/project/requests/", "https://pypi.example/project/flask/", "https://pypi.example/project/django/",
		"https://pypi.example/project/numpy/", "https://pypi.example/user/alice/", "https://other.example/project/x/"}
	e.patterns = e.detectPatterns(context.Background(), append([]string{pages[0].URL}, sitemap...))
	cs := e.fromPageShapes(pages, sitemap)
	if len(cs) != 1 {
		t.Fatalf("want 1 page tool (project), got %d", len(cs))
	}
	c := cs[0]
	if c.tool.ID != "project_page" || c.tool.Request.URL != "https://pypi.example/project/{{name}}/" || c.param("name").values[0] != "httpx" {
		t.Fatalf("tool: %+v", c.tool)
	}
}

// The user's example: /project/<name>/ is one item family; /manage/account and
// /manage/organizations are distinct sections.
func TestPatternsItemsAndSections(t *testing.T) {
	e := New(&decide.Decider{}, Options{})
	e.origin = "https://pypi.example"
	urls := []string{"https://pypi.example/project/102203594-topsis/", "https://pypi.example/project/1neuron-pypi-overlordiam/",
		"https://pypi.example/project/256-encrypt/", "https://pypi.example/project/3pc-panel/", "https://pypi.example/project/a2rpc/",
		"https://pypi.example/project/aad/", "https://pypi.example/manage/account/", "https://pypi.example/manage/organizations/",
		"https://pypi.example/classifiers/"}
	ps := e.detectPatterns(context.Background(), urls)
	kinds := map[string]string{}
	for _, p := range ps {
		kinds[p.Template] = p.Kind
	}
	if kinds["/project/{name}/"] != "items" || kinds["/manage/{name}/"] != "sections" || len(ps) != 2 {
		t.Fatalf("patterns: %v", kinds)
	}
	pages := buildPages(nil, nil, nil, urls, nil, ps)
	var rows []string
	for _, p := range pages {
		rows = append(rows, p.Path)
	}
	want := "/project/{name}/ /manage/account/ /manage/organizations/ /classifiers/"
	if strings.Join(rows, " ") != want {
		t.Fatalf("site map rows: %v", rows)
	}
	if pages[0].Count != 6 || len(pages[0].Examples) != 5 || !pages[0].Pattern {
		t.Fatalf("pattern row: %+v", pages[0])
	}
	e.declTpls = []string{"https://crates.example/api/v1/crates/{{name}}/owners"}
	u, _ := url.Parse("https://crates.example/api/v1/crates/axmg/owners")
	if p, params := e.templateAPIPath(u, nil); p != "/api/v1/crates/{name}/owners" || params[0].value != "axmg" {
		t.Fatalf("declared template: %s %v", p, params)
	}
}

func TestPatternTwoSegments(t *testing.T) {
	e := New(&decide.Decider{}, Options{})
	e.origin = "https://pypi.example"
	var urls []string
	for _, x := range []string{"a/1.0.1", "b/2.4.34", "c/0.2.1", "d/2027.0.6", "e/0.4.8"} {
		urls = append(urls, "https://pypi.example/project/"+x+"/")
	}
	ps := e.detectPatterns(context.Background(), urls)
	if len(ps) != 1 || ps[0].Template != "/project/{name}/{version}/" || ps[0].Kind != "items" {
		t.Fatalf("patterns: %+v", ps[0])
	}
}

// TestPlanLinks: level 2 visits entry pages, samples one item per family and skips list pages.
func TestPlanLinks(t *testing.T) {
	e := New(&decide.Decider{}, Options{})
	e.origin = "https://shop.example"
	o := e.origin
	infos := []LinkInfo{
		{URL: o + "/search", Text: "Search", Region: "header"},
		{URL: o + "/docs", Text: "Docs", Region: "nav"},
		{URL: o + "/models", Text: "Models", Region: "nav"},
		{URL: o + "/models?p=2", Text: "Next", Region: "main"},
		{URL: o + "/models?sort=new", Text: "Newest", Region: "main"},
		{URL: o + "/contact", Text: "Contact", Region: "footer"},
	}
	for _, n := range []string{"llama", "qwen", "mistral", "gemma", "phi"} {
		infos = append(infos, LinkInfo{URL: o + "/m/" + n, Text: n, Region: "main", Repeat: 5})
	}
	for _, n := range []string{"lamp", "desk", "chair", "sofa", "rug", "bed"} {
		infos = append(infos, LinkInfo{URL: o + "/p/" + n + "-1", Text: n, Region: "main", Repeat: 6})
	}
	var sitemap []string
	for i := 0; i < 30; i++ {
		sitemap = append(sitemap, fmt.Sprintf("%s/project/pkg%d/", o, i))
	}
	plan := e.planLinks(context.Background(), o+"/", infos, sitemap)
	want := []string{o + "/search", o + "/docs", o + "/models", o + "/contact"}
	if fmt.Sprint(plan.Entries) != fmt.Sprint(want) {
		t.Errorf("entries %v, want %v", plan.Entries, want)
	}
	if len(plan.Items) != 3 { // /m/{name}, /p/{name}, /project/{name}/: one sample each
		t.Errorf("items %v, want one sample per family (3)", plan.Items)
	}
	if len(plan.Pages) != 2 {
		t.Errorf("list pages %v, want the pager and the sort link", plan.Pages)
	}
}

// TestOwnerPatterns: /{owner}/{name} repos form one family; site sections stay apart.
func TestOwnerPatterns(t *testing.T) {
	e := New(&decide.Decider{}, Options{})
	e.origin = "https://hub.example"
	o := e.origin
	urls := []string{o + "/docs", o + "/blog", o + "/docs/peft", o + "/docs/hub", o + "/blog/one", o + "/blog/two"}
	for _, r := range []string{"alice/llama", "alice/qwen", "bob/phi", "carol/gemma", "dave/mistral", "erin/bert"} {
		urls = append(urls, o+"/"+r)
	}
	ps := e.detectPatterns(context.Background(), urls)
	var owner *Pattern
	for _, p := range ps {
		if p.AnyPrefix {
			owner = p
		}
	}
	if owner == nil || owner.Template != "/{owner}/{name}" || owner.Count != 6 {
		t.Fatalf("owner family missing: %+v", ps)
	}
	if m := matchPattern(ps, o+"/frank/new-model"); m != owner {
		t.Errorf("a new owner path should match the owner family, got %+v", m)
	}
	if m := matchPattern(ps, o+"/docs/zzz"); m == owner {
		t.Error("/docs/zzz is a docs section, not an owner repo")
	}
}

// TestFilterVariantAndOwnerAPI: a query variant of the start page is a list page; an API URL
// under an owner family is templated.
func TestFilterVariantAndOwnerAPI(t *testing.T) {
	e := New(&decide.Decider{}, Options{})
	e.origin = "https://hub.example"
	o := e.origin
	infos := []LinkInfo{
		{URL: o + "/models?pipeline_tag=text-generation", Text: "Text generation", Region: "main"},
		{URL: o + "/docs", Text: "Docs", Region: "nav"},
	}
	for _, r := range []string{"alice/llama", "bob/phi", "carol/gemma", "dave/mistral", "erin/bert"} {
		infos = append(infos, LinkInfo{URL: o + "/" + r, Region: "main", Repeat: 5})
		infos = append(infos, LinkInfo{URL: o + "/" + strings.Split(r, "/")[0], Region: "main", Repeat: 5})
	}
	plan := e.planLinks(context.Background(), o+"/models", infos, nil)
	if plan.Kind[o+"/models?pipeline_tag=text-generation"] != "page" {
		t.Errorf("filter variant: %q, want page", plan.Kind[o+"/models?pipeline_tag=text-generation"])
	}
	var urls []string
	for _, i := range infos {
		urls = append(urls, i.URL)
	}
	e.patterns = e.detectPatterns(context.Background(), urls)
	u, _ := url.Parse(o + "/alice/llama/funding_links")
	if p, params := e.templateAPIPath(u, nil); p != "/{owner}/{name}/funding_links" || len(params) != 2 {
		t.Errorf("owner API template: %s %v", p, params)
	}
}
