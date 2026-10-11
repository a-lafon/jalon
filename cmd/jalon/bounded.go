package main

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// bounded runs work and returns its output, or nothing when it takes longer
// than limit. The output is written by the caller, so a late result is never
// half written.
func bounded(ctx context.Context, limit time.Duration, work func() []byte) []byte {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	done := make(chan []byte, 1)
	go func() { done <- work() }()
	select {
	case b := <-done:
		return b
	case <-ctx.Done():
		return nil
	}
}

// sessionsDir is where the status line and the prompt hook share the state of
// each client session: ~/.jalon/sessions.
func sessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".jalon", "sessions"), nil
}
