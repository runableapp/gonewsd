// Copyright © 2026 Runable.app. GPL-3.0.
//
// Package config parses and holds gonewsd server configuration. It reads a
// newsd-style config file (key-value pairs), applies defaults, and resolves
// paths and the run-as user (UID/GID). Supports listen, logging, spool, auth, and NNTP options.
package config

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Log level constants (match newsd)
const (
	LogError = iota
	LogInfo
	LogDebug
)

// Config holds all server configuration.
type Config struct {
	HostnameLookups int  // 0=off, 1=on, 2=double
	NoRecurseMsgDir bool // when listing groups, don't recurse into msg dirs
	MsgModDirs      bool // store articles in N/ subdirs (e.g. 1000/1234)

	ListenAddr string // "host:port" or ":port" for bind

	ErrorLog    string // path, "stderr", "syslog", or "|command"
	ErrorLogHex bool   // log non-ASCII as <0x##>

	LogLevel   int   // LogError, LogInfo, LogDebug
	MaxClients uint  // 0 = unlimited
	MaxLogSize int64 // bytes; 0 = no rotation

	ServerName  string
	SendMail    string
	SpamFilter  string
	SpoolDir    string
	PostCommand string
	Timeout     time.Duration

	User    string
	UID     uint32
	GID     uint32
	BadUser bool

	// Auth: SQLite-backed multi-user; see manuals/AUTH-DESIGN-AND-ANALYSIS.md
	AuthMode string // public, private, read_public_write_private
	AuthDB   string // path to SQLite auth database
	AuthLog  string // path for auth events (failures/success); empty = use ErrorLog
	PidFile  string // optional; server writes PID for CLI reload signal
}

// DefaultConfig returns config with newsd-compatible defaults.
func DefaultConfig() *Config {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "localhost"
	}
	return &Config{
		HostnameLookups: 0,
		NoRecurseMsgDir: true,
		MsgModDirs:      false,
		ListenAddr:      ":119",
		ErrorLog:        "stderr",
		ErrorLogHex:     true,
		LogLevel:        LogInfo,
		MaxClients:      0,
		MaxLogSize:      1024 * 1024, // 1m
		ServerName:      hostname,
		SendMail:        "/usr/sbin/sendmail -t",
		SpamFilter:      "",
		SpoolDir:        "/var/spool/gonewsd",
		PostCommand:     "-",
		Timeout:         12 * 3600 * time.Second,
		User:            "news",
		AuthMode:        "public",
		AuthDB:          "",
		AuthLog:         "",
		PidFile:         "",
	}
}

