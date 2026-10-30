package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// threadPrintLimit is the size of a thread above which jalon handoffs -thread
// prints only its first handoff and its last threadTail ones: a bound above the
// longest thread measured, 14 handoffs and 86 KB, not a measured limit.
const (
	threadPrintLimit = 100 << 10
	threadTail       = 5
)

// runHandoffs prints a handoff of the project in the working directory, picked
// from args, or with -thread the thread it ends, with their text, so the
// /jalon-resume and /jalon-recap commands leave the listing to the binary.
func runHandoffs(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("handoffs", flag.ContinueOnError)
	thread := flags.Bool("thread", false, "print the whole thread the handoff ends, back to its first handoff")
	if err := flags.Parse(args); err != nil {
		return err
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	folder := handoffFolder(wd)
	names, err := listHandoffs(folder)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Fprintf(out, "No handoff in %s.\n", folder)
		return nil
	}
	picked, matched := pickHandoff(folder, names, wd, flags.Args())
	if !matched {
		fmt.Fprintf(out, "No handoff matched %q: the latest one is used.\n", strings.Join(flags.Args(), " "))
	}
	if *thread {
		return printThread(out, folder, names, picked)
	}
	i := slices.Index(names, picked)
	before := "none"
	if i > 0 {
		prev := names[max(0, i-2):i]
		slices.Reverse(prev)
		before = strings.Join(prev, ", ")
	}
	parent, left := followedHandoff(folder, names, picked)
	// transcript.pickedHandoff reads these two first lines back from the transcript.
	fmt.Fprintf(out, "Folder: %s\nHandoff: %s\nBefore it: %s\n", folder, picked, before)
	if left != "" {
		fmt.Fprintln(out, left)
	}
	if err := printHandoff(out, folder, picked, ""); err != nil {
		return err
	}
	if parent == "" {
		return nil
	}
	return printHandoff(out, folder, parent, ", the handoff it follows")
}

// followedHandoff is the handoff picked follows, to print with it so a resume
// reads the generation before it without opening the file, and left is the line
// saying why there is none to print: it is not in the folder, or the two are
// over threadPrintLimit.
func followedHandoff(folder string, names []string, picked string) (parent, left string) {
	parent = relHandoff(folder, followsOf(filepath.Join(folder, picked)))
	switch {
	case parent == "":
		return "", ""
	case !slices.Contains(names, parent):
		return "", fmt.Sprintf("Goes on in %s, which %s names and which is not in the folder.", parent, picked)
	case handoffSize(folder, picked)+handoffSize(folder, parent) > threadPrintLimit:
		return "", fmt.Sprintf("Not printed, as the thread is over %s: %s.", kb(threadPrintLimit), parent)
	}
	return parent, ""
}

// handoffSize is the size of the handoff, 0 when it cannot be read.
func handoffSize(folder, name string) int64 {
	fi, err := os.Stat(filepath.Join(folder, name))
	if err != nil {
		return 0
	}
	return fi.Size()
}

// relHandoff is the handoff a Follows: line names, by its path relative to the
// folder.
func relHandoff(folder, follows string) string {
	if filepath.IsAbs(follows) {
		if rel, err := filepath.Rel(folder, follows); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return follows
}

// listHandoffs is the .md files under folder, at any depth, by their path
// relative to it with / separators, in path order: the last is the latest. A
// missing folder holds none.
func listHandoffs(folder string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(folder, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == folder && os.IsNotExist(err) {
				return fs.SkipAll
			}
			return err
		}
		if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".md") {
			rel, err := filepath.Rel(folder, path)
			if err != nil {
				return err
			}
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	slices.Sort(names)
	return names, err
}

// pickHandoff picks a handoff from names: the one args name by its path,
// absolute or relative to wd or to folder; else the latest whose name or first
// heading holds args' words, ignoring case; else the latest, and matched is
// false when args were given.
func pickHandoff(folder string, names []string, wd string, args []string) (picked string, matched bool) {
	latest := names[len(names)-1]
	if len(args) == 0 {
		return latest, true
	}
	if len(args) == 1 {
		for _, p := range []string{args[0], filepath.Join(wd, args[0]), filepath.Join(folder, args[0])} {
			if !filepath.IsAbs(p) {
				continue
			}
			if rel, err := filepath.Rel(folder, p); err == nil && slices.Contains(names, filepath.ToSlash(rel)) {
				return filepath.ToSlash(rel), true
			}
		}
	}
	words := strings.ToLower(strings.Join(args, " "))
	for i := len(names) - 1; i >= 0; i-- {
		if strings.Contains(strings.ToLower(names[i]), words) ||
			strings.Contains(strings.ToLower(firstHeading(filepath.Join(folder, names[i]))), words) {
			return names[i], true
		}
	}
	return latest, false
}

// headLines is how many lines of a handoff are searched for its first heading
// and its Follows: line.
const headLines = 10

// firstHeading is the first Markdown heading in the handoff's first lines, ""
// when there is none.
func firstHeading(path string) string {
	h, _ := headLine(path, func(line string) (string, bool) {
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#")), true
		}
		return "", false
	})
	return h
}

