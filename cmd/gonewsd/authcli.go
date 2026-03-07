// Copyright © 2026 Runable.app. GPL-3.0.
//
// authcli.go implements the gonewsd auth CLI subcommands: adduser, listuser,
// deleteuser, updateuser, addgroup, deletegroup, updategroup, listgroup. It
// prompts for email, password, and groups (interactive or via flags), validates
// input, and calls the auth package to update the SQLite DB. Used by main when
// the first non-flag argument is one of these subcommands.
package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"gonewsd/internal/auth"
	"gonewsd/internal/config"
	"gonewsd/internal/group"

	"github.com/chzyer/readline"
	"github.com/mattn/go-isatty"
	"github.com/olekukonko/tablewriter"
)

// flagSet returns a new FlagSet for a subcommand with usage line showing "gonewsd <name> <usage>".
func flagSet(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, "Usage: gonewsd %s\n", usage) }
	return fs
}

// interactive returns true if stdin is a TTY (user can be prompted).
func interactive() bool {
	return isatty.IsTerminal(os.Stdin.Fd())
}

// prompt asks for a value on stdin; returns trimmed line or empty on error/EOF.
func prompt(ask string) string {
	fmt.Fprint(os.Stderr, ask)
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		return strings.TrimSpace(sc.Text())
	}
	return ""
}

// promptIfMissing returns value if non-empty; otherwise if interactive, prompts and returns trimmed input; else "".
func promptIfMissing(value, ask string) string {
	if value != "" {
		return value
	}
	if !interactive() {
		return ""
	}
	return prompt(ask)
}

const passwordPromptHint = "password (9-20 chars, letters/digits/special, no spaces): "

const emptyHint = "cannot be empty. Press Ctrl-C to cancel, or enter a value."

const confirmYNHint = "⚠️ Please enter y or n (Ctrl-C to cancel)."

// promptConfirmYN prompts with promptText [y/N], in a loop. Returns true for y/Y (confirm),
// false for n/N or EOF (cancel). On any other input, prints confirmYNHint and re-prompts.
func promptConfirmYN(promptText string) bool {
	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Fprint(os.Stderr, promptText)
		if !sc.Scan() {
			fmt.Fprintln(os.Stderr, "Cancelled.")
			return false
		}
		s := strings.ToLower(strings.TrimSpace(sc.Text()))
		switch s {
		case "y":
			return true
		case "n":
			fmt.Fprintln(os.Stderr, "Cancelled.")
			return false
		case "":
			fmt.Fprintln(os.Stderr, confirmYNHint)
		default:
			fmt.Fprintln(os.Stderr, confirmYNHint)
		}
	}
}

// promptEmailIfMissing returns value if non-empty and valid email; otherwise if interactive, prompts
// in a loop until valid. Empty input re-prompts (Ctrl-C to cancel). Rejects invalid formats and echoes input.
func promptEmailIfMissing(value string) string {
	if value != "" && auth.ValidateEmail(value) {
		return value
	}
	if !interactive() {
		return value
	}
	for {
		value = prompt("username (email): ")
		if value == "" {
			fmt.Fprintln(os.Stderr, "⚠️ Username "+emptyHint)
			continue
		}
		if auth.ValidateEmail(value) {
			return value
		}
		fmt.Fprintln(os.Stderr, "⚠️ username must be a valid email address (e.g. user@example.com)")
	}
}

// promptPasswordIfMissing returns value if non-empty and valid password; otherwise if interactive, prompts
// with requirement hint and loops until valid. Empty input re-prompts (Ctrl-C to cancel).
func promptPasswordIfMissing(value string) string {
	if value != "" && auth.ValidatePassword(value) == "" {
		return value
	}
	if !interactive() {
		return value
	}
	for {
		value = prompt(passwordPromptHint)
		if value == "" {
			fmt.Fprintln(os.Stderr, "⚠️ Password "+emptyHint)
			continue
		}
		msg := auth.ValidatePassword(value)
		if msg == "" {
			return value
		}
		fmt.Fprintf(os.Stderr, "⚠️ %s\n", msg)
	}
}

const usernamePrompt = "username (email): "

// promptExistingUserEmail returns value if non-empty, valid email, and user exists in DB; otherwise
// if interactive, prompts in a loop until valid existing user. When user not found, re-prompts with
// previous input pre-filled so user can correct. Empty input re-prompts (Ctrl-C to cancel).
func promptExistingUserEmail(db *sql.DB, value string) string {
	exists, _ := auth.UserExists(db, value)
	if value != "" && auth.ValidateEmail(value) && exists {
		return value
	}
	if !interactive() {
		return value
	}
	rl, err := readline.NewEx(&readline.Config{
		Prompt:      usernamePrompt,
		Stdin:       os.Stdin,
		Stdout:      os.Stderr,
		Stderr:      os.Stderr,
		HistoryFile: "",
	})
	if err != nil {
		for {
			value = prompt(usernamePrompt)
			if value == "" {
				fmt.Fprintln(os.Stderr, "⚠️ Username "+emptyHint)
				continue
			}
			if !auth.ValidateEmail(value) {
				fmt.Fprintln(os.Stderr, "⚠️ username must be a valid email address (e.g. user@example.com)")
				continue
			}
			exists, _ := auth.UserExists(db, value)
			if !exists {
				fmt.Fprintf(os.Stderr, "⚠️ user not found: %s\n", value)
				continue
			}
			return value
		}
	}
	defer rl.Close()
	var prefill string
	for {
		var line string
		var readErr error
		if prefill != "" {
			line, readErr = rl.ReadlineWithDefault(prefill)
		} else {
			line, readErr = rl.Readline()
		}
		line = strings.TrimSpace(line)
		if readErr == io.EOF {
			return ""
		}
		if readErr != nil {
			return ""
		}
		if line == "" {
			fmt.Fprintln(os.Stderr, "⚠️ Username "+emptyHint)
			continue
		}
		if !auth.ValidateEmail(line) {
			fmt.Fprintln(os.Stderr, "⚠️ username must be a valid email address (e.g. user@example.com)")
			prefill = line
			continue
		}
		exists, _ := auth.UserExists(db, line)
		if !exists {
			fmt.Fprintf(os.Stderr, "⚠️ user not found: %s\n", line)
			prefill = line
			continue
		}
		return line
	}
}

