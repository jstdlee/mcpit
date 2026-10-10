package explore

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/jstdlee/mcpit/cli/internal/decide"
)

// Link kinds of the decision point link.kind. Rules give the facts (region, repeat count,
// item family, pager); the decision model decides; the rule fallback runs without a model.
var linkKindOptions = [][2]string{
	{"entry", "A main page of the site: a section, search, docs, tools, account, contact, about or a category hub."},
	{"item", "One item of a list: a product, model, package, post, user, video or article page."},
	{"page", "Another page of the same list: pagination, sorting or a filter variant."},
}

var pagerRe = regexp.MustCompile(`(?i)([?&](page|p|pg|offset|start|cursor|sort|order|orderby)=)|/page/\d+/?$`)

var regionRank = map[string]int{"header": 0, "nav": 1, "main": 2, "aside": 3, "footer": 4, "sitemap": 5}

// linkPlan is the level-2 plan: entry pages to visit, item pages (one sample at most),
// and list pages that are never visited.
type linkPlan struct {
	Entries []string
	Items   []string // one sample per item family
	AllItem []string // every item link (site map families)
	Pages   []string
	Listed  []string // sitemap URLs past the question limit: site map only
	Kind    map[string]string
	Text    map[string]string
}

// planLinks sorts the links of the start page (and the sitemap) into entries, items and
// list pages. Members of an item family (path.pattern) are items without a further question.
func (e *Explorer) planLinks(ctx context.Context, seed string, infos []LinkInfo, sitemap []string) *linkPlan {
	type cand struct {
		LinkInfo
		order int
	}
	seen := map[string]bool{templateOf(indexless(seed)): true}
	var cs []*cand
	add := func(li LinkInfo) {
		li.URL = indexless(stripFragment(li.URL))
		tpl := templateOf(li.URL)
		if seen[tpl] || !e.sameOrigin(li.URL) || !isPageURL(li.URL) || !e.allowed(li.URL) {
			return
		}
		seen[tpl] = true
		cs = append(cs, &cand{li, len(cs)})
	}
	e.navPrefix, e.cardPrefix = map[string]bool{}, map[string]bool{}
	for _, li := range infos {
		if u, err := url.Parse(li.URL); err == nil && e.sameOrigin(li.URL) {
			segs := strings.Split(strings.Trim(u.Path, "/"), "/")
			switch {
			case segs[0] == "":
			case li.Region != "main":
				e.navPrefix[segs[0]] = true
			case len(segs) == 1 && li.Repeat >= 4:
				e.cardPrefix[segs[0]] = true
			}
		}
		add(li)
	}
	for _, u := range sitemap {
		add(LinkInfo{URL: u, Region: "sitemap"})
	}
	pool := make([]string, 0, len(cs)+len(sitemap))
	for _, c := range cs {
		pool = append(pool, c.URL)
	}
	pats := e.detectPatterns(ctx, append(pool, sitemap...))

	plan := &linkPlan{Kind: map[string]string{}, Text: map[string]string{}}
	var ask []*cand
	family := map[string]*Pattern{}
	// A link to a listed path with a query is a filter or sort variant of that list (fact).
	bare := map[string]bool{}
	if u, err := url.Parse(indexless(seed)); err == nil {
		bare[u.Path] = true
	}
	for _, c := range cs {
		if u, err := url.Parse(c.URL); err == nil && u.RawQuery == "" {
			bare[u.Path] = true
		}
	}
	for _, c := range cs {
		plan.Text[c.URL] = c.Text
		if u, err := url.Parse(c.URL); err == nil && u.RawQuery != "" && bare[u.Path] {
			plan.Kind[c.URL] = "page"
			continue
		}
		if p := matchPattern(pats, c.URL); p != nil && p.Kind == "items" {
			plan.Kind[c.URL] = "item"
			family[c.URL] = p
			continue
		}
		if u, err := url.Parse(c.URL); err == nil {
			if p := underItems(pats, u); p != nil { // a part of one item (/{owner}/{name}/forks)
				plan.Kind[c.URL] = "item"
				family[c.URL] = p
				continue
			}
		}
		ask = append(ask, c)
	}
	// Page links first, sitemap last: the model sees the links a visitor sees.
	sort.SliceStable(ask, func(i, j int) bool { return regionRank[ask[i].Region] < regionRank[ask[j].Region] })
	// The model judges the first 60 links. Sitemap URLs past that are only listed (kind
	// "listed": in the site map, never visited); other page links use the rule fallback.
	if len(ask) > 60 {
		for _, c := range ask[60:] {
			if c.Region == "sitemap" {
				plan.Kind[c.URL] = "listed"
			} else {
				plan.Kind[c.URL] = ruleLinkKind(c.LinkInfo)
			}
		}
		ask = ask[:60]
	}
	// One call per 12 links, all calls at once.
	var mu sync.Mutex
	var wg sync.WaitGroup
	for start := 0; start < len(ask); start += 12 {
		end := min(start+12, len(ask))
		var chunk []LinkInfo
		for _, c := range ask[start:end] {
			chunk = append(chunk, c.LinkInfo)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := map[string]string{}
			e.decideLinkKinds(ctx, chunk, got)
			mu.Lock()
			maps.Copy(plan.Kind, got)
			mu.Unlock()
		}()
	}
	wg.Wait()

	sort.SliceStable(cs, func(i, j int) bool {
		ri, rj := regionRank[cs[i].Region], regionRank[cs[j].Region]
		if ri != rj {
			return ri < rj
		}
		return cs[i].order < cs[j].order
	})
	sampled := map[string]bool{}
	for _, c := range cs {
		switch plan.Kind[c.URL] {
		case "entry":
			plan.Entries = append(plan.Entries, c.URL)
		case "item":
			plan.AllItem = append(plan.AllItem, c.URL)
			key := shapeOf(c.URL)
			if p := family[c.URL]; p != nil {
				key = p.Key
			} else if c.Group > 0 {
				key = fmt.Sprintf("group %d", c.Group) // cards of one grid or list
			}
			if !sampled[key] {
				sampled[key] = true
				plan.Items = append(plan.Items, c.URL)
			}
		case "page":
			plan.Pages = append(plan.Pages, c.URL)
		default:
			plan.Listed = append(plan.Listed, c.URL)
		}
	}
	return plan
}

