package advisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

func TestRecordAndLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if _, ok := Load(dir, "s1"); ok {
		t.Fatal("a session with no file must not load")
	}
	if err := Record(dir, "s1", 120_000, t0, "", false); err != nil {
		t.Fatal(err)
	}
	got, ok := Load(dir, "s1")
	if !ok || got.Size != 120_000 || got.LastAdvised != 0 || !got.Recorded.Equal(t0) {
		t.Errorf("state = %+v, %v", got, ok)
	}
}

func TestStatePermissionsAndNoLeftovers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := Record(dir, "s1", 1, t0, "", false); err != nil {
		t.Fatal(err)
	}
	if err := MarkAdvised(dir, "s1", 1, t0, ""); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "s1.json" {
		t.Fatalf("folder holds %v, want only s1.json (no temp files)", entries)
	}
	if fi, _ := entries[0].Info(); fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestUnsafeSessionIDsAreIgnored(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions")
	for _, id := range []string{"", "../x", "a/b", "..", "a.b", `a\b`, "a b", "é"} {
		if SafeSessionID(id) {
			t.Errorf("%q must be unsafe", id)
		}
		if Record(dir, id, 1, t0, "", false) == nil || MarkAdvised(dir, id, 1, t0, "") == nil {
			t.Errorf("%q: a write must be refused", id)
		}
		if _, ok := Load(dir, id); ok {
			t.Errorf("%q: loaded", id)
		}
	}
	if _, err := os.Stat(root + "/x.json"); err == nil {
		t.Error("a file escaped the state folder")
	}
	for _, id := range []string{"3f2a-91bc_X", "abc"} {
		if !SafeSessionID(id) {
			t.Errorf("%q must be safe", id)
		}
	}
}

func TestMarkAdvisedKeepsTheSizeAndRecordKeepsTheStep(t *testing.T) {
	dir := t.TempDir()
	Record(dir, "s", 210_000, t0, "", false)
	MarkAdvised(dir, "s", 210_000, t0, "")
	Record(dir, "s", 260_000, t0.Add(time.Minute), "", false)
	got, _ := Load(dir, "s")
	if got.Size != 260_000 || got.LastAdvised != 210_000 {
		t.Errorf("state = %+v, want size 260000 and last advised 210000", got)
	}
}

func TestSetHandoffKeepsTheRestAndRecordKeepsTheHandoff(t *testing.T) {
	dir := t.TempDir()
	Record(dir, "s", 210_000, t0, "", false)
	MarkAdvised(dir, "s", 210_000, t0, "")
	if err := SetHandoff(dir, "s", "p.md", "r4sefxw4", "2026-10-12-0900-r4sefxw4"); err != nil {
		t.Fatal(err)
	}
	Record(dir, "s", 260_000, t0.Add(time.Minute), "", false)
	got, _ := Load(dir, "s")
	want := Session{Size: 260_000, Recorded: t0.Add(time.Minute), LastAdvised: 210_000,
		Parent: "p.md", Thread: "r4sefxw4", Proposed: "2026-10-12-0900-r4sefxw4"}
	if got != want {
		t.Errorf("state = %+v, want %+v", got, want)
	}
	if SetHandoff(dir, "../x", "", "", "") == nil {
		t.Error("an unsafe session id was written")
	}
}

func TestStateWithoutHandoffHasNoHandoffFields(t *testing.T) {
	dir := t.TempDir()
	Record(dir, "s", 210_000, t0, "", false)
	MarkAdvised(dir, "s", 210_000, t0, "")
	b, _ := os.ReadFile(filepath.Join(dir, "s.json"))
	if want := `{"size":210000,"recorded":"2026-10-12T09:00:00Z","last_advised":210000}`; string(b) != want {
		t.Errorf("state file = %s, want %s", b, want)
	}
}

func TestPromptIDsAreWrittenByTheirOwnWritesAndKeptByTheOthers(t *testing.T) {
	dir := t.TempDir()
	Record(dir, "s", 210_000, t0, "p1", false)
	MarkAdvised(dir, "s", 210_000, t0, "p1")
	SetFloor(dir, "s", 50_000)
	SetHandoff(dir, "s", "", "r4sefxw4", "2026-10-12/0900-r4sefxw4")
	if got, _ := Load(dir, "s"); got.Prompt != "p1" || got.AdvisedPrompt != "p1" {
		t.Errorf("state = %+v, want prompt and advised prompt p1", got)
	}
	Record(dir, "s", 220_000, t0, "p2", false)
	if got, _ := Load(dir, "s"); got.Prompt != "p2" || got.AdvisedPrompt != "p1" {
		t.Errorf("state = %+v, want prompt p2 and advised prompt p1 kept", got)
	}
}

