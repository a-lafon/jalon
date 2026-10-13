package claudecode

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"

	"github.com/a-lafon/jalon/internal/client"
)

var _ client.SkillInstaller = Adapter{}

// skill is the user skill /jalon, which only the user invokes.
//
//go:embed SKILL.md
var skill []byte

// skillMarker is the line that tells a skill Jalon wrote from someone else's.
const skillMarker = "<!-- Written by jalon init, which overwrites edits; jalon init --uninstall removes it. -->"

// SkillPath is the user skill file, which applies to every project.
func (Adapter) SkillPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills", "jalon", "SKILL.md"), nil
}

func (Adapter) Skill() []byte { return skill }

func (Adapter) OwnsSkill(content []byte) bool {
	return bytes.Contains(content, []byte(skillMarker))
}