const groupsPrompt = "groups (comma-separated or *): "

const groupNamePrompt = "newsgroup name: "

// promptGroupNameRequired returns value if non-empty and valid format; otherwise if interactive, prompts
// in a loop until valid non-empty input. Empty input re-prompts (use Ctrl-C to cancel). Validates format.
func promptGroupNameRequired(value, promptLabel string) string {
	validateFormat := func(s string) bool { return len(auth.InvalidGroupNames(s, nil)) == 0 }
	if value != "" && validateFormat(value) {
		return value
	}
	if !interactive() {
		return value
	}
	for {
		value = prompt(promptLabel)
		if value == "" {
			fmt.Fprintln(os.Stderr, "⚠️ Group name "+emptyHint)
			continue
		}
		if validateFormat(value) {
			return value
		}
		invalid := auth.InvalidGroupNames(value, nil)
		if len(invalid) > 0 {
			fmt.Fprintf(os.Stderr, "⚠️ invalid group name: %s (use letters, digits, dots, hyphens; e.g. test.group1)\n", invalid[0])
		}
	}
}

// groupExists returns true if the given group name exists in the auth DB.
func groupExists(db *sql.DB, name string) bool {
	for _, g := range existingGroupNames(db) {
		if g == name {
			return true
		}
	}
	return false
}

const groupNotFoundHint = "Group not found. Press Ctrl-C to cancel, or enter an existing group name."
const groupExistsHint = "Group already exists. Press Ctrl-C to cancel, or enter a new group name."

// promptExistingGroupName returns value if non-empty, valid format, and group exists; otherwise if
// interactive, prompts in a loop until valid existing group. When not found, re-prompts with pre-fill.
// promptExistingGroupName returns value if non-empty and group exists; otherwise prompts until valid existing group (interactive).
func promptExistingGroupName(_ *config.Config, db *sql.DB, value string) string {
	validNames := existingGroupNames(db)
	validateFormat := func(s string) bool { return len(auth.InvalidGroupNames(s, nil)) == 0 }
	exists := groupExists(db, value)
	if value != "" && validateFormat(value) && exists {
		return value
	}
	if !interactive() {
		return value
	}
	rl, err := readline.NewEx(&readline.Config{
		Prompt:      groupNamePrompt,
		Stdin:       os.Stdin,
		Stdout:      os.Stderr,
		Stderr:      os.Stderr,
		HistoryFile: "",
	})
	if err != nil {
		for {
			value = prompt(groupNamePrompt)
			if value == "" {
				fmt.Fprintln(os.Stderr, "⚠️ Group name "+emptyHint)
				continue
			}
			if len(auth.InvalidGroupNames(value, nil)) != 0 {
				fmt.Fprintln(os.Stderr, "⚠️ invalid group name (use letters, digits, dots, hyphens; e.g. test.group1)")
				continue
			}
			if !groupExists(db, value) {
				fmt.Fprintf(os.Stderr, "⚠️ group not found: %s\n", value)
				fmt.Fprintln(os.Stderr, "ℹ️ Existing groups: "+strings.Join(validNames, ", "))
				continue
			}
			return value
		}
	}
	defer rl.Close()
	var prefill string
	for {
		var line string
		var readErr error
		if prefill != "" {
			line, readErr = rl.ReadlineWithDefault(prefill)
		} else {
			line, readErr = rl.Readline()
		}
		line = strings.TrimSpace(line)
		if readErr == io.EOF {
			return ""
		}
		if readErr != nil {
			return ""
		}
		if line == "" {
			fmt.Fprintln(os.Stderr, "⚠️ Group name "+emptyHint)
			continue
		}
		if len(auth.InvalidGroupNames(line, nil)) != 0 {
			fmt.Fprintln(os.Stderr, "⚠️ invalid group name (use letters, digits, dots, hyphens; e.g. test.group1)")
			prefill = line
			continue
		}
		if !groupExists(db, line) {
			fmt.Fprintf(os.Stderr, "⚠️ group not found: %s\n", line)
			fmt.Fprintln(os.Stderr, "ℹ️ Existing groups: "+strings.Join(validNames, ", "))
			prefill = line
			continue
		}
		return line
	}
}

// promptNewGroupName returns value if non-empty and valid format (caller must check group does not exist); otherwise if
// interactive, prompts in a loop until valid new group name. When already exists, re-prompts with pre-fill.
// promptNewGroupName returns value if non-empty and valid format; otherwise prompts until valid new group name (interactive).
func promptNewGroupName(_ *config.Config, db *sql.DB, value string) string {
	validNames := existingGroupNames(db)
	validateFormat := func(s string) bool { return len(auth.InvalidGroupNames(s, nil)) == 0 }
	// If user supplied a name on the command line and it's valid format, return it (addgroup will reject if already exists).
	if value != "" && validateFormat(value) {
		return value
	}
	if !interactive() {
		return value
	}
	rl, err := readline.NewEx(&readline.Config{
		Prompt:      groupNamePrompt,
		Stdin:       os.Stdin,
		Stdout:      os.Stderr,
		Stderr:      os.Stderr,
		HistoryFile: "",
	})
	if err != nil {
		for {
			value = prompt(groupNamePrompt)
			if value == "" {
				fmt.Fprintln(os.Stderr, "⚠️ Group name "+emptyHint)
				continue
			}
			if len(auth.InvalidGroupNames(value, nil)) != 0 {
				fmt.Fprintln(os.Stderr, "⚠️ invalid group name (use letters, digits, dots, hyphens; e.g. test.group1)")
				continue
			}
			if groupExists(db, value) {
				fmt.Fprintf(os.Stderr, "⚠️ group already exists: %s\n", value)
				fmt.Fprintln(os.Stderr, "ℹ️ Existing groups: "+strings.Join(validNames, ", "))
				continue
			}
			return value
		}
	}
	defer rl.Close()
	var prefill string
	for {
		var line string
		var readErr error
		if prefill != "" {
			line, readErr = rl.ReadlineWithDefault(prefill)
		} else {
			line, readErr = rl.Readline()
		}
		line = strings.TrimSpace(line)
		if readErr == io.EOF {
			return ""
		}
		if readErr != nil {
			return ""
		}
		if line == "" {
			fmt.Fprintln(os.Stderr, "⚠️ Group name "+emptyHint)
			continue
		}
		if len(auth.InvalidGroupNames(line, nil)) != 0 {
			fmt.Fprintln(os.Stderr, "⚠️ invalid group name (use letters, digits, dots, hyphens; e.g. test.group1)")
			prefill = line
			continue
		}
		if groupExists(db, line) {
			fmt.Fprintf(os.Stderr, "⚠️ group already exists: %s\n", line)
			fmt.Fprintln(os.Stderr, "ℹ️ Existing groups: "+strings.Join(validNames, ", "))
			prefill = line
			continue
		}
		return line
	}
}

