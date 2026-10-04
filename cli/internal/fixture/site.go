// Package fixture is a small shop website for tests. It has the things the
// explorer must find (live-search API, detail API, GraphQL, search form, contact
// form with a CSRF token) and the things it must skip (assets, tracking, login,
// translations, robots-disallowed pages).
package fixture

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

type Product struct {
	ID       int     `json:"id"`
	Title    string  `json:"title"`
	Category string  `json:"category"`
	Price    float64 `json:"price"`
}

var products = []Product{
	{981, "Desk lamp", "lighting", 39.9}, {982, "Floor lamp", "lighting", 89},
	{983, "Test tube rack", "lab", 12.5}, {984, "Oak table", "furniture", 420},
	{985, "Table lamp test edition", "lighting", 55},
}

// Site counts what reached it, so tests can check that explore never posted a form.
type Site struct {
	mu       sync.Mutex
	Hits     map[string]int
	Contacts int
}

func New() *Site { return &Site{Hits: map[string]int{}} }

const page = `<!doctype html><html><head><title>%s · Fixture Shop</title>
<link rel="stylesheet" href="/static/site.css">
<link rel="search" type="application/opensearchdescription+xml" href="/opensearch.xml" title="Fixture Shop">
<link rel="manifest" href="/manifest.webmanifest">
</head><body>
<nav><a href="/">Home</a> <a href="/products">Products</a> <a href="/products/981">Desk lamp</a>
<a href="/contact">Contact</a> <a href="/login">Sign in</a> <a href="/admin">Admin</a></nav>
<main>%s</main>
<img src="/static/logo.png" alt="">
<script src="/static/app.js"></script>
<script>
fetch('/locales/en.json');
navigator.sendBeacon && navigator.sendBeacon('/collect?ev=page_view', 'x');
</script>
</body></html>`

