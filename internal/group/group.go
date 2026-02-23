// Copyright © 2026 Runable.app. GPL-3.0.
//
// Package group manages newsgroups and their spool layout. It reads/writes
// .info and .config files, builds group lists from the spool, acquires
// read/write locks via .lock files, and handles posting (including spam
// filter, Xref, and postcommand). Also provides header parsing and Path
// updates for NNTP.
package group

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gonewsd/internal/article"
	"gonewsd/internal/config"
	"gonewsd/internal/logging"
)

const (
	lineLen  = 4096
	groupMax = 1024
)

// Group represents a newsgroup and its metadata.
type Group struct {
	Name      string
	Start     uint64
	End       uint64
	Total     uint64
	Desc      string
	Creator   string
	CCPost    string
	ReplyTo   string
	VoidEmail string
	PostLimit int
	Ctime     int64
	Valid     bool
	Errmsg    string
}

// LoadInfo reads the group's .info file (or builds it if missing).
func (g *Group) LoadInfo(cfg *config.Config, groupName string, doLock bool) error {
	g.Valid = false
	g.Name = groupName
	if len(groupName) >= groupMax {
		g.Errmsg = "group name too long"
		return fmt.Errorf("%s", g.Errmsg)
	}
	dir := g.Dirname(cfg)
	if _, err := os.Stat(dir); err != nil {
		g.Errmsg = fmt.Sprintf("invalid group name %q: %v", groupName, err)
		return fmt.Errorf("%s", g.Errmsg)
	}

	infoPath := filepath.Join(dir, ".info")
	f, err := os.Open(infoPath)
	if err != nil {
		if os.IsNotExist(err) {
			if !g.isValidGroup(cfg) {
				g.Errmsg = "invalid group"
				return fmt.Errorf("%s", g.Errmsg)
			}
			return g.BuildInfo(cfg, doLock)
		}
		g.Errmsg = fmt.Sprintf("%s: %v", infoPath, err)
		return err
	}
	defer f.Close()

	var unlock func()
	if doLock {
		unlock, err = g.readLock(cfg)
		if err != nil {
			return err
		}
		defer unlock()
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		var start, end, total uint64
		if _, err := fmt.Sscanf(line, "start %d", &start); err == nil {
			g.Start = start
			continue
		}
		if _, err := fmt.Sscanf(line, "end %d", &end); err == nil {
			g.End = end
			continue
		}
		if _, err := fmt.Sscanf(line, "total %d", &total); err == nil {
			g.Total = total
			continue
		}
	}

	g.Valid = true
	return scanner.Err()
}

// BuildInfo scans the group directory for articles and writes .info.
func (g *Group) BuildInfo(cfg *config.Config, doLock bool) error {
	var unlock func()
	if doLock {
		var err error
		unlock, err = g.writeLock(cfg)
		if err != nil {
			return err
		}
		defer unlock()
	}

	g.Start = 0
	g.End = 0
	g.Total = 0
	dir := g.Dirname(cfg)

	entries, err := os.ReadDir(dir)
	if err != nil {
		g.Errmsg = err.Error()
		return err
	}

	for _, e := range entries {
		name := e.Name()
		if name == "" || (name[0] < '0' || name[0] > '9') {
			continue
		}
		path := filepath.Join(dir, name)

		if cfg.MsgModDirs {
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				continue
			}
			subs, err := os.ReadDir(path)
			if err != nil {
				continue
			}
			for _, sub := range subs {
				subName := sub.Name()
				if subName == "" || (subName[0] < '0' || subName[0] > '9') {
					continue
				}
				artnum, err := strconv.ParseUint(subName, 10, 64)
				if err != nil {
					continue
				}
				subPath := filepath.Join(path, subName)
				subInfo, err := os.Stat(subPath)
				if err != nil || subInfo.IsDir() {
					continue
				}
				if g.Total == 0 || artnum < g.Start {
					g.Start = artnum
				}
				if g.Total == 0 || artnum > g.End {
					g.End = artnum
				}
				g.Total++
			}
			continue
		}

		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		artnum, err := strconv.ParseUint(name, 10, 64)
		if err != nil {
			continue
		}
		if g.Total == 0 || artnum < g.Start {
			g.Start = artnum
		}
		if g.Total == 0 || artnum > g.End {
			g.End = artnum
		}
		g.Total++
	}

	return g.SaveInfo(cfg, false)
}