// promptGroupsIfMissing returns groups if non-empty and valid; otherwise if interactive, prints valid
// groups and "* is valid (all groups).", then prompts.
// promptGroupsIfMissing returns groups if non-empty and valid; otherwise if interactive, prompts until valid comma-separated or *.
func promptGroupsIfMissing(db *sql.DB, groups string) string {
	validNames := existingGroupNames(db)
	validate := func(s string) []string { return auth.InvalidGroupNames(s, validNames) }
	if groups != "" && len(validate(groups)) == 0 {
		return groups
	}
	if !interactive() {
		return groups
	}
	// Show valid groups and that * is allowed
	if len(validNames) == 0 {
		fmt.Fprintln(os.Stderr, "ℹ️ Valid groups: (none yet; create groups with: gonewsd addgroup -group <name>)")
	} else {
		fmt.Fprintf(os.Stderr, "ℹ️ Valid groups: %s\n", strings.Join(validNames, ", "))
	}
	fmt.Fprintln(os.Stderr, "ℹ️ You may enter \"*\" for all groups, or comma-separated group names from the list above.")
	fmt.Fprintln(os.Stderr, "ℹ️ Press Ctrl-C to cancel.")
	rl, err := readline.NewEx(&readline.Config{
		Prompt:      groupsPrompt,
		Stdin:       os.Stdin,
		Stdout:      os.Stderr,
		Stderr:      os.Stderr,
		HistoryFile: "", // disable history for this prompt
	})
	if err != nil {
		// Fallback to simple prompt without pre-fill (e.g. not a TTY or readline failed)
		for {
			groups = prompt(groupsPrompt)
			if groups == "" {
				fmt.Fprintln(os.Stderr, "⚠️ Groups "+emptyHint)
				continue
			}
			if len(validate(groups)) == 0 {
				return groups
			}
			fmt.Fprintf(os.Stderr, "⚠️ invalid group(s): %s\n", strings.Join(validate(groups), ", "))
			fmt.Fprintln(os.Stderr, "Please fix the invalid name(s) and re-enter.")
		}
	}
	defer rl.Close()
	var prefill string
	for {
		var line string
		var readErr error
		if prefill != "" {
			line, readErr = rl.ReadlineWithDefault(prefill)
		} else {
			line, readErr = rl.Readline()
		}
		line = strings.TrimSpace(line)
		if readErr == io.EOF {
			return ""
		}
		if readErr != nil {
			// e.g. ErrInterrupt (Ctrl-C); treat as cancel
			return ""
		}
		if line == "" {
			fmt.Fprintln(os.Stderr, "⚠️ Groups "+emptyHint)
			continue
		}
		invalid := validate(line)
		if len(invalid) == 0 {
			return line
		}
		fmt.Fprintf(os.Stderr, "⚠️ invalid group(s): %s\n", strings.Join(invalid, ", "))
		fmt.Fprintln(os.Stderr, "Please fix the invalid name(s) and re-enter.")
		prefill = line // next prompt shows this so user can edit
	}
}

// runAuthCLI dispatches to the appropriate auth subcommand (adduser, listuser, deleteuser, updateuser, addgroup, deletegroup, updategroup, listgroup).
func runAuthCLI(cfg *config.Config, subcommand string, args []string) {
	if cfg.AuthDB == "" {
		fmt.Fprintf(os.Stderr, "🛑 Error: auth.db not set in config\n")
		os.Exit(1)
	}
	db, err := auth.OpenDB(cfg.AuthDB, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: auth db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := auth.EnsureSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: auth schema: %v\n", err)
		os.Exit(1)
	}

	switch subcommand {
	case "adduser":
		runAddUser(cfg, db, args)
	case "listuser":
		runListUser(db, args)
	case "deleteuser":
		runDeleteUser(db, args)
	case "updateuser":
		runUpdateUser(cfg, db, args)
	case "addgroup":
		runAddGroup(cfg, db, args)
	case "deletegroup":
		runDeleteGroup(cfg, db, args)
	case "updategroup":
		runUpdateGroup(cfg, db, args)
	case "listgroup":
		runListGroup(cfg, db, args)
	default:
		fmt.Fprintf(os.Stderr, "🛑 Error: unknown auth subcommand %q\n", subcommand)
		os.Exit(1)
	}
	triggerReload(cfg)
	os.Exit(0)
}

// triggerReload sends SIGHUP to the gonewsd process if PidFile is set, so the server reloads the auth store.
func triggerReload(cfg *config.Config) {
	if cfg.PidFile == "" {
		return
	}
	b, err := os.ReadFile(cfg.PidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	proc.Signal(syscall.SIGHUP)
}

// existingGroupNames returns all group names from the auth DB (for validation and prompts).
func existingGroupNames(db *sql.DB) []string {
	// Normalized schema: groups live in DB (no spool-only groups).
	grps, err := auth.ListGroups(db)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(grps))
	for _, g := range grps {
		out = append(out, g.Groupname)
	}
	return out
}

