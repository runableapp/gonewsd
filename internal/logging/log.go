// Copyright © 2026 Runable.app. GPL-3.0.
//
// Package logging provides newsd-compatible logging for gonewsd. It supports
// multiple backends (file, stderr, syslog, pipe), log rotation by size, a
// separate auth log, and level filtering (error/info/debug).
package logging

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gonewsd/internal/config"
)

// Logger provides newsd-compatible logging with rotation and multiple backends.
type Logger struct {
	level      int
	errorLog   string
	authLog    string // separate auth log path; empty = use main writer for auth
	maxLogSize int64
	mu         sync.Mutex
	writer     io.Writer
	file       *os.File
	authFile   *os.File // auth log file when authLog path is set
	pipeCmd    *exec.Cmd
	syslog     *syslogWriter
}

// NewLogger creates a logger from config. Call Init to open the log destination.
func NewLogger(cfg *config.Config) *Logger {
	return &Logger{
		level:      cfg.LogLevel,
		errorLog:   cfg.ErrorLog,
		authLog:    cfg.AuthLog,
		maxLogSize: cfg.MaxLogSize,
	}
}

// Init opens the log destination (file, stderr, syslog, or pipe) and optionally the auth log file.
func (l *Logger) Init() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.closeLocked()

	switch l.errorLog {
	case "stderr":
		l.writer = os.Stderr
	case "syslog":
		w, err := openSyslog("gonewsd")
		if err != nil {
			return fmt.Errorf("syslog: %w", err)
		}
		l.syslog = w
		l.writer = w
	default:
		if strings.HasPrefix(l.errorLog, "|") {
			cmdStr := strings.TrimSpace(l.errorLog[1:])
			parts := strings.Fields(cmdStr)
			if len(parts) == 0 {
				l.writer = os.Stderr
				return nil
			}
			cmd := exec.Command(parts[0], parts[1:]...)
			cmd.Stderr = os.Stderr
			stdin, err := cmd.StdinPipe()
			if err != nil {
				return fmt.Errorf("log pipe: %w", err)
			}
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("log pipe start: %w", err)
			}
			l.pipeCmd = cmd
			l.writer = stdin
		} else {
			// File
			f, err := os.OpenFile(l.errorLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				return fmt.Errorf("open log file %q: %w", l.errorLog, err)
			}
			l.file = f
			l.writer = f
		}
	}
	if l.authLog != "" {
		f, err := os.OpenFile(l.authLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return fmt.Errorf("open auth log %q: %w", l.authLog, err)
		}
		l.authFile = f
	}
	return nil
}

// closeLocked closes all log destinations (auth file, main file, pipe, syslog) and clears writer. Caller must hold mu.
func (l *Logger) closeLocked() {
	if l.authFile != nil {
		l.authFile.Close()
		l.authFile = nil
	}
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
	if l.pipeCmd != nil {
		l.pipeCmd.Process.Kill()
		l.pipeCmd = nil
	}
	if l.syslog != nil {
		l.syslog.Close()
		l.syslog = nil
	}
	l.writer = nil
}

// Close closes the log destination.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closeLocked()
	return nil
}

// Log writes a message if level <= logger level. Format is fmt-style.
// Hex encoding (ErrorLogHex) is not applied here; newsd only hex-encodes the
// GOT line (client input), which is done by the caller in server.go. So SEND
// and other lines show actual characters (e.g. tab) like newsd.
func (l *Logger) Log(level int, format string, args ...interface{}) {
	if level > l.level {
		return
	}
	msg := fmt.Sprintf(format, args...)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.syslog != nil {
		l.syslog.Write([]byte(msg + "\n"))
		return
	}

	if l.file != nil {
		l.rotateLocked(false)
		timestamp := time.Now().Format(time.ANSIC)
		line := fmt.Sprintf("%s gonewsd[%d]: %s", timestamp, os.Getpid(), msg)
		if !strings.HasSuffix(line, "\n") {
			line += "\n"
		}
		l.file.Write([]byte(line))
		l.file.Sync()
		return
	}

	// stderr or pipe
	timestamp := time.Now().Format(time.ANSIC)
	line := fmt.Sprintf("%s gonewsd[%d]: %s", timestamp, os.Getpid(), msg)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	l.writer.Write([]byte(line))
}

// LogAuth writes an auth-related message (e.g. auth failure). Uses AuthLog file if configured, else main writer.
func (l *Logger) LogAuth(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format(time.ANSIC)
	line := fmt.Sprintf("%s gonewsd[%d]: %s", timestamp, os.Getpid(), msg)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.writer
	if l.authFile != nil {
		w = l.authFile
	}
	w.Write([]byte(line))
	if l.authFile != nil {
		l.authFile.Sync()
	}
}