// SaveInfo writes the .info file.
func (g *Group) SaveInfo(cfg *config.Config, doLock bool) error {
	var unlock func()
	if doLock {
		var err error
		unlock, err = g.writeLock(cfg)
		if err != nil {
			return err
		}
		defer unlock()
	}

	path := filepath.Join(g.Dirname(cfg), ".info")
	data := fmt.Sprintf("start       %d\nend         %d\ntotal       %d\n", g.Start, g.End, g.Total)
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		g.Errmsg = err.Error()
		return err
	}
	// Chown to configured user when running as root
	if os.Geteuid() == 0 && !cfg.BadUser {
		if err := os.Chown(path, int(cfg.UID), int(cfg.GID)); err != nil {
			g.Errmsg = err.Error()
			return fmt.Errorf("chown %q: %w", path, err)
		}
	}
	return nil
}

// isValidGroup returns true if the group directory contains a .config file.
func (g *Group) isValidGroup(cfg *config.Config) bool {
	path := filepath.Join(g.Dirname(cfg), ".config")
	_, err := os.Stat(path)
	return err == nil
}

// LoadConfig reads the group's .config file.
func (g *Group) LoadConfig(cfg *config.Config, doLock bool, log *logging.Logger) error {
	path := filepath.Join(g.Dirname(cfg), ".config")
	f, err := os.Open(path)
	if err != nil {
		g.Errmsg = fmt.Sprintf("%s: %v", path, err)
		return err
	}
	defer f.Close()

	var unlock func()
	if doLock {
		unlock, err = g.readLock(cfg)
		if err != nil {
			return err
		}
		defer unlock()
	}
	_ = unlock

	scanner := bufio.NewScanner(f)
	g.CCPost = ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		if strings.HasPrefix(line, "description ") {
			g.Desc = strings.TrimSpace(line[len("description "):])
			continue
		}
		var s string
		if _, err := fmt.Sscanf(line, "creator %s", &s); err == nil {
			g.Creator = s
			continue
		}
		if _, err := fmt.Sscanf(line, "postlimit %d", &g.PostLimit); err == nil {
			continue
		}
		if _, err := fmt.Sscanf(line, "ccpost %s", &s); err == nil {
			if g.CCPost != "" && g.CCPost != "-" && !strings.HasSuffix(g.CCPost, ",") {
				g.CCPost += ","
			}
			g.CCPost += s
			continue
		}
		if _, err := fmt.Sscanf(line, "replyto %s", &s); err == nil {
			g.ReplyTo = s
			continue
		}
		if _, err := fmt.Sscanf(line, "voidemail %s", &s); err == nil {
			g.VoidEmail = s
			continue
		}
	}
	if g.CCPost == "" {
		g.CCPost = "-"
	}
	if log != nil {
		log.Log(config.LogDebug, "ccpost is now '%s' %s", g.CCPost, g.Errmsg)
	}
	return scanner.Err()
}

// SaveConfig writes the .config file.
func (g *Group) SaveConfig(cfg *config.Config) error {
	unlock, err := g.writeLock(cfg)
	if err != nil {
		return err
	}
	defer unlock()

	path := filepath.Join(g.Dirname(cfg), ".config")
	data := fmt.Sprintf("description %s\ncreator     %s\npostlimit   %d\nccpost      %s\nreplyto     %s\nvoidemail   %s\n",
		g.Desc, g.Creator, g.PostLimit, g.CCPost, g.ReplyTo, g.VoidEmail)
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		return err
	}
	// Chown to configured user when running as root
	if os.Geteuid() == 0 && !cfg.BadUser {
		if err := os.Chown(path, int(cfg.UID), int(cfg.GID)); err != nil {
			return fmt.Errorf("chown %q: %w", path, err)
		}
	}
	return nil
}

