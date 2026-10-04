package explore

import (
	"net/url"
	"regexp"
	"strings"
)

// Robots is a robots.txt file parsed per RFC 9309: user-agent groups, allow and
// disallow rules with "*" and "$", longest match wins, allow wins a tie.
type Robots struct {
	groups   []robotsGroup
	Sitemaps []string
	Extra    map[string]string // non-standard lines such as "Llms:"
}

type robotsGroup struct {
	agents []string
	rules  []robotsRule
}

type robotsRule struct {
	allow bool
	path  string
	re    *regexp.Regexp
}

// ParseRobots reads a robots.txt body.
func ParseRobots(text string) *Robots {
	r := &Robots{Extra: map[string]string{}}
	var cur *robotsGroup
	lastWasAgent := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "user-agent":
			if cur == nil || !lastWasAgent {
				r.groups = append(r.groups, robotsGroup{})
				cur = &r.groups[len(r.groups)-1]
			}
			cur.agents = append(cur.agents, strings.ToLower(v))
			lastWasAgent = true
			continue
		case "allow", "disallow":
			if cur != nil {
				if v == "" {
					if k == "disallow" {
						break // empty disallow allows everything
					}
				} else {
					cur.rules = append(cur.rules, robotsRule{allow: k == "allow", path: v, re: robotsPattern(v)})
				}
			}
		case "sitemap":
			r.Sitemaps = appendUnique(r.Sitemaps, v)
		default:
			if v != "" {
				r.Extra[k] = v
			}
		}
		lastWasAgent = false
	}
	return r
}

func robotsPattern(p string) *regexp.Regexp {
	anchored := strings.HasSuffix(p, "$")
	p = strings.TrimSuffix(p, "$")
	var b strings.Builder
	b.WriteString("^")
	for _, part := range strings.Split(p, "*") {
		if b.Len() > 1 {
			b.WriteString(".*")
		}
		b.WriteString(regexp.QuoteMeta(part))
	}
	if anchored {
		b.WriteString("$")
	}
	return regexp.MustCompile(b.String())
}

// Allowed reports whether agent (a product token such as "mcpit") may fetch rawURL.
func (r *Robots) Allowed(agent, rawURL string) bool {
	if r == nil {
		return true
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	rules := r.rulesFor(strings.ToLower(agent))
	best, bestLen, allow := -1, -1, true
	for i, rule := range rules {
		if rule.re.MatchString(path) {
			l := len(rule.path)
			if l > bestLen || (l == bestLen && rule.allow) {
				best, bestLen, allow = i, l, rule.allow
			}
		}
	}
	if best < 0 {
		return true
	}
	return allow
}

// rulesFor joins every group that names the agent; without one, the "*" groups.
func (r *Robots) rulesFor(agent string) []robotsRule {
	var named, star []robotsRule
	for _, g := range r.groups {
		for _, a := range g.agents {
			switch {
			case a == agent:
				named = append(named, g.rules...)
			case a == "*":
				star = append(star, g.rules...)
			}
		}
	}
	if named != nil {
		return named
	}
	return star
}