// runAddUser adds a new user (email, password, realname, groups) to the auth DB; prompts if interactive.
func runAddUser(_ *config.Config, db *sql.DB, args []string) {
	fs := flagSet("adduser", "adduser -user <email> -pass <password> [-realname <name>] -groups <list|*>")
	user := fs.String("user", "", "username (email)")
	pass := fs.String("pass", "", "password")
	realname := fs.String("realname", "", "real name / display name (optional)")
	groups := fs.String("groups", "", "comma-separated groups or *")
	fs.Parse(args)
	*user = promptEmailIfMissing(*user)
	if *user == "" {
		fs.Usage()
		os.Exit(1)
	}
	*pass = promptPasswordIfMissing(*pass)
	if *pass == "" {
		fs.Usage()
		os.Exit(1)
	}
	if *realname == "" && interactive() {
		*realname = prompt("real name (optional, press Enter to skip): ")
	}
	*groups = promptGroupsIfMissing(db, *groups)
	if *groups == "" {
		fs.Usage()
		os.Exit(1)
	}
	if !auth.ValidateEmail(*user) {
		fmt.Fprintf(os.Stderr, "⚠️ username must be a valid email address\n")
		os.Exit(1)
	}
	if msg := auth.ValidatePassword(*pass); msg != "" {
		fmt.Fprintf(os.Stderr, "⚠️ %s\n", msg)
		os.Exit(1)
	}
	if msg := auth.ValidateGroups(*groups, existingGroupNames(db)); msg != "" {
		fmt.Fprintf(os.Stderr, "⚠️ %s\n", msg)
		os.Exit(1)
	}
	hash, err := auth.HashPassword(*pass)
	if err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: hash password: %v\n", err)
		os.Exit(1)
	}
	if err := auth.AddUser(db, *user, hash, *realname, *groups); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: adduser: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("🎉 User %s added.\n", *user)
}

// runListUser lists all users and their groups from the auth DB (table output).
func runListUser(db *sql.DB, args []string) {
	fs := flagSet("listuser", "listuser [-format pretty|json]")
	format := fs.String("format", "", "output format: pretty (ASCII table), json, or default (tab-separated)")
	fs.Parse(args)
	list, err := auth.ListUsers(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: listuser: %v\n", err)
		os.Exit(1)
	}
	switch *format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(list); err != nil {
			fmt.Fprintf(os.Stderr, "🛑 Error: listuser: %v\n", err)
			os.Exit(1)
		}
		return
	case "pretty":
		tbl := tablewriter.NewTable(os.Stdout)
		tbl.Header("username", "realname", "groups")
		data := make([][]any, 0, len(list))
		for _, u := range list {
			data = append(data, []any{u.Username, u.RealName, u.Groups})
		}
		tbl.Bulk(data)
		tbl.Render()
		return
	}
	fmt.Println("username\trealname\tgroups")
	for _, u := range list {
		fmt.Printf("%s\t%s\t%s\n", u.Username, u.RealName, u.Groups)
	}
}

const updateGroupsPrompt = "new groups (leave blank to keep current): "
const updatePasswordPrompt = "new password (leave blank to keep current): "

// promptPasswordOptional prompts for an optional new password; empty means keep current. When
// non-empty, validates and re-prompts with pre-fill on invalid. Used by updateuser.
// promptPasswordOptional prompts for an optional new password (for updateuser); returns empty if non-interactive or skip.
func promptPasswordOptional() string {
	if !interactive() {
		return ""
	}
	rl, err := readline.NewEx(&readline.Config{
		Prompt:      updatePasswordPrompt,
		Stdin:       os.Stdin,
		Stdout:      os.Stderr,
		Stderr:      os.Stderr,
		HistoryFile: "",
	})
	if err != nil {
		for {
			value := prompt(updatePasswordPrompt)
			if value == "" {
				return ""
			}
			if msg := auth.ValidatePassword(value); msg != "" {
				fmt.Fprintf(os.Stderr, "⚠️ %s\n", msg)
				continue
			}
			return value
		}
	}
	defer rl.Close()
	var prefill string
	for {
		var line string
		var readErr error
		if prefill != "" {
			line, readErr = rl.ReadlineWithDefault(prefill)
		} else {
			line, readErr = rl.Readline()
		}
		line = strings.TrimSpace(line)
		if readErr == io.EOF {
			return ""
		}
		if readErr != nil {
			return ""
		}
		if line == "" {
			return ""
		}
		if msg := auth.ValidatePassword(line); msg == "" {
			return line
		}
		fmt.Fprintf(os.Stderr, "⚠️ %s\n", auth.ValidatePassword(line))
		prefill = line
	}
}

// promptGroupsOptional prompts for groups; empty is allowed (return ""). When non-empty, validates.
// Used by updateuser.
// promptGroupsOptional prompts for optional groups (comma-separated or *); returns empty if non-interactive.
func promptGroupsOptional(_ *config.Config, db *sql.DB) string {
	validNames := existingGroupNames(db)
	validate := func(s string) []string { return auth.InvalidGroupNames(s, validNames) }
	if !interactive() {
		return ""
	}
	rl, err := readline.NewEx(&readline.Config{
		Prompt:      updateGroupsPrompt,
		Stdin:       os.Stdin,
		Stdout:      os.Stderr,
		Stderr:      os.Stderr,
		HistoryFile: "",
	})
	if err != nil {
		for {
			groups := prompt(updateGroupsPrompt)
			if groups == "" {
				return ""
			}
			if len(validate(groups)) == 0 {
				return groups
			}
			fmt.Fprintf(os.Stderr, "⚠️ invalid group(s): %s\n", strings.Join(validate(groups), ", "))
		}
	}
	defer rl.Close()
	var prefill string
	for {
		var line string
		var readErr error
		if prefill != "" {
			line, readErr = rl.ReadlineWithDefault(prefill)
		} else {
			line, readErr = rl.Readline()
		}
		line = strings.TrimSpace(line)
		if readErr == io.EOF {
			return ""
		}
		if readErr != nil {
			return ""
		}
		if line == "" {
			return ""
		}
		if len(validate(line)) == 0 {
			return line
		}
		fmt.Fprintf(os.Stderr, "⚠️ invalid group(s): %s\n", strings.Join(validate(line), ", "))
		prefill = line
	}
}

