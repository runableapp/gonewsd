// Copyright © 2026 Runable.app. GPL-3.0.
//
// Package auth (db.go) implements the SQLite-backed auth database. It defines
// the schema (users, groups, group_membership), opens the DB (read-only for
// server, read-write for CLI), loads data into the in-memory Store, and
// provides CRUD for users and groups (adduser, deleteuser, addgroup, etc.).
package auth

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const schemaV1 = `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
	user_id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS groups (
	group_id INTEGER PRIMARY KEY AUTOINCREMENT,
	groupname TEXT NOT NULL UNIQUE,
	group_perm TEXT NOT NULL,
	world_perm TEXT NOT NULL,
	archived INTEGER NOT NULL DEFAULT 0,
	description TEXT NOT NULL DEFAULT '',
	creator TEXT NOT NULL DEFAULT '-',
	postlimit INTEGER NOT NULL DEFAULT 1000,
	ccpost TEXT NOT NULL DEFAULT '-',
	replyto TEXT NOT NULL DEFAULT '-',
	voidemail TEXT NOT NULL DEFAULT 'root'
);

CREATE TABLE IF NOT EXISTS group_membership (
	user_id INTEGER NOT NULL,
	group_id INTEGER NOT NULL,
	PRIMARY KEY (user_id, group_id),
	FOREIGN KEY(user_id) REFERENCES users(user_id) ON DELETE CASCADE,
	FOREIGN KEY(group_id) REFERENCES groups(group_id) ON DELETE CASCADE
);
`

// migrateGroupColumns adds new columns to groups table if they don't exist (for existing databases).
func migrateGroupColumns(db *sql.DB) error {
	// Check if description column exists
	rows, err := db.Query("PRAGMA table_info(groups)")
	if err != nil {
		return err
	}
	defer rows.Close()

	hasDescription := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "description" {
			hasDescription = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if !hasDescription {
		// Add the new columns
		alterStmts := []string{
			"ALTER TABLE groups ADD COLUMN description TEXT NOT NULL DEFAULT ''",
			"ALTER TABLE groups ADD COLUMN creator TEXT NOT NULL DEFAULT '-'",
			"ALTER TABLE groups ADD COLUMN postlimit INTEGER NOT NULL DEFAULT 1000",
			"ALTER TABLE groups ADD COLUMN ccpost TEXT NOT NULL DEFAULT '-'",
			"ALTER TABLE groups ADD COLUMN replyto TEXT NOT NULL DEFAULT '-'",
			"ALTER TABLE groups ADD COLUMN voidemail TEXT NOT NULL DEFAULT 'root'",
		}
		for _, stmt := range alterStmts {
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("migrate groups: %s: %w", stmt, err)
			}
		}
	}
	return nil
}

