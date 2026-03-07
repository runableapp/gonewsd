//go:build windows

package main

import (
	"os"
	"os/signal"

	"gonewsd/internal/config"
)

func isRootUser() bool {
	return false
}

// runAs is a no-op on Windows: uid/gid switching is Unix-specific.
func runAs(cfg *config.Config) error {
	_ = cfg
	return nil
}

// daemonize is a no-op on Windows.
func daemonize() error {
	return nil
}

func notifySignals(sigCh chan<- os.Signal) {
	signal.Notify(sigCh, os.Interrupt)
}

func isReloadSignal(sig os.Signal) bool {
	_ = sig
	return false
}