// runUpdateUser updates a user's password, realname, and/or group memberships in the auth DB; prompts if interactive.
func runUpdateUser(cfg *config.Config, db *sql.DB, args []string) {
	fs := flagSet("updateuser", "updateuser -user <email> [-pass <password>] [-realname <name>] [-groups <list|*>]")
	user := fs.String("user", "", "username (email) to update")
	pass := fs.String("pass", "", "new password (omit to leave unchanged)")
	realname := fs.String("realname", "", "new real name (omit to leave unchanged)")
	groups := fs.String("groups", "", "new groups: comma-separated or * (omit to leave unchanged)")
	fs.Parse(args)
	*user = promptExistingUserEmail(db, *user)
	if *user == "" {
		fs.Usage()
		os.Exit(1)
	}
	if *pass == "" && *realname == "" && *groups == "" {
		if interactive() {
			*pass = promptPasswordOptional()
			if *realname == "" {
				*realname = promptIfMissing("", "new real name (leave blank to keep current): ")
			}
			if *groups == "" {
				*groups = promptGroupsOptional(cfg, db)
			}
		}
		if *pass == "" && *realname == "" && *groups == "" {
			fmt.Fprintf(os.Stderr, "⚠️ specify -pass, -realname, and/or -groups to update\n")
			fs.Usage()
			os.Exit(1)
		}
	}
	if !auth.ValidateEmail(*user) {
		fmt.Fprintf(os.Stderr, "⚠️ username must be a valid email address\n")
		os.Exit(1)
	}
	var newHash, newGroups string
	if *pass != "" {
		if msg := auth.ValidatePassword(*pass); msg != "" {
			fmt.Fprintf(os.Stderr, "⚠️ %s\n", msg)
			os.Exit(1)
		}
		var err error
		newHash, err = auth.HashPassword(*pass)
		if err != nil {
			fmt.Fprintf(os.Stderr, "🛑 Error: hash password: %v\n", err)
			os.Exit(1)
		}
	}
	if *groups != "" {
		if msg := auth.ValidateGroups(*groups, existingGroupNames(db)); msg != "" {
			fmt.Fprintf(os.Stderr, "⚠️ %s\n", msg)
			os.Exit(1)
		}
		newGroups = *groups
	}
	if err := auth.UpdateUser(db, *user, newHash, *realname, newGroups); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: updateuser: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("🎉 User %s updated.\n", *user)
}

// runDeleteUser removes a user from the auth DB; prompts for confirmation unless -y.
func runDeleteUser(db *sql.DB, args []string) {
	fs := flagSet("deleteuser", "deleteuser -user <email> [-y]")
	user := fs.String("user", "", "username (email) to remove")
	yes := fs.Bool("y", false, "skip confirmation")
	fs.Parse(args)
	*user = promptExistingUserEmail(db, *user)
	if *user == "" {
		fs.Usage()
		os.Exit(1)
	}
	if !*yes {
		groups, _ := auth.GetUserGroups(db, *user)
		aclInfo := ""
		if groups != "" {
			aclInfo = " (groups: " + groups + ")"
		}
		if !promptConfirmYN(fmt.Sprintf("Remove user %s%s? [y/N] ", *user, aclInfo)) {
			os.Exit(1)
		}
	}
	if err := auth.DeleteUser(db, *user); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: deleteuser: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("🎉 User %s removed.\n", *user)
}

// groupDir returns the spool directory path for the given group name (dots to path separators).
func groupDir(cfg *config.Config, groupName string) string {
	pathGroup := strings.ReplaceAll(groupName, ".", string(filepath.Separator))
	return filepath.Join(cfg.SpoolDir, pathGroup)
}

// normalizePerm normalizes a permission string to "r", "w", or "rw" (invalid input returns "r").
func normalizePerm(p string) string {
	p = strings.TrimSpace(strings.ToLower(p))
	if p == "r" || p == "w" || p == "rw" {
		return p
	}
	return "rw"
}

const permPromptHint = "Enter r (read only), w (write only), or rw (read+write). Default: rw."

// promptPerm prompts for a permission (r, w, rw) with explanation; empty means default rw.
// Returns normalized value. Used by addgroup when -g/-o not supplied.
// promptPerm prompts for r/w/rw with default "rw"; returns default if non-interactive.
func promptPerm(promptLabel, explanation string) string {
	return promptPermWithDefault(promptLabel, explanation, "rw")
}

// promptPermWithDefault prompts for r/w/rw; empty input returns defaultVal. Used by updategroup to show current.
// promptPermWithDefault prompts for r/w/rw with the given default; returns default if non-interactive.
func promptPermWithDefault(promptLabel, explanation, defaultVal string) string {
	if !interactive() {
		return defaultVal
	}
	fmt.Fprintln(os.Stderr, "ℹ️ "+explanation)
	fmt.Fprintln(os.Stderr, permPromptHint)
	for {
		fmt.Fprintf(os.Stderr, "%s [r|w|rw] (current: %s): ", promptLabel, defaultVal)
		sc := bufio.NewScanner(os.Stdin)
		if !sc.Scan() {
			return defaultVal
		}
		s := strings.TrimSpace(strings.ToLower(sc.Text()))
		if s == "" {
			return defaultVal
		}
		if s == "r" || s == "w" || s == "rw" {
			return s
		}
		fmt.Fprintf(os.Stderr, "⚠️ Please enter r, w, or rw (Ctrl-C to cancel).\n")
	}
}

// promptArchived prompts for archived yes/no; empty input keeps current. Used by updategroup.
// promptArchived prompts for y/n to set archived; returns current if non-interactive.
func promptArchived(current bool) bool {
	if !interactive() {
		return current
	}
	currentStr := "no"
	if current {
		currentStr = "yes"
	}
	for {
		fmt.Fprintf(os.Stderr, "Archived? [y/N] (current: %s): ", currentStr)
		sc := bufio.NewScanner(os.Stdin)
		if !sc.Scan() {
			return current
		}
		s := strings.TrimSpace(strings.ToLower(sc.Text()))
		if s == "" {
			return current
		}
		if s == "y" || s == "yes" {
			return true
		}
		if s == "n" || s == "no" {
			return false
		}
		fmt.Fprintln(os.Stderr, "⚠️ Please enter y or n (Ctrl-C to cancel).")
	}
}

