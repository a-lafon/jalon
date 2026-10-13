package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/a-lafon/jalon/internal/advisor"
	"github.com/a-lafon/jalon/internal/cli"
	"github.com/a-lafon/jalon/internal/client"
	"github.com/a-lafon/jalon/internal/config"
	"github.com/a-lafon/jalon/internal/handoff"
)

// hookTimeout bounds the whole command: the hook runs before every prompt is
// sent and must never delay it noticeably. Tests lift it, so a slow runner
// never loses the output.
var hookTimeout = 500 * time.Millisecond

// resetInstruction is what the session's model is asked to do when a reset is
// advised. It is the same for every client. {tokens}, {dir} (the absolute
// handoff folder), {name} (the handoff file name prefix) and {header} (the
// handoff's first lines) are filled in, and so are the client's commands:
// {clear}, {compact} and {include}, the prefix that attaches a file.
const resetInstruction = `Jalon: this conversation holds about {tokens}K tokens of context. Answer the user's message first.
Then end your reply with a short section written as a Markdown blockquote (every line starts with "> "), whose first words are
**Jalon** —, in the language the user writes in; it recommends ONE option:
1. New session: the latest request starts a task unrelated to the earlier work. Say so in one line.
2. Handoff: the same task continues and much state must carry over. Write {dir}/{name}-<short-slug>.md.
   {header} Then: goal, decisions and why, what was dropped and why, current state, the user's latest
   request and the answer this reply gives it, files involved, next steps, pitfalls and what not to redo. State the facts the next steps need from outside sources
   (tickets, docs, web pages, logs): a new session cannot see them. No raw tool output. Then give the user the lines to copy, in a code block: {clear}, then {include}{dir}/{name}-<short-slug>.md (the full path) followed by a request
   to resume from it.
   If a skill or command you were given defines your role for the ongoing work, whether it was invoked at the start
   or later, say so in the handoff and list any background task to restart; then the second line is that command
   followed by the handoff's full path as plain text and a request to resume from it. Do not re-invoke a skill
   used once for a side task.
3. Compact: the same task continues with little state to carry. Give one ready line, in a code block: {compact} <what to keep;
   what to drop>.
While background work you started that will report back (agents, background commands) is still running, do not recommend
a new session or {clear}: its result would reach a conversation that lost why it was started, or end with this session.
Recommend a compact whose line keeps the running work, what to do with its result, and the handoff path and first lines
of option 2; add one line saying the user can ask for that handoff once the work has reported.
Keep the section under 10 lines, apart from the handoff file. Give it in this reply only: do not repeat it in later replies.`

// reminderPrefix starts the stop hook's reason when the reply lacks the section
// asked at the start of the turn. The context report tells it from an advice
// by its prefix, so a reminder never counts as a second advice.
const reminderPrefix = "Jalon: reminder"

const hookUsage = `usage: jalon hook prompt|stop [-client name] [-config path]`

// runHook runs "jalon hook <name>": the prompt hook or the stop hook.
func runHook(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "prompt":
			return runPromptHook(ctx, args[1:], stdin, out, time.Now())
		case "stop":
			return runStopHook(ctx, args[1:], stdin, out)
		}
	}
	return errors.New(hookUsage)
}

// stateMaxAge is how long a session's state file is kept after its last record.
const stateMaxAge = 7 * 24 * time.Hour

