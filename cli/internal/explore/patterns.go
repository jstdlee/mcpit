package explore

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/jstdlee/mcpit/cli/internal/decide"
)

// Pattern is a family of paths that differ in one segment. Kind "items" means the
// members are interchangeable items of one collection (/project/httpx/, /project/flask/):
// one template is enough. Kind "sections" means distinct pages (/manage/account/,
// /manage/organizations/) that stay separate.
type Pattern struct {
	Key       string // shape key
	Prefix    string // first path segment
	Template  string // /project/{name}/
	Param     string // name | id (first varying segment)
	Pos       int    // first segment index that varies
	Params    []string
	Positions []int  // all varying segment indexes (at most 2)
	Kind      string // items | sections
	Members   []string
	Count     int
	AnyPrefix bool            // /{owner}/{name}: the first segment varies too
	Sections  map[string]bool // with AnyPrefix: first segments that are site sections (/docs, /blog)
}

var patternKindOptions = [][2]string{
	{"items", "Many pages of the same kind; the varying part is an identifier chosen by users or a catalog (a package or model name, an id, a slug, a user name): one template is enough."},
	{"sections", "Pages whose varying part is a fixed word chosen by the site (account, settings, organizations, industry, articles, a docs chapter): each page has its own meaning; keep each one."},
}

func shapeKey(u *url.URL) (string, []string, bool) {
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) < 2 || segs[0] == "" {
		return "", nil, false
	}
	slash := strings.HasSuffix(u.Path, "/")
	return fmt.Sprintf("%s/%d/%t", segs[0], len(segs), slash), segs, slash
}

