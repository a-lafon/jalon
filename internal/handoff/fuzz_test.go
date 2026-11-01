package handoff

import (
	"path/filepath"
	"strings"
	"testing"
)

// FuzzContainment: a mention accepted as being in root must resolve under root.
// The text is written by the model, so its shape is not ours to choose.
func FuzzContainment(f *testing.F) {
	f.Add("@/home/me/repo/.jalon/handoffs/2026-10-09/1402-abcd1234-x.md", "/home/me/repo")
	f.Add("/r/.jalon/handoffs/../../etc/passwd.md", "/r")
	f.Add("/r/.jalon/handoffs/..md", "/r")
	f.Fuzz(func(t *testing.T, text, root string) {
		if root == "" || !filepath.IsAbs(root) {
			return
		}
		for _, m := range Mentions(text) {
			if !m.In(root) {
				continue
			}
			p := filepath.Clean(filepath.Join(root, ".jalon", "handoffs", m.Name))
			prefix := strings.TrimSuffix(filepath.Clean(root), string(filepath.Separator)) + string(filepath.Separator)
			if !strings.HasPrefix(p, prefix) {
				t.Fatalf("mention %q in root %q escapes to %q", m.Name, root, p)
			}
		}
	})
}