// runPromptHook works as a client's prompt hook (Claude Code's UserPromptSubmit
// by default). It outputs
// nothing, and exits 0, unless a reset is advised, and it never fails the
// prompt: any error just means no advice this time. It reads the context size
// from the transcript, so it needs no status line.
func runPromptHook(ctx context.Context, args []string, stdin io.Reader, out io.Writer, now time.Time) error {
	flags := flag.NewFlagSet("hook prompt", flag.ContinueOnError)
	configPath := flags.String("config", config.DefaultPath(), "path to the config file")
	clientName := flags.String("client", defaultClient, "coding agent that runs the hook")
	if err := flags.Parse(args); err != nil {
		return err
	}
	adapter, err := clientFor(*clientName)
	if err != nil {
		fmt.Fprintln(stderr, "jalon:", err)
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, 4<<20))
	if err != nil {
		return nil
	}
	prompt, err := adapter.ReadPrompt(raw)
	// A prompt a person did not write, such as a message from another session,
	// gets no advice and leaves no trace: the advice waits for the next message.
	if err != nil || !prompt.Human || !advisor.SafeSessionID(prompt.Session) {
		return nil
	}

	b := bounded(ctx, hookTimeout, func() []byte {
		dir, err := sessionsDir()
		if err != nil {
			return nil
		}
		advisor.Sweep(dir, now, stateMaxAge)
		cfg, err := cli.LoadConfig(flags, *configPath)
		if err != nil || !cfg.Context.Enabled {
			return nil
		}

		handoffDir, root := filepath.Join(".jalon", "handoffs"), ""
		if prompt.Dir != "" {
			root = projectRoot(prompt.Dir)
			handoffDir = filepath.Join(root, ".jalon", "handoffs")
		}
		// A resume shows as the handoff's path in the prompt: the advice's own
		// resume line holds it. Only the session's first prompt written by a
		// person is read, when the hook holds no state for it yet: a later path
		// is a mention, not a resume.
		if _, seen := advisor.Load(dir, prompt.Session); root != "" && !seen {
			if name := resumedHandoff(prompt.Text, root); name != "" {
				_ = advisor.SetHandoff(dir, prompt.Session, name, threadOf(name), "")
			}
		}
		// The size is read from the client's own record of the conversation, so
		// the hook needs no status line.
		size := adapter.ReadTail(prompt, "", false).Size
		if size <= 0 {
			// A session's first prompt has no size yet: its ID still tells the stop
			// hook its turn.
			if prompt.ID != "" {
				_ = advisor.SetPrompt(dir, prompt.Session, prompt.ID)
			}
			return nil
		}
		if advisor.Record(dir, prompt.Session, size, now, prompt.ID, false) != nil {
			return nil
		}
		st, ok := advisor.Load(dir, prompt.Session)
		if !ok {
			return nil
		}
		// The floor is what the conversation starts with, read once from the head
		// of the transcript and kept; the first advice counts the growth above it.
		if st.Floor == 0 {
			if floor, ok := adapter.ContextFloor(prompt); ok && advisor.SetFloor(dir, prompt.Session, floor) == nil {
				st.Floor = floor
			}
		}
		advice, _ := advisor.Advise(advisor.State{LastAdvised: st.LastAdvised}, st.Size, st.Floor,
			advisor.Config{AdviseTokens: cfg.Context.AdviseTokens, StepTokens: cfg.Context.AdviseStepTokens})
		if advice.Kind != advisor.Reset {
			return nil
		}
		// Only a session a person typed in is advised: in one a program drives, the
		// reply is the program's output. A program that resumes a session a person
		// typed in is not told apart. The prompt is not in the transcript yet, so any
		// typed prompt before it shows the session is typed in; it is looked for
		// only now, since in a program's session the search reads 8 MB.
		if !st.Typed {
			if !adapter.ReadTail(prompt, "", true).Typed ||
				advisor.Record(dir, prompt.Session, st.Size, now, prompt.ID, true) != nil {
				return nil
			}
		}

		// A handoff written from the last advice's name is the parent of the next one.
		parent := st.Parent
		if st.Proposed != "" {
			if name := lastHandoff(handoffDir, st.Proposed+"-"); name != "" {
				parent = name
			}
		}
		thread := st.Thread
		if thread == "" {
			if thread, err = newThreadID(threadRand); err != nil {
				return nil
			}
		}
		day := now.Format("2006-01-02")
		name := day + "/" + now.Format("1504") + "-" + thread
		// The folder is best effort: the advice is given even when it cannot be made.
		if root != "" {
			_ = ensureHandoffFolder(root, day)
		}
		header := fmt.Sprintf(`Its first line is "Session: %s".`, prompt.Session)
		if parent != "" {
			header = fmt.Sprintf(`Its first lines are "Follows: %s" and "Session: %s".`, parent, prompt.Session)
		}
		// Record the advice before it is shown, so the next prompt does not repeat it.
		_ = advisor.MarkAdvised(dir, prompt.Session, st.Size, now, prompt.ID)
		_ = advisor.SetHandoff(dir, prompt.Session, parent, thread, name)

		kTokens := thousandsK(st.Size)
		cmds := adapter.Commands()
		instruction := strings.NewReplacer(
			"{tokens}", strings.TrimSuffix(kTokens, "K"),
			"{dir}", handoffDir,
			"{name}", name,
			"{header}", header,
			"{clear}", cmds.Clear,
			"{compact}", cmds.Compact,
			"{include}", cmds.Include,
		).Replace(resetInstruction)
		reply, err := adapter.Reply(
			advisor.MessagePrefix+kTokens+" tokens, a reset proposal follows the answer.", instruction)
		if err != nil {
			return nil
		}
		return reply
	})
	_, _ = out.Write(b)
	return nil
}

