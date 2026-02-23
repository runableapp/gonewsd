// Copyright © 2026 Runable.app. GPL-3.0.
//
// Package auth (store.go) holds the in-memory auth state used by the NNTP
// server. Store holds users, groups, and memberships; it is populated from
// the DB at startup and on SIGHUP. It provides ValidateUser, CanRead, CanPost,
// and FilterGroupsForLIST based on session and ACLs.
package auth

import (
	"strings"
	"sync"
)

// AuthMode is the global default when a group has no ACL.
const (
	ModePublic                 = "public"
	ModePrivate                = "private"
	ModeReadPublicWritePrivate = "read_public_write_private"
)

// Perm values for group_perm / world_perm.
const (
	PermR  = "r"
	PermW  = "w"
	PermRW = "rw"
)

// Session holds per-connection auth state after AUTHINFO success.
// Nil or Username=="" means not authenticated.
type Session struct {
	Username string   // email
	RealName string   // display name (optional)
	Groups   []string // resolved group list (from memberships)
}

// User is an in-memory user record (from DB).
type User struct {
	UserID       int64
	Username     string
	PasswordHash string
	RealName     string
}

// Group is an in-memory group record (from DB).
type Group struct {
	GroupID     int64
	Groupname   string
	GroupPerm   string // r, w, rw
	WorldPerm   string
	Archived    bool
	Description string
	Creator     string
	PostLimit   int
	CCPost      string
	ReplyTo     string
	VoidEmail   string
}

// Store holds in-memory users, groups, and memberships; read-only for server.
type Store struct {
	mu         sync.RWMutex
	users      map[string]*User           // key: username (email)
	groups     map[string]*Group          // key: groupname
	membership map[string]map[string]bool // username -> groupname -> true
	mode       string                     // public, private, read_public_write_private
}

// NewStore creates an empty store with the given global mode.
func NewStore(mode string) *Store {
	if mode == "" {
		mode = ModePublic
	}
	return &Store{
		users:      make(map[string]*User),
		groups:     make(map[string]*Group),
		membership: make(map[string]map[string]bool),
		mode:       mode,
	}
}

// SetMode sets the global auth mode.
func (st *Store) SetMode(mode string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if mode != "" {
		st.mode = mode
	}
}

// LoadUser adds or replaces a user in memory (call after loading from DB).
func (st *Store) LoadUser(u *User) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.users[u.Username] = u
}

// LoadGroup adds or replaces a group in memory.
func (st *Store) LoadGroup(g *Group) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.groups[g.Groupname] = g
}

// LoadMembership adds user->group membership in memory.
func (st *Store) LoadMembership(username, groupname string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if username == "" || groupname == "" {
		return
	}
	m := st.membership[username]
	if m == nil {
		m = make(map[string]bool)
		st.membership[username] = m
	}
	m[groupname] = true
}

// Clear removes all users, groups, and memberships (before full reload).
func (st *Store) Clear() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.users = make(map[string]*User)
	st.groups = make(map[string]*Group)
	st.membership = make(map[string]map[string]bool)
}

// ValidateUser checks credentials and returns true if valid.
func (st *Store) ValidateUser(username, password string) bool {
	st.mu.RLock()
	u, exists := st.users[username]
	st.mu.RUnlock()
	if !exists || u == nil {
		return false
	}
	return ComparePassword(u.PasswordHash, password)
}

// ResolveSessionGroups returns this user's group memberships.
func (st *Store) ResolveSessionGroups(username string) []string {
	st.mu.RLock()
	m := st.membership[username]
	st.mu.RUnlock()
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for g := range m {
		out = append(out, g)
	}
	return out
}

// GetUserRealName returns the real name for a user, or "" if not found.
func (st *Store) GetUserRealName(username string) string {
	st.mu.RLock()
	u, exists := st.users[username]
	st.mu.RUnlock()
	if !exists || u == nil {
		return ""
	}
	return u.RealName
}

// UserInGroup returns true if session has this group.
func UserInGroup(session *Session, groupName string) bool {
	if session == nil || session.Username == "" {
		return false
	}
	for _, g := range session.Groups {
		if g == groupName {
			return true
		}
	}
	return false
}

// permHas returns true if the permission string (r, w, or rw) contains the required flag (r or w).
func permHas(perm, flag string) bool {
	return strings.Contains(perm, flag)
}

// CanRead returns true if the session can read the given group.
// groupNamesFromSpool is used to apply global default for groups with no ACL.
func (st *Store) CanRead(session *Session, groupName string, groupExistsInSpool bool) bool {
	st.mu.RLock()
	g, hasGroup := st.groups[groupName]
	mode := st.mode
	st.mu.RUnlock()

	// Normalized schema: groups must exist in DB (no spool-only groups).
	if !hasGroup {
		return false
	}
	if hasGroup && g.Archived {
		return false
	}
	if !groupExistsInSpool {
		return false
	}

	if hasGroup {
		inGroup := UserInGroup(session, groupName)
		var perm string
		if inGroup {
			perm = g.GroupPerm
		} else {
			perm = g.WorldPerm
		}
		return permHas(perm, PermR)
	}

	// No ACL: use global default
	switch mode {
	case ModePublic:
		return true
	case ModePrivate:
		return session != nil && session.Username != "" && UserInGroup(session, groupName)
	case ModeReadPublicWritePrivate:
		return true
	default:
		return true
	}
}

// CanPost returns true if the session can post to the given group.
func (st *Store) CanPost(session *Session, groupName string, groupExistsInSpool bool) bool {
	st.mu.RLock()
	g, hasGroup := st.groups[groupName]
	mode := st.mode
	st.mu.RUnlock()

	// Normalized schema: groups must exist in DB (no spool-only groups).
	if !hasGroup {
		return false
	}
	if hasGroup && g.Archived {
		return false
	}
	if !groupExistsInSpool {
		return false
	}

	if hasGroup {
		inGroup := UserInGroup(session, groupName)
		var perm string
		if inGroup {
			perm = g.GroupPerm
		} else {
			perm = g.WorldPerm
		}
		return permHas(perm, PermW)
	}

	switch mode {
	case ModePublic:
		return true
	case ModePrivate:
		return session != nil && session.Username != "" && UserInGroup(session, groupName)
	case ModeReadPublicWritePrivate:
		return session != nil && session.Username != "" && UserInGroup(session, groupName)
	default:
		return true
	}
}

// IsArchived returns true if the group has an ACL and is archived.
func (st *Store) IsArchived(groupName string) bool {
	st.mu.RLock()
	g, hasGroup := st.groups[groupName]
	st.mu.RUnlock()
	return hasGroup && g.Archived
}

// FilterGroupsForLIST returns a subset of names: groups that exist in spool and are not archived and session can read.
func (st *Store) FilterGroupsForLIST(session *Session, spoolGroupNames []string) []string {
	var out []string
	for _, name := range spoolGroupNames {
		if st.IsArchived(name) {
			continue
		}
		if st.CanRead(session, name, true) {
			out = append(out, name)
		}
	}
	return out
}
