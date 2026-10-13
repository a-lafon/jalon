package claudecode

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSkillFrontmatter(t *testing.T) {
	parts := bytes.SplitN(Adapter{}.Skill(), []byte("---\n"), 3)
	if len(parts) != 3 || len(parts[0]) != 0 {
		t.Fatalf("no frontmatter:\n%s", Adapter{}.Skill())
	}
	var fm struct {
		Name                   string `yaml:"name"`
		Description            string `yaml:"description"`
		ArgumentHint           string `yaml:"argument-hint"`
		DisableModelInvocation bool   `yaml:"disable-model-invocation"`
	}
	if err := yaml.Unmarshal(parts[1], &fm); err != nil {
		t.Fatal(err)
	}
	if fm.Name != "jalon" || fm.Description == "" || strings.Contains(fm.Description, "\n") ||
		!strings.Contains(fm.ArgumentHint, "resume") || !fm.DisableModelInvocation {
		t.Errorf("frontmatter = %+v", fm)
	}
	if !(Adapter{}).OwnsSkill(parts[2]) {
		t.Error("the body lacks the marker")
	}
}

func TestOwnsSkill(t *testing.T) {
	if (Adapter{}).OwnsSkill([]byte("---\nname: jalon\n---\nSomeone else's skill.\n")) {
		t.Error("a skill without the marker is owned")
	}
}

func TestSkillPath(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	if path, err := (Adapter{}).SkillPath(); err != nil || path != filepath.Join("/home/u", ".claude", "skills", "jalon", "SKILL.md") {
		t.Errorf("path = %q, %v", path, err)
	}
}
