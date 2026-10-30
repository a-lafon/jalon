package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/a-lafon/jalon/internal/advisor"
	"github.com/a-lafon/jalon/internal/cli"
	"github.com/a-lafon/jalon/internal/config"
)

// statuslineTimeout bounds the whole command: the status line is redrawn after
// every assistant message and must never lag. Tests lift it, as hookTimeout.
var statuslineTimeout = 200 * time.Millisecond

// runStatusline prints the context size the client reports on stdin, behind a
// pole whose bands fill as the context grows above the session's floor, which it
// reads from the state file the hook keeps; the pole is full, and a reset
// advised, by the hook's rule. It writes no file: the
// prompt hook reads the size from the transcript itself, so this is only a
// display, for clients that run a status line. It prints nothing when
// the size is missing or on any error, so a status line it is appended to keeps
// working; the one exception is a config that does not load, which is named,
// since it stops the hooks too and lasts until the file is edited.
func runStatusline(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("statusline", flag.ContinueOnError)
	configPath := flags.String("config", config.DefaultPath(), "path to the config file")
	clientName := flags.String("client", defaultClient, "coding agent that runs the status line")
	if err := flags.Parse(args); err != nil {
		return err
	}
	adapter, err := clientFor(*clientName)
	if err != nil {
		fmt.Fprintln(stderr, "jalon:", err)
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return nil
	}
	size, session, ok := adapter.StatusSize(raw)
	if !ok {
		return nil
	}

	b := bounded(ctx, statuslineTimeout, func() []byte {
		// The status line is the one place the user looks at every turn, so a
		// config that does not load is said there and not only on stderr.
		cfg, loaded := loadConfig(flags, *configPath)
		if !loaded {
			return []byte("jalon: bad config\n")
		}
		var buf bytes.Buffer
		buf.WriteString("jalon ")
		if !cfg.Context.Enabled {
			fmt.Fprintf(&buf, "%s\n", cli.TokensK(float64(size)))
			return buf.Bytes()
		}
		// Until a hook has kept the floor, the growth is unknown: an
		// empty pole rather than one counted from zero, which a session that
		// starts heavy, as in Claude Desktop, would fill and then empty.
		filled := 0
		if floor := sessionFloor(session); floor > 0 {
			filled = poleBands(size-floor, cfg.Context.AdviseTokens)
		}
		fmt.Fprintf(&buf, "%s %s", pole(filled, os.Getenv("NO_COLOR") == ""), cli.TokensK(float64(size)))
		if filled == poleLength {
			buf.WriteString(" · reset advised")
		}
		buf.WriteByte('\n')
		return buf.Bytes()
	})
	_, _ = out.Write(b)
	return nil
}

// sessionFloor is the floor the prompt hook kept for the session, read without
// writing anything. It is 0 when the session ID is not safe, or the state is
// missing, unreadable or holds no floor.
func sessionFloor(session string) int {
	if !advisor.SafeSessionID(session) {
		return 0
	}
	dir, err := sessionsDir()
	if err != nil {
		return 0
	}
	st, _ := advisor.Load(dir, session)
	return st.Floor
}

// poleLength is the number of bands of the pole, as on the logo.
const poleLength = 3

// poleRed is the red of the pole's bands, #E8381B, as a 24-bit ANSI color.
const poleRed = "\x1b[38;2;232;56;27m"

// poleBands is how many bands of the pole the growth above the floor fills: one
// per third of the threshold, so the pole is full exactly when the hook advises.
func poleBands(growth, adviseTokens int) int {
	if growth <= 0 || adviseTokens <= 0 {
		return 0
	}
	return min(growth*poleLength/adviseTokens, poleLength)
}

// pole draws the bands, the filled ones first, in red when color is true.
func pole(filled int, color bool) string {
	bands := strings.Repeat("▰", filled)
	if color && filled > 0 {
		bands = poleRed + bands + "\x1b[0m"
	}
	return bands + strings.Repeat("▱", poleLength-filled)
}