// rotateLocked rotates the main log file if size exceeds maxLogSize (or if force). Caller must hold mu.
func (l *Logger) rotateLocked(force bool) {
	if l.file == nil || l.maxLogSize <= 0 && !force {
		return
	}
	if !force {
		info, err := os.Stat(l.errorLog)
		if err != nil {
			return
		}
		if info.Size() <= l.maxLogSize {
			return
		}
	}
	oldPath := l.errorLog + ".O"
	l.file.Close()
	l.file = nil
	if err := os.Rename(l.errorLog, oldPath); err != nil {
		// Reopen for writing so we don't lose future logs
		f, _ := os.OpenFile(l.errorLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if f != nil {
			l.file = f
			l.writer = f
		}
		return
	}
	f, err := os.OpenFile(l.errorLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		l.writer = os.Stderr
		return
	}
	l.file = f
	l.writer = f
}

// Rotate forces log rotation (acquires lock).
func (l *Logger) Rotate(force bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	l.rotateLocked(force)
	return nil
}

// RotateWithLock forces log rotation; caller must hold the lock (e.g. for rotate command).
func (l *Logger) RotateWithLock(force bool) error {
	if l.file == nil {
		return nil
	}
	l.rotateLocked(force)
	return nil
}

// Lock acquires the log file lock (for external rotate coordination). No-op for non-file.
func (l *Logger) Lock() {
	// In Go we use mutex; for multi-process we'd use flock on l.file.
	// newsd uses flock so that logrotate or rotate command can coordinate.
	// For single-process gonewsd, mu is enough. For rotate we open, lock, rotate, unlock.
	l.mu.Lock()
}

// Unlock releases the log file lock.
func (l *Logger) Unlock() {
	l.mu.Unlock()
}

// SetLevel sets the log level (for -d debug).
func (l *Logger) SetLevel(level int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// LogSelf writes current config summary to the log (for startup).
func (l *Logger) LogSelf(cfg *config.Config) {
	l.Log(config.LogInfo, "ErrorLog %s", cfg.ErrorLog)
	var hostLookup string
	switch cfg.HostnameLookups {
	case 1:
		hostLookup = "on"
	case 2:
		hostLookup = "double"
	default:
		hostLookup = "off"
	}
	l.Log(config.LogInfo, "HostnameLookups %s", hostLookup)
	l.Log(config.LogInfo, "Listen %s", cfg.ListenAddr)
	var logLevel string
	switch cfg.LogLevel {
	case config.LogError:
		logLevel = "error"
	case config.LogDebug:
		logLevel = "debug"
	default:
		logLevel = "info"
	}
	l.Log(config.LogInfo, "LogLevel %s", logLevel)
	l.Log(config.LogInfo, "MaxClients %d", cfg.MaxClients)
	l.Log(config.LogInfo, "MaxLogSize %d", cfg.MaxLogSize)
	l.Log(config.LogInfo, "SendMail %s", cfg.SendMail)
	l.Log(config.LogInfo, "ServerName %s", cfg.ServerName)
	l.Log(config.LogInfo, "SpamFilter %s", cfg.SpamFilter)
	l.Log(config.LogInfo, "SpoolDir %s", cfg.SpoolDir)
	l.Log(config.LogInfo, "PostCommand %s", cfg.PostCommand)
	l.Log(config.LogInfo, "Timeout %s", cfg.Timeout)
	l.Log(config.LogInfo, "User %s", cfg.User)
	l.Log(config.LogInfo, "AuthMode %s AuthDB %s AuthLog %s", cfg.AuthMode, cfg.AuthDB, cfg.AuthLog)
	pidFile := cfg.PidFile
	if pidFile == "" {
		pidFile = "-"
	}
	l.Log(config.LogInfo, "PidFile %s", pidFile)
}

// ParseSize parses a size string like "1m" into bytes. Exposed for config if needed.
func ParseSize(s string) int64 {
	s = strings.TrimSpace(s)
	var n int64
	var unit string
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	fmt.Sscanf(s[:i], "%d", &n)
	if i < len(s) {
		unit = strings.ToLower(strings.TrimSpace(s[i:]))
	}
	switch unit {
	case "k":
		return n * 1024
	case "m":
		return n * 1024 * 1024
	case "g":
		return n * 1024 * 1024 * 1024
	}
	return n
}
