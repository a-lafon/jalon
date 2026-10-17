package handoff

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMentions(t *testing.T) {
	text := "@/home/me/repo/.jalon/handoffs/2026-10-09-1402-abcd1234-x.md reprends, " +
		"and \"/other/.jalon/handoffs/b.md\" or relative/.jalon/handoffs/c.md, not /r/.jalon/handoffs/.md nor /r/.jalon/handoffs/d.txt"
	ms := Mentions(text)
	if len(ms) != 3 {
		t.Fatalf("mentions = %+v, want 3", ms)
	}
	want := []string{"/home/me/repo/.jalon/handoffs/2026-10-09-1402-abcd1234-x.md", "/other/.jalon/handoffs/b.md", ""}
	for i, m := range ms {
		if got := m.Path(); got != want[i] {
			t.Errorf("path %d = %q, want %q", i, got, want[i])
		}
	}
	if !ms[0].In("/home/me/repo") || ms[0].In("/home/me/other") || ms[0].Name != "2026-10-09-1402-abcd1234-x.md" {
		t.Errorf("first mention = %+v", ms[0])
	}
}

func TestMentionInADateFolder(t *testing.T) {
	ms := Mentions("@/r/.jalon/handoffs/2026-10-10/0847-abcd1234-x.md reprends, not /r/.jalon/handoffs/2026-10-10/ nor /r/.jalon/handoffs/notes/a.md")
	if len(ms) != 1 || ms[0].Name != "2026-10-10/0847-abcd1234-x.md" || ms[0].Path() != "/r/.jalon/handoffs/2026-10-10/0847-abcd1234-x.md" {
		t.Errorf("mentions = %+v", ms)
	}
}

func TestMentionInARootWithASpace(t *testing.T) {
	ms := Mentions("resume /Users/me/My Repo/.jalon/handoffs/a.md please")
	if len(ms) != 1 || !ms[0].In("/Users/me/My Repo") || ms[0].Name != "a.md" {
		t.Errorf("mentions = %+v", ms)
	}
}

func TestIsPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/r/.jalon/handoffs/a.md":            true,
		"/r/.jalon/handoffs/a.md.bak":        false,
		"r/.jalon/handoffs/a.md":             false,
		"/r/.jalon/handoffs/sub/a.md":        false,
		"/r/.jalon/handoffs/2026-10-10/a.md": true,
		"/r/docs/a.md":                       false,
	} {
		if got := IsPath(p); got != want {
			t.Errorf("IsPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestSessionOf(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if got := SessionOf(write("a.md", "Follows: x.md\nSession: a1b2c3d4\n\n# Handoff\n")); got != "a1b2c3d4" {
		t.Errorf("session = %q", got)
	}
	late := write("b.md", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\nSession: s\n")
	if got := SessionOf(late); got != "" {
		t.Errorf("a Session: line after the first 10 lines = %q, want none", got)
	}
	if got := SessionOf(filepath.Join(dir, "missing.md")); got != "" {
		t.Errorf("missing file = %q", got)
	}
}
