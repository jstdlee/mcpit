package explore

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jstdlee/mcpit/cli/internal/decide"
)

// Decision points used during exploration. Rules give facts; the decision model
// decides; the rule fallback runs only when no decision model is configured.

var reqKindOptions = [][2]string{
	{"api", "A data endpoint a user task could call: search, list, detail, filter, lookup."},
	{"asset", "A static file or UI text: script, style, font, image, manifest, translations."},
	{"tracking", "Analytics, telemetry, error reporting or ads."},
	{"auth", "Login, session or token handling."},
	{"other", "Bot checks, feature flags or anything else."},
}

var formKindOptions = [][2]string{
	{"search", "Searches the site content by text."},
	{"filter", "Filters or sorts a list without free text."},
	{"login", "Signs in with a user name or email and a password."},
	{"signup", "Creates a new account."},
	{"checkout", "Pays for or orders something."},
	{"contact", "Sends a message to the site owner."},
	{"comment", "Posts a comment or review."},
	{"subscribe", "Signs up for a newsletter or alerts."},
	{"settings", "Changes how the page looks or its language: theme, font size, locale, cookie consent."},
	{"other", "Anything else."},
}

var effectOptions = [][2]string{
	{"read", "Only reads data; changes nothing."},
	{"write", "Creates or changes data, sends a message, or adds to a cart."},
	{"payment", "Pays, orders or transfers money."},
	{"destructive", "Deletes data or closes an account."},
}

var paramRoleOptions = [][2]string{
	{"query", "Free search text typed by the user."},
	{"page", "Page number, offset or cursor."},
	{"size", "Page size or result limit."},
	{"sort", "Sort field or order."},
	{"filter", "A filter value such as category, color or price."},
	{"id", "The id of one item."},
	{"token", "A session, CSRF or anti-bot token."},
	{"tracking", "Analytics or campaign tracking value."},
	{"const", "A fixed client setting such as locale or version."},
}

// assetTypes are CDP resource types that are facts, not decisions.
var assetTypes = map[string]bool{"Image": true, "Font": true, "Stylesheet": true, "Media": true,
	"Manifest": true, "TextTrack": true, "Script": true, "Document": true}

// classifyRequests decides req.kind for the requests that rules cannot settle.
func (e *Explorer) classifyRequests(ctx context.Context, page string, entries []*NetEntry) map[*NetEntry]string {
	out := map[*NetEntry]string{}
	state := map[string]any{"page": page}
	reqs := map[string]any{}
	qs := map[string]decide.Question{}
	subj := map[string]string{}
	byID := map[string]*NetEntry{}
	for i, n := range entries {
		if assetTypes[n.Type] {
			out[n] = "asset" // fact from the resource type
			continue
		}
		id := fmt.Sprintf("r%d", i)
		reqs[id] = map[string]any{"url": n.URL, "method": n.Method, "type": n.Type, "mime": n.MIME,
			"status": n.Status, "initiator": n.Initiator, "body": clip(n.Body, 300), "preview": clip(n.Preview, 200)}
		qs[id] = decide.Choice("What is network request "+id+"?", reqKindOptions)
		subj[id] = n.Method + " " + n.URL
		byID[id] = n
	}
	if len(qs) == 0 {
		return out
	}
	state["requests"] = reqs
	ans, err := e.D.Ask(ctx, "req.kind", state, qs, subj)
	for id, n := range byID {
		if err == nil {
			if a, ok := ans[id]; ok {
				out[n] = a.Choice
				continue
			}
		}
		out[n] = ruleReqKind(n)
	}
	return out
}

var trackingHost = regexp.MustCompile(`(google-analytics|googletagmanager|doubleclick|segment\.(io|com)|sentry\.io|hotjar|mixpanel|amplitude|facebook\.com/tr|clarity\.ms|plausible|posthog)`)

func ruleReqKind(n *NetEntry) string {
	u := strings.ToLower(n.URL)
	switch {
	case trackingHost.MatchString(u) || n.Type == "Ping":
		return "tracking"
	case strings.Contains(u, "/cdn-cgi/"):
		return "other"
	case regexp.MustCompile(`/(auth|login|logout|session|token|oauth)\b`).MatchString(u):
		return "auth"
	case regexp.MustCompile(`/(i18n|locales?|translations?)/`).MatchString(u):
		return "asset"
	case strings.Contains(n.MIME, "json") || strings.Contains(u, "graphql"):
		return "api"
	}
	return "other"
}

func (e *Explorer) classifyForms(ctx context.Context, forms []Form) []string {
	out := make([]string, len(forms))
	qs := map[string]decide.Question{}
	subj := map[string]string{}
	fs := map[string]any{}
	for i, f := range forms {
		id := fmt.Sprintf("f%d", i)
		fs[id] = map[string]any{"page": f.Page, "action": f.Action, "method": f.Method, "fields": fieldSummary(f.Fields), "submit": f.Submit, "role": f.Role}
		qs[id] = decide.Choice("What kind of form is "+id+"?", formKindOptions)
		subj[id] = f.Method + " " + f.Action
	}
	if len(qs) == 0 {
		return out
	}
	ans, err := e.D.Ask(ctx, "form.kind", map[string]any{"forms": fs}, qs, subj)
	for i, f := range forms {
		id := fmt.Sprintf("f%d", i)
		if err == nil {
			if a, ok := ans[id]; ok {
				out[i] = a.Choice
				continue
			}
		}
		out[i] = ruleFormKind(f)
	}
	return out
}