func TestTypedIsRecordedOnce(t *testing.T) {
	dir := t.TempDir()
	Record(dir, "s", 100_000, t0, "p1", false)
	if got, _ := Load(dir, "s"); got.Typed {
		t.Error("a session no typed prompt showed in is typed")
	}
	Record(dir, "s", 110_000, t0, "p2", true)
	Record(dir, "s", 120_000, t0, "p3", false)
	MarkAdvised(dir, "s", 120_000, t0, "p3")
	if got, _ := Load(dir, "s"); !got.Typed {
		t.Error("the session lost its typed prompt")
	}
}

func TestStateWrittenBeforePromptIDsLoads(t *testing.T) {
	dir := t.TempDir()
	old := `{"size":210000,"recorded":"2026-10-12T09:00:00Z","last_advised":210000,"floor":50000,"thread":"r4sefxw4"}`
	if err := os.WriteFile(filepath.Join(dir, "s.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := Load(dir, "s")
	if !ok || got.Prompt != "" || got.AdvisedPrompt != "" || got.LastAdvised != 210_000 || got.Thread != "r4sefxw4" {
		t.Errorf("state = %+v, %v", got, ok)
	}
	// A client without prompt IDs leaves none in the file.
	Record(dir, "s", 220_000, t0, "", false)
	if b, _ := os.ReadFile(filepath.Join(dir, "s.json")); strings.Contains(string(b), "prompt") {
		t.Errorf("state file = %s, want no prompt fields", b)
	}
}

func TestStepResetsWhenTheSizeFallsUnderHalf(t *testing.T) {
	dir := t.TempDir()
	Record(dir, "s", 320_000, t0, "", false)
	MarkAdvised(dir, "s", 320_000, t0, "")
	Record(dir, "s", 160_000, t0, "", false) // exactly half: not a reset
	if got, _ := Load(dir, "s"); got.LastAdvised != 320_000 {
		t.Errorf("at half: last advised = %d, want it kept", got.LastAdvised)
	}
	Record(dir, "s", 18_000, t0, "", false)
	if got, _ := Load(dir, "s"); got.Size != 18_000 || got.LastAdvised != 0 {
		t.Errorf("after compaction: %+v", got)
	}
}

func TestSweepDeletesOnlyOldStateFiles(t *testing.T) {
	dir := t.TempDir()
	Record(dir, "old", 1, t0.Add(-8*24*time.Hour), "", false)
	Record(dir, "fresh", 1, t0.Add(-6*24*time.Hour), "", false)
	os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("{"), 0o600)
	os.Chtimes(filepath.Join(dir, "corrupt.json"), t0.Add(-9*24*time.Hour), t0.Add(-9*24*time.Hour))
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("keep"), 0o600)
	Sweep(dir, t0, 7*24*time.Hour)
	for name, want := range map[string]bool{"old.json": false, "corrupt.json": false, "fresh.json": true, "notes.txt": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", name, err == nil, want)
		}
	}
	Sweep(filepath.Join(dir, "missing"), t0, time.Hour) // no folder: no panic
}

func TestSweepTouchesNothingButOldStateFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions")
	old := t0.Add(-30 * 24 * time.Hour)
	for path, content := range map[string]string{
		"config.yaml":              "context:\n",
		"sessions/s1.json":         `{"size":1,"recorded":"2026-09-01T00:00:00Z"}`,
		"sessions/sub/s2.json":     `{"size":1,"recorded":"2026-09-01T00:00:00Z"}`,
		"sessions/notes.txt":       "keep",
		"sessions/.tmp-123":        "keep",
		"handoffs/2026-09-01/a.md": "keep",
	} {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, old, old)
	}
	Sweep(dir, t0, 7*24*time.Hour)
	left := map[string]bool{}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			left[rel] = true
		}
		return nil
	})
	want := map[string]bool{"config.yaml": true, "sessions/sub/s2.json": true, "sessions/notes.txt": true, "sessions/.tmp-123": true, "handoffs/2026-09-01/a.md": true}
	if len(left) != len(want) {
		t.Errorf("files left = %v, want %v", left, want)
	}
	for p := range want {
		if !left[p] {
			t.Errorf("%s was deleted", p)
		}
	}
}
