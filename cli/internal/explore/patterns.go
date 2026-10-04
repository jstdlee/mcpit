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
}

var patternKindOptions = [][2]string{
	{"items", "Interchangeable items of one collection, such as products, projects, packages, users or articles: one template is enough."},
	{"sections", "Distinct pages with their own meaning, such as account, settings, organizations, about or help: keep each one."},
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
	var out []*Pattern
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
	qs := map[string]decide.Question{}
	subj := map[string]string{}
	state := map[string]any{}
	for i, p := range ps {
		id := fmt.Sprintf("p%d", i)
		ex := p.Members
		if len(ex) > 10 {
			ex = ex[:10]
		}
		state[id] = map[string]any{"template": p.Template, "pages": p.Count, "examples": ex}
		qs[id] = decide.Choice("Are the pages of path family "+id+" interchangeable items or distinct sections?", patternKindOptions)
		subj[id] = p.Template
	}
	ans, err := e.D.Ask(ctx, "path.pattern", map[string]any{"site": e.origin, "families": state}, qs, subj)
	for i, p := range ps {
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
	key, _, _ := shapeKey(u)
	for _, p := range ps {
		if p.Key == key {
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
