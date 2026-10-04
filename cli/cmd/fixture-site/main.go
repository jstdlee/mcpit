// Command fixture-site serves the test shop on a local port for manual end-to-end runs.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/jstdlee/mcpit/cli/internal/fixture"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7810", "listen address")
	flag.Parse()
	log.Printf("fixture shop on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, fixture.New()))
}
