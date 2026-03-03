//go:build windows

// Copyright © 2026 Runable.app. GPL-3.0.
//
// lock_windows.go provides Windows-compatible group locking stubs.
// Windows does not support syscall.Flock, so this falls back to best-effort
// lock-file open/close semantics.
package group

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gonewsd/internal/config"
)

// Dirname returns the group directory path under SpoolDir.
func (g *Group) Dirname(cfg *config.Config) string {
	pathGroup := strings.ReplaceAll(g.Name, ".", string(filepath.Separator))
	return filepath.Join(cfg.SpoolDir, pathGroup)
}

// openLockFile opens (or creates) the .lock file and chowns it to the configured user when running as root.
func openLockFile(cfg *config.Config, path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Chown to configured user when running as root (same as .config and .info)
	if os.Geteuid() == 0 && !cfg.BadUser {
		if err := os.Chown(path, int(cfg.UID), int(cfg.GID)); err != nil {
			f.Close()
			return nil, fmt.Errorf("chown %q: %w", path, err)
		}
	}
	return f, nil
}

// readLock acquires a best-effort shared lock on Windows by holding the lock file open.
func (g *Group) readLock(cfg *config.Config) (unlock func(), err error) {
	path := filepath.Join(g.Dirname(cfg), ".lock")
	f, err := openLockFile(cfg, path)
	if err != nil {
		return nil, err
	}
	return func() {
		f.Close()
	}, nil
}

// writeLock acquires a best-effort exclusive lock on Windows by holding the lock file open.
func (g *Group) writeLock(cfg *config.Config) (unlock func(), err error) {
	path := filepath.Join(g.Dirname(cfg), ".lock")
	f, err := openLockFile(cfg, path)
	if err != nil {
		return nil, err
	}
	return func() {
		f.Close()
	}, nil
}
