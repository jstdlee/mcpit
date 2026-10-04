package explore

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// NetEntry is one request, with the fields the DevTools Network panel shows.
type NetEntry struct {
	ID        string `json:"-"`
	Page      string `json:"page"`
	Action    string `json:"action,omitempty"` // load | scroll | type | click | submit
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

// Form is one <form> from the DOM tree.
type Form struct {
	Page   string  `json:"page"`
	Action string  `json:"action"`
	Method string  `json:"method"`
	Fields []Field `json:"fields"`
	Submit string  `json:"submit,omitempty"`
	Role   string  `json:"role,omitempty"` // ARIA role of the form or parent
	ID     string  `json:"-"`
}

// Input is a text-like control a user types into (in or outside a form).
type Input struct {
	Sel         string `json:"sel"`
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Type        string `json:"type"`
	Placeholder string `json:"placeholder,omitempty"`
	Label       string `json:"label,omitempty"`
	Visible     bool   `json:"visible"`
	InForm      bool   `json:"inForm,omitempty"`
}

// Button is a clickable control that is not a link.
type Button struct {
	Sel   string `json:"sel"`
	Label string `json:"label"`
	Type  string `json:"type,omitempty"`
	Role  string `json:"role,omitempty"`
}

// PageResult is what one visit captured.
type PageResult struct {
	URL      string
	Title    string
	Text     string // start of the visible text
	Blocked  bool   // the page is a browser check, captcha or error page
	Heading  string
	MetaDesc string
	LLMsLink string
	Links    []string
	Forms    []Form
	Inputs   []Input
	Buttons  []Button
	Net      []*NetEntry
	Scripts  []string
	Search   []string // selectors of inputs that were typed into
	Clicked  []string // labels of buttons that were clicked
	DOMSize  int
}

// ButtonPicker returns the selectors of buttons that are safe and useful to click
// (tabs, location, "more"). The explorer answers it with the decision point button.kind.
type ButtonPicker func(ctx context.Context, page string, buttons []Button) []string

// Capturer drives one headless Chromium over CDP.
type Capturer struct {
	allocCtx    context.Context
	cancel      context.CancelFunc
	ProbeValues []string // typed into every text input, one after another
	Wait        time.Duration
	Geo         [2]float64 // position reported to navigator.geolocation
	PickButtons ButtonPicker
	MaxClicks   int
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
	return &Capturer{allocCtx: ctx, cancel: cancel, ProbeValues: []string{"test"}, Wait: 1500 * time.Millisecond,
		Geo: [2]float64{40.7128, -74.0060}, MaxClicks: 8}
}

func (c *Capturer) Close() { c.cancel() }

// geoJS answers navigator.geolocation with a fixed position, so location buttons work headless.
const geoJS = `(() => {
  const pos = {coords: {latitude: %f, longitude: %f, accuracy: 30, altitude: null, altitudeAccuracy: null, heading: null, speed: null}, timestamp: Date.now()};
  const geo = {getCurrentPosition: (ok) => setTimeout(() => ok(pos), 50), watchPosition: (ok) => { setTimeout(() => ok(pos), 50); return 1; }, clearWatch: () => {}};
  try { Object.defineProperty(navigator, 'geolocation', {get: () => geo}); } catch (e) {}
  if (navigator.permissions && navigator.permissions.query) {
    const q = navigator.permissions.query.bind(navigator.permissions);
    navigator.permissions.query = (d) => d && d.name === 'geolocation' ? Promise.resolve({state: 'granted', onchange: null}) : q(d);
  }
})()`

const domJS = `(() => {
  const vis = el => !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length) && getComputedStyle(el).visibility !== 'hidden';
  const label = el => {
    if (el.labels && el.labels[0]) return el.labels[0].innerText.trim().slice(0, 80);
    return (el.getAttribute('aria-label') || el.placeholder || el.title || '').trim().slice(0, 80);
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
  const textTypes = ['text', 'search', 'tel', 'number', 'url', ''];
  let n = Number(document.documentElement.getAttribute('data-mcpit-n') || 0);
  const mark = el => {
    if (!el.hasAttribute('data-mcpit')) { el.setAttribute('data-mcpit', String(n++)); }
    return '[data-mcpit="' + el.getAttribute('data-mcpit') + '"]';
  };
  const inputs = [...document.querySelectorAll('input, textarea')]
    .filter(el => el.tagName === 'TEXTAREA' || textTypes.includes((el.getAttribute('type') || '').toLowerCase()))
    .filter(el => !el.disabled && !el.readOnly)
    .slice(0, 30)
    .map(el => ({sel: mark(el), id: el.id || '', name: el.name || '', type: (el.getAttribute('type') || el.tagName).toLowerCase(),
      placeholder: (el.placeholder || '').slice(0, 80), label: label(el), visible: vis(el), inForm: !!el.form}));
  const buttons = [...document.querySelectorAll('button, [role="button"], [role="tab"], input[type="button"]')]
    .filter(el => vis(el) && !el.disabled && !el.closest('form'))
    .slice(0, 40)
    .map(el => ({sel: mark(el), label: ((el.innerText || el.value || '').trim() || el.getAttribute('aria-label') || el.title || el.id || el.className || '').toString().slice(0, 80),
      type: (el.getAttribute('type') || '').toLowerCase(), role: el.getAttribute('role') || ''}));
  document.documentElement.setAttribute('data-mcpit-n', String(n));
  const links = [...document.querySelectorAll('a[href]')].map(a => a.href).filter(h => /^https?:/.test(h));
  const scripts = [...document.scripts].map(s => s.src).filter(Boolean);
  const h = document.querySelector('h1') || document.querySelector('h2');
  const meta = document.querySelector('meta[name="description"]');
  const llms = document.querySelector('link[rel="llms"]');
  return {title: document.title, text: (document.body ? document.body.innerText : '').replace(/\s+/g, ' ').trim().slice(0, 600), heading: h ? h.innerText.trim().slice(0, 120) : '', metaDesc: meta ? (meta.content || '').slice(0, 300) : '',
    llms: llms ? llms.href : '', forms, inputs, buttons, links: [...new Set(links)].slice(0, 400), scripts,
    domSize: document.getElementsByTagName('*').length};
})()`

type domResult struct {
	Title    string   `json:"title"`
	Text     string   `json:"text"`
	Heading  string   `json:"heading"`
	MetaDesc string   `json:"metaDesc"`
	LLMs     string   `json:"llms"`
	Forms    []Form   `json:"forms"`
	Inputs   []Input  `json:"inputs"`
	Buttons  []Button `json:"buttons"`
	Links    []string `json:"links"`
	Scripts  []string `json:"scripts"`
	DOMSize  int      `json:"domSize"`
}

// setValueJS types a value the way frameworks expect (native setter + input event).
const setValueJS = `((sel, v) => {
  const el = document.querySelector(sel); if (!el) return false;
  el.focus();
  const proto = el.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, v);
  el.dispatchEvent(new Event('input', {bubbles: true}));
  el.dispatchEvent(new Event('change', {bubbles: true}));
  return true;
})(%q, %q)`

const enterJS = `((sel) => {
  const el = document.querySelector(sel); if (!el) return false;
  for (const t of ['keydown', 'keypress', 'keyup']) el.dispatchEvent(new KeyboardEvent(t, {key: 'Enter', code: 'Enter', keyCode: 13, which: 13, bubbles: true}));
  return true;
})(%q)`

const clickJS = `((sel) => { const el = document.querySelector(sel); if (!el) return false; el.click(); return true; })(%q)`

// Visit loads one page, records its network traffic and DOM, then acts like a user:
// it scrolls, types probe values into every visible text input, and clicks the buttons
// the picker allows (tabs, location, more), typing into inputs those buttons reveal.
func (c *Capturer) Visit(parent context.Context, pageURL string, act bool) (*PageResult, error) {
	tabCtx, cancelTab := chromedp.NewContext(c.allocCtx)
	defer cancelTab()
	ctx, cancel := context.WithTimeout(tabCtx, 90*time.Second)
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

	setup := chromedp.Func(func(ctx context.Context, _ *chromedp.Target) error {
		if _, err := chromedp.Call(ctx, network.Enable, network.EnableParams{}); err != nil {
			return err
		}
		_, err := chromedp.Call(ctx, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: fmt.Sprintf(geoJS, c.Geo[0], c.Geo[1])})
		return err
	})
	if err := chromedp.Do(ctx, setup); err != nil {
		return nil, err
	}
	// Heavy pages may never fire "load": wait at most 25 s, then read the page as it is.
	navCtx, cancelNav := context.WithTimeout(ctx, 25*time.Second)
	navErr := chromedp.Do(navCtx, chromedp.Navigate(pageURL))
	cancelNav()
	if navErr != nil && ctx.Err() != nil {
		return nil, navErr
	}
	if err := chromedp.Do(ctx, chromedp.Sleep(c.Wait)); err != nil {
		return nil, err
	}
	dom, err := chromedp.Run(ctx, chromedp.Evaluate[domResult](domJS))
	if err != nil {
		return nil, err
	}
	res := &PageResult{URL: pageURL, Title: dom.Title, Text: dom.Text, Heading: dom.Heading, MetaDesc: dom.MetaDesc, LLMsLink: dom.LLMs,
		Links: dom.Links, Scripts: dom.Scripts, DOMSize: dom.DOMSize, Inputs: dom.Inputs, Buttons: dom.Buttons}
	for _, f := range dom.Forms {
		f.Page = pageURL
		res.Forms = append(res.Forms, f)
	}

	setAction := func(a string) {
		mu.Lock()
		action = a
		mu.Unlock()
	}
	eval := func(js string) {
		_, _ = chromedp.Run(ctx, chromedp.Evaluate[bool](js))
	}
	if act {
		setAction("scroll")
		_ = chromedp.Do(ctx, chromedp.Evaluate[chromedp.Void](`window.scrollTo(0, document.body.scrollHeight)`), chromedp.Sleep(c.Wait/2))
		eval(`window.scrollTo(0, 0) || true`)

		typed := map[string]bool{}
		typeAll := func(inputs []Input) {
			for _, in := range inputs {
				if !in.Visible || typed[in.Sel] {
					continue
				}
				typed[in.Sel] = true
				res.Search = append(res.Search, in.Sel)
				for _, v := range c.ProbeValues {
					setAction("type")
					eval(fmt.Sprintf(setValueJS, in.Sel, v))
					_ = chromedp.Do(ctx, chromedp.Sleep(c.Wait))
					if !in.InForm {
						setAction("submit")
						eval(fmt.Sprintf(enterJS, in.Sel))
						_ = chromedp.Do(ctx, chromedp.Sleep(c.Wait/2))
					}
					c.readBodies(ctx, &mu, entries, &order)
				}
				eval(fmt.Sprintf(setValueJS, in.Sel, ""))
			}
		}
		typeAll(dom.Inputs)

		if c.PickButtons != nil && len(dom.Buttons) > 0 {
			picked := c.PickButtons(ctx, pageURL, dom.Buttons)
			if len(picked) > c.MaxClicks {
				picked = picked[:c.MaxClicks]
			}
			labels := map[string]string{}
			for _, b := range dom.Buttons {
				labels[b.Sel] = b.Label
			}
			for _, sel := range picked {
				setAction("click")
				eval(fmt.Sprintf(clickJS, sel))
				_ = chromedp.Do(ctx, chromedp.Sleep(c.Wait))
				res.Clicked = append(res.Clicked, labels[sel])
				c.readBodies(ctx, &mu, entries, &order)
				// A tab may reveal inputs that were hidden before.
				if again, err := chromedp.Run(ctx, chromedp.Evaluate[domResult](domJS)); err == nil {
					typeAll(again.Inputs)
					for _, in := range again.Inputs {
						if !containsInput(res.Inputs, in.Sel) {
							res.Inputs = append(res.Inputs, in)
						}
					}
				}
			}
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

func containsInput(list []Input, sel string) bool {
	for _, x := range list {
		if x.Sel == sel {
			return true
		}
	}
	return false
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
