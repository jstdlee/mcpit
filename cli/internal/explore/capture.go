package explore

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// NetEntry is one request, with the fields the DevTools Network panel shows.
type NetEntry struct {
	ID        string `json:"-"`
	Page      string `json:"page"`
	Action    string `json:"action,omitempty"` // load | type | submit | scroll
	URL       string `json:"url"`
	Method    string `json:"method"`
	Type      string `json:"type"` // CDP resource type: XHR, Fetch, Script, Image, ...
	MIME      string `json:"mime,omitempty"`
	Status    int    `json:"status,omitempty"`
	Initiator string `json:"initiator,omitempty"`
	Body      string `json:"body,omitempty"`    // request body, truncated
	Preview   string `json:"preview,omitempty"` // response body, truncated
	ReqCookie bool   `json:"-"`
}

// Field is one form control from the DOM tree.
type Field struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Label    string   `json:"label,omitempty"`
	Required bool     `json:"required,omitempty"`
	Pattern  string   `json:"pattern,omitempty"`
	Options  []string `json:"options,omitempty"`
	Value    string   `json:"value,omitempty"` // hidden fields only
}

// Form is one <form> (or a standalone search box) from the DOM tree.
type Form struct {
	Page   string  `json:"page"`
	Action string  `json:"action"`
	Method string  `json:"method"`
	Fields []Field `json:"fields"`
	Submit string  `json:"submit,omitempty"`
	Role   string  `json:"role,omitempty"` // ARIA role of the form or parent
	ID     string  `json:"-"`
}

// PageResult is what one visit captured.
type PageResult struct {
	URL     string
	Title   string
	Links   []string
	Forms   []Form
	Net     []*NetEntry
	Scripts []string
	Search  []string // selectors of standalone search inputs
	DOMSize int
}

// Capturer drives one headless Chromium over CDP.
type Capturer struct {
	allocCtx  context.Context
	cancel    context.CancelFunc
	ProbeText string
	Wait      time.Duration
}

func NewCapturer(parent context.Context, chromePath string) *Capturer {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.WindowSize(1280, 900),
		chromedp.UserAgent("Mozilla/5.0 (X11; Linux) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140 Safari/537.36 mcpit/0.1"),
	)
	if chromePath != "" {
		opts = append(opts, chromedp.ExecPath(chromePath))
	}
	ctx, cancel := chromedp.NewExecAllocator(parent, opts...)
	return &Capturer{allocCtx: ctx, cancel: cancel, ProbeText: "test", Wait: 1500 * time.Millisecond}
}

func (c *Capturer) Close() { c.cancel() }

const domJS = `(() => {
  const vis = el => !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
  const label = el => {
    if (el.labels && el.labels[0]) return el.labels[0].innerText.trim().slice(0, 80);
    return (el.getAttribute('aria-label') || el.placeholder || '').trim().slice(0, 80);
  };
  const field = el => {
    const f = {name: el.name || el.id || '', type: (el.type || el.tagName).toLowerCase(), label: label(el)};
    if (el.required) f.required = true;
    if (el.pattern) f.pattern = el.pattern;
    if (el.tagName === 'SELECT') f.options = [...el.options].map(o => o.value).filter(v => v !== '').slice(0, 30);
    if (f.type === 'hidden') f.value = (el.value || '').slice(0, 200);
    return f;
  };
  const forms = [...document.forms].map(fm => ({
    action: fm.action || location.href,
    method: (fm.getAttribute('method') || 'GET').toUpperCase(),
    fields: [...fm.elements].filter(e => e.name && !['submit','button','reset','image'].includes(e.type)).map(field),
    submit: ([...fm.elements].find(e => e.type === 'submit') || {}).value || ([...fm.querySelectorAll('button')][0] || {}).innerText || '',
    role: fm.getAttribute('role') || (fm.closest('[role]') || {getAttribute(){return ''}}).getAttribute('role') || '',
  }));
  const search = [...document.querySelectorAll('input')].filter(el => !el.form && vis(el) &&
    (el.type === 'search' || /search|query|^q$|keyword/i.test(el.name + ' ' + el.id + ' ' + (el.placeholder||'') + ' ' + (el.getAttribute('aria-label')||''))))
    .map((el, i) => { el.setAttribute('data-mcpit-search', String(i)); return '[data-mcpit-search="' + i + '"]'; });
  const formSearch = [...document.forms].flatMap(fm => [...fm.elements].filter(el => el.tagName === 'INPUT' && vis(el) &&
    (el.type === 'search' || /search|query|^q$|keyword/i.test(el.name + ' ' + el.id + ' ' + (el.placeholder||'')))))
    .map((el, i) => { el.setAttribute('data-mcpit-fsearch', String(i)); return '[data-mcpit-fsearch="' + i + '"]'; });
  const links = [...document.querySelectorAll('a[href]')].map(a => a.href).filter(h => /^https?:/.test(h));
  const scripts = [...document.scripts].map(s => s.src).filter(Boolean);
  return {title: document.title, forms, search, formSearch, links: [...new Set(links)].slice(0, 400), scripts, domSize: document.getElementsByTagName('*').length};
})()`

type domResult struct {
	Title      string   `json:"title"`
	Forms      []Form   `json:"forms"`
	Search     []string `json:"search"`
	FormSearch []string `json:"formSearch"`
	Links      []string `json:"links"`
	Scripts    []string `json:"scripts"`
	DOMSize    int      `json:"domSize"`
}

