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
	"strconv"
	"strings"
	"time"
	"unicode"

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

// The instruction is what the session's model is asked to do when a reset is
// advised: an opening, which says when to give the section, then the body,
// which describes it. The body is the same for both hooks and every client, so
// the advice cannot drift between them. {tokens} is filled in the openings;
// {dir} (the absolute handoff folder), {name} (the handoff file name prefix)
// and {header} (the handoff's first lines) in the body, and so are the
// client's commands: {clear}, {compact}, and {resume}, the line that resumes
// from the handoff (see resumeLine).
const (
	promptOpening = `Jalon: this conversation holds about {tokens}K tokens of context. Answer the user's message first.
Then end your reply with `
	stopOpening = advisor.MessagePrefix + `{tokens}K tokens. Your answer to the user is done and shown: do not repeat it.
Add, as your last message, `
	instructionBody = `a short section written as a Markdown blockquote (every line starts with "> "), whose first words are
**Jalon** —, in the language the user writes in; it recommends ONE option:
1. New session: the latest request starts a task unrelated to the earlier work. Say so in one line.
2. Handoff: the same task continues and much state must carry over. Write {dir}/{name}-<short-slug>.md,
   in the language the user writes in. {header} Then: goal, decisions and why, what was dropped and why, current state (what was checked, with the
   command or test run and its result in a few words; what was not: ideas, assumptions, untested fixes), the user's latest request and the answer this reply gives it, files involved, next steps (proposals to confirm with the user, not orders), pitfalls and what not to redo.
   Never list an outward action (push, pull or merge request, API call, message to a person or another session) as a thing
   to do: at most as waiting for the user's OK. State the facts the next steps need from outside sources
   (tickets, docs, web pages, logs): a new session cannot see them. No raw tool output. Then give the user the lines to copy, in a code block: {clear}, then {resume}
   If a skill or command you were given defines your role for the ongoing work, whether it was invoked at the start
   or later, say so in the handoff and list any background task to restart; then the second line is that command
   followed by the handoff's full path as plain text and ` + resumeRequest + `. Do not re-invoke a skill
   used once for a side task.
3. Compact: the same task continues with little state to carry. Give one ready line, in a code block: {compact} <what to keep;
   what to drop>.
While background work you started that will report back (agents, background commands) is still running, do not recommend
a new session or {clear}: its result would reach a conversation that lost why it was started, or end with this session.
Recommend a compact whose line keeps the running work, what to do with its result, and the handoff path and first lines
of option 2; add one line saying the user can ask for that handoff once the work has reported.
{cache}Keep the section under 10 lines, apart from the handoff file. Give it in this reply only: do not repeat it in later replies.`
)

// cacheLine fills {cache} when the cache lifetime is known: after a longer
// pause the first request writes the whole context to the cache again, which
// costs many times reading it, and the same for a reset taken then.
const cacheLine = `Add one line to the section: the prompt cache keeps this context for {ttl}; after a longer pause, the next request
writes it all again, so the option is best taken before such a pause.
`