func fieldSummary(fs []Field) []map[string]any {
	var out []map[string]any
	for _, f := range fs {
		m := map[string]any{"name": f.Name, "type": f.Type}
		if f.Label != "" {
			m["label"] = f.Label
		}
		if len(f.Options) > 0 {
			m["options"] = f.Options
		}
		out = append(out, m)
	}
	return out
}

func ruleFormKind(f Form) string {
	names := ""
	for _, x := range f.Fields {
		names += " " + strings.ToLower(x.Name+" "+x.Type+" "+x.Label)
	}
	switch {
	case strings.Contains(names, "password") && strings.Contains(names, "confirm"):
		return "signup"
	case strings.Contains(names, "password"):
		return "login"
	case regexp.MustCompile(`card|cvv|payment`).MatchString(names):
		return "checkout"
	case regexp.MustCompile(`\b(q|query|search|keyword)\b`).MatchString(names) || f.Role == "search":
		return "search"
	case regexp.MustCompile(`message|subject`).MatchString(names):
		return "contact"
	case regexp.MustCompile(`comment|review`).MatchString(names):
		return "comment"
	case regexp.MustCompile(`newsletter|subscribe`).MatchString(names) || (len(f.Fields) == 1 && strings.Contains(names, "email")):
		return "subscribe"
	case f.Method == "GET":
		return "filter"
	}
	return "other"
}

// decideEffects asks tool.effect for tools whose method does not settle it.
func (e *Explorer) decideEffects(ctx context.Context, cands []*candidate) {
	qs := map[string]decide.Question{}
	subj := map[string]string{}
	ts := map[string]any{}
	for i, c := range cands {
		if c.effectFact != "" {
			c.tool.Effect = c.effectFact
			continue
		}
		id := fmt.Sprintf("t%d", i)
		ts[id] = map[string]any{"method": c.tool.Request.Method, "url": c.tool.Request.URL, "body": c.sampleBody, "kind": c.formKind, "fields": c.paramNames()}
		qs[id] = decide.Choice("What does calling tool "+id+" do to data on the site?", effectOptions)
		subj[id] = c.tool.Request.Method + " " + c.tool.Request.URL
	}
	if len(qs) == 0 {
		return
	}
	ans, err := e.D.Ask(ctx, "tool.effect", map[string]any{"tools": ts}, qs, subj)
	for i, c := range cands {
		id := fmt.Sprintf("t%d", i)
		if _, ok := qs[id]; !ok {
			continue
		}
		if err == nil {
			if a, ok := ans[id]; ok {
				c.tool.Effect = a.Choice
				continue
			}
		}
		c.tool.Effect = ruleEffect(c)
	}
}

func ruleEffect(c *candidate) string {
	s := strings.ToLower(c.tool.Request.URL + " " + c.sampleBody + " " + c.formKind)
	switch {
	case regexp.MustCompile(`checkout|payment|order/submit|purchase`).MatchString(s):
		return "payment"
	case regexp.MustCompile(`delete|remove|destroy`).MatchString(s):
		return "destructive"
	case strings.Contains(s, "graphql") && regexp.MustCompile(`"query"\s*:\s*"\s*(query|\{)`).MatchString(c.sampleBody):
		return "read"
	case c.formKind == "search" || c.formKind == "filter":
		return "read"
	}
	return "write"
}

// decideParamRoles asks param.role for every parameter of every candidate in one batch.
func (e *Explorer) decideParamRoles(ctx context.Context, cands []*candidate) {
	qs := map[string]decide.Question{}
	subj := map[string]string{}
	ps := map[string]any{}
	type ref struct {
		c    *candidate
		name string
	}
	refs := map[string]ref{}
	for i, c := range cands {
		for j, p := range c.params {
			if p.roleFact != "" {
				p.role = p.roleFact
				continue
			}
			id := fmt.Sprintf("p%d_%d", i, j)
			ps[id] = map[string]any{"endpoint": c.tool.Request.Method + " " + c.tool.Request.URL, "name": p.name, "values": p.values, "in": p.in}
			qs[id] = decide.Choice("What is the role of parameter "+id+"?", paramRoleOptions)
			subj[id] = c.tool.Request.URL + " ?" + p.name
			refs[id] = ref{c, p.name}
		}
	}
	if len(qs) == 0 {
		return
	}
	ans, err := e.D.Ask(ctx, "param.role", map[string]any{"probeText": e.ProbeText, "params": ps}, qs, subj)
	for id, r := range refs {
		p := r.c.param(r.name)
		if err == nil {
			if a, ok := ans[id]; ok {
				p.role = a.Choice
				continue
			}
		}
		p.role = ruleParamRole(p, e.ProbeText)
	}
}