// Visit loads one page, records its network traffic and DOM, then acts on
// standalone search boxes (type a safe probe value) to reveal live-search APIs.
func (c *Capturer) Visit(parent context.Context, pageURL string, act bool) (*PageResult, error) {
	tabCtx, cancelTab := chromedp.NewContext(c.allocCtx)
	defer cancelTab()
	ctx, cancel := context.WithTimeout(tabCtx, 45*time.Second)
	defer cancel()
	go func() {
		select {
		case <-parent.Done():
			cancel()
		case <-ctx.Done():
		}
	}()

	var mu sync.Mutex
	entries := map[network.RequestID]*NetEntry{}
	var order []network.RequestID
	action := "load"
	// Start the browser tab, then subscribe before navigating.
	if err := chromedp.Do(ctx); err != nil {
		return nil, err
	}
	reqEvents := chromedp.Events(ctx, network.RequestWillBeSent)
	respEvents := chromedp.Events(ctx, network.ResponseReceived)
	go func() {
		for e, err := range reqEvents {
			if err != nil {
				return
			}
			if e.Request == nil || strings.HasPrefix(e.Request.URL, "data:") || strings.HasPrefix(e.Request.URL, "blob:") {
				continue
			}
			mu.Lock()
			n := &NetEntry{ID: string(e.RequestID), Page: pageURL, Action: action, URL: e.Request.URL,
				Method: e.Request.Method, Type: string(e.Type), Initiator: initiator(e.Initiator)}
			if e.Request.HasPostData {
				n.Body = postData(e.Request)
			}
			if _, ok := entries[e.RequestID]; !ok {
				order = append(order, e.RequestID)
			}
			entries[e.RequestID] = n
			mu.Unlock()
		}
	}()
	go func() {
		for e, err := range respEvents {
			if err != nil {
				return
			}
			mu.Lock()
			if n := entries[e.RequestID]; n != nil && e.Response != nil {
				n.Status = int(e.Response.Status)
				n.MIME = e.Response.MimeType
				if n.Type == "" {
					n.Type = string(e.Type)
				}
			}
			mu.Unlock()
		}
	}()

	enable := chromedp.Func(func(ctx context.Context, _ *chromedp.Target) error {
		_, err := chromedp.Call(ctx, network.Enable, network.EnableParams{})
		return err
	})
	if err := chromedp.Do(ctx, enable, chromedp.Navigate(pageURL), chromedp.Sleep(c.Wait)); err != nil {
		return nil, err
	}
	dom, err := chromedp.Run(ctx, chromedp.Evaluate[domResult](domJS))
	if err != nil {
		return nil, err
	}
	res := &PageResult{URL: pageURL, Title: dom.Title, Links: dom.Links, Scripts: dom.Scripts, DOMSize: dom.DOMSize}
	for _, f := range dom.Forms {
		f.Page = pageURL
		res.Forms = append(res.Forms, f)
	}
	res.Search = dom.Search

	setAction := func(a string) {
		mu.Lock()
		action = a
		mu.Unlock()
	}
	if act {
		// Scroll once to trigger lazy loading, then type into standalone search boxes.
		setAction("scroll")
		_ = chromedp.Do(ctx, chromedp.Evaluate[chromedp.Void](`window.scrollTo(0, document.body.scrollHeight)`), chromedp.Sleep(c.Wait/2))
		for _, sel := range dom.Search {
			setAction("type")
			_ = chromedp.Do(ctx, chromedp.SendKeys(chromedp.CSS(sel), c.ProbeText), chromedp.Sleep(c.Wait))
			c.readBodies(ctx, &mu, entries, &order)
			setAction("submit")
			_ = chromedp.Do(ctx, chromedp.SendKeys(chromedp.CSS(sel), "\r"), chromedp.Sleep(c.Wait))
		}
	}
	c.readBodies(ctx, &mu, entries, &order)
	mu.Lock()
	for _, id := range order {
		if n := entries[id]; n != nil {
			res.Net = append(res.Net, n)
		}
	}
	mu.Unlock()
	return res, nil
}

// readBodies reads response previews for data-like requests while the browser still has them.
func (c *Capturer) readBodies(ctx context.Context, mu *sync.Mutex, entries map[network.RequestID]*NetEntry, order *[]network.RequestID) {
	mu.Lock()
	var todo []network.RequestID
	for _, id := range *order {
		if n := entries[id]; n != nil && n.Preview == "" && wantsBody(n) {
			todo = append(todo, id)
		}
	}
	mu.Unlock()
	for _, id := range todo {
		r, err := chromedp.Run(ctx, chromedp.Action[network.GetResponseBodyResult](func(ctx context.Context, _ *chromedp.Target) (network.GetResponseBodyResult, error) {
			return chromedp.Call(ctx, network.GetResponseBody, network.GetResponseBodyParams{RequestID: id})
		}))
		if err != nil {
			continue
		}
		mu.Lock()
		if n := entries[id]; n != nil {
			n.Preview = clip(string(r.Body), 600)
		}
		mu.Unlock()
	}
}

func wantsBody(n *NetEntry) bool {
	switch n.Type {
	case "XHR", "Fetch", "EventSource", "Other":
	default:
		return false
	}
	return n.Status > 0 && n.Status < 400 &&
		(strings.Contains(n.MIME, "json") || strings.Contains(n.MIME, "text") || strings.Contains(n.MIME, "xml") || n.MIME == "")
}

func initiator(in *network.Initiator) string {
	if in == nil {
		return ""
	}
	s := string(in.Type)
	if in.URL != "" {
		s += " " + in.URL
	} else if in.Stack != nil && len(in.Stack.CallFrames) > 0 {
		f := in.Stack.CallFrames[0]
		s += " " + f.FunctionName + "@" + f.URL
	}
	return clip(s, 160)
}

func postData(r *network.Request) string {
	var b strings.Builder
	for _, e := range r.PostDataEntries {
		b.Write(e.Bytes)
	}
	return clip(b.String(), 1000)
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