// detectPatterns groups same-shape paths and asks the decision point path.pattern
// whether each family is items or sections. Rules give the facts (count, example
// values); the decision model decides; the rule fallback runs without a model.
func (e *Explorer) detectPatterns(ctx context.Context, urls []string) []*Pattern {
	type grp struct {
		segs  [][]string
		paths []string
		slash bool
	}
	groups := map[string]*grp{}
	seen := map[string]bool{}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.RawQuery != "" || seen[u.Path] {
			continue
		}
		key, segs, slash := shapeKey(u)
		if key == "" {
			continue
		}
		seen[u.Path] = true
		g := groups[key]
		if g == nil {
			g = &grp{slash: slash}
			groups[key] = g
		}
		g.segs = append(g.segs, segs)
		g.paths = append(g.paths, u.Path)
	}
	// Owner namespaces (/{owner}/{name} on GitHub or Hugging Face): many first segments with few
	// pages each, none of them a top-level page of the site, form one family.
	top := map[string]bool{}
	for _, raw := range urls {
		if u, err := url.Parse(raw); err == nil {
			// A one-segment page is a site section, unless it is only linked from repeated
			// cards (owner avatars on GitHub or Hugging Face).
			if segs := strings.Split(strings.Trim(u.Path, "/"), "/"); len(segs) == 1 && segs[0] != "" && !e.cardPrefix[segs[0]] {
				top[segs[0]] = true
			}
		}
	}
	type ownerGrp struct {
		keys  []string
		segs  [][]string
		paths []string
		slash bool
	}
	for k := range e.navPrefix {
		top[k] = true
	}
	owners := map[string]*ownerGrp{}
	for key, g := range groups {
		if top[g.segs[0][0]] {
			continue
		}
		ok := fmt.Sprintf("*/%d/%t", len(g.segs[0]), g.slash)
		o := owners[ok]
		if o == nil {
			o = &ownerGrp{slash: g.slash}
			owners[ok] = o
		}
		o.keys = append(o.keys, key)
		o.segs = append(o.segs, g.segs...)
		o.paths = append(o.paths, g.paths...)
	}
	var out []*Pattern
	for ok, o := range owners {
		// A big group that is most of the family is a section of its own (/products/...).
		for i := 0; i < len(o.keys); i++ {
			if g := groups[o.keys[i]]; len(g.segs) >= 50 && len(g.segs)*10 > len(o.paths)*3 {
				o.keys = append(o.keys[:i], o.keys[i+1:]...)
				o.paths = o.paths[:0]
				o.segs = o.segs[:0]
				for _, k := range o.keys {
					o.paths = append(o.paths, groups[k].paths...)
					o.segs = append(o.segs, groups[k].segs...)
				}
				i = -1
			}
		}
		if len(o.keys) < 4 {
			continue
		}
		var vary []int
		for i := 0; i < len(o.segs[0]); i++ {
			for _, sg := range o.segs[1:] {
				if sg[i] != o.segs[0][i] {
					vary = append(vary, i)
					break
				}
			}
		}
		if len(vary) == 0 || len(vary) > 2 || vary[0] != 0 {
			continue
		}
		for _, k := range o.keys {
			delete(groups, k)
		}
		tpl := append([]string(nil), o.segs[0]...)
		params := []string{"owner"}
		tpl[0] = "{owner}"
		if len(vary) == 2 {
			params = append(params, "name")
			tpl[vary[1]] = "{name}"
		}
		t := "/" + strings.Join(tpl, "/")
		if o.slash {
			t += "/"
		}
		out = append(out, &Pattern{Key: ok, Prefix: "owner", Template: t, Param: "owner", Pos: 0, Params: params, Positions: vary,
			Members: o.paths, Count: len(o.paths), AnyPrefix: true, Sections: top})
	}
	for key, g := range groups {
		if len(g.segs) < 2 {
			continue
		}
		first := g.segs[0]
		var vary []int
		for i := 1; i < len(first); i++ {
			for _, sg := range g.segs[1:] {
				if sg[i] != first[i] {
					vary = append(vary, i)
					break
				}
			}
		}
		if len(vary) == 0 || len(vary) > 2 {
			continue
		}
		tpl := append([]string(nil), first...)
		var params []string
		for n, i := range vary {
			vals := []string{}
			for _, sg := range g.segs {
				vals = append(vals, sg[i])
			}
			name := "name"
			switch {
			case allNumeric(vals):
				name = "id"
			case n > 0 && allVersions(vals):
				name = "version"
			case n > 0:
				name = "name2"
			}
			params = append(params, name)
			tpl[i] = "{" + name + "}"
		}
		t := "/" + strings.Join(tpl, "/")
		if g.slash {
			t += "/"
		}
		out = append(out, &Pattern{Key: key, Prefix: first[0], Template: t, Param: params[0], Pos: vary[0], Params: params, Positions: vary, Members: g.paths, Count: len(g.paths)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	e.decidePatternKinds(ctx, out)
	return out
}

func (e *Explorer) decidePatternKinds(ctx context.Context, ps []*Pattern) {
	if len(ps) == 0 {
		return
	}
	if len(ps) > 40 {
		for _, p := range ps[40:] {
			p.Kind = rulePatternKind(p)
		}
		ps = ps[:40]
	}
	qs := map[string]decide.Question{}
	subj := map[string]string{}
	state := map[string]any{}
	for i, p := range ps {
		if p.Count >= 50 {
			p.Kind = "items" // fixed-name sections never number in the fifties
			continue
		}
		id := fmt.Sprintf("p%d", i)
		ex := p.Members
		if len(ex) > 10 {
			ex = ex[:10]
		}
		state[id] = map[string]any{"template": p.Template, "pages": p.Count, "examples": ex}
		qs[id] = decide.Choice("Are the pages of path family "+id+" interchangeable items or distinct sections?", patternKindOptions)
		subj[id] = p.Template
	}
	if len(qs) == 0 {
		return
	}
	ans, err := e.D.Ask(ctx, "path.pattern", map[string]any{"site": e.origin, "families": state}, qs, subj)
	for i, p := range ps {
		if p.Kind != "" {
			continue
		}
		if err == nil {
			if a, ok := ans[fmt.Sprintf("p%d", i)]; ok && a.Choice != "" {
				p.Kind = a.Choice
				continue
			}
		}
		p.Kind = rulePatternKind(p)
	}
}

var wordSeg = regexp.MustCompile(`^[a-z]+$`)
var versionSeg = regexp.MustCompile(`^v?\d+(\.\d+)*([.-][0-9a-z]+)*$`)

func allVersions(vs []string) bool {
	for _, v := range vs {
		if !versionSeg.MatchString(v) {
			return false
		}
	}
	return len(vs) > 0
}

func rulePatternKind(p *Pattern) string {
	if p.Count >= 5 {
		return "items"
	}
	words := 0
	for _, m := range p.Members {
		segs := strings.Split(strings.Trim(m, "/"), "/")
		if p.Pos < len(segs) && wordSeg.MatchString(segs[p.Pos]) {
			words++
		}
	}
	if words == p.Count {
		return "sections"
	}
	return "items"
}

// match returns the pattern a path belongs to, if any.
func matchPattern(ps []*Pattern, raw string) *Pattern {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	key, segs, slash := shapeKey(u)
	for _, p := range ps {
		if p.Key == key {
			return p
		}
	}
	if key == "" {
		return nil
	}
	any := fmt.Sprintf("*/%d/%t", len(segs), slash)
	for _, p := range ps {
		if p.AnyPrefix && p.Key == any && !p.Sections[segs[0]] {
			return p
		}
	}
	return nil
}

// templateRe turns /api/v1/crates/{name}/owners into a regexp for literal URLs.
func templateRe(tpl string) *regexp.Regexp {
	parts := regexp.MustCompile(`\{\{?[A-Za-z0-9_]+\}?\}`).Split(tpl, -1)
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile("^" + strings.Join(parts, `[^/]+`) + "$")
}

// underItems returns the item family whose template is a strict prefix of the URL path:
// /{owner}/{name}/stargazers lies under /{owner}/{name}.
func underItems(ps []*Pattern, u *url.URL) *Pattern {
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	for _, p := range ps {
		n := len(strings.Split(strings.Trim(p.Template, "/"), "/"))
		if p.Kind != "items" || len(segs) <= n {
			continue
		}
		head := "/" + strings.Join(segs[:n], "/")
		if strings.HasSuffix(p.Template, "/") {
			head += "/"
		}
		if matchPattern([]*Pattern{p}, u.Scheme+"://"+u.Host+head) == p {
			return p
		}
	}
	return nil
}
