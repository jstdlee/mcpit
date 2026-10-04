// Package browser loads one page in headless Chrome and returns its visible text. The
// explorer uses it to test tools behind browser checks; the executor uses it for tools
// whose executor is "headless".
package browser

import (
	"context"
	"os"
	"time"

	"github.com/chromedp/chromedp"
)

type Page struct {
	URL    string
	Title  string
	Text   string
	Status int
}

const textJS = `(() => {
  const clone = document.body ? document.body.cloneNode(true) : null;
  if (!clone) return '';
  clone.querySelectorAll('script,style,noscript,svg,template').forEach(n => n.remove());
  return clone.innerText || '';
})()`

// Fetch opens url in a fresh headless browser, waits for scripts, and returns the text.
func Fetch(parent context.Context, url string) (*Page, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.WindowSize(1280, 900),
		chromedp.UserAgent("Mozilla/5.0 (X11; Linux) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140 Safari/537.36 mcpit/0.1"),
	)
	if p := os.Getenv("MCPIT_CHROME"); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}
	actx, cancelA := chromedp.NewExecAllocator(parent, opts...)
	defer cancelA()
	tctx, cancelT := chromedp.NewContext(actx)
	defer cancelT()
	ctx, cancel := context.WithTimeout(tctx, 45*time.Second)
	defer cancel()
	// Start the tab on the long-lived context first: chromedp binds the browser to the first
	// context it runs on, and cancelling a shorter navigation context would close the tab.
	if err := chromedp.Do(ctx); err != nil {
		return nil, err
	}
	navCtx, cancelNav := context.WithTimeout(ctx, 25*time.Second)
	navErr := chromedp.Do(navCtx, chromedp.Navigate(url))
	cancelNav()
	if navErr != nil && ctx.Err() != nil {
		return nil, navErr
	}
	// Browser checks often finish with a reload: give them a moment.
	_ = chromedp.Do(ctx, chromedp.Sleep(2500*time.Millisecond))
	title, _ := chromedp.Run(ctx, chromedp.Evaluate[string](`document.title`))
	text, err := chromedp.Run(ctx, chromedp.Evaluate[string](textJS))
	if err != nil {
		return nil, err
	}
	loc, _ := chromedp.Run(ctx, chromedp.Evaluate[string](`location.href`))
	return &Page{URL: loc, Title: title, Text: text, Status: 200}, nil
}
