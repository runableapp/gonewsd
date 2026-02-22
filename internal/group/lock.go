// Copyright © 2026 Runable.app. GPL-3.0.
//
// lock.go provides per-group file locking. Dirname returns the group's directory
// path under SpoolDir. readLock acquires a shared flock on the group's .lock file;
// writeLock acquires an exclusive flock. Callers use the returned unlock function to release the lock.
package group

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

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

// readLock acquires a shared lock on the group's .lock file.
func (g *Group) readLock(cfg *config.Config) (unlock func(), err error) {
	path := filepath.Join(g.Dirname(cfg), ".lock")
	f, err := openLockFile(cfg, path)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock(%s, LOCK_SH): %w", path, err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// writeLock acquires an exclusive lock on the group's .lock file.
func (g *Group) writeLock(cfg *config.Config) (unlock func(), err error) {
	path := filepath.Join(g.Dirname(cfg), ".lock")
	f, err := openLockFile(cfg, path)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock(%s, LOCK_EX): %w", path, err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