func (e *Explorer) decideLinkKinds(ctx context.Context, list []LinkInfo, out map[string]string) {
	qs := map[string]decide.Question{}
	subj := map[string]string{}
	state := map[string]any{}
	for i, li := range list {
		id := fmt.Sprintf("l%d", i)
		u, _ := url.Parse(li.URL)
		path := li.URL
		if u != nil {
			path = u.RequestURI()
		}
		state[id] = map[string]any{"path": path, "text": li.Text, "region": li.Region, "repeatedCards": li.Repeat}
		qs[id] = decide.Choice("What kind of page does link "+id+" open?", linkKindOptions)
		subj[id] = li.URL
	}
	ans, err := e.D.Ask(ctx, "link.kind", map[string]any{"site": e.origin, "links": state}, qs, subj)
	for i, li := range list {
		if err == nil {
			if a, ok := ans[fmt.Sprintf("l%d", i)]; ok && a.Choice != "" {
				out[li.URL] = a.Choice
				continue
			}
		}
		out[li.URL] = ruleLinkKind(li)
	}
}

func ruleLinkKind(li LinkInfo) string {
	switch {
	case pagerRe.MatchString(li.URL):
		return "page"
	case li.Region == "main" && li.Repeat >= 4:
		return "item"
	default:
		return "entry"
	}
}

// indexless maps /index.html and /index.php to their directory: the same page as "/".
func indexless(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	for _, ix := range []string{"index.html", "index.htm", "index.php"} {
		if strings.HasSuffix(u.Path, "/"+ix) {
			u.Path = strings.TrimSuffix(u.Path, ix)
			return u.String()
		}
	}
	return raw
}