// LoadInfoFull loads both .info and .config for a group by name.
func (g *Group) LoadInfoFull(cfg *config.Config, groupName string, log *logging.Logger) error {
	g.Valid = false
	g.Name = groupName
	dir := g.Dirname(cfg)
	info, err := os.Stat(dir)
	if err != nil {
		g.Errmsg = fmt.Sprintf("invalid group name %q: %v", groupName, err)
		return fmt.Errorf("%s", g.Errmsg)
	}
	g.Ctime = info.ModTime().Unix()

	if err := g.LoadInfo(cfg, groupName, true); err != nil {
		return err
	}
	if err := g.LoadConfig(cfg, true, log); err != nil {
		return err
	}
	g.Valid = true
	return nil
}

// GetHeaderValue returns the value of a header (case-insensitive).
func GetHeaderValue(headers []string, fieldName string) (string, bool) {
	fieldLower := strings.ToLower(strings.TrimSuffix(fieldName, ":") + ":")
	for _, h := range headers {
		if len(h) < len(fieldLower) {
			continue
		}
		if strings.ToLower(h[:len(fieldLower)]) == fieldLower {
			val := strings.TrimSpace(h[len(fieldLower):])
			return val, true
		}
	}
	return "", false
}

// GetHeaderIndex returns the index of a header (case-insensitive).
func GetHeaderIndex(headers []string, fieldName string) int {
	fieldLower := strings.ToLower(strings.TrimSuffix(fieldName, ":") + ":")
	for i, h := range headers {
		if len(h) >= len(fieldLower) && strings.ToLower(h[:len(fieldLower)]) == fieldLower {
			return i
		}
	}
	return -1
}

// UpdatePath adds or prepends the server hostname to the Path header (modifies headers in place).
func UpdatePath(cfg *config.Config, headers *[]string) {
	pathStr := "Path:"
	pathLen := len(pathStr)
	hostname := cfg.ServerName
	for i, h := range *headers {
		if len(h) >= pathLen && strings.EqualFold(h[:pathLen], pathStr) {
			rest := strings.TrimSpace(h[pathLen:])
			if rest != "" {
				(*headers)[i] = pathStr + " " + hostname + ", " + rest
			} else {
				(*headers)[i] = pathStr + " " + hostname
			}
			return
		}
	}
	*headers = append(*headers, pathStr+" "+hostname)
}

// DateRFC822 returns current date in RFC 822/2822 format.
func DateRFC822() string {
	t := time.Now()
	return t.Format(time.RFC1123Z)
}

// ParseArticle splits the message into headers and body (blank-line separated).
func ParseArticle(msg string) (headers, body []string) {
	var inHeader bool = true
	var line strings.Builder
	for i := 0; i < len(msg); i++ {
		c := msg[i]
		if c == '\r' {
			continue
		}
		if c == '\n' {
			s := line.String()
			line.Reset()
			if inHeader {
				if s == "" {
					inHeader = false
					continue
				}
				headers = append(headers, s)
			} else {
				body = append(body, s)
			}
			continue
		}
		line.WriteByte(c)
	}
	if line.Len() > 0 {
		if inHeader {
			headers = append(headers, line.String())
		} else {
			body = append(body, line.String())
		}
	}
	return headers, body
}

// GetMessageID reads the Message-ID header from an article file.
func (g *Group) GetMessageID(cfg *config.Config, artnum uint64) (string, error) {
	path := article.GetArticlePath(cfg, g.Name, artnum)
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		if len(line) >= 11 && strings.EqualFold(line[:11], "Message-ID:") {
			return strings.TrimSpace(line[11:]), nil
		}
	}
	return "", fmt.Errorf("no Message-ID")
}

