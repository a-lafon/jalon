package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/a-lafon/jalon/internal/client"
	"github.com/a-lafon/jalon/internal/client/claudecode"
	"github.com/a-lafon/jalon/internal/client/codex"
)

// defaultClient is used when --client is not given, so settings written before
// clients were selectable keep working.
const defaultClient = "claude-code"

// adapters is the registry: a new client is one package and one line here.
var adapters = map[string]client.Adapter{
	claudecode.Adapter{}.Name(): claudecode.Adapter{},
	codex.Adapter{}.Name():      codex.Adapter{},
}

// stderr is where a command explains why it printed nothing.
var stderr io.Writer = os.Stderr

// clientFor returns the adapter registered under name.
func clientFor(name string) (client.Adapter, error) {
	if a, ok := adapters[name]; ok {
		return a, nil
	}
	names := make([]string, 0, len(adapters))
	for n := range adapters {
		names = append(names, n)
	}
	sort.Strings(names)
	return nil, fmt.Errorf("unknown client %q (known: %s)", name, strings.Join(names, ", "))
}
