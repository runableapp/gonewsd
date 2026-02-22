// Copyright © 2026 Runable.app. GPL-3.0.
//
// Package nntp implements the NNTP server. It accepts TCP connections,
// parses client commands (GROUP, LIST, ARTICLE, POST, AUTHINFO, etc.),
// enforces auth and ACLs via the auth Store, and delegates to the group
// and article packages for spool access. Handles dot-stuffing, CRLF, and
// newsd-compatible response codes.
package nntp

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gonewsd/internal/article"
	"gonewsd/internal/auth"
	"gonewsd/internal/config"
	"gonewsd/internal/group"
	"gonewsd/internal/logging"
)

// asciiHexEncode returns a copy of s with non-printable/non-ASCII bytes encoded as <0x##> (same as newsd AsciiHexEncode).
func asciiHexEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c <= 0x7e {
			b.WriteByte(c)
		} else {
			b.WriteString(fmt.Sprintf("<0x%02x>", c))
		}
	}
	return b.String()
}

// truncateForLog returns the first n bytes of s, hex-encoding non-ASCII for debugging.
func truncateForLog(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return asciiHexEncode(s)
}

// Overview format (same as newsd)
var OverviewFormat = []string{
	"Subject:",
	"From:",
	"Date:",
	"Message-ID:",
	"References:",
	"Bytes:",
	"Lines:",
	"Xref:full",
	"Reply-To:",
}

// Server is the NNTP server.
type Server struct {
	cfg       *config.Config
	log       *logging.Logger
	authStore *auth.Store // nil = no auth (public)
	ln        net.Listener
	clients   int64
	connMu    sync.Mutex
	conns     map[net.Conn]struct{}
}

// NewServer creates a new NNTP server. authStore may be nil (all access allowed).
func NewServer(cfg *config.Config, log *logging.Logger, authStore *auth.Store) *Server {
	return &Server{cfg: cfg, log: log, authStore: authStore, conns: make(map[net.Conn]struct{})}
}

// SetAuthStore sets the auth store (e.g. after SIGHUP reload). May be nil.
func (s *Server) SetAuthStore(st *auth.Store) {
	s.authStore = st
}

// canRead returns true if the session may read the group; if authStore is nil, always true.
func (s *Server) canRead(session *auth.Session, groupName string, groupExists bool) bool {
	if s.authStore == nil {
		return true
	}
	return s.authStore.CanRead(session, groupName, groupExists)
}

// canPost returns true if the session may post to the group; if authStore is nil, always true.
func (s *Server) canPost(session *auth.Session, groupName string, groupExists bool) bool {
	if s.authStore == nil {
		return true
	}
	return s.authStore.CanPost(session, groupName, groupExists)
}

// listGroups returns the list of group names visible to the session (filtered by auth if enabled).
func (s *Server) listGroups(session *auth.Session) []string {
	all := group.AllGroups(s.cfg)
	if s.authStore == nil {
		return all
	}
	return s.authStore.FilterGroupsForLIST(session, all)
}

// sessionUsername returns the session's username, or empty if not authenticated.
func sessionUsername(session *auth.Session) string {
	if session == nil {
		return ""
	}
	return session.Username
}

// Listen binds to the configured address.
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return err
	}
	s.ln = ln
	return nil
}

