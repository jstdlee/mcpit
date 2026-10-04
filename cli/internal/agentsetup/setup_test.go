package agentsetup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallMergesAndWritesSkill(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".omp"), 0o755)
	os.WriteFile(filepath.Join(dir, ".omp", "mcp.json"), []byte(`{"mcpServers":{"other":{"command":"x"}},"keep":1}`), 0o644)
	r, err := Install("omp", "project", dir, "mcpit", map[string]string{"MCPIT_HOME": "/tmp/h"}, true)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	b, _ := os.ReadFile(r.MCPFile)
	json.Unmarshal(b, &doc)
	servers := doc["mcpServers"].(map[string]any)
	if servers["other"] == nil || servers["mcpit"] == nil || doc["keep"] == nil {
		t.Fatalf("merge lost entries: %s", b)
	}
	skill, _ := os.ReadFile(filepath.Join(r.SkillDir, "SKILL.md"))
	if !strings.HasPrefix(string(skill), "---\nname: mcpit") {
		t.Fatal("skill not written")
	}
	if r, _ := Install("codex", "user", dir, "mcpit", nil, false); !strings.Contains(r.Manual, "config.toml") {
		t.Fatal("codex needs manual note")
	}
	if _, err := Install("nope", "project", dir, "mcpit", nil, false); err == nil {
		t.Fatal("unknown agent accepted")
	}
}