func (s *Site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.Hits[r.Method+" "+r.URL.Path]++
	s.mu.Unlock()
	p := r.URL.Path
	switch {
	case p == "/":
		html(w, "Home", `<h1>Fixture Shop</h1>
<input type="search" id="live" placeholder="Search products" aria-label="Search">
<ul id="results"></ul>
<form action="/search" method="get" role="search"><input name="q" type="text" placeholder="Search"><select name="category"><option value="">All</option><option value="lighting">Lighting</option><option value="lab">Lab</option></select><button>Search</button></form>
<script>
const box = document.getElementById('live');
let t;
box.addEventListener('input', () => { clearTimeout(t); t = setTimeout(async () => {
  const r = await fetch('/api/search?q=' + encodeURIComponent(box.value) + '&page=1');
  const j = await r.json();
  document.getElementById('results').innerHTML = j.results.map(x => '<li>' + x.title + '</li>').join('');
}, 200); });
</script>`)
	case p == "/products":
		html(w, "Products", `<h1>Products</h1><div id="filters"></div><ul id="list"></ul>
<script>
fetch('/graphql', {method: 'POST', headers: {'content-type': 'application/json'},
  body: JSON.stringify({operationName: 'ProductFilters', query: 'query ProductFilters($category: String) { filters(category: $category) { name values } }', variables: {category: 'lighting'}})})
  .then(r => r.json()).then(j => { document.getElementById('filters').textContent = JSON.stringify(j.data); });
fetch('/api/products?category=lighting&limit=10').then(r => r.json()).then(j => {
  document.getElementById('list').innerHTML = j.items.map(x => '<li><a href="/products/' + x.id + '">' + x.title + '</a></li>').join('');
});
</script>`)
	case strings.HasPrefix(p, "/products/"):
		id := strings.TrimPrefix(p, "/products/")
		html(w, "Product", `<h1 id="t"></h1><p id="price"></p><div id="rec"></div>
<script>
fetch('/api/products/`+id+`').then(r => r.json()).then(j => { document.getElementById('t').textContent = j.title; });
fetch('/api/recommendations?pid=`+id+`&limit=4').then(r => r.json()).then(j => { document.getElementById('rec').textContent = j.items.length + ' related'; });
</script>`)
	case p == "/search":
		q := strings.ToLower(r.URL.Query().Get("q"))
		var li []string
		for _, x := range match(q, r.URL.Query().Get("category")) {
			li = append(li, fmt.Sprintf(`<li><a href="/products/%d">%s</a> $%.2f</li>`, x.ID, x.Title, x.Price))
		}
		html(w, "Search", `<h1>Results for `+escape(q)+`</h1><ul>`+strings.Join(li, "")+`</ul>`)
	case p == "/contact" && r.Method == http.MethodGet:
		html(w, "Contact", `<h1>Contact us</h1><form action="/contact" method="post">
<input type="hidden" name="csrf_token" value="b7f2c9e1a04d4e8f9c3b2a1d0e9f8a7b">
<label>Email <input name="email" type="email" required></label>
<label>Message <textarea name="message" required></textarea></label>
<button type="submit">Send</button></form>`)
	case p == "/contact" && r.Method == http.MethodPost:
		r.ParseForm()
		if r.PostForm.Get("csrf_token") != "b7f2c9e1a04d4e8f9c3b2a1d0e9f8a7b" {
			http.Error(w, "bad token", http.StatusForbidden)
			return
		}
		s.mu.Lock()
		s.Contacts++
		s.mu.Unlock()
		html(w, "Thanks", `<p>Thanks, we got your message.</p>`)
	case p == "/login":
		html(w, "Sign in", `<form action="/login" method="post"><input name="email" type="email"><input name="password" type="password"><button>Sign in</button></form>`)
	case p == "/admin":
		html(w, "Admin", `<form action="/admin/delete" method="post"><input name="user_id"><button>Delete user</button></form>`)
	case p == "/api/search":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		writeJSON(w, map[string]any{"results": match(strings.ToLower(r.URL.Query().Get("q")), ""), "page": max(page, 1)})
	case p == "/api/products":
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items := match("", r.URL.Query().Get("category"))
		if limit > 0 && limit < len(items) {
			items = items[:limit]
		}
		writeJSON(w, map[string]any{"items": items})
	case strings.HasPrefix(p, "/api/products/"):
		id, _ := strconv.Atoi(strings.TrimPrefix(p, "/api/products/"))
		for _, x := range products {
			if x.ID == id {
				writeJSON(w, x)
				return
			}
		}
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	case p == "/api/recommendations":
		writeJSON(w, map[string]any{"items": products[1:3]})
	case p == "/graphql" && r.Method == http.MethodPost:
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, map[string]any{"data": map[string]any{"filters": []map[string]any{{"name": "color", "values": []string{"black", "white"}}, {"name": "category", "values": []any{body.Variables["category"]}}}}})
	case p == "/locales/en.json":
		writeJSON(w, map[string]string{"search": "Search", "add_to_cart": "Add to cart"})
	case p == "/collect":
		w.WriteHeader(http.StatusNoContent)
	case p == "/robots.txt":
		fmt.Fprint(w, "User-agent: *\nDisallow: /admin\n")
	case p == "/opensearch.xml":
		w.Header().Set("Content-Type", "application/opensearchdescription+xml")
		fmt.Fprintf(w, `<?xml version="1.0"?><OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/"><ShortName>Fixture</ShortName><Url type="text/html" template="http://%s/search?q={searchTerms}"/></OpenSearchDescription>`, r.Host)
	case p == "/static/app.js":
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, "window.app = {version: 1};")
	case p == "/static/site.css":
		w.Header().Set("Content-Type", "text/css")
		fmt.Fprint(w, "body{font-family:sans-serif}")
	case p == "/static/logo.png":
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("\x89PNG\r\n\x1a\n"))
	case p == "/manifest.webmanifest":
		w.Header().Set("Content-Type", "application/manifest+json")
		fmt.Fprint(w, `{"name":"Fixture Shop"}`)
	default:
		http.NotFound(w, r)
	}
}

func match(q, category string) []Product {
	out := []Product{}
	for _, x := range products {
		if (q == "" || strings.Contains(strings.ToLower(x.Title), q)) && (category == "" || x.Category == category) {
			out = append(out, x)
		}
	}
	return out
}

func html(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, page, title, body)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func escape(s string) string {
	return strings.NewReplacer("<", "&lt;", ">", "&gt;", "&", "&amp;", `"`, "&quot;").Replace(s)
}