// Addr returns the listener address.
func (s *Server) Addr() net.Addr {
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Close closes the listener.
func (s *Server) Close() error {
	if s.ln == nil {
		return nil
	}
	return s.ln.Close()
}

// CloseAllConns closes all active client connections so handler goroutines exit (for graceful shutdown).
func (s *Server) CloseAllConns() {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	for conn := range s.conns {
		conn.Close()
	}
}

// Accept accepts a connection and spawns a goroutine to handle it.
func (s *Server) Accept() (net.Conn, error) {
	conn, err := s.ln.Accept()
	if err != nil {
		return nil, err
	}
	if s.cfg.MaxClients > 0 {
		n := atomic.LoadInt64(&s.clients)
		if n >= int64(s.cfg.MaxClients) {
			s.send(conn, "400 Server has too many connections open -- try again later")
			conn.Close()
			return s.Accept() // accept next
		}
	}
	atomic.AddInt64(&s.clients, 1)
	s.connMu.Lock()
	s.conns[conn] = struct{}{}
	s.connMu.Unlock()
	go s.handleConn(conn)
	return conn, nil
}

// Serve runs the accept loop (blocking).
func (s *Server) Serve() error {
	for {
		_, err := s.Accept()
		if err != nil {
			return err
		}
	}
}

// handleConn runs the NNTP command loop for one client: reads lines, dispatches commands, sends responses.
func (s *Server) handleConn(conn net.Conn) {
	remoteAddr := conn.RemoteAddr().String()
	defer func() {
		s.log.Log(config.LogInfo, "Connection from %s closed", remoteAddr)
		s.connMu.Lock()
		delete(s.conns, conn)
		s.connMu.Unlock()
		atomic.AddInt64(&s.clients, -1)
		conn.Close()
	}()

	s.log.Log(config.LogInfo, "Connection from %s", remoteAddr)

	s.send(conn, "200 newsd news server ready - posting ok")

	var curGroup group.Group
	var curArticle article.Article
	var session *auth.Session // per-connection; set after AUTHINFO success
	authUser := ""
	authSimple := false

	// Read exactly like newsd: one byte at a time from raw socket, no buffering.
	for {
		if s.cfg.Timeout > 0 {
			conn.SetReadDeadline(time.Now().Add(s.cfg.Timeout))
		}
		line, err := readLineRaw(conn)
		if err != nil {
			if err != io.EOF {
				s.log.Log(config.LogError, "read line: %v", err)
			}
			break
		}
		line = strings.TrimRight(line, "\r\n")
		line = strings.TrimRight(line, "\r")

		// Log every line the client sends (same as newsd GOT: at L_INFO; ErrorLog_Hex encodes binary as <0x##>).
		// SECURITY: mask AUTHINFO PASS and AUTHINFO SIMPLE credentials to avoid logging passwords.
		logLine := line
		upperLine := strings.ToUpper(strings.TrimSpace(line))
		if strings.HasPrefix(upperLine, "AUTHINFO PASS") {
			logLine = "AUTHINFO PASS ********"
		}
		if authSimple {
			// AUTHINFO SIMPLE continuation line is "username password" -- mask the password
			logLine = "(AUTHINFO SIMPLE credentials masked)"
		}
		if s.cfg.ErrorLogHex {
			s.log.Log(config.LogInfo, "GOT: '%s' from %s", asciiHexEncode(logLine), remoteAddr)
		} else {
			s.log.Log(config.LogInfo, "GOT: '%s' from %s", logLine, remoteAddr)
		}

		fields := strings.Fields(line)
		var cmd, arg1, arg2 string
		if len(fields) >= 1 {
			cmd = fields[0]
		}
		if len(fields) >= 2 {
			arg1 = fields[1]
		}
		if len(fields) >= 3 {
			arg2 = strings.Join(fields[2:], " ")
		}
		// Same as newsd: if no command (empty/whitespace line), continue without sending
		if len(fields) < 1 {
			continue
		}

		// AUTHINFO SIMPLE continuation
		if authSimple {
			authSimple = false
			if cmd == "" || arg1 == "" {
				s.send(conn, "501 Bad or unknown argument")
				continue
			}
			if s.authStore != nil && s.authStore.ValidateUser(cmd, arg1) {
				session = &auth.Session{Username: cmd, Groups: s.authStore.ResolveSessionGroups(cmd)}
				s.log.LogAuth("auth ok (SIMPLE) user=%q from %s", cmd, remoteAddr)
				s.send(conn, "250 Authenticated OK")
			} else {
				if s.authStore != nil {
					s.log.LogAuth("auth failed (SIMPLE) user=%q from %s", cmd, remoteAddr)
				}
				s.send(conn, "452 Authorization rejected")
			}
			continue
		}

		cmdUpper := strings.ToUpper(cmd)
		switch cmdUpper {
		case "AUTHINFO":
			if s.authStore == nil {
				s.send(conn, "281 No authentication needed")
				continue
			}
			switch strings.ToUpper(arg1) {
			case "SIMPLE":
				s.send(conn, "350 Go ahead with username and password")
				authSimple = true
			case "USER":
				if arg2 == "" {
					s.send(conn, "501 Bad or unknown argument")
					continue
				}
				authUser = arg2
				s.send(conn, "381 Now supply your password")
			case "PASS":
				if arg2 == "" {
					s.send(conn, "501 Bad or unknown argument")
					continue
				}
				if authUser == "" {
					s.send(conn, "482 User must be specified first")
					continue
				}
				if s.authStore.ValidateUser(authUser, arg2) {
					session = &auth.Session{Username: authUser, Groups: s.authStore.ResolveSessionGroups(authUser)}
					s.log.LogAuth("auth ok user=%q from %s", authUser, remoteAddr)
					s.send(conn, "281 Authenticated OK")
				} else {
					s.log.LogAuth("auth failed user=%q from %s", authUser, remoteAddr)
					s.send(conn, "482 Authentication failed")
				}
				authUser = ""
			case "GENERIC":
				s.send(conn, "501 'AUTHINFO GENERIC' not supported")
			default:
				s.send(conn, "501 Bad or unknown argument")
			}

		case "CHECK", "TAKETHIS":
			s.send(conn, "400 not accepting articles - we are not a news feed")

		case "MODE":
			if strings.ToUpper(arg1) == "STREAM" {
				s.send(conn, "500 Streaming not implemented on this server")
				continue
			}
			if strings.ToUpper(arg1) == "READER" {
				s.send(conn, "200 erco's newsd server ready (posting ok)")
				continue
			}
			s.send(conn, "500 What?")

		case "LIST":
			// newsd: "LIST ACTIVE <wildmat>" -> 501 wildmats not supported
			if strings.ToUpper(arg1) == "ACTIVE" && arg2 != "" {
				s.send(conn, "501 LIST ACTIVE <wildmat>: wildmats not supported")
				continue
			}
			namesForList := s.listGroups(session)
			switch strings.ToUpper(arg1) {
			case "EXTENSIONS":
				s.send(conn, "202 Extensions supported:\r\nLISTGROUP\r\nMODE\r\nXREPLIC\r\nXOVER\r\nDATE\r\n.")
			case "ACTIVE", "":
				s.send(conn, "215 list of newsgroups follows")
				for _, name := range namesForList {
					var g group.Group
					if g.LoadInfoFull(s.cfg, name, s.log) != nil {
						continue
					}
					// Post permission is enforced by auth/ACL at POST time; LIST shows y
					s.send(conn, fmt.Sprintf("%s %d %d y", g.Name, g.Total, g.Start))
				}
				s.send(conn, ".")
			case "ACTIVE.TIMES":
				s.send(conn, "215 information follows")
				for _, name := range namesForList {
					var g group.Group
					if g.LoadInfoFull(s.cfg, name, s.log) != nil {
						continue
					}
					s.send(conn, fmt.Sprintf("%s %d %s", g.Name, g.Ctime, g.Creator))
				}
				s.send(conn, ".")
			case "DISTRIBUTIONS", "DISTRIB.PATS":
				s.send(conn, "503 Not implemented on this server")
			case "NEWSGROUPS":
				s.send(conn, "215 information follows")
				for _, name := range namesForList {
					var g group.Group
					if g.LoadInfoFull(s.cfg, name, s.log) != nil {
						continue
					}
					s.send(conn, fmt.Sprintf("%s %s", g.Name, g.Desc))
				}
				s.send(conn, ".")
			case "OVERVIEW.FMT":
				s.send(conn, "215 information follows")
				for _, h := range OverviewFormat {
					s.send(conn, h)
				}
				s.send(conn, ".")
			case "SUBSCRIPTIONS":
				s.send(conn, "215 information follows")
				s.send(conn, "rush.general") // newsd HACK: TBD
				s.send(conn, ".")
			default:
				s.send(conn, "501 Syntax error")
			}

		case "LISTGROUP":
			if arg1 != "" {
				if !auth.ValidGroupName(arg1) {
					s.send(conn, "501 invalid group name")
					continue
				}
				var g group.Group
				if g.LoadInfoFull(s.cfg, arg1, s.log) != nil {
					s.send(conn, fmt.Sprintf("411 No such newsgroup: %s", g.Errmsg))
					continue
				}
				if !s.canRead(session, arg1, true) {
					s.send(conn, "480 Authentication required")
					continue
				}
				curGroup = g
				_ = curArticle.Load(s.cfg, curGroup.Name, curGroup.Start)
			}
			if !curGroup.Valid {
				s.send(conn, "412 Not currently in newsgroup")
				continue
			}
			if !s.canRead(session, curGroup.Name, true) {
				s.send(conn, "480 Authentication required")
				continue
			}
			s.send(conn, "211 list of article numbers follow")
			for n := curGroup.Start; n <= curGroup.End; n++ {
				s.send(conn, strconv.FormatUint(n, 10))
			}
			s.send(conn, ".")

		case "XREPLIC":
			s.send(conn, "437 'xreplic' not implemented on this server")

		case "XOVER":
			if !curGroup.Valid {
				s.send(conn, "412 Not in a newsgroup")
				continue
			}
			if !s.canRead(session, curGroup.Name, true) {
				s.send(conn, "480 Authentication required")
				continue
			}
			sart, eart := curGroup.Start, curGroup.End
			if arg1 != "" {
				if n, _ := fmt.Sscanf(arg1, "%d-%d", &sart, &eart); n >= 1 {
					if n == 1 {
						eart = curGroup.End
					}
				} else if n, _ := fmt.Sscanf(arg1, "%d-", &sart); n == 1 {
					eart = curGroup.End
				} else {
					var single uint64
					fmt.Sscanf(arg1, "%d", &single)
					sart, eart = single, single
				}
			}
			if sart < curGroup.Start {
				sart = curGroup.Start
			}
			if sart > curGroup.End {
				sart = curGroup.End
			}
			if eart < curGroup.Start {
				eart = curGroup.Start
			}
			if eart > curGroup.End {
				eart = curGroup.End
			}
			if sart > eart {
				sart = eart
			}
			s.send(conn, "224 overview follows")
			for n := sart; n <= eart; n++ {
				var a article.Article
				if a.Load(s.cfg, curGroup.Name, n) != nil {
					continue
				}
				s.send(conn, a.Overview(OverviewFormat))
			}
			s.send(conn, ".")

		case "GROUP":
			if arg1 == "" {
				s.send(conn, "501 syntax error; expected 'GROUP <group-name>'")
				continue
			}
			if !auth.ValidGroupName(arg1) {
				s.send(conn, "501 invalid group name")
				continue
			}
			var g group.Group
			if g.LoadInfoFull(s.cfg, arg1, s.log) != nil {
				s.send(conn, fmt.Sprintf("411 No such newsgroup: %s", g.Errmsg))
				continue
			}
			if !s.canRead(session, arg1, true) {
				s.send(conn, "480 Authentication required")
				continue
			}
			curGroup = g
			_ = curArticle.Load(s.cfg, curGroup.Name, curGroup.Start)
			s.send(conn, fmt.Sprintf("211 %d %d %d %s group selected", curGroup.Total, curGroup.Start, curGroup.End, curGroup.Name))

		case "HELP":
			s.send(conn, "100 help text follows")
			s.send(conn, "CHECK\r\n"+
				"TAKETHIS\r\n"+
				"MODE [stream|reader]\r\n"+
				"LIST [active|active.times|distributions|distrib.pats|newsgroups|overview.fmt|subscriptions]\r\n"+
				"LISTGROUP [newsgroup]\r\n"+
				"XREPLIC\r\n"+
				"XOVER [msg#|msg#-|msg#-msg#]\r\n"+
				"GROUP newsgroup\r\n"+
				"HELP\r\n"+
				"NEWGROUPS [YY]yymmdd hhmmss [GMT|UTC] [distributions]\r\n"+
				"NEWNEWS\r\n"+
				"NEXT\r\n"+
				"HEAD [msg#|<msgid>]\r\n"+
				"BODY [msg#|<msgid>]\r\n"+
				"ARTICLE [msg#|<msgid>]\r\n"+
				"AUTHINFO [user|pass] <value>\r\n"+
				"AUTHINFO simple\r\n"+
				"STAT [msg#|<msgid>]\r\n"+
				"POST\r\n"+
				"DATE\r\n"+
				"QUIT\r\n"+
				".")
		case "NEWGROUPS":
			// newsd: NEWGROUPS <YYMMDD> <HHMMSS> - both must be 6 chars
			if len(arg1) != 6 || len(arg2) != 6 {
				s.send(conn, "501 Bad or missing date/time arguments")
				continue
			}
			var year, mon, day, hour, min, sec int
			if n1, _ := fmt.Sscanf(arg1, "%2d%2d%2d", &year, &mon, &day); n1 != 3 {
				s.send(conn, "501 Bad date/time argument")
				continue
			}
			if n2, _ := fmt.Sscanf(arg2, "%2d%2d%2d", &hour, &min, &sec); n2 != 3 {
				s.send(conn, "501 Bad date/time argument")
				continue
			}
			s.send(conn, "231 list of new newsgroups follows")
			for _, name := range s.listGroups(session) {
				s.send(conn, name)
			}
			s.send(conn, ".")
		case "NEWNEWS":
			s.send(conn, "501 Command not implemented on server")
		case "NEXT":
			if !curGroup.Valid {
				s.send(conn, "412 no newsgroup selected")
				continue
			}
			if !s.canRead(session, curGroup.Name, true) {
				s.send(conn, "480 Authentication required")
				continue
			}
			if !curArticle.Valid {
				s.send(conn, "420 no article has been selected")
				continue
			}
			next := curArticle.Number + 1
			if next < curGroup.Start || next > curGroup.End {
				s.send(conn, "421 no next article in this group")
				continue
			}
			restoreArt := curArticle
			if err := curArticle.Load(s.cfg, curGroup.Name, next); err != nil {
				errmsg := curArticle.Errmsg
				curArticle = restoreArt
				s.send(conn, fmt.Sprintf("421 error retrieving article %d: %s", next, errmsg))
				continue
			}
			s.send(conn, fmt.Sprintf("223 %d %s article retrieved - request text separately", next, curArticle.MessageID))

		case "HEAD", "BODY", "ARTICLE", "STAT":
			if !curGroup.Valid {
				s.send(conn, "412 Not currently in newsgroup")
				continue
			}
			if !s.canRead(session, curGroup.Name, true) {
				s.send(conn, "480 Authentication required")
				continue
			}
			var theArt uint64
			restoreCur := false
			prevNum := curArticle.Number
			if arg1 == "" {
				theArt = curArticle.Number
				restoreCur = true
			} else if len(arg1) > 0 && arg1[0] == '<' {
				artnum, findErr := curGroup.FindArticleByMessageID(s.cfg, arg1)
				if findErr != nil {
					s.send(conn, "430 no such article found")
					continue
				}
				theArt = artnum
				restoreCur = true
			} else if len(arg1) > 0 && arg1[0] >= '0' && arg1[0] <= '9' {
				parsed, err := strconv.ParseUint(arg1, 10, 64)
				if err != nil {
					s.send(conn, "501 bad article number")
					continue
				}
				theArt = parsed
			} else {
				s.send(conn, "501 bad argument")
				continue
			}
			if theArt < curGroup.Start || theArt > curGroup.End {
				s.send(conn, fmt.Sprintf("423 no such article in group (range %d-%d)", curGroup.Start, curGroup.End))
				continue
			}
			if curArticle.Load(s.cfg, curGroup.Name, theArt) != nil {
				s.send(conn, fmt.Sprintf("430 no such article: %s", curArticle.Errmsg))
				continue
			}
			switch cmdUpper {
			case "ARTICLE":
				s.send(conn, fmt.Sprintf("220 %d %s article retrieved - head and body follow", theArt, curArticle.MessageID))
				curArticle.SendArticle(conn, true, true, s.log)
				s.send(conn, ".")
			case "HEAD":
				s.send(conn, fmt.Sprintf("221 %d %s article retrieved - head follows", theArt, curArticle.MessageID))
				curArticle.SendHead(conn, s.log)
				s.send(conn, ".")
			case "BODY":
				s.send(conn, fmt.Sprintf("222 %d %s article retrieved - body follows", theArt, curArticle.MessageID))
				s.send(conn, "")
				curArticle.SendBody(conn, s.log)
				s.send(conn, ".")
			case "STAT":
				s.send(conn, fmt.Sprintf("223 %d %s article retrieved - request text separately", theArt, curArticle.MessageID))
			}
			if restoreCur {
				_ = curArticle.Load(s.cfg, curGroup.Name, prevNum)
			}

		case "POST":
			s.send(conn, "340 Continue posting; Period on a line by itself to end")
			conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
			msg, err := readPostBodyRaw(conn)
			conn.SetReadDeadline(time.Time{}) // reset for next command
			if err != nil {
				s.send(conn, "441 Posting failed")
				continue
			}
			// Debug: log raw bytes of POST body to diagnose UTF-8 issues (debug level only)
			s.log.Log(config.LogDebug, "POST body length=%d first100bytes=%q", len(msg), truncateForLog(msg, 100))
			headers, body := group.ParseArticle(msg)
			postgroup, hasGroup := group.GetHeaderValue(headers, "Newsgroups:")
			if !hasGroup || postgroup == "" {
				s.send(conn, "441 article has no Newsgroups field")
				continue
			}
			if !auth.ValidGroupName(postgroup) {
				s.send(conn, "441 invalid newsgroup name")
				continue
			}
			var gCheck group.Group
			if gCheck.LoadInfoFull(s.cfg, postgroup, s.log) != nil {
				s.send(conn, fmt.Sprintf("441 %s", gCheck.Errmsg))
				continue
			}
			if !s.canPost(session, postgroup, true) {
				s.log.LogAuth("post denied group=%q user=%q from %s", postgroup, sessionUsername(session), remoteAddr)
				s.send(conn, "480 Authentication required")
				continue
			}
			// Line limit from current group (same as newsd: group.PostLimit())
			limit := 0
			if curGroup.Valid {
				limit = curGroup.PostLimit
			}
			if limit > 0 {
				linecount := countPostLines(msg)
				if linecount > limit {
					s.send(conn, fmt.Sprintf("411 Not Posted: article exceeds sanity line limit of %d.", limit))
					continue
				}
			}
			// Same as newsd: log ParseArticle head/body at L_DEBUG when debug enabled
			for i, h := range headers {
				s.log.Log(config.LogDebug, "ParseArticle: --- head[%03d]: '%s'", i, h)
			}
			for i, b := range body {
				s.log.Log(config.LogDebug, "ParseArticle: --- body[%03d]: '%s'", i, b)
			}
			group.UpdatePath(s.cfg, &headers)
			var g group.Group
			if err := g.Post(s.cfg, OverviewFormat, headers, body, remoteAddr, false, false, s.log); err != nil {
				s.send(conn, fmt.Sprintf("441 %s", g.Errmsg))
				continue
			}
			s.log.LogAuth("post ok group=%q user=%q from %s", postgroup, sessionUsername(session), remoteAddr)
			s.send(conn, "240 Article posted successfully.")

		case "DATE":
			t := time.Now().UTC()
			s.send(conn, fmt.Sprintf("111 %04d%02d%02d%02d%02d%02d", t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second()))

		case "QUIT":
			s.send(conn, "205 goodbye.")
			return

		default:
			s.send(conn, "500 Command not understood")
		}
	}
}

// send writes a CRLF-terminated line to the connection (same as newsd C: write(msgsock, out.c_str(), out.length())).
func (s *Server) send(conn net.Conn, msg string) {
	conn.Write([]byte(msg + "\r\n"))
	// Log what we send (same as newsd Server::Send L_DEBUG "SEND: %.4000s").
	const maxLog = 4000
	logMsg := msg
	if len(logMsg) > maxLog {
		logMsg = logMsg[:maxLog]
	}
	s.log.Log(config.LogDebug, "SEND: %s", logMsg)
}

// readByteRaw reads one byte from the connection (same as newsd: read(msgsock, &c, 1)).
func readByteRaw(r io.Reader) (byte, error) {
	var b [1]byte
	n, err := r.Read(b[:])
	if n < 1 {
		return 0, err
	}
	return b[0], nil
}

// readLineRaw reads a CRLF- or LF-terminated line one byte at a time (same as newsd C ~270-284).
func readLineRaw(r io.Reader) (string, error) {
	var line []byte
	const maxLine = 4096
	for len(line) < maxLine {
		c, err := readByteRaw(r)
		if err != nil {
			return string(line), err
		}
		if c == '\n' {
			break
		}
		if c == '\r' {
			// consume \n after \r
			if _, err := readByteRaw(r); err != nil {
				return string(line), err
			}
			break
		}
		line = append(line, c)
	}
	return string(line), nil
}

// postReadMode matches newsd Server.C POST state machine (RFC 3977 3.1.1).
type postReadMode int

const (
	postModeNormal  postReadMode = iota // normal data
	postModeCRLF                        // received \n
	postModeCRLFDot                     // received \n.
)

// countPostLines counts lines in the POST body with newsd's rule: lines longer
// than 80 chars count as multiple lines (same as newsd Server.C linechars/linecount).
func countPostLines(msg string) int {
	linechars := 0
	linecount := 0
	for i := 0; i < len(msg); i++ {
		c := msg[i]
		linechars++
		if linechars > 80 || c == '\n' {
			linechars = 0
			linecount++
		}
	}
	if linechars > 0 {
		linecount++
	}
	return linecount
}

// maxPostSize is the maximum POST body size (10 MB). Prevents memory exhaustion from oversized posts.
const maxPostSize = 10 * 1024 * 1024

// readPostBodyRaw reads the POST body one byte at a time from the raw connection
// (same as newsd C: while (read(msgsock, &c, 1) == 1) { ... }). No buffering.
func readPostBodyRaw(r io.Reader) (string, error) {
	var msg strings.Builder
	mode := postModeNormal
	for {
		c, err := readByteRaw(r)
		if err != nil {
			return msg.String(), err
		}
		if msg.Len() > maxPostSize {
			return "", fmt.Errorf("POST body exceeds maximum size (%d bytes)", maxPostSize)
		}
		if c == '\r' {
			continue // ignore \r like C
		}
		switch c {
		case '\n':
			if mode == postModeCRLFDot {
				return msg.String(), nil
			}
			mode = postModeCRLF
			msg.WriteByte(c)
		case '.':
			if mode == postModeCRLF {
				mode = postModeCRLFDot
				continue
			}
			// Lenient: ".\n" or ".\r\n" without newline before dot (same as newsd)
			if mode == postModeNormal {
				next, err := readByteRaw(r)
				if err != nil {
					msg.WriteByte(c)
					return msg.String(), err
				}
				if next == '\n' {
					return msg.String(), nil
				}
				if next == '\r' {
					if _, err := readByteRaw(r); err != nil {
						msg.WriteByte(c)
						msg.WriteByte(next)
						return msg.String(), err
					}
					return msg.String(), nil
				}
				msg.WriteByte(c)
				msg.WriteByte(next)
				mode = postModeNormal
				continue
			}
			mode = postModeNormal
			msg.WriteByte(c)
		default:
			mode = postModeNormal
			msg.WriteByte(c)
		}
	}
}