func ruleParamRole(p *param, probe string) string {
	n := strings.ToLower(p.name)
	for _, v := range p.values {
		if strings.EqualFold(v, probe) {
			return "query"
		}
	}
	switch {
	case regexp.MustCompile(`^(page|p|offset|cursor|after|start)$`).MatchString(n):
		return "page"
	case regexp.MustCompile(`^(limit|size|per_?page|count|rows|pagesize)$`).MatchString(n):
		return "size"
	case regexp.MustCompile(`sort|order`).MatchString(n):
		return "sort"
	case regexp.MustCompile(`^(utm_|gclid|fbclid|_ga)`).MatchString(n):
		return "tracking"
	case regexp.MustCompile(`csrf|token|nonce|_t$`).MatchString(n):
		return "token"
	case regexp.MustCompile(`^(q|query|search|keyword|term|text)$`).MatchString(n):
		return "query"
	case regexp.MustCompile(`(^|_)id$`).MatchString(n):
		return "id"
	case regexp.MustCompile(`^(lang|locale|client|v|version|format|hl)$`).MatchString(n):
		return "const"
	}
	return "filter"
}

// rankLinks uses link.next to order links when the page budget cannot visit them all.
func (e *Explorer) rankLinks(ctx context.Context, links []string) []string {
	if !e.D.Available() || len(links) <= 1 {
		return ruleRankLinks(links)
	}
	qs := map[string]decide.Question{}
	subj := map[string]string{}
	ls := map[string]any{}
	for i, l := range links {
		id := fmt.Sprintf("l%d", i)
		ls[id] = l
		qs[id] = decide.Question{Type: "score", Instructions: "How likely is it that link " + id + " opens a page with search, filters, lists of items or forms?",
			Criteria: []string{"Very unlikely", "Unlikely", "Likely", "Very likely"}}
		subj[id] = l
	}
	ans, err := e.D.Ask(ctx, "link.next", map[string]any{"links": ls}, qs, subj)
	if err != nil {
		return ruleRankLinks(links)
	}
	type sc struct {
		l string
		s float64
	}
	var arr []sc
	for i, l := range links {
		arr = append(arr, sc{l, ans[fmt.Sprintf("l%d", i)].Score})
	}
	for i := 1; i < len(arr); i++ {
		for j := i; j > 0 && arr[j].s > arr[j-1].s; j-- {
			arr[j], arr[j-1] = arr[j-1], arr[j]
		}
	}
	out := make([]string, len(arr))
	for i, x := range arr {
		out[i] = x.l
	}
	return out
}

func ruleRankLinks(links []string) []string {
	score := func(l string) int {
		u, _ := url.Parse(l)
		s := 0
		if u != nil && u.RawQuery != "" {
			s += 2
		}
		if regexp.MustCompile(`search|list|categor|product|catalog|browse|shop|find|item`).MatchString(strings.ToLower(l)) {
			s += 3
		}
		return s
	}
	out := append([]string(nil), links...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && score(out[j]) > score(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// decideUseful asks tool.useful for found (not declared) tools: would an agent call it
// for a user task? It drops page-data bundles, ad and telemetry endpoints and UI forms.
func (e *Explorer) decideUseful(ctx context.Context, cands []*candidate) []*candidate {
	if !e.D.Available() {
		return cands
	}
	qs := map[string]decide.Question{}
	subj := map[string]string{}
	tools := map[string]any{}
	for i, c := range cands {
		if c.source == "openapi" || c.source == "opensearch" {
			continue // declared by the site
		}
		if c.formKind != "" && c.formKind != "other" && c.formKind != "filter" {
			continue // form.kind already decided what this form is for
		}
		id := fmt.Sprintf("t%d", i)
		tools[id] = map[string]any{"method": c.tool.Request.Method, "url": c.tool.Request.URL, "params": c.paramNames(), "form": c.formKind, "sample": clip(c.samplePreview, 160)}
		qs[id] = decide.Noul("Tool " + id + " gives an agent content or an action a user of this site would want. Page data bundles, analytics, ads, account status checks and display settings do not count.")
		subj[id] = c.tool.Request.Method + " " + c.tool.Request.URL
	}
	if len(qs) == 0 {
		return cands
	}
	ans, err := e.D.Ask(ctx, "tool.useful", map[string]any{"site": e.origin, "tools": tools}, qs, subj)
	if err != nil {
		return cands
	}
	var out []*candidate
	for i, c := range cands {
		a, asked := ans[fmt.Sprintf("t%d", i)]
		if asked && a.Noul <= 0.2 {
			e.say("drop %s %s (tool.useful %.2f)", c.tool.Request.Method, c.tool.Request.URL, a.Noul)
			continue
		}
		out = append(out, c)
	}
	return out
}
