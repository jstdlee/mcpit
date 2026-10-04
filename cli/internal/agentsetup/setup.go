// Package agentsetup writes the mcpit MCP server entry and skill into agent configs.
package agentsetup

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed SKILL.md
var Skill string

// Agent describes where one agent keeps MCP servers and skills.
type Agent struct {
	Name     string
	MCPFile  func(scope, cwd, home string) string // JSON file with "mcpServers" (or "servers" for VS Code)
	Key      string                               // top-level key of the server map
	SkillDir func(scope, cwd, home string) string
	Note     string // what to do when the file is not JSON we can edit
}

func join(parts ...string) string { return filepath.Join(parts...) }

var Agents = map[string]Agent{
	"omp": {Name: "oh-my-pi (omp)", Key: "mcpServers",
		MCPFile: func(s, cwd, home string) string {
			if s == "user" {
				return join(home, ".omp", "agent", "mcp.json")
			}
			return join(cwd, ".omp", "mcp.json")
		},
		SkillDir: func(s, cwd, home string) string {
			if s == "user" {
				return join(home, ".omp", "agent", "skills", "mcpit")
			}
			return join(cwd, ".omp", "skills", "mcpit")
		}},
	"claude": {Name: "Claude Code", Key: "mcpServers",
		MCPFile: func(s, cwd, home string) string {
			if s == "user" {
				return "" // user scope lives in ~/.claude.json; use the CLI
			}
			return join(cwd, ".mcp.json")
		},
		SkillDir: func(s, cwd, home string) string {
			if s == "user" {
				return join(home, ".claude", "skills", "mcpit")
			}
			return join(cwd, ".claude", "skills", "mcpit")
		},
		Note: "claude mcp add --scope user mcpit -- mcpit serve"},
	"cursor": {Name: "Cursor", Key: "mcpServers",
		MCPFile: func(s, cwd, home string) string {
			if s == "user" {
				return join(home, ".cursor", "mcp.json")
			}
			return join(cwd, ".cursor", "mcp.json")
		}},
	"vscode": {Name: "VS Code (Copilot agent mode)", Key: "servers",
		MCPFile: func(s, cwd, home string) string {
			if s == "user" {
				return ""
			}
			return join(cwd, ".vscode", "mcp.json")
		},
		Note: "VS Code user scope: run \"MCP: Add Server\" and enter the command `mcpit serve`"},
	"codex": {Name: "OpenAI Codex CLI",
		Note: "add to ~/.codex/config.toml:\n[mcp_servers.mcpit]\ncommand = \"mcpit\"\nargs = [\"serve\"]",
		SkillDir: func(s, cwd, home string) string {
			if s == "user" {
				return join(home, ".codex", "skills", "mcpit")
			}
			return join(cwd, ".codex", "skills", "mcpit")
		}},
	"gemini": {Name: "Gemini CLI", Key: "mcpServers",
		MCPFile: func(s, cwd, home string) string {
			if s == "user" {
				return join(home, ".gemini", "settings.json")
			}
			return join(cwd, ".gemini", "settings.json")
		}},
}

func Names() []string {
	var n []string
	for k := range Agents {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// Server is the MCP server entry. Env is optional (an isolated store or config dir).
func Server(command string, env map[string]string) map[string]any {
	s := map[string]any{"command": command, "args": []string{"serve"}}
	if len(env) > 0 {
		s["env"] = env
	}
	return s
}

// Result says what Install changed or what the user must do by hand.
type Result struct {
	MCPFile  string
	SkillDir string
	Manual   string
}

// Install merges the mcpit server into the agent's MCP file and writes the skill.
func Install(agent, scope, cwd, command string, env map[string]string, withSkill bool) (*Result, error) {
	a, ok := Agents[agent]
	if !ok {
		return nil, fmt.Errorf("unknown agent %q (one of: %s)", agent, strings.Join(Names(), ", "))
	}
	if scope != "project" && scope != "user" {
		return nil, errors.New("scope is project or user")
	}
	home, _ := os.UserHomeDir()
	r := &Result{}
	file := ""
	if a.MCPFile != nil {
		file = a.MCPFile(scope, cwd, home)
	}
	if file == "" {
		r.Manual = a.Note
	} else {
		if err := mergeServer(file, a.Key, Server(command, env)); err != nil {
			return nil, err
		}
		r.MCPFile = file
	}
	if withSkill && a.SkillDir != nil {
		dir := a.SkillDir(scope, cwd, home)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(Skill), 0o644); err != nil {
			return nil, err
		}
		r.SkillDir = dir
	}
	if a.MCPFile == nil {
		r.Manual = a.Note
	}
	return r, nil
}

// mergeServer adds or replaces servers.mcpit in a JSON file, keeping everything else.
func mergeServer(file, key string, server map[string]any) error {
	doc := map[string]any{}
	if b, err := os.ReadFile(file); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		if err := json.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("%s is not plain JSON (%v); add the server by hand", file, err)
		}
	}
	servers, _ := doc[key].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["mcpit"] = server
	doc[key] = servers
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return os.WriteFile(file, append(b, '\n'), 0o644)
}

// Snippet is the JSON block for agents we cannot edit directly.
func Snippet(key string) string {
	b, _ := json.MarshalIndent(map[string]any{key: map[string]any{"mcpit": Server("mcpit", nil)}}, "", "  ")
	return string(b)
}
