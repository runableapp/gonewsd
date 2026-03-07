//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"gonewsd/internal/config"
)

func isRootUser() bool {
	return os.Geteuid() == 0
}

// runAs switches to root dir and, if running as root, drops privileges to Config.User (UID/GID).
func runAs(cfg *config.Config) error {
	if err := os.Chdir("/"); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: chdir(\"/\"): %v\n", err)
		return err
	}
	if cfg.BadUser {
		fmt.Fprintf(os.Stderr, "🛑 Error: bad user %q\n", cfg.User)
		return fmt.Errorf("bad user")
	}
	if !isRootUser() {
		return nil
	}
	if err := syscall.Setgroups([]int{int(cfg.GID)}); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: setgroups: %v\n", err)
		return err
	}
	if err := syscall.Setgid(int(cfg.GID)); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: setgid: %v\n", err)
		return err
	}
	if err := syscall.Setuid(int(cfg.UID)); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: setuid: %v\n", err)
		return err
	}
	return nil
}

// daemonize detaches the process: redirects stdin/stdout/stderr to /dev/null and creates a new session.
// If already a session leader (e.g. launched by systemd), Setsid() returns EPERM; we ignore that.
func daemonize() error {
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	os.Stdin = null
	os.Stdout = null
	os.Stderr = null
	_, err = syscall.Setsid()
	if err != nil && err != syscall.EPERM {
		return err
	}
	return nil
}

func notifySignals(sigCh chan<- os.Signal) {
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
}

func isReloadSignal(sig os.Signal) bool {
	return sig == syscall.SIGHUP
}