// runStopHook works as a client's stop hook (Claude Code's Stop), run when the
// model ends a turn that a person's prompt started. It blocks the end of the
// turn once, so the model adds the section, when the reply lacks the section
// the prompt hook asked for. Like the prompt hook, it never fails the turn.
func runStopHook(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("hook stop", flag.ContinueOnError)
	configPath := flags.String("config", config.DefaultPath(), "path to the config file")
	clientName := flags.String("client", defaultClient, "coding agent that runs the hook")
	if err := flags.Parse(args); err != nil {
		return err
	}
	adapter, err := clientFor(*clientName)
	if err != nil {
		fmt.Fprintln(stderr, "jalon:", err)
		return nil
	}
	stopper, ok := adapter.(client.Stopper)
	if !ok {
		fmt.Fprintf(stderr, "jalon: %s has no stop hook\n", *clientName)
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, 4<<20))
	if err != nil {
		return nil
	}
	ev, err := stopper.ReadStop(raw)
	// Blocking once is enough: a model that ignores the reason costs one more
	// request, never a loop.
	if err != nil || ev.Blocked || ev.ID == "" || !advisor.SafeSessionID(ev.Session) {
		return nil
	}

	b := bounded(ctx, hookTimeout, func() []byte {
		dir, err := sessionsDir()
		if err != nil {
			return nil
		}
		// Only a turn the prompt hook saw start from a person's prompt, and
		// advised at: not one a notice or another session started.
		st, ok := advisor.Load(dir, ev.Session)
		if !ok || st.Prompt != ev.ID || st.AdvisedPrompt != ev.ID {
			return nil
		}
		cfg, err := cli.LoadConfig(flags, *configPath)
		if err != nil || !cfg.Context.Enabled {
			return nil
		}
		// The instruction asks to end the reply with the section, so only the
		// last message counts.
		if strings.Contains(ev.LastMessage, "**Jalon**") {
			return nil
		}
		// After a compaction during the turn, the advice is moot.
		if advisor.Compacted(st.LastAdvised, adapter.ReadTail(ev.Prompt, "", false).Size) {
			return nil
		}
		// A program that runs claude -p reads the last message as its output: a
		// block would replace it. Only a turn whose prompt a person typed is
		// blocked; that is looked for only before blocking, since in a program's
		// session the search reads 8 MB.
		if !adapter.ReadTail(ev.Prompt, ev.ID, true).Typed {
			return nil
		}
		handoffDir := handoffFolder(ev.Dir)
		reason := reminderPrefix + ", context at " + thousandsK(st.LastAdvised) +
			" tokens: your reply did not end with the section asked at the start of this turn. Do not repeat your answer.\n"
		if written := lastHandoff(handoffDir, st.Proposed+"-"); st.Proposed != "" && written != "" {
			reason += "The handoff " + filepath.Join(handoffDir, written) + " is already written: do not write another file, give the section with its lines to copy for it.\n"
		}
		// One sentence, not the body: the model got the body at the start of the
		// turn, and Claude Code's terminal prints the whole reason as an error.
		reason += "Add now, as your last message, that section: a Markdown blockquote starting with **Jalon** — that recommends ONE option, a new session, a handoff written to " +
			filepath.Join(handoffDir, st.Proposed) + "-<short-slug>.md with the lines to copy, or a " + adapter.Commands().Compact + " line."
		reply, err := stopper.StopReply(reason, "")
		if err != nil {
			return nil
		}
		return reply
	})
	_, _ = out.Write(b)
	return nil
}

