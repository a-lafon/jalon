package claudecode

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestSkillsFrontmatter(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	skills, err := Adapter{}.Skills("/opt/my jalon/jalon")
	if err != nil || len(skills) != len(skillNames) {
		t.Fatalf("skills = %d, %v", len(skills), err)
	}
	for i, sk := range skills {
		name := skillNames[i]
		if want := filepath.Join("/home/u", ".claude", "skills", name, "SKILL.md"); sk.Path != want {
			t.Errorf("path = %q, want %q", sk.Path, want)
		}
		parts := bytes.SplitN(sk.Content, []byte("---\n"), 3)
		if len(parts) != 3 || len(parts[0]) != 0 {
			t.Fatalf("%s: no frontmatter:\n%s", name, sk.Content)
		}
		var fm struct {
			Name                   string `yaml:"name"`
			Description            string `yaml:"description"`
			DisableModelInvocation bool   `yaml:"disable-model-invocation"`
		}
		if err := yaml.Unmarshal(parts[1], &fm); err != nil {
			t.Fatal(err)
		}
		if fm.Name != name || fm.Description == "" || strings.Contains(fm.Description, "\n") || !fm.DisableModelInvocation {
			t.Errorf("%s: frontmatter = %+v", name, fm)
		}
		if !(Adapter{}).OwnsSkill(parts[2]) {
			t.Errorf("%s: the body lacks the marker", name)
		}
		if bytes.Contains(sk.Content, []byte("{jalon}")) {
			t.Errorf("%s: {jalon} left", name)
		}
	}
	if !bytes.Contains(skills[3].Content, []byte("`'/opt/my jalon/jalon' context-report $ARGUMENTS`")) {
		t.Errorf("report skill does not run the binary:\n%s", skills[3].Content)
	}
	// A plain path stays as is; one the shell would expand is quoted, as in the hooks.
	for exe, want := range map[string]string{"/opt/homebrew/bin/jalon": "`/opt/homebrew/bin/jalon context-report", "/opt/$HOME/jalon": "`'/opt/$HOME/jalon' context-report"} {
		got, _ := Adapter{}.Skills(exe)
		if !bytes.Contains(got[3].Content, []byte(want)) {
			t.Errorf("%s: report skill lacks %q", exe, want)
		}
	}
}

// skillNames is the set jalon init installs, in the order it prints them.
var skillNames = []string{"jalon-resume", "jalon-recap", "jalon-handoff", "jalon-report", "jalon-help"}

// TestSkillsAsWritten pins the files written for Claude Code: testdata holds
// them as they were before the text was shared with other clients.
func TestSkillsAsWritten(t *testing.T) {
	const exe = "/opt/my jalon/jalon"
	skills, err := Adapter{}.Skills(exe)
	if err != nil || len(skills) != len(skillNames) {
		t.Fatalf("skills = %d, %v", len(skills), err)
	}
	for i, sk := range skills {
		want, err := os.ReadFile(filepath.Join("testdata", "skills", skillNames[i], "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		want = bytes.ReplaceAll(want, []byte("{jalon}"), []byte("'"+exe+"'"))
		if !bytes.Equal(sk.Content, want) {
			t.Errorf("%s differs from testdata:\n%s", skillNames[i], sk.Content)
		}
	}
}

func TestOwnsSkill(t *testing.T) {
	if (Adapter{}).OwnsSkill([]byte("---\nname: jalon\n---\nSomeone else's skill.\n")) {
		t.Error("a skill without the marker is owned")
	}
}

func TestRetiredSkills(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	if paths, err := (Adapter{}).RetiredSkills(); err != nil || len(paths) != 1 || paths[0] != filepath.Join("/home/u", ".claude", "skills", "jalon", "SKILL.md") {
		t.Errorf("retired = %q, %v", paths, err)
	}
}
