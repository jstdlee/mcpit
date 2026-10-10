// Command mcpit turns websites into agent tools: explore once, reuse fast, share
// through the registry. `mcpit serve` runs the MCP server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jstdlee/mcpit/cli/internal/agentsetup"
	"github.com/jstdlee/mcpit/cli/internal/app"
	"github.com/jstdlee/mcpit/cli/internal/config"
	"github.com/jstdlee/mcpit/cli/internal/decide"
	"github.com/jstdlee/mcpit/cli/internal/explore"
	"github.com/jstdlee/mcpit/cli/internal/mcpserver"
	"github.com/jstdlee/mcpit/cli/internal/registry"
	"github.com/jstdlee/mcpit/cli/internal/sitepack"
)

var version = "0.1.0-dev"

const usage = `mcpit — make any website usable by an agent.

Usage:
  mcpit explore <url> [--depth 1] [--max-pages 15] [--no-verify]
  mcpit tools <site>
  mcpit guide <site>                           llms.txt, robots, agent card and the site map
  mcpit find <site> [--task "what you want to do"]
  mcpit call <site> <tool> [--args '{"q":"lamp"}'] [--yes]
  mcpit serve [--http 127.0.0.1:7801]          MCP server (stdio by default)
  mcpit pull <site>                            get the active version from the registry
  mcpit key init [--name NAME] | key status    device key for submitting
  mcpit submit <site>                          share a local sitepack
  mcpit status <submission-id>
  mcpit store ls | show <site> | rm <site> | export <site> | import <file>
  mcpit config show | set registry <url> | set decider <clef|systemone|none> [endpoint]
  mcpit setup <agent> [--scope project|user] [--no-skill]   add the MCP server + skill to an agent
                                             agents: claude, codex, cursor, gemini, omp, vscode
  mcpit setup skill [--dir DIR]                write the mcpit agent skill (SKILL.md)
  mcpit doctor | version

Sites can be a URL, an origin or a host name.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "mcpit:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	if cmd == "version" {
		fmt.Println("mcpit", version)
		return nil
	}
	if cmd == "setup" {
		return cmdSetup(rest)
	}
	a, err := app.New()
	if err != nil {
		return err
	}
	switch cmd {
	case "explore":
		return cmdExplore(ctx, a, rest)
	case "tools":
		return cmdTools(ctx, a, rest)
	case "guide":
		return cmdGuide(ctx, a, rest)
	case "find":
		return cmdFind(ctx, a, rest)
	case "call":
		return cmdCall(ctx, a, rest)
	case "serve":
		return cmdServe(ctx, a, rest)
	case "pull":
		return cmdPull(ctx, a, rest)
	case "key":
		return cmdKey(ctx, a, rest)
	case "submit":
		return cmdSubmit(ctx, a, rest)
	case "status":
		return cmdStatus(ctx, a, rest)
	case "store":
		return cmdStore(a, rest)
	case "config":
		return cmdConfig(a, rest)
	case "doctor":
		return cmdDoctor(ctx, a)
	case "setup":
		return cmdSetup(rest)
	}
	return fmt.Errorf("unknown command %q (see mcpit help)", cmd)
}

// parse splits flags and positionals, allowing flags after positionals.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	return pos, nil
}

func need(pos []string, n int, what string) error {
	if len(pos) < n {
		return fmt.Errorf("missing %s (see mcpit help)", what)
	}
	return nil
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func cmdExplore(ctx context.Context, a *app.App, args []string) error {
	fs := flag.NewFlagSet("explore", flag.ContinueOnError)
	depth := fs.Int("depth", 1, "page levels: 1 = only the URL (default), 2 = also the entry pages it links to (slow on big sites)")
	maxPages := fs.Int("max-pages", 15, "page budget")
	noVerify := fs.Bool("no-verify", false, "skip test calls of read tools")
	chrome := fs.String("chrome", os.Getenv("MCPIT_CHROME"), "path to Chrome/Chromium")
	quiet := fs.Bool("quiet", false, "no progress output")
	dump := fs.String("dump", "", "write the raw capture to this JSON file")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, "url"); err != nil {
		return err
	}
	if *depth < 1 || *depth > 2 {
		return errors.New("depth is 1 or 2 (page levels)")
	}
	start := time.Now()
	fmt.Fprintf(os.Stderr, "decision model: %s\n", a.Decider.Model())
	p, err := a.Explore(ctx, pos[0], explore.Options{Depth: *depth, MaxPages: *maxPages, ChromePath: *chrome, Verify: !*noVerify, Dump: *dump,
		Progress: func(s string) {
			if !*quiet {
				fmt.Fprintln(os.Stderr, "  "+s)
			}
		}})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "explored %s in %s\n", p.Origin, time.Since(start).Round(time.Millisecond))
	printTools(p)
	return nil
}

func printTools(p *sitepack.Pack) {
	extra := ""
	if p.Guide != nil {
		var docs []string
		for name, d := range map[string]*sitepack.GuideDoc{"robots": p.Guide.Robots, "llms": p.Guide.LLMs, "agent": p.Guide.AgentCard, "catalog": p.Guide.APICatalog, "ai-plugin": p.Guide.AIPlugin, "mcp": p.Guide.MCP} {
			if d != nil {
				docs = append(docs, name)
			}
		}
		sort.Strings(docs)
		extra = fmt.Sprintf(", guide: %s", strings.Join(docs, " "))
	}
	fmt.Printf("%s — %d tools, %d pages%s\n", p.Origin, len(p.Tools), len(p.Pages), extra)
	for _, t := range p.Tools {
		var params []string
		if props, ok := t.InputSchema["properties"].(map[string]any); ok {
			for k := range props {
				params = append(params, k)
			}
		}
		fmt.Printf("  %-28s %-6s %s  [%s]\n", t.ID, t.Effect, t.Description, strings.Join(params, ", "))
	}
}

func cmdTools(ctx context.Context, a *app.App, args []string) error {
	if err := need(args, 1, "site"); err != nil {
		return err
	}
	p, src, err := a.Pack(ctx, args[0])
	if err != nil {
		return err
	}
	p, integ := a.Check(ctx, p)
	fmt.Fprintf(os.Stderr, "source: %s\n", src)
	printAlerts(integ)
	printTools(p)
	return nil
}

func printAlerts(in *app.Integrity) {
	if in == nil {
		return
	}
	if in.Updated != "" {
		fmt.Fprintf(os.Stderr, "updated to registry version %s (signature ok)\n", in.Updated)
	}
	for _, a := range in.Alerts {
		label := "WARNING"
		if in.Level == "block" {
			label = "BLOCKED"
		}
		fmt.Fprintf(os.Stderr, "%s: %s\n", label, a)
	}
}

func cmdGuide(ctx context.Context, a *app.App, args []string) error {
	if err := need(args, 1, "site"); err != nil {
		return err
	}
	p, _, err := a.Pack(ctx, args[0])
	if err != nil {
		return err
	}
	if p.Guide != nil {
		if p.Guide.Description != "" {
			fmt.Println("About:", p.Guide.Description)
		}
		for _, d := range []*sitepack.GuideDoc{p.Guide.Robots, p.Guide.LLMs, p.Guide.AgentCard, p.Guide.APICatalog, p.Guide.AIPlugin, p.Guide.MCP} {
			if d != nil {
				fmt.Printf("\n== %s (%d chars)\n%s\n", d.URL, len(d.Text), clipLines(d.Text, 12))
			}
		}
	}
	fmt.Printf("\n== site map (%d pages)\n", len(p.Pages))
	for _, pg := range p.Pages {
		fmt.Printf("  %-12s %-40s %s\n", pg.Category, pg.Path, pg.Title)
	}
	return nil
}

func clipLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		return strings.Join(lines[:n], "\n") + "\n  …"
	}
	return s
}

func cmdFind(ctx context.Context, a *app.App, args []string) error {
	fs := flag.NewFlagSet("find", flag.ContinueOnError)
	task := fs.String("task", "", "what you want to do")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, "site"); err != nil {
		return err
	}
	p, src, err := a.Pack(ctx, pos[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "source: %s\n", src)
	if *task == "" {
		printTools(p)
		return nil
	}
	id, prob, err := a.Pick(ctx, p, *task)
	if err != nil {
		return err
	}
	fmt.Printf("%s (%.2f)\n", id, prob)
	return nil
}

func cmdCall(ctx context.Context, a *app.App, args []string) error {
	fs := flag.NewFlagSet("call", flag.ContinueOnError)
	raw := fs.String("args", "{}", "JSON arguments")
	yes := fs.Bool("yes", false, "confirm a tool that changes data")
	force := fs.Bool("force", false, "use a site the integrity check blocked (after reading the alert)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 2, "site and tool"); err != nil {
		return err
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(*raw), &in); err != nil {
		return fmt.Errorf("--args: %w", err)
	}
	r, integ, err := a.CallChecked(ctx, pos[0], pos[1], in, *yes, *force)
	printAlerts(integ)
	if err != nil {
		if errors.Is(err, app.ErrBlocked) {
			return errors.New("blocked by the integrity check (see the alert); rerun with --force only if you accept the risk")
		}
		return err
	}
	printJSON(r)
	return nil
}

func cmdServe(ctx context.Context, a *app.App, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("http", "", "serve streamable HTTP on this address instead of stdio")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if *addr == "" {
		return mcpserver.Stdio(ctx, a)
	}
	if !strings.HasPrefix(*addr, "127.0.0.1:") && !strings.HasPrefix(*addr, "localhost:") {
		return errors.New("--http must listen on 127.0.0.1 or localhost")
	}
	srv := &http.Server{Addr: *addr, Handler: mcpserver.HTTPHandler(a)}
	go func() { <-ctx.Done(); srv.Close() }()
	fmt.Fprintf(os.Stderr, "mcpit MCP server on http://%s\n", *addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func cmdPull(ctx context.Context, a *app.App, args []string) error {
	if err := need(args, 1, "site"); err != nil {
		return err
	}
	p, err := a.Pull(ctx, args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "pulled version %s (signature ok)\n", p.Version)
	printTools(p)
	return nil
}

func cmdKey(ctx context.Context, a *app.App, args []string) error {
	if err := need(args, 1, "init or status"); err != nil {
		return err
	}
	switch args[0] {
	case "init":
		fs := flag.NewFlagSet("key init", flag.ContinueOnError)
		host, _ := os.Hostname()
		name := fs.String("name", "mcpit@"+host, "a name the moderator sees")
		if _, err := parse(fs, args[1:]); err != nil {
			return err
		}
		k, created, err := registry.InitKey(config.Dir())
		if err != nil {
			return err
		}
		if created {
			fmt.Println("created device key", k.ID)
		} else {
			fmt.Println("device key", k.ID)
		}
		a.Registry.Key = k
		st, err := a.Registry.Register(ctx, *name)
		if err != nil {
			return fmt.Errorf("key saved locally, but registration failed: %w", err)
		}
		fmt.Printf("registered at %s: %s\n", a.Registry.Base, st.State)
		if st.State == "pending" {
			fmt.Println("A moderator must approve this key before your first submit. Check with `mcpit key status`.")
		}
		return nil
	case "status":
		if a.Registry.Key == nil {
			return errors.New("no device key: run `mcpit key init`")
		}
		st, err := a.Registry.KeyStatus(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", st.ID, st.State)
		return nil
	}
	return fmt.Errorf("unknown key command %q", args[0])
}

func cmdSubmit(ctx context.Context, a *app.App, args []string) error {
	if err := need(args, 1, "site"); err != nil {
		return err
	}
	sub, err := a.Submit(ctx, args[0])
	if err != nil {
		return err
	}
	printJSON(sub)
	return nil
}

func cmdStatus(ctx context.Context, a *app.App, args []string) error {
	if err := need(args, 1, "submission id"); err != nil {
		return err
	}
	sub, err := a.Registry.Status(ctx, args[0])
	if err != nil {
		return err
	}
	printJSON(sub)
	return nil
}

func cmdStore(a *app.App, args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	switch args[0] {
	case "ls":
		packs, err := a.Sites("")
		if err != nil {
			return err
		}
		for _, p := range packs {
			src := "local"
			if p.Registry != nil {
				src = "registry " + p.Version
			}
			fmt.Printf("%-40s %3d tools  %s\n", p.Origin, len(p.Tools), src)
		}
		fmt.Fprintln(os.Stderr, "store:", a.Store.Dir)
		return nil
	case "show", "export":
		if err := need(args, 2, "site"); err != nil {
			return err
		}
		p, err := a.Store.Load(args[1])
		if err != nil {
			return err
		}
		printJSON(p)
		return nil
	case "rm":
		if err := need(args, 2, "site"); err != nil {
			return err
		}
		return a.Store.Remove(args[1])
	case "import":
		if err := need(args, 2, "file"); err != nil {
			return err
		}
		b, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		var p sitepack.Pack
		if err := json.Unmarshal(b, &p); err != nil {
			return err
		}
		p.Registry = nil
		return a.Store.Save(&p)
	}
	return fmt.Errorf("unknown store command %q", args[0])
}

func cmdConfig(a *app.App, args []string) error {
	if len(args) == 0 || args[0] == "show" {
		c := *a.Cfg
		if c.Decider.Token != "" {
			c.Decider.Token = "(set)"
		}
		printJSON(c)
		return nil
	}
	if args[0] != "set" || len(args) < 3 {
		return errors.New("usage: mcpit config set registry <url> | set decider <clef|systemone|none> [endpoint]")
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	switch args[1] {
	case "registry":
		c.Registry = args[2]
	case "decider":
		c.Decider.Provider = args[2]
		if len(args) > 3 {
			c.Decider.Endpoint = args[3]
		}
	default:
		return fmt.Errorf("unknown setting %q", args[1])
	}
	c.Decider.Token, c.Decider.AccountID = "", "" // never persist env values by accident
	return c.Save()
}

func cmdSetup(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mcpit setup <%s|skill>", strings.Join(agentsetup.Names(), "|"))
	}
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	scope := fs.String("scope", "project", "project (this folder) or user (all projects)")
	noSkill := fs.Bool("no-skill", false, "do not write the agent skill")
	dir := fs.String("dir", "", "folder for `setup skill`")
	command := fs.String("command", "", "command the agent runs (default: this mcpit binary)")
	var envs multiFlag
	fs.Var(&envs, "env", "KEY=VALUE passed to the MCP server (repeatable)")
	if _, err := parse(fs, args[1:]); err != nil {
		return err
	}
	if args[0] == "skill" {
		d := *dir
		if d == "" {
			d = filepath.Join(".", "skills", "mcpit")
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		f := filepath.Join(d, "SKILL.md")
		fmt.Println("wrote", f)
		return os.WriteFile(f, []byte(agentsetup.Skill), 0o644)
	}
	cmd := *command
	if cmd == "" {
		if exe, err := os.Executable(); err == nil {
			cmd = exe
		} else {
			cmd = "mcpit"
		}
	}
	env := map[string]string{}
	for _, kv := range envs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("--env %q: want KEY=VALUE", kv)
		}
		env[k] = v
	}
	cwd, _ := os.Getwd()
	r, err := agentsetup.Install(args[0], *scope, cwd, cmd, env, !*noSkill)
	if err != nil {
		return err
	}
	if r.MCPFile != "" {
		fmt.Println("MCP server added:", r.MCPFile)
	}
	if r.SkillDir != "" {
		fmt.Println("skill written:   ", filepath.Join(r.SkillDir, "SKILL.md"))
	}
	if r.Manual != "" {
		fmt.Println("do this by hand: ", r.Manual)
	}
	fmt.Println("Restart the agent, then ask it to use mcpit (try: \"use mcpit to search pypi.org for requests\").")
	return nil
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func cmdDoctor(ctx context.Context, a *app.App) error {
	fmt.Println("config dir: ", config.Dir())
	fmt.Println("store dir:  ", a.Store.Dir)
	fmt.Println("registry:   ", a.Registry.Base)
	if a.Registry.Key != nil {
		fmt.Println("device key: ", a.Registry.Key.ID)
	} else {
		fmt.Println("device key:  none (mcpit key init)")
	}
	fmt.Println("decider:    ", a.Decider.Model())
	if a.Decider.Available() {
		start := time.Now()
		ans, err := a.Decider.Ask(ctx, "doctor", "GET https://example.com/api/search?q=lamp returns JSON results.",
			map[string]decide.Question{"api": decide.Noul("This is a data API a user task could call.")}, nil)
		if err != nil {
			fmt.Println("  test call failed:", err)
		} else {
			fmt.Printf("  test call ok: %.2f in %s\n", ans["api"].Noul, time.Since(start).Round(time.Millisecond))
		}
	}
	return nil
}