// OpenDB opens the SQLite database at path (create if not exists). For server: open read-only and load into store. For CLI: open read-write.
func OpenDB(path string, readOnly bool) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("auth db path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir auth db dir: %w", err)
	}
	dsn := path
	if readOnly {
		dsn = path + "?mode=ro"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// EnsureSchema creates tables if they do not exist. Call with a read-write connection (CLI or init).
func EnsureSchema(db *sql.DB) error {
	// We do not migrate legacy schemas. If a legacy auth.db is detected, require recreating it.
	if legacy, err := hasLegacySchema(db); err != nil {
		return err
	} else if legacy {
		return fmt.Errorf("legacy auth.db schema detected (old users/groups tables). Please delete auth.db and re-run to create a fresh database")
	}
	if _, err := db.Exec(schemaV1); err != nil {
		return err
	}
	// Migrate existing databases to add new group columns
	return migrateGroupColumns(db)
}

// LoadStoreFromDB loads all users and group_acls from db into store. Call after OpenDB (read-only or read-write).
func LoadStoreFromDB(db *sql.DB, store *Store) error {
	store.Clear()

	// Load users
	rows, err := db.Query("SELECT user_id, username, password_hash FROM users")
	if err != nil {
		return fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.UserID, &u.Username, &u.PasswordHash); err != nil {
			return err
		}
		store.LoadUser(&u)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Load groups (ACLs are embedded in groups table)
	rows2, err := db.Query("SELECT group_id, groupname, group_perm, world_perm, archived FROM groups")
	if err != nil {
		return fmt.Errorf("query groups: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var g Group
		var archived int
		if err := rows2.Scan(&g.GroupID, &g.Groupname, &g.GroupPerm, &g.WorldPerm, &archived); err != nil {
			return err
		}
		g.Archived = archived != 0
		store.LoadGroup(&g)
	}
	if err := rows2.Err(); err != nil {
		return err
	}

	// Load memberships
	rows3, err := db.Query(`
		SELECT u.username, g.groupname
		FROM group_membership m
		JOIN users u ON u.user_id = m.user_id
		JOIN groups g ON g.group_id = m.group_id`)
	if err != nil {
		return fmt.Errorf("query group_membership: %w", err)
	}
	defer rows3.Close()
	for rows3.Next() {
		var username, groupname string
		if err := rows3.Scan(&username, &groupname); err != nil {
			return err
		}
		store.LoadMembership(username, groupname)
	}
	return rows3.Err()
}

// AddUser inserts a user (CLI adduser). EnsureSchema must have been called.
func AddUser(db *sql.DB, username, passwordHash, groups string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec("INSERT INTO users (username, password_hash) VALUES (?, ?)", username, passwordHash)
	if err != nil {
		return err
	}
	uid, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if err := setMembershipTx(tx, int64(uid), groups); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteUser deletes a user by username (CLI deleteuser).
func DeleteUser(db *sql.DB, username string) error {
	res, err := db.Exec("DELETE FROM users WHERE username = ?", username)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user not found: %s", username)
	}
	return nil
}

// UpdateUser updates a user's password and/or group memberships (CLI updateuser).
// Empty newPasswordHash or newGroups means leave unchanged.
func UpdateUser(db *sql.DB, username, newPasswordHash, newGroups string) error {
	var userID int64
	var curHash string
	err := db.QueryRow("SELECT user_id, password_hash FROM users WHERE username = ?", username).Scan(&userID, &curHash)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("user not found: %s", username)
		}
		return err
	}
	if newPasswordHash == "" {
		newPasswordHash = curHash
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE users SET password_hash = ? WHERE user_id = ?", newPasswordHash, userID); err != nil {
		return err
	}
	if newGroups != "" {
		if _, err := tx.Exec("DELETE FROM group_membership WHERE user_id = ?", userID); err != nil {
			return err
		}
		if err := setMembershipTx(tx, userID, newGroups); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UserExists returns true if the username exists in the users table.
func UserExists(db *sql.DB, username string) (bool, error) {
	var n int
	err := db.QueryRow("SELECT 1 FROM users WHERE username = ? LIMIT 1", username).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// GetUserGroups returns the comma-separated groups for a user, or empty and nil if not found.
func GetUserGroups(db *sql.DB, username string) (string, error) {
	rows, err := db.Query(`
		SELECT g.groupname
		FROM group_membership m
		JOIN users u ON u.user_id = m.user_id
		JOIN groups g ON g.group_id = m.group_id
		WHERE u.username = ?
		ORDER BY g.groupname`, username)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return "", err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	// If user doesn't exist, return empty, nil.
	var exists int
	if err := db.QueryRow("SELECT 1 FROM users WHERE username = ? LIMIT 1", username).Scan(&exists); err == sql.ErrNoRows {
		return "", nil
	} else if err != nil {
		return "", err
	}
	return strings.Join(out, ","), nil
}

// ListUserGroups returns the groups list for a user.
func ListUserGroups(db *sql.DB, username string) ([]string, error) {
	rows, err := db.Query(`
		SELECT g.groupname
		FROM group_membership m
		JOIN users u ON u.user_id = m.user_id
		JOIN groups g ON g.group_id = m.group_id
		WHERE u.username = ?
		ORDER BY g.groupname`, username)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListUsers returns all usernames and groups (no password hashes). For CLI listuser.
func ListUsers(db *sql.DB) ([]struct{ Username, Groups string }, error) {
	rows, err := db.Query("SELECT username FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct{ Username, Groups string }
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		groups, err := GetUserGroups(db, u)
		if err != nil {
			return nil, err
		}
		out = append(out, struct{ Username, Groups string }{u, groups})
	}
	return out, rows.Err()
}

// GroupInfo holds group metadata for UpsertGroup.
type GroupInfo struct {
	GroupPerm   string
	WorldPerm   string
	Archived    bool
	Description string
	Creator     string
	PostLimit   int
	CCPost      string
	ReplyTo     string
	VoidEmail   string
}

// UpsertGroup inserts or updates a group and its permissions (CLI addgroup/updategroup).
func UpsertGroup(db *sql.DB, groupname string, info GroupInfo) error {
	arch := 0
	if info.Archived {
		arch = 1
	}
	_, err := db.Exec(`INSERT INTO groups (groupname, group_perm, world_perm, archived, description, creator, postlimit, ccpost, replyto, voidemail) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(groupname) DO UPDATE SET 
			group_perm=excluded.group_perm, 
			world_perm=excluded.world_perm, 
			archived=excluded.archived,
			description=excluded.description,
			creator=excluded.creator,
			postlimit=excluded.postlimit,
			ccpost=excluded.ccpost,
			replyto=excluded.replyto,
			voidemail=excluded.voidemail`,
		groupname, info.GroupPerm, info.WorldPerm, arch, info.Description, info.Creator, info.PostLimit, info.CCPost, info.ReplyTo, info.VoidEmail)
	return err
}

// DeleteGroup removes a group from the groups table; membership rows cascade.
func DeleteGroup(db *sql.DB, groupname string) error {
	_, err := db.Exec("DELETE FROM groups WHERE groupname = ?", groupname)
	return err
}

// SetGroupArchived sets archived flag for a group (CLI deletegroup → archive).
func SetGroupArchived(db *sql.DB, groupname string, archived bool) error {
	arch := 0
	if archived {
		arch = 1
	}
	res, err := db.Exec("UPDATE groups SET archived = ? WHERE groupname = ?", arch, groupname)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("group not found: %s", groupname)
	}
	return nil
}

// ListGroups returns all groups (for CLI listgroup).
func ListGroups(db *sql.DB) ([]Group, error) {
	rows, err := db.Query("SELECT group_id, groupname, group_perm, world_perm, archived, description, creator, postlimit, ccpost, replyto, voidemail FROM groups ORDER BY groupname")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		var archived int
		if err := rows.Scan(&g.GroupID, &g.Groupname, &g.GroupPerm, &g.WorldPerm, &archived, &g.Description, &g.Creator, &g.PostLimit, &g.CCPost, &g.ReplyTo, &g.VoidEmail); err != nil {
			return nil, err
		}
		g.Archived = archived != 0
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGroup returns a group, or nil if not found.
func GetGroup(db *sql.DB, groupname string) (*Group, error) {
	var g Group
	var archived int
	err := db.QueryRow("SELECT group_id, groupname, group_perm, world_perm, archived, description, creator, postlimit, ccpost, replyto, voidemail FROM groups WHERE groupname = ?", groupname).
		Scan(&g.GroupID, &g.Groupname, &g.GroupPerm, &g.WorldPerm, &archived, &g.Description, &g.Creator, &g.PostLimit, &g.CCPost, &g.ReplyTo, &g.VoidEmail)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	g.Archived = archived != 0
	return &g, nil
}

// hasLegacySchema returns true if the database has the old group_acls table (legacy schema).
func hasLegacySchema(db *sql.DB) (bool, error) {
	// Legacy had group_acls table and users.groups column.
	var n int
	if err := db.QueryRow("SELECT 1 FROM sqlite_master WHERE type='table' AND name='group_acls' LIMIT 1").Scan(&n); err == sql.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

// setMembershipTx inserts group_membership rows for the user; groups is comma-separated or "*" for all groups.
func setMembershipTx(tx *sql.Tx, userID int64, groups string) error {
	groups = NormalizeGroups(groups)
	if groups == "" {
		return nil
	}
	if groups == "*" {
		_, err := tx.Exec(`INSERT OR IGNORE INTO group_membership (user_id, group_id)
			SELECT ?, group_id FROM groups`, userID)
		return err
	}
	for _, name := range strings.Split(groups, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var gid int64
		if err := tx.QueryRow(`SELECT group_id FROM groups WHERE groupname = ?`, name).Scan(&gid); err == sql.ErrNoRows {
			return fmt.Errorf("no such group %q", name)
		} else if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO group_membership (user_id, group_id) VALUES (?, ?)`, userID, gid); err != nil {
			return err
		}
	}
	return nil
}