// FindArticleByMessageID does a linear search from End to Start.
func (g *Group) FindArticleByMessageID(cfg *config.Config, msgid string) (uint64, error) {
	for artnum := g.End; artnum >= g.Start; artnum-- {
		id, err := g.GetMessageID(cfg, artnum)
		if err != nil {
			continue
		}
		if id == msgid {
			return artnum, nil
		}
	}
	g.Errmsg = "Message-ID not found: " + msgid
	return 0, fmt.Errorf("%s", g.Errmsg)
}

// Post writes an article to the group.
func (g *Group) Post(cfg *config.Config, overview []string, headers, body []string, remoteIP string, forcePost, preserveDate bool, log *logging.Logger) error {
	postgroup, ok := GetHeaderValue(headers, "Newsgroups:")
	if !ok {
		g.Errmsg = "article has no 'Newsgroups' field"
		return fmt.Errorf("%s", g.Errmsg)
	}
	g.Name = postgroup

	unlock, err := g.writeLock(cfg)
	if err != nil {
		return err
	}
	defer unlock()

	if err := g.LoadInfo(cfg, postgroup, false); err != nil {
		g.Errmsg = "no such group"
		return err
	}
	if err := g.LoadConfig(cfg, false, log); err != nil {
		g.Errmsg = "no such group"
		return err
	}
	g.Valid = true

	if cfg.SpamFilter != "" {
		cmd := exec.Command("sh", "-c", cfg.SpamFilter+` >/dev/null 2>/dev/null`)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			g.Errmsg = "spam filter command failed to execute"
			return fmt.Errorf("%s", g.Errmsg)
		}
		if err := cmd.Start(); err != nil {
			g.Errmsg = "spam filter command failed to execute"
			return err
		}
		for _, h := range headers {
			fmt.Fprintln(stdin, h)
		}
		fmt.Fprintln(stdin, "")
		for _, b := range body {
			fmt.Fprintln(stdin, b)
		}
		stdin.Close()
		if err := cmd.Wait(); err != nil {
			g.Errmsg = "spam filter rejected message"
			return fmt.Errorf("%s", g.Errmsg)
		}
	}

	var msgnum uint64
	for msgnum = g.End + 1; ; msgnum++ {
		path := article.GetArticlePath(cfg, postgroup, msgnum)
		if cfg.MsgModDirs {
			modDir := filepath.Dir(path)
			if _, err := os.Stat(modDir); os.IsNotExist(err) {
				if err := os.MkdirAll(modDir, 0777); err != nil {
					g.Errmsg = err.Error()
					return err
				}
			}
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			g.Errmsg = err.Error()
			return err
		}

		dateIdx := GetHeaderIndex(headers, "Date:")
		hostIdx := GetHeaderIndex(headers, "NNTP-Posting-Host:")
		dateFlag := dateIdx >= 0
		if hostIdx >= 0 {
			headers = append(headers[:hostIdx], headers[hostIdx+1:]...)
			if dateIdx >= 0 && hostIdx < dateIdx {
				dateIdx--
			}
		}
		if dateIdx >= 0 && !preserveDate {
			headers = append(headers[:dateIdx], headers[dateIdx+1:]...)
		}

		headers = append(headers, fmt.Sprintf("Xref: %s %s:%d", cfg.ServerName, postgroup, msgnum))
		if !preserveDate || !dateFlag {
			headers = append(headers, "Date: "+DateRFC822())
		}
		headers = append(headers, "NNTP-Posting-Host: "+remoteIP)

		if _, ok := GetHeaderValue(headers, "Message-ID:"); !ok {
			headers = append(headers, fmt.Sprintf("Message-ID: <%d-%s@%s>", msgnum, postgroup, cfg.ServerName))
		}
		if _, ok := GetHeaderValue(headers, "Lines:"); !ok {
			headers = append(headers, fmt.Sprintf("Lines: %d", len(body)))
		}

		for _, h := range headers {
			fmt.Fprintln(f, h)
		}
		fmt.Fprintln(f, "")
		for _, b := range body {
			fmt.Fprintln(f, b)
		}
		f.Sync()
		f.Close()

		if g.Total == 0 && g.Start == 0 {
			g.Start = 1
		}
		g.End = msgnum
		g.Total++

		if err := g.SaveInfo(cfg, false); err != nil {
			return err
		}

		if cfg.PostCommand != "" && cfg.PostCommand != "-" {
			// SECURITY: pass arguments individually to avoid shell injection via group name or path.
			exec.Command(cfg.PostCommand, postgroup, strconv.FormatUint(msgnum, 10), path).Run()
		}
		return nil
	}
}

