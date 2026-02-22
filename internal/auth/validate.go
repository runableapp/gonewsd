// Copyright © 2026 Runable.app. GPL-3.0.
//
// Package auth (validate.go) validates usernames (email format), passwords
// (length, charset, no spaces), and group names (format and blocklist).
// It also normalizes comma-separated group lists and checks them against
// existing groups for CLI use.
package auth

import (
	"regexp"
	"strings"
	"unicode"
)

// Valid newsgroup name: one or more components (alphanumeric, dot, hyphen); e.g. test.group1, comp.os.linux.
var groupNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*$`)

// ValidGroupName returns true if name is a safe, valid newsgroup name for use in
// file paths and commands. It rejects empty, blocklisted, or badly formatted names,
// and names containing path traversal components (e.g. "..", "/", "\").
func ValidGroupName(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	if groupsBlocklist[name] {
		return false
	}
	if !groupNameRegex.MatchString(name) {
		return false
	}
	// Reject path traversal: after replacing "." with "/" (as Dirname does),
	// ensure no ".." component exists.
	for _, part := range strings.Split(name, ".") {
		if part == ".." || part == "" {
			return false
		}
	}
	return true
}

// Email regex (reasonable subset: local@domain.tld).
var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

// ValidateEmail returns true if s is a valid email format.
func ValidateEmail(s string) bool {
	return emailRegex.MatchString(strings.TrimSpace(s))
}

// Blocklist of values that look like config directives, not newsgroup names.
var groupsBlocklist = map[string]bool{
	"auth.db": true, "auth.log": true, "auth.mode": true,
	"pidfile": true, "test.conf": true, ".config": true,
}

// InvalidGroupNames returns the list of group names in the comma-separated groups string
// that are invalid (empty segment, blocklisted, bad format, or not in existingGroups).
// If groups is "*" or valid, returns nil. Used by CLI to tell user which names to fix.
func InvalidGroupNames(groups string, existingGroups []string) []string {
	s := strings.TrimSpace(groups)
	if s == "" {
		return nil // caller treats empty separately
	}
	if s == "*" {
		return nil
	}
	existingSet := make(map[string]bool)
	for _, g := range existingGroups {
		existingSet[g] = true
	}
	var invalid []string
	for _, part := range strings.Split(s, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			invalid = append(invalid, "(empty segment)")
			continue
		}
		if len(name) > 1 && name[0] == '*' {
			invalid = append(invalid, name)
			continue
		}
		if groupsBlocklist[name] {
			invalid = append(invalid, name)
			continue
		}
		if !groupNameRegex.MatchString(name) {
			invalid = append(invalid, name)
			continue
		}
		if len(existingSet) > 0 && !existingSet[name] {
			invalid = append(invalid, name)
		}
	}
	return invalid
}

// NormalizeGroups trims the groups string and each comma-separated segment, then rejoins with
// a single comma and no spaces. If "*" is mixed with group names, returns "*" only.
func NormalizeGroups(groups string) string {
	s := strings.TrimSpace(groups)
	if s == "" || s == "*" {
		return s
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		name := strings.TrimSpace(part)
		if name == "*" {
			return "*"
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return strings.Join(out, ",")
}

// ValidateGroups checks that groups is either "*" or a comma-separated list of valid newsgroup names.
// If existingGroups is non-nil, each group name must exist in that list (from spool or group_acls).
// Returns an error message or empty string if valid.
func ValidateGroups(groups string, existingGroups []string) string {
	s := strings.TrimSpace(groups)
	if s == "" {
		return "groups cannot be empty"
	}
	if s == "*" {
		return ""
	}
	existingSet := make(map[string]bool)
	for _, g := range existingGroups {
		existingSet[g] = true
	}
	for _, part := range strings.Split(s, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			return "groups: empty segment in list"
		}
		if len(name) > 1 && name[0] == '*' {
			return "groups: use * alone for all groups, not with other names"
		}
		if groupsBlocklist[name] {
			return "groups: " + name + " is not a newsgroup name (did you mean -groups '*' or a group like test.group1?)"
		}
		if !groupNameRegex.MatchString(name) {
			return "groups: invalid name " + name + " (use letters, digits, dots, hyphens; e.g. test.group1 or *)"
		}
		if len(existingSet) > 0 && !existingSet[name] {
			return "groups: no such group " + name + " (create it with addgroup first)"
		}
	}
	return ""
}

// ValidatePassword checks length (9-20) and charset: letters, digits, typable special; no space, no non-ASCII.
// Returns an error message or empty string if valid.
func ValidatePassword(p string) string {
	const minLen = 9
	const maxLen = 20
	if len(p) < minLen {
		return "password must be more than 8 characters"
	}
	if len(p) > maxLen {
		return "password must be at most 20 characters"
	}
	for _, r := range p {
		if r == ' ' {
			return "password must not contain spaces"
		}
		if r > unicode.MaxASCII {
			return "password must not contain non-ASCII characters"
		}
		if !isAllowedPasswordChar(r) {
			return "password contains disallowed character (use letters, digits, and typable special characters)"
		}
	}
	return ""
}

// isAllowedPasswordChar returns true if the rune is allowed in a password (letters, digits, typable specials).
func isAllowedPasswordChar(r rune) bool {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return true
	}
	// Typable special chars (common keyboard)
	switch r {
	case '!', '@', '#', '$', '%', '^', '&', '*', '(', ')', '-', '_', '=', '+',
		'[', ']', '{', '}', '\\', '|', ';', ':', '\'', '"', ',', '.', '<', '>', '/', '?', '`', '~':
		return true
	}
	return false
}
