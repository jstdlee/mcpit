package explore

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	pages := buildPages(nil, sitemap, e.feedItems)
	if len(pages) != 4 || pages[2].Title != "First post" || pages[2].Category != "news" {
		t.Errorf("pages: %+v", pages)
	}
}

func TestPageShapes(t *testing.T) {
	e := New(&decide.Decider{}, Options{})
	e.origin = "https://pypi.example"
	pages := []*PageResult{{URL: "https://pypi.example/project/httpx/", Title: "httpx · PyPI"}, {URL: "https://pypi.example/help/"}}
	sitemap := []string{"https://pypi.example/project/requests/", "https://pypi.example/project/flask/", "https://pypi.example/user/alice/", "https://other.example/project/x/"}
	cs := e.fromPageShapes(pages, sitemap)
	if len(cs) != 1 {
		t.Fatalf("want 1 page tool (project), got %d", len(cs))
	}
	c := cs[0]
	if c.tool.ID != "project_page" || c.tool.Request.URL != "https://pypi.example/project/{{name}}/" || c.param("name").values[0] != "httpx" {
		t.Fatalf("tool: %+v", c.tool)
	}
}