// aclWarn prints a warning when both group and world perm are rw (open ACL).
func aclWarn(gPerm, oPerm string) {
	if oPerm == "rw" && gPerm == "rw" {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "💬  Note: o=rw makes g=rw redundant (world is larger scope than group).")
	}
	if oPerm == "rw" && gPerm == "r" {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "💬  Note: g=r with o=rw is equivalent to g=rw o=rw (world already has write).")
	}
}

// promptString prompts for a string value with a default; returns default if non-interactive.
func promptString(label, explanation, defaultVal string) string {
	if !interactive() {
		return defaultVal
	}
	fmt.Fprintf(os.Stderr, "ℹ️ %s\n", explanation)
	promptText := fmt.Sprintf("%s [%s]: ", label, defaultVal)
	line := prompt(promptText)
	if line == "" {
		return defaultVal
	}
	return line
}

// promptInt prompts for an integer value with a default; returns default if non-interactive.
func promptInt(label, explanation string, defaultVal int) int {
	if !interactive() {
		return defaultVal
	}
	fmt.Fprintf(os.Stderr, "ℹ️ %s\n", explanation)
	promptText := fmt.Sprintf("%s [%d]: ", label, defaultVal)
	for {
		line := prompt(promptText)
		if line == "" {
			return defaultVal
		}
		val, err := strconv.Atoi(line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️ Invalid number: %s. Try again.\n", line)
			continue
		}
		return val
	}
}

// runAddGroup adds a newsgroup ACL to the auth DB (group name and r/w/rw permissions); prompts if interactive.
func runAddGroup(cfg *config.Config, db *sql.DB, args []string) {
	fs := flagSet("addgroup", "addgroup -group <name> [-g group_perm] [-o world_perm] [-desc description] [-creator name] [-postlimit n] [-ccpost email] [-replyto email] [-voidemail email]")
	groupName := fs.String("group", "", "newsgroup name")
	gPerm := fs.String("g", "", "group permission: r, w, rw (default rw)")
	oPerm := fs.String("o", "", "world permission: r, w, rw (default rw)")
	desc := fs.String("desc", "", "group description (default: group name)")
	creator := fs.String("creator", "", "creator name (default: -)")
	postLimit := fs.Int("postlimit", 0, "max articles per posting (default: 1000)")
	ccPost := fs.String("ccpost", "", "email to CC posts to (default: -)")
	replyTo := fs.String("replyto", "", "Reply-To header for posts (default: -)")
	voidEmail := fs.String("voidemail", "", "email for void/bounce (default: root)")
	fs.Parse(args)
	*groupName = promptNewGroupName(cfg, db, *groupName)
	if *groupName == "" {
		fs.Usage()
		os.Exit(1)
	}
	if groupExists(db, *groupName) {
		g, _ := auth.GetGroup(db, *groupName)
		archived := "no"
		if g != nil && g.Archived {
			archived = "yes"
		}
		if g != nil {
			fmt.Fprintf(os.Stderr, "⚠️ group already exists: %s (g=%s o=%s archived=%s). Use updategroup to change permissions.\n", *groupName, g.GroupPerm, g.WorldPerm, archived)
		} else {
			fmt.Fprintf(os.Stderr, "⚠️ group already exists: %s. Use updategroup to change permissions.\n", *groupName)
		}
		os.Exit(1)
	}

	// Prompt for permissions
	if *gPerm == "" {
		if interactive() {
			*gPerm = promptPerm("group permission", "Group permission: what authenticated users in this group can do (read articles, post, or both).")
		} else {
			*gPerm = "rw"
		}
	}
	if *oPerm == "" {
		if interactive() {
			fmt.Fprintln(os.Stderr, "")
			*oPerm = promptPerm("world permission", "World permission: what everyone else can do (including unauthenticated). r=read only, w=write only, rw=read+write.")
		} else {
			*oPerm = "rw"
		}
	}
	*gPerm = normalizePerm(*gPerm)
	*oPerm = normalizePerm(*oPerm)

	// Prompt for additional fields
	if *desc == "" {
		if interactive() {
			fmt.Fprintln(os.Stderr, "")
		}
		*desc = promptString("description", "Short description of the group.", *groupName)
	}
	if *creator == "" {
		if interactive() {
			fmt.Fprintln(os.Stderr, "")
		}
		*creator = promptString("creator", "Name of the group creator (or - for none).", "-")
	}
	if *postLimit == 0 {
		if interactive() {
			fmt.Fprintln(os.Stderr, "")
		}
		*postLimit = promptInt("postlimit", "Maximum articles per posting session (0 = unlimited).", 1000)
	}
	if *ccPost == "" {
		if interactive() {
			fmt.Fprintln(os.Stderr, "")
		}
		*ccPost = promptString("ccpost", "Email address to CC all posts to (or - for none).", "-")
	}
	if *replyTo == "" {
		if interactive() {
			fmt.Fprintln(os.Stderr, "")
		}
		*replyTo = promptString("replyto", "Reply-To header for posts (or - for none).", "-")
	}
	if *voidEmail == "" {
		if interactive() {
			fmt.Fprintln(os.Stderr, "")
		}
		*voidEmail = promptString("voidemail", "Email for void/bounce messages.", "root")
	}

	aclWarn(*gPerm, *oPerm)

	gr := group.Group{Name: *groupName, Desc: *desc, Creator: *creator, CCPost: *ccPost, ReplyTo: *replyTo, VoidEmail: *voidEmail, PostLimit: *postLimit}
	dir := gr.Dirname(cfg)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: mkdir %s: %v\n", dir, err)
		os.Exit(1)
	}
	// Chown directory (and parent hierarchy) to configured user when running as root
	if isRootUser() && !cfg.BadUser {
		// Walk up from group dir to spool dir, chowning each level
		for d := dir; d != cfg.SpoolDir && len(d) > len(cfg.SpoolDir); d = filepath.Dir(d) {
			if err := os.Chown(d, int(cfg.UID), int(cfg.GID)); err != nil {
				fmt.Fprintf(os.Stderr, "🛑 Error: chown %s: %v\n", d, err)
				os.Exit(1)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".config")); os.IsNotExist(err) {
		if err := gr.SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "🛑 Error: save .config: %v\n", err)
			os.Exit(1)
		}
	}
	if err := gr.BuildInfo(cfg, true); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: build .info: %v\n", err)
		os.Exit(1)
	}
	info := auth.GroupInfo{
		GroupPerm:   *gPerm,
		WorldPerm:   *oPerm,
		Archived:    false,
		Description: *desc,
		Creator:     *creator,
		PostLimit:   *postLimit,
		CCPost:      *ccPost,
		ReplyTo:     *replyTo,
		VoidEmail:   *voidEmail,
	}
	if err := auth.UpsertGroup(db, *groupName, info); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: addgroup acl: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("🎉 Group %s added (g=%s o=%s desc=%q).\n", *groupName, *gPerm, *oPerm, *desc)
}

