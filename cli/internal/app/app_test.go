package app

import (
	"testing"

	"github.com/jstdlee/mcpit/cli/internal/sitepack"
)

func TestCoveredBy(t *testing.T) {
	tpl := sitepack.Tool{ID: "item", Request: sitepack.Request{Method: "GET", URL: "https://x.example/{{owner}}/{{name}}/funding_links"}}
	lit := sitepack.Tool{ID: "lit", Request: sitepack.Request{Method: "GET", URL: "https://x.example/alice/llama/funding_links"}}
	if coveredBy(lit, []sitepack.Tool{tpl}) != "item" || coveredBy(tpl, []sitepack.Tool{tpl, lit}) != "" {
		t.Error("coveredBy")
	}
}
