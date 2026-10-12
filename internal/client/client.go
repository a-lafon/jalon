// Package client is the contract between Jalon and a coding agent. Everything
// the context advisor needs from a client goes through an Adapter, so supporting
// another client means one new package that implements it, plus its
// registration in cmd/jalon. The advisor, the state file and the handoff folder
// know nothing of any client.
package client

// Prompt is a prompt event as the client reports it.
type Prompt struct {
	// Session is the client's session ID, used as the state file name.
	Session string
	// ID is the prompt's ID, "" when the client gives none. The stop event of
	// the turn the prompt starts carries the same ID.
	ID string
	// Dir is the project directory, where the handoff folder is found. It is the
	// directory the session started in when the client says so, else its
	// working directory.
	Dir string
	// Human is false for prompts a person did not write: notices, messages from
	// other agents, background task reports.
	Human bool
	// Text is the prompt as it was written, where a resume from a handoff
	// shows as the handoff's path.
	Text string
	// Ref is a client-specific handle to find the context size. Claude Code: the
	// transcript path.
	Ref string
}

// Tail is what the end of the client's record of the conversation shows.
type Tail struct {
	// Size is the size in tokens of the main conversation's context, 0 when it
	// cannot be told.
	Size int
	// Typed is true when it was looked for and shows a prompt typed by a
	// person, not sent by a program; when a prompt ID was asked for, only that
	// prompt counts.
	Typed bool
}

// Stop is a stop event: the model ended its turn.
type Stop struct {
	// Prompt holds the session, the ID of the prompt that started the turn, the
	// project directory and the handle to find the context size; its Human and
	// Text are not set.
	Prompt
	// Blocked is true when the stop hook already blocked the end of this turn.
	Blocked bool
	// LastMessage is the text of the turn's last assistant message.
	LastMessage string
}

// Stopper is implemented by adapters whose client has a stop hook, run when the
// model ends its turn.
type Stopper interface {
	// ReadStop parses the client's stop hook input.
	ReadStop(stdin []byte) (Stop, error)
	// StopReply formats a stop hook output that blocks the end of the turn once:
	// reason goes to the model, which goes on, and userMessage, when not "", to
	// the user.
	StopReply(reason, userMessage string) ([]byte, error)
}

// Commands are what the user types to reset a context in this client.
type Commands struct {
	Clear   string // starts an empty conversation, e.g. "/clear"
	Compact string // compacts the conversation, e.g. "/compact"
	// Include is the prefix that attaches a file to a prompt, followed by its
	// absolute path, e.g. "@".
	Include string
}

// Adapter is everything Jalon needs from one client.
type Adapter interface {
	// Name is the value of --client.
	Name() string
	// ReadPrompt parses the client's prompt hook input.
	ReadPrompt(stdin []byte) (Prompt, error)
	// ReadTail reads the end of the conversation of p once: the context size,
	// and with typed, whether a prompt typed by a person shows, the one with ID
	// promptID when it is not "". Looking for a typed prompt can read much
	// further back, so it is asked for only before advice.
	ReadTail(p Prompt, promptID string, typed bool) Tail
	// ContextFloor is the size in tokens of the conversation's first request,
	// which a reset cannot remove, and false when it cannot be told yet.
	ContextFloor(Prompt) (int, bool)
	// Commands returns the client's reset commands.
	Commands() Commands
	// Reply formats the hook output: a message for the user and context for the model.
	Reply(userMessage, modelContext string) ([]byte, error)
	// StatusSize reads the context size and the session ID ("" when the input has
	// none) from the client's status line input, and false for the size when
	// the input has none or the client has no status line.
	StatusSize(stdin []byte) (size int, session string, ok bool)
}

// Installer is implemented by adapters that can install Jalon's commands into
// their client's settings. jalon init reads and writes the file; an Installer
// only transforms its content, and returns it unchanged when nothing changes.
type Installer interface {
	// SettingsPath is the client's default settings file.
	SettingsPath() (string, error)
	// Install returns the settings with the hooks running exe, and what
	// display asks of the status line, plus what it did and why.
	Install(settings []byte, exe string, display Display) ([]byte, []string, error)
	// Uninstall returns the settings without the commands Jalon installed.
	Uninstall(settings []byte) ([]byte, []string, error)
}

// Display is how jalon init shows the pole, besides the advice.
type Display int

const (
	// NoDisplay leaves the status line as it is.
	NoDisplay Display = iota
	// StatusLine installs Jalon's status line when there is none.
	StatusLine
	// Mod removes Jalon's status line: a mod the client installs draws the pole.
	Mod
)

// Runner runs a command of the client's own CLI and returns its standard
// output, with an error when it cannot start, wrapping exec.ErrNotFound when
// the command is not found, or when it exits with a failure.
type Runner func(name string, args ...string) ([]byte, error)

// ModInstaller is implemented by adapters whose client draws the pole with a
// mod that the client's own CLI installs; jalon init runs the commands through
// a Runner.
type ModInstaller interface {
	// CheckMod tells whether the client can install the mod, before anything is
	// written.
	CheckMod(run Runner) error
	// InstallMod installs the mod, which runs exe, or with dryRun says what it
	// would run.
	InstallMod(run Runner, exe string, dryRun bool) (string, error)
	// UninstallMod removes the mod, or with dryRun says what it would run. A mod
	// that is not installed is not an error.
	UninstallMod(run Runner, dryRun bool) (string, error)
}