// runDeleteGroup removes a group from the auth DB or marks it archived; prompts for confirmation unless -y.
func runDeleteGroup(cfg *config.Config, db *sql.DB, args []string) {
	fs := flagSet("deletegroup", "deletegroup -group <name> [-archive] [-y]")
	groupName := fs.String("group", "", "newsgroup name")
	archive := fs.Bool("archive", false, "archive instead of delete")
	yes := fs.Bool("y", false, "skip confirmation")
	fs.Parse(args)
	if *groupName != "" {
		if len(auth.InvalidGroupNames(*groupName, nil)) != 0 {
			fmt.Fprintf(os.Stderr, "⚠️ invalid group name: %s\n", *groupName)
			os.Exit(1)
		}
		if !groupExists(db, *groupName) {
			validNames := existingGroupNames(db)
			fmt.Fprintf(os.Stderr, "⚠️ no such group: %s\n", *groupName)
			if len(validNames) > 0 {
				fmt.Fprintf(os.Stderr, "ℹ️ Existing groups: %s\n", strings.Join(validNames, ", "))
			}
			os.Exit(1)
		}
	} else {
		*groupName = promptExistingGroupName(cfg, db, *groupName)
		if *groupName == "" {
			fs.Usage()
			os.Exit(1)
		}
	}
	if !*yes {
		if !promptConfirmYN(fmt.Sprintf("Delete group %s? [y/N] ", *groupName)) {
			os.Exit(1)
		}
	}
	if *archive {
		if err := auth.SetGroupArchived(db, *groupName, true); err != nil {
			fmt.Fprintf(os.Stderr, "🛑 Error: deletegroup archive: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🎉 Group %s archived.\n", *groupName)
	} else {
		if err := auth.DeleteGroup(db, *groupName); err != nil {
			fmt.Fprintf(os.Stderr, "🛑 Error: deletegroup: %v\n", err)
			os.Exit(1)
		}
		dir := groupDir(cfg, *groupName)
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "🛑 Error: remove spool dir %s: %v\n", dir, err)
		}
		fmt.Printf("🎉 Group %s deleted.\n", *groupName)
	}
}

// promptStringWithDefault prompts for a string value with current default shown; returns current if empty.
func promptStringWithDefault(label, explanation, currentVal string) string {
	if !interactive() {
		return currentVal
	}
	fmt.Fprintf(os.Stderr, "ℹ️ %s\n", explanation)
	promptText := fmt.Sprintf("%s [%s]: ", label, currentVal)
	line := prompt(promptText)
	if line == "" {
		return currentVal
	}
	return line
}

// promptIntWithDefault prompts for an integer value with current default shown; returns current if empty.
func promptIntWithDefault(label, explanation string, currentVal int) int {
	if !interactive() {
		return currentVal
	}
	fmt.Fprintf(os.Stderr, "ℹ️ %s\n", explanation)
	promptText := fmt.Sprintf("%s [%d]: ", label, currentVal)
	for {
		line := prompt(promptText)
		if line == "" {
			return currentVal
		}
		val, err := strconv.Atoi(line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️ Invalid number: %s. Try again.\n", line)
			continue
		}
		return val
	}
}

