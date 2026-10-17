package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/a-lafon/jalon/internal/client"
)

var skillNames = []string{"jalon-resume", "jalon-recap", "jalon-handoff", "jalon-report", "jalon-help"}

func TestSkills(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	skills, err := Adapter{}.Skills("/opt/my jalon/jalon")
	if err != nil || len(skills) != 2*len(skillNames) {
		t.Fatalf("skills = %d, %v", len(skills), err)
	}
	for i, name := range skillNames {
		dir := filepath.Join("/home/u", ".agents", "skills", name)
		policyFile, skill := skills[2*i], skills[2*i+1]
		// The policy comes first, so uninstall empties agents/ before the skill's folder.
		if policyFile.Path != filepath.Join(dir, "agents", "openai.yaml") || skill.Path != filepath.Join(dir, "SKILL.md") {
			t.Fatalf("paths = %q, %q", policyFile.Path, skill.Path)
		}
		var p struct {
			Policy struct {
				AllowImplicitInvocation *bool `yaml:"allow_implicit_invocation"`
			} `yaml:"policy"`
		}
		if err := yaml.Unmarshal(policyFile.Content, &p); err != nil || p.Policy.AllowImplicitInvocation == nil || *p.Policy.AllowImplicitInvocation {
			t.Errorf("%s: policy = %s, %v", name, policyFile.Content, err)
		}
		parts := bytes.SplitN(skill.Content, []byte("---\n"), 3)
		if len(parts) != 3 || len(parts[0]) != 0 {
			t.Fatalf("%s: no frontmatter:\n%s", name, skill.Content)
		}
		var fm map[string]string
		if err := yaml.Unmarshal(parts[1], &fm); err != nil {
			t.Fatal(err)
		}
		if len(fm) != 2 || fm["name"] != name || fm["description"] == "" {
			t.Errorf("%s: frontmatter = %v", name, fm)
		}
		for _, f := range []client.Skill{policyFile, skill} {
			if !(Adapter{}).OwnsSkill(f.Content) {
				t.Errorf("%s: no marker", f.Path)
			}
		}
		body := string(parts[2])
		// Codex expands nothing: no Claude Code argument, command or tool, no placeholder left.
		for _, bad := range []string{"$ARGUMENTS", "`/jalon-", "Claude Code:", "Read tool", "{"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s holds %q:\n%s", name, bad, body)
			}
		}
		if name != "jalon-help" && !strings.Contains(body, "words after `$"+name+"`") {
			t.Errorf("%s does not say where its arguments are:\n%s", name, body)
		}
	}
	if report := string(skills[7].Content); !strings.Contains(report, "`'/opt/my jalon/jalon' context-report <arguments>`") || !strings.Contains(report, "Claude Code's transcripts only") {
		t.Errorf("report skill:\n%s", report)
	}
	if help := string(skills[9].Content); !strings.Contains(help, "Jalon's commands in Codex:\n- `$jalon-resume") ||
		!strings.Contains(help, "`jalon init --client codex`: install or update the hooks and these commands") || strings.Contains(help, "statusline") {
		t.Errorf("help skill:\n%s", help)
	}
}

// The skills go where Codex documents them, wherever CODEX_HOME is.
func TestSkillsIgnoreCodexHome(t *testing.T) {
	t.Setenv("CODEX_HOME", "/elsewhere")
	t.Setenv("HOME", "/home/u")
	skills, err := Adapter{}.Skills("jalon")
	if err != nil || skills[1].Path != filepath.Join("/home/u", ".agents", "skills", "jalon-resume", "SKILL.md") {
		t.Errorf("path = %q, %v", skills[1].Path, err)
	}
	if retired, err := (Adapter{}).RetiredSkills(); len(retired) != 0 || err != nil {
		t.Errorf("retired = %q, %v", retired, err)
	}
}

func TestOwnsSkill(t *testing.T) {
	for _, content := range []string{"---\nname: jalon-help\n---\nSomeone else's skill.\n", "policy:\n  allow_implicit_invocation: false\n"} {
		if (Adapter{}).OwnsSkill([]byte(content)) {
			t.Errorf("owned: %q", content)
		}
	}
}

func TestCommandsOfferTheSkillsJalonWrote(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write := func(name, content string) {
		path := filepath.Join(home, ".agents", "skills", name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := client.Commands{Clear: "/new", Compact: "/compact"}
	if got := (Adapter{}).Commands(); got != base {
		t.Errorf("no skill: %+v", got)
	}
	write("jalon-handoff", "---\nname: jalon-handoff\n---\nMine.\n")
	if got := (Adapter{}).Commands(); got != base {
		t.Errorf("someone else's skill: %+v", got)
	}
	skills, _ := Adapter{}.Skills("jalon")
	write("jalon-resume", string(skills[1].Content))
	write("jalon-handoff", string(skills[5].Content))
	if got, want := (Adapter{}).Commands(), (client.Commands{Clear: "/new", Compact: "/compact", Resume: "$jalon-resume", Handoff: "$jalon-handoff"}); got != want {
		t.Errorf("commands = %+v, want %+v", got, want)
	}
}