// cacheLifetime says d in words: "an hour", "5 minutes".
func cacheLifetime(d time.Duration) string {
	switch {
	case d == time.Hour:
		return "an hour"
	case d%time.Hour == 0:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
}

// resumeRequest is what follows the handoff's path on the resume line. A new
// session told only to resume checks again the state the handoff gives and
// starts on its next step, which costs requests the handoff was written to save.
const resumeRequest = `a request, in the user's language,
   to read it, sum up its goal, state and next step in three lines, keeping what was not checked marked as not
   checked, then wait for the user's request without checking again the state it describes; the request names no task
   or next step`

// resumeLine is the line that resumes from the handoff: the client's resume
// command with the path when it has one, which does what resumeRequest asks,
// else the path attached and resumeRequest.
func resumeLine(cmds client.Commands) string {
	if cmds.Resume != "" {
		return cmds.Resume + " {dir}/{name}-<short-slug>.md (the full path), with nothing after it."
	}
	return cmds.Include + "{dir}/{name}-<short-slug>.md (the full path) followed by " + resumeRequest + "."
}

// handoffOpening answers the client's handoff command: the advice's body,
// with the handoff the user asked for in place of the option the size would
// choose. {focus} is the user's words after the command, when there are any.
const handoffOpening = `Jalon: the user typed {command} to ask for a handoff of this conversation now.{focus}
Recommend option 2, the handoff, whatever the context size, and add one line with option 3's ready compact line, in case
the user prefers compacting. End your reply with `

// handoffAskedPrefix starts the message the user sees for the handoff command.
// It is not advisor.MessagePrefix, so the context report does not count it as
// an advice.
const handoffAskedPrefix = "Jalon: handoff asked"

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
			return runStopHook(ctx, args[1:], stdin, out, time.Now())
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

		// A resume shows as the handoff's path in the prompt: the advice's own
		// resume line holds it. Only the session's first prompt written by a
		// person is read, when the hook holds no state for it yet: a later path
		// is a mention, not a resume.
		if _, seen := advisor.Load(dir, prompt.Session); prompt.Dir != "" && !seen {
			if name := resumedHandoff(prompt.Text, projectRoot(prompt.Dir)); name != "" {
				_ = advisor.SetHandoff(dir, prompt.Session, name, threadOf(name), "")
			}
		}
		tail := adapter.ReadTail(prompt, "", false)
		if words, ok := handoffAsked(prompt.Text, adapter); ok {
			return askHandoff(dir, adapter, prompt, tail, cfg, words, now)
		}
		st, ok := measure(dir, adapter, prompt, tail, cfg, now)
		if !ok {
			return writeBack(adapter, prompt, st)
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
		body, ok := recordAdvice(dir, adapter, prompt, st, tail.CacheTTL, now)
		if !ok {
			return nil
		}
		kTokens := cli.TokensK(float64(st.Size))
		reply, err := adapter.Reply(
			advisor.MessagePrefix+kTokens+" tokens, a reset proposal follows the answer.",
			strings.ReplaceAll(promptOpening, "{tokens}", strings.TrimSuffix(kTokens, "K"))+body)
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
// the prompt hook asked for, or when the context crossed the threshold during
// the turn. Like the prompt hook, it never fails the turn.
func runStopHook(ctx context.Context, args []string, stdin io.Reader, out io.Writer, now time.Time) error {
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
		// Only a turn the prompt hook saw start from a person's prompt: not one
		// a notice or another session started.
		st, ok := advisor.Load(dir, ev.Session)
		if !ok || st.Prompt != ev.ID {
			return nil
		}
		// A program that runs claude -p reads the last message as its output: a
		// block would replace it. Only a turn whose prompt a person typed is
		// blocked; that is looked for only before blocking, since in a program's
		// session the search reads 8 MB.
		typed := func() bool { return adapter.ReadTail(ev.Prompt, ev.ID, true).Typed }
		cfg, err := cli.LoadConfig(flags, *configPath)
		if err != nil || !cfg.Context.Enabled {
			return nil
		}

		if st.AdvisedPrompt == ev.ID {
			// The instruction asks to end the reply with the section, so only the
			// last message counts.
			if strings.Contains(ev.LastMessage, "**Jalon**") {
				return nil
			}
			// After a compaction during the turn, the advice is moot.
			if advisor.Compacted(st.LastAdvised, adapter.ReadTail(ev.Prompt, "", false).Size) || !typed() {
				return nil
			}
			handoffDir := handoffFolder(ev.Dir)
			reason := reminderPrefix + ", context at " + cli.TokensK(float64(st.LastAdvised)) +
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
		}

		tail := adapter.ReadTail(ev.Prompt, "", false)
		st, ok = measure(dir, adapter, ev.Prompt, tail, cfg, now)
		if !ok || !typed() {
			return nil
		}
		// A model may end its reply with a section of its own, as the advice of an
		// earlier turn asked: that reply already proposes a reset, so the advice
		// is recorded, and a block would only make it give a second section.
		if hasSection(ev.LastMessage) {
			_ = advisor.MarkAdvised(dir, ev.Session, st.Size, now, ev.ID)
			return nil
		}
		body, ok := recordAdvice(dir, adapter, ev.Prompt, st, tail.CacheTTL, now)
		if !ok {
			return nil
		}
		kTokens := cli.TokensK(float64(st.Size))
		reply, err := stopper.StopReply(
			strings.ReplaceAll(stopOpening, "{tokens}", strings.TrimSuffix(kTokens, "K"))+body,
			advisor.MessagePrefix+kTokens+" tokens, a reset proposal follows.")
		if err != nil {
			return nil
		}
		return reply
	})
	_, _ = out.Write(b)
	return nil
}