// followsOf is the handoff the handoff at path continues, from its Follows:
// line, "" when it has none.
func followsOf(path string) string {
	f, _ := headLine(path, func(line string) (string, bool) {
		v, ok := strings.CutPrefix(line, "Follows:")
		return strings.TrimSpace(v), ok
	})
	return f
}

// headLine is the value match finds in the first of the file's first headLines
// lines it accepts.
func headLine(path string, match func(string) (string, bool)) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for i := 0; i < headLines && sc.Scan(); i++ {
		if v, ok := match(strings.TrimSpace(sc.Text())); ok {
			return v, true
		}
	}
	return "", false
}

// printThread prints the thread that ends with last, first handoff first, and
// the text of all its handoffs, or of the first and the last threadTail ones
// when they total more than threadPrintLimit.
func printThread(out io.Writer, folder string, names []string, last string) error {
	chain := []string{last}
	missing, namedBy := "", ""
	for cur := last; ; {
		next := relHandoff(folder, followsOf(filepath.Join(folder, cur)))
		if next == "" {
			break
		}
		if !slices.Contains(names, next) {
			missing, namedBy = next, cur
			break
		}
		if slices.Contains(chain, next) {
			break // a loop of Follows: lines: the thread ends where it comes back
		}
		chain = append(chain, next)
		cur = next
	}
	slices.Reverse(chain)

	var total int64
	for _, n := range chain {
		fi, err := os.Stat(filepath.Join(folder, n))
		if err != nil {
			return err
		}
		total += fi.Size()
	}
	if len(chain) == 1 {
		fmt.Fprintf(out, "Folder: %s\nThread: 1 handoff, %s, %s: it starts the thread.\n", folder, kb(total), last)
	} else {
		fmt.Fprintf(out, "Folder: %s\nThread: %d handoffs, %s, from %s to %s.\n", folder, len(chain), kb(total), chain[0], last)
	}
	if missing != "" {
		fmt.Fprintf(out, "Goes on in %s, which %s names and which is not in the folder.\n", missing, namedBy)
	}
	printed := chain
	if total > threadPrintLimit && len(chain) > 1+threadTail {
		skipped := chain[1 : len(chain)-threadTail]
		fmt.Fprintf(out, "Not printed, as the thread is over %s: %s.\n", kb(threadPrintLimit), strings.Join(skipped, ", "))
		printed = append([]string{chain[0]}, chain[len(chain)-threadTail:]...)
	}
	for _, n := range printed {
		if err := printHandoff(out, folder, n, ""); err != nil {
			return err
		}
	}
	return nil
}

// printHandoff prints the handoff's name, what note says it is, and its text.
func printHandoff(out io.Writer, folder, name, note string) error {
	b, err := os.ReadFile(filepath.Join(folder, name))
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\n=== %s%s\n%s", name, note, b)
	if len(b) > 0 && b[len(b)-1] != '\n' {
		fmt.Fprintln(out)
	}
	return nil
}

// kb is size in kilobytes, rounded up, as "86 KB".
func kb(size int64) string { return fmt.Sprintf("%d KB", (size+1023)/1024) }