// runUpdateGroup updates a group's permissions and/or archived flag in the auth DB; prompts if interactive.
func runUpdateGroup(cfg *config.Config, db *sql.DB, args []string) {
	fs := flagSet("updategroup", "updategroup -group <name> [-g group_perm] [-o world_perm] [-archive] [-desc description] [-creator name] [-postlimit n] [-ccpost email] [-replyto email] [-voidemail email]")
	groupName := fs.String("group", "", "newsgroup name")
	gPerm := fs.String("g", "", "group permission: r, w, rw (empty = no change)")
	oPerm := fs.String("o", "", "world permission: r, w, rw (empty = no change)")
	archive := fs.Bool("archive", false, "set archived")
	desc := fs.String("desc", "", "group description (empty = no change)")
	creator := fs.String("creator", "", "creator name (empty = no change)")
	postLimit := fs.Int("postlimit", -1, "max articles per posting (negative = no change)")
	ccPost := fs.String("ccpost", "", "email to CC posts to (empty = no change)")
	replyTo := fs.String("replyto", "", "Reply-To header for posts (empty = no change)")
	voidEmail := fs.String("voidemail", "", "email for void/bounce (empty = no change)")
	fs.Parse(args)
	if *groupName != "" {
		if len(auth.InvalidGroupNames(*groupName, nil)) != 0 {
			fmt.Fprintf(os.Stderr, "⚠️ invalid group name: %s\n", *groupName)
			os.Exit(1)
		}
		if !groupExists(db, *groupName) {
			validNames := existingGroupNames(db)
			fmt.Fprintf(os.Stderr, "⚠️ no such group: %s\n", *groupName)
			if len(validNames) > 0 {
				fmt.Fprintf(os.Stderr, "ℹ️ Existing groups: %s\n", strings.Join(validNames, ", "))
			}
			os.Exit(1)
		}
	} else {
		*groupName = promptExistingGroupName(cfg, db, *groupName)
		if *groupName == "" {
			fs.Usage()
			os.Exit(1)
		}
	}
	existing, err := auth.GetGroup(db, *groupName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: updategroup: %v\n", err)
		os.Exit(1)
	}

	// Start with existing values as defaults
	info := auth.GroupInfo{
		GroupPerm:   "rw",
		WorldPerm:   "rw",
		Archived:    *archive,
		Description: *groupName,
		Creator:     "-",
		PostLimit:   1000,
		CCPost:      "-",
		ReplyTo:     "-",
		VoidEmail:   "root",
	}
	if existing != nil {
		info.GroupPerm = existing.GroupPerm
		info.WorldPerm = existing.WorldPerm
		info.Archived = existing.Archived || *archive
		info.Description = existing.Description
		info.Creator = existing.Creator
		info.PostLimit = existing.PostLimit
		info.CCPost = existing.CCPost
		info.ReplyTo = existing.ReplyTo
		info.VoidEmail = existing.VoidEmail
	}

	// Update from flags or prompts
	if *gPerm != "" {
		info.GroupPerm = normalizePerm(*gPerm)
	} else if interactive() {
		fmt.Fprintln(os.Stderr, "")
		info.GroupPerm = promptPermWithDefault("group permission", "Group permission: what authenticated users in this group can do (read articles, post, or both).", info.GroupPerm)
	}
	if *oPerm != "" {
		info.WorldPerm = normalizePerm(*oPerm)
	} else if interactive() {
		fmt.Fprintln(os.Stderr, "")
		info.WorldPerm = promptPermWithDefault("world permission", "World permission: what everyone else can do (including unauthenticated). r=read only, w=write only, rw=read+write.", info.WorldPerm)
	}
	if *desc != "" {
		info.Description = *desc
	} else if interactive() {
		fmt.Fprintln(os.Stderr, "")
		info.Description = promptStringWithDefault("description", "Short description of the group.", info.Description)
	}
	if *creator != "" {
		info.Creator = *creator
	} else if interactive() {
		fmt.Fprintln(os.Stderr, "")
		info.Creator = promptStringWithDefault("creator", "Name of the group creator (or - for none).", info.Creator)
	}
	if *postLimit >= 0 {
		info.PostLimit = *postLimit
	} else if interactive() {
		fmt.Fprintln(os.Stderr, "")
		info.PostLimit = promptIntWithDefault("postlimit", "Maximum articles per posting session (0 = unlimited).", info.PostLimit)
	}
	if *ccPost != "" {
		info.CCPost = *ccPost
	} else if interactive() {
		fmt.Fprintln(os.Stderr, "")
		info.CCPost = promptStringWithDefault("ccpost", "Email address to CC all posts to (or - for none).", info.CCPost)
	}
	if *replyTo != "" {
		info.ReplyTo = *replyTo
	} else if interactive() {
		fmt.Fprintln(os.Stderr, "")
		info.ReplyTo = promptStringWithDefault("replyto", "Reply-To header for posts (or - for none).", info.ReplyTo)
	}
	if *voidEmail != "" {
		info.VoidEmail = *voidEmail
	} else if interactive() {
		fmt.Fprintln(os.Stderr, "")
		info.VoidEmail = promptStringWithDefault("voidemail", "Email for void/bounce messages.", info.VoidEmail)
	}
	if !*archive && interactive() {
		fmt.Fprintln(os.Stderr, "")
		info.Archived = promptArchived(info.Archived)
	}

	aclWarn(info.GroupPerm, info.WorldPerm)
	if err := auth.UpsertGroup(db, *groupName, info); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: updategroup: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("🎉 Group %s updated (g=%s o=%s archived=%v).\n", *groupName, info.GroupPerm, info.WorldPerm, info.Archived)
}

// runListGroup lists all groups in the auth DB (pretty, json, or tsv format).
func runListGroup(_ *config.Config, db *sql.DB, args []string) {
	fs := flagSet("listgroup", "listgroup [-format pretty|json|tsv]")
	format := fs.String("format", "", "output format: default (vertical with ACL), pretty (ASCII table), json, tsv (tab-separated)")
	fs.Parse(args)
	dbGroups, err := auth.ListGroups(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: listgroup: %v\n", err)
		os.Exit(1)
	}
	switch *format {
	case "json":
		type row struct {
			Groupname string `json:"groupname"`
			GroupPerm string `json:"group_perm"`
			WorldPerm string `json:"world_perm"`
			Archived  bool   `json:"archived"`
		}
		out := make([]row, 0, len(dbGroups))
		for _, g := range dbGroups {
			out = append(out, row{g.Groupname, g.GroupPerm, g.WorldPerm, g.Archived})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(os.Stderr, "🛑 Error: listgroup: %v\n", err)
			os.Exit(1)
		}
		return
	case "pretty":
		tbl := tablewriter.NewTable(os.Stdout)
		tbl.Header("groupname", "group_perm", "world_perm", "archived")
		data := make([][]any, 0, len(dbGroups))
		for _, g := range dbGroups {
			arch := "no"
			if g.Archived {
				arch = "yes"
			}
			data = append(data, []any{g.Groupname, g.GroupPerm, g.WorldPerm, arch})
		}
		tbl.Bulk(data)
		tbl.Render()
		return
	case "tsv", "tab":
		fmt.Println("groupname\tgroup_perm\tworld_perm\tarchived")
		for _, g := range dbGroups {
			arch := "no"
			if g.Archived {
				arch = "yes"
			}
			fmt.Printf("%s\t%s\t%s\t%s\n", g.Groupname, g.GroupPerm, g.WorldPerm, arch)
		}
		return
	}
	// Default: vertical block per group with ACL
	for i, g := range dbGroups {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("group: %s\n", g.Groupname)
		fmt.Printf("  group_perm: %s\n", g.GroupPerm)
		fmt.Printf("  world_perm: %s\n", g.WorldPerm)
		fmt.Printf("  archived: %v\n", g.Archived)
	}
}