// handoffFolder is the handoff folder of the project in dir, relative when the
// client gave no directory.
func handoffFolder(dir string) string {
	if dir == "" {
		return filepath.Join(".jalon", "handoffs")
	}
	return filepath.Join(projectRoot(dir), ".jalon", "handoffs")
}

// threadRand is where new thread IDs are drawn from; tests replace it.
var threadRand io.Reader = rand.Reader

// threadIDLen is the length of a thread ID, in characters of threadDigits.
const threadIDLen = 8

const threadDigits = "0123456789abcdefghijklmnopqrstuvwxyz"

// newThreadID draws a thread ID from r. Bytes from 252 up are dropped, so each
// of the 36 digits is equally likely (252 = 7 × 36).
func newThreadID(r io.Reader) (string, error) {
	id := make([]byte, 0, threadIDLen)
	b := make([]byte, 16)
	for len(id) < threadIDLen {
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		for _, c := range b {
			if c < 252 && len(id) < threadIDLen {
				id = append(id, threadDigits[c%36])
			}
		}
	}
	return string(id), nil
}

// handoffName matches a handoff name with a thread: date and time, thread ID,
// slug. The date is a day folder (2026-10-09/1358-...) or, in a flat name with no
// day folder, part of the file name (2026-10-09-1358-...).
var handoffName = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[-/]\d{4}-([0-9a-z]{8})-.+\.md$`)

// threadOf is the thread ID in a handoff name, "" for a name without one.
func threadOf(name string) string {
	if m := handoffName.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return ""
}

// resumedHandoff is the name, relative to the handoff folder of root, of the
// last existing handoff that text names by its path, "" when there is none.
func resumedHandoff(text, root string) string {
	found := ""
	for _, m := range handoff.Mentions(text) {
		if !m.In(root) {
			continue
		}
		if fi, err := os.Stat(filepath.Join(root, ".jalon", "handoffs", m.Name)); err == nil && fi.Mode().IsRegular() {
			found = m.Name
		}
	}
	return found
}

// lastHandoff is the name, relative to folder, of the last handoff in name
// order that starts with prefix, "" when there is none. A prefix with a day
// folder, 2026-10-09/1358-r4sefxw4-, is looked up in that day folder.
func lastHandoff(folder, prefix string) string {
	day := ""
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		day, prefix = prefix[:i+1], prefix[i+1:]
	}
	entries, err := os.ReadDir(filepath.Join(folder, day))
	if err != nil {
		return ""
	}
	last := ""
	for _, e := range entries {
		if n := e.Name(); e.Type().IsRegular() && strings.HasPrefix(n, prefix) && strings.HasSuffix(n, ".md") {
			last = day + n // ReadDir sorts by name
		}
	}
	return last
}

// projectRoot is the git top level of dir, or dir itself when it is not inside
// a git repository. A top level is the nearest parent holding a .git entry,
// a directory, or a file for a worktree or a submodule.
func projectRoot(dir string) string {
	for d := dir; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

// ensureHandoffFolder creates <root>/.jalon/handoffs/<day>, and
// <root>/.jalon/.gitignore holding "*" when there is none, so the folder is
// never committed. The folders it creates are private to the user: handoffs
// summarise conversations. It never changes an existing file, and creates
// nothing when .jalon, handoffs or the day folder is a symlink or a file, which
// could lead outside the project.
func ensureHandoffFolder(root, day string) error {
	dir := filepath.Join(root, ".jalon")
	for _, d := range []string{dir, filepath.Join(dir, "handoffs"), filepath.Join(dir, "handoffs", day)} {
		if fi, err := os.Lstat(d); err == nil && !fi.IsDir() {
			return fmt.Errorf("%s is not a directory", d)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "handoffs", day), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".gitignore"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = f.WriteString("*\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