// IsCCPost returns true if CCPost is set.
func (g *Group) IsCCPost() bool {
	return g.CCPost != "" && g.CCPost != "-"
}

// IsReplyTo returns true if ReplyTo is set.
func (g *Group) IsReplyTo() bool {
	return g.ReplyTo != "" && g.ReplyTo != "-"
}

// AllGroups returns all group names under SpoolDir (recurses respecting NoRecurseMsgDir).
func AllGroups(cfg *config.Config) []string {
	var names []string
	dir := cfg.SpoolDir
	allGroupsRecurse(cfg, dir, "", &names)
	sort.Slice(names, func(i, j int) bool { return names[i] > names[j] })
	return names
}

// allGroupsRecurse walks the spool directory and appends group names (those with .config) to names, respecting NoRecurseMsgDir.
func allGroupsRecurse(cfg *config.Config, baseDir, subdir string, names *[]string) {
	var fullDir string
	if subdir != "" {
		fullDir = filepath.Join(baseDir, subdir)
	} else {
		fullDir = baseDir
	}
	entries, err := os.ReadDir(fullDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name()[0] == '.' {
			continue
		}
		path := filepath.Join(fullDir, e.Name())
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		configPath := filepath.Join(path, ".config")
		_, err = os.Stat(configPath)
		isgroup := err == nil
		groupName := e.Name()
		if subdir != "" {
			groupName = strings.ReplaceAll(subdir, string(filepath.Separator), ".") + "." + e.Name()
		}
		if isgroup {
			*names = append(*names, groupName)
		}
		newSubdir := e.Name()
		if subdir != "" {
			newSubdir = subdir + string(filepath.Separator) + e.Name()
		}
		if cfg.NoRecurseMsgDir && !isgroup {
			allGroupsRecurse(cfg, baseDir, newSubdir, names)
		} else if !cfg.NoRecurseMsgDir {
			allGroupsRecurse(cfg, baseDir, newSubdir, names)
		}
	}
}

// DeleteArticle removes an article file and rebuilds the group .info.
func (g *Group) DeleteArticle(cfg *config.Config, artnum uint64) error {
	path := article.GetArticlePath(cfg, g.Name, artnum)
	if err := os.Remove(path); err != nil {
		return err
	}
	return g.BuildInfo(cfg, true)
}

// WildmatMatch tests whether name matches the wildmat pattern.
// Supports * (match any characters) and ? (match one character).
func WildmatMatch(pattern, name string) bool {
	return doWildmat(pattern, name)
}

func doWildmat(pattern, name string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			for len(pattern) > 0 && pattern[0] == '*' {
				pattern = pattern[1:]
			}
			if len(pattern) == 0 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if doWildmat(pattern, name[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(name) == 0 {
				return false
			}
			pattern = pattern[1:]
			name = name[1:]
		default:
			if len(name) == 0 || pattern[0] != name[0] {
				return false
			}
			pattern = pattern[1:]
			name = name[1:]
		}
	}
	return len(name) == 0
}