// hasSection tells whether msg holds a Jalon section: a blockquote line that
// starts with **Jalon**, not the name in bold within an answer.
func hasSection(msg string) bool {
	for line := range strings.Lines(msg) {
		if strings.HasPrefix(strings.TrimSpace(line), "> **Jalon**") {
			return true
		}
	}
	return false
}

// handoffAsked tells whether text is the client's handoff command, alone or
// followed by a space, and returns the words after it.
func handoffAsked(text string, adapter client.Adapter) (string, bool) {
	text = strings.TrimSpace(text)
	// Only a command reads the client's skill files, not every prompt.
	if !strings.HasPrefix(text, "/") {
		return "", false
	}
	cmd := adapter.Commands().Handoff
	rest, ok := strings.CutPrefix(text, cmd)
	if cmd == "" || !ok || rest != "" && !unicode.IsSpace(rune(rest[0])) {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// askHandoff answers the handoff command at any size: it records the size as
// for any prompt, then names the handoff and records it as an advice does, so
// its thread, the stop hook's reminder and the write-back note work as for an
// advice, and the next advice comes one step later.
func askHandoff(dir string, adapter client.Adapter, p client.Prompt, tail client.Tail, cfg config.Config, words string, now time.Time) []byte {
	measure(dir, adapter, p, tail, cfg, now)
	st, _ := advisor.Load(dir, p.Session)
	body, ok := recordAdvice(dir, adapter, p, st, tail.CacheTTL, now)
	if !ok {
		return nil
	}
	focus := ""
	if words != "" {
		focus = " The user's words after the command say what to stress: " + strconv.Quote(words) + "."
	}
	message := handoffAskedPrefix + "."
	if st.Size > 0 {
		message = handoffAskedPrefix + " at " + cli.TokensK(float64(st.Size)) + " tokens."
	}
	opening := strings.NewReplacer("{command}", adapter.Commands().Handoff, "{focus}", focus).Replace(handoffOpening)
	reply, err := adapter.Reply(message, opening+body)
	if err != nil {
		return nil
	}
	return reply
}

// writeBackNote reminds the model, on each prompt after the advice whose
// handoff was written, to keep that handoff up to date: it is written in the
// reply to the advised prompt, and what comes after would not be in it.
const writeBackNote = `Jalon: the handoff {path} was written at the last advice of this conversation and is not resumed yet.
Answer the user's message first. If this turn changes what the handoff records (current state, what was checked,
decisions, next steps, the user's latest request), update it with a short edit at the end of the turn; otherwise
leave it. Do not mention this note.`

// writeBack is the prompt hook's output for a prompt that gets no advice: the
// write-back note when the last advice's handoff was written and the session
// has not been compacted since (a compaction resets LastAdvised), else
// nothing. It writes nothing.
func writeBack(adapter client.Adapter, p client.Prompt, st advisor.Session) []byte {
	if st.Proposed == "" || st.LastAdvised == 0 {
		return nil
	}
	handoffDir := handoffFolder(p.Dir)
	written := lastHandoff(handoffDir, st.Proposed+"-")
	if written == "" {
		return nil
	}
	reply, err := adapter.Reply("", strings.ReplaceAll(writeBackNote, "{path}", filepath.Join(handoffDir, written)))
	if err != nil {
		return nil
	}
	return reply
}

// measure records the context size of the turn of p in the session's state,
// read from the transcript's tail, with the floor when the state holds none,
// and runs the advisor. It returns the state, and true when a reset is advised.
func measure(dir string, adapter client.Adapter, p client.Prompt, tail client.Tail, cfg config.Config, now time.Time) (advisor.Session, bool) {
	// The size is read from the client's own record of the conversation, so
	// the hook needs no status line.
	if tail.Size <= 0 {
		// A session's first prompt has no size yet: its ID still tells the stop
		// hook its turn.
		if p.ID != "" {
			_ = advisor.SetPrompt(dir, p.Session, p.ID)
		}
		return advisor.Session{}, false
	}
	if advisor.Record(dir, p.Session, tail.Size, now, p.ID, tail.Typed) != nil {
		return advisor.Session{}, false
	}
	st, ok := advisor.Load(dir, p.Session)
	if !ok {
		return st, false
	}
	// The floor is what the conversation starts with, read once from the head
	// of the transcript and kept; the first advice counts the growth above it.
	if st.Floor == 0 {
		if floor, ok := adapter.ContextFloor(p); ok && advisor.SetFloor(dir, p.Session, floor) == nil {
			st.Floor = floor
		}
	}
	advice, _ := advisor.Advise(advisor.State{LastAdvised: st.LastAdvised}, st.Size, st.Floor,
		advisor.Config{AdviseTokens: cfg.Context.AdviseTokens, StepTokens: cfg.Context.AdviseStepTokens})
	return st, advice.Kind == advisor.Reset
}

// recordAdvice does what an advice at the state's size needs before it is
// shown: it names the next handoff, creates its folder, and records the advice
// at the prompt of p, so the next prompt does not repeat it. It returns the
// instruction body, filled in, with the cache lifetime ttl when it is not 0.
func recordAdvice(dir string, adapter client.Adapter, p client.Prompt, st advisor.Session, ttl time.Duration, now time.Time) (string, bool) {
	handoffDir := handoffFolder(p.Dir)
	// A handoff written from the last advice's name is the parent of the next one.
	parent := st.Parent
	if st.Proposed != "" {
		if name := lastHandoff(handoffDir, st.Proposed+"-"); name != "" {
			parent = name
		}
	}
	thread := st.Thread
	if thread == "" {
		var err error
		if thread, err = newThreadID(threadRand); err != nil {
			return "", false
		}
	}
	day := now.Format("2006-01-02")
	name := day + "/" + now.Format("1504") + "-" + thread
	// The folder is best effort: the advice is given even when it cannot be made.
	if p.Dir != "" {
		_ = ensureHandoffFolder(projectRoot(p.Dir))
	}
	// Record the advice before it is shown, so the next prompt does not repeat it.
	// A handoff asked before the first size is known records no advised size.
	if st.Size > 0 {
		_ = advisor.MarkAdvised(dir, p.Session, st.Size, now, p.ID)
	}
	_ = advisor.SetHandoff(dir, p.Session, parent, thread, name)
	return fillBody(adapter, handoffDir, name, handoffHeader(parent, p.Session), ttl), true
}

// handoffFolder is the handoff folder of the project in dir, relative when the
// client gave no directory.
func handoffFolder(dir string) string {
	if dir == "" {
		return filepath.Join(".jalon", "handoffs")
	}
	return filepath.Join(projectRoot(dir), ".jalon", "handoffs")
}

// handoffHeader is what the instruction asks the handoff's first lines to be.
func handoffHeader(parent, session string) string {
	if parent != "" {
		return fmt.Sprintf(`Its first lines are "Follows: %s" and "Session: %s".`, parent, session)
	}
	return fmt.Sprintf(`Its first line is "Session: %s".`, session)
}

// fillBody is the instruction body for the handoff name prefix name in
// handoffDir, with the client's commands and, when ttl is not 0, the cache line.
func fillBody(adapter client.Adapter, handoffDir, name, header string, ttl time.Duration) string {
	cmds := adapter.Commands()
	cache := ""
	if ttl > 0 {
		cache = strings.ReplaceAll(cacheLine, "{ttl}", cacheLifetime(ttl))
	}
	// {resume} and {cache} go first: the line {resume} puts in holds {dir} and {name}.
	body := strings.NewReplacer("{resume}", resumeLine(cmds), "{cache}", cache).Replace(instructionBody)
	return strings.NewReplacer(
		"{dir}", handoffDir,
		"{name}", name,
		"{header}", header,
		"{clear}", cmds.Clear,
		"{compact}", cmds.Compact,
	).Replace(body)
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

// ensureHandoffFolder creates <root>/.jalon/handoffs, and
// <root>/.jalon/.gitignore holding "*" when there is none, so the folder is
// never committed. The folders it creates are private to the user: handoffs
// summarise conversations. The day folder is left to the model's write, so an
// advice not taken leaves no empty day folder. It never changes an existing
// file, and creates nothing when .jalon or handoffs is a symlink or a file,
// which could lead outside the project.
func ensureHandoffFolder(root string) error {
	dir := filepath.Join(root, ".jalon")
	for _, d := range []string{dir, filepath.Join(dir, "handoffs")} {
		if fi, err := os.Lstat(d); err == nil && !fi.IsDir() {
			return fmt.Errorf("%s is not a directory", d)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "handoffs"), 0o700); err != nil {
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