// Load reads configuration from a file and merges with defaults.
func (c *Config) Load(path string) error {
	def := DefaultConfig()
	*c = *def

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("unable to open configuration file %q: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	linenum := 0
	for scanner.Scan() {
		linenum++
		line := scanner.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var name, value string
		if idx := strings.IndexFunc(line, func(r rune) bool { return r == ' ' || r == '\t' }); idx > 0 {
			name = strings.TrimSpace(line[:idx])
			value = strings.TrimSpace(line[idx:])
		} else {
			continue
		}
		if value == "" {
			fmt.Fprintf(os.Stderr, "gonewsd: %s missing value on line %d of %q\n", name, linenum, path)
			continue
		}

		nameLower := strings.ToLower(name)
		switch nameLower {
		case "hostnamelookups":
			switch strings.ToLower(value) {
			case "off", "no":
				c.HostnameLookups = 0
			case "on", "yes":
				c.HostnameLookups = 1
			case "double":
				c.HostnameLookups = 2
			default:
				badValue(name, value, linenum, path)
			}
		case "norecursemsgdir":
			c.NoRecurseMsgDir = parseBool(value)
		case "msgmoddirs":
			c.MsgModDirs = parseBool(value)
		case "listen":
			c.ListenAddr = parseListen(value)
		case "logfile":
			fmt.Fprintf(os.Stderr, "gonewsd: %s on line %d of %q is deprecated; use ErrorLog instead\n", name, linenum, path)
			c.ErrorLog = value
		case "errorlog":
			c.ErrorLog = value
		case "errorlog.hex":
			c.ErrorLogHex = parseBool(value)
		case "loglevel":
			switch strings.ToLower(value) {
			case "error":
				c.LogLevel = LogError
			case "info":
				c.LogLevel = LogInfo
			case "debug":
				c.LogLevel = LogDebug
			default:
				badValue(name, value, linenum, path)
			}
		case "maxclients":
			n, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				badValue(name, value, linenum, path)
			} else {
				c.MaxClients = uint(n)
			}
		case "maxlogsize":
			c.MaxLogSize = parseSize(value)
		case "newshostname":
			fmt.Fprintf(os.Stderr, "gonewsd: %s on line %d of %q is deprecated; use ServerName instead\n", name, linenum, path)
			c.ServerName = value
		case "servername":
			c.ServerName = value
		case "sendmail":
			c.SendMail = value
		case "spamfilter":
			c.SpamFilter = value
		case "spooldir":
			c.SpoolDir = value
		case "postcommand":
			if value != "" {
				c.PostCommand = value
			} else {
				c.PostCommand = "-"
			}
		case "timeout":
			n, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				badValue(name, value, linenum, path)
			} else {
				c.Timeout = time.Duration(n) * time.Second
			}
		case "user":
			c.User = value
			c.lookupUser()
			if c.BadUser {
				badValue(name, value, linenum, path)
			}
		case "auth.mode":
			switch strings.ToLower(value) {
			case "public", "private", "read_public_write_private":
				c.AuthMode = value
			default:
				badValue(name, value, linenum, path)
			}
		case "auth.db":
			c.AuthDB = value
		case "auth.log":
			c.AuthLog = value
		case "pidfile":
			c.PidFile = value
		default:
			fmt.Fprintf(os.Stderr, "gonewsd: Unknown config directive %q on line %d of %q\n", name, linenum, path)
		}
	}

	// Resolve User if not yet done (e.g. User was set before Auth directives)
	if c.UID == 0 && c.GID == 0 && !c.BadUser {
		c.lookupUser()
	}

	// Resolve relative paths to absolute so they still work after runAs() does Chdir("/").
	for _, pathField := range []*string{&c.AuthDB, &c.AuthLog, &c.PidFile, &c.SpoolDir} {
		if *pathField != "" && !filepath.IsAbs(*pathField) {
			if abs, err := filepath.Abs(*pathField); err == nil {
				*pathField = abs
			}
		}
	}

	return scanner.Err()
}

// badValue prints a warning to stderr for an invalid config value.
func badValue(name, value string, line int, path string) {
	fmt.Fprintf(os.Stderr, "gonewsd: Bad %s value %q on line %d of %q\n", name, value, line, path)
}

// parseBool returns true for "on", "yes", or "1" (case-insensitive).
func parseBool(s string) bool {
	s = strings.ToLower(s)
	return s == "on" || s == "yes" || s == "1"
}

// parseSize parses a size string with optional k/m/g suffix into bytes.
func parseSize(s string) int64 {
	s = strings.TrimSpace(s)
	var n int64
	var unit string
	fmt.Sscanf(s, "%d%s", &n, &unit)
	unit = strings.ToLower(unit)
	switch unit {
	case "k":
		return n * 1024
	case "m":
		return n * 1024 * 1024
	case "g":
		return n * 1024 * 1024 * 1024
	case "":
		return n
	default:
		return n
	}
}

// parseListen parses a listen address (host:port, port number, or service name) and returns "host:port".
func parseListen(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ":119"
	}
	// Check for host:port
	if host, port, err := net.SplitHostPort(s); err == nil {
		return net.JoinHostPort(host, port)
	}
	// Single number = port only
	if _, err := strconv.Atoi(s); err == nil {
		return ":" + s
	}
	// Try as service name (e.g. "nntp")
	if port, err := net.LookupPort("tcp", s); err == nil {
		return fmt.Sprintf(":%d", port)
	}
	return ":119"
}

// lookupUser resolves Config.User to UID/GID via system user lookup; sets BadUser on failure.
func (c *Config) lookupUser() {
	u, err := user.Lookup(c.User)
	if err != nil {
		c.BadUser = true
		c.UID = 0
		c.GID = 0
		return
	}
	uid, _ := strconv.ParseUint(u.Uid, 10, 32)
	gid, _ := strconv.ParseUint(u.Gid, 10, 32)
	c.UID = uint32(uid)
	c.GID = uint32(gid)
	c.BadUser = false
}
