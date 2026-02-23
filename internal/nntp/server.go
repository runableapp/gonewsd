// Copyright © 2026 Runable.app. GPL-3.0.
//
// Package nntp implements the NNTP server. It accepts TCP connections,
// parses client commands (GROUP, LIST, ARTICLE, POST, AUTHINFO, etc.),
// enforces auth and ACLs via the auth Store, and delegates to the group
// and article packages for spool access. Handles dot-stuffing, CRLF, and
// newsd-compatible response codes.
package nntp

import (
	"compress/flate"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
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
	tlsConfig *tls.Config
}

// NewServer creates a new NNTP server. authStore may be nil (all access allowed).
func NewServer(cfg *config.Config, log *logging.Logger, authStore *auth.Store) *Server {
	s := &Server{cfg: cfg, log: log, authStore: authStore, conns: make(map[net.Conn]struct{})}
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			log.Log(config.LogError, "TLS: failed to load cert/key: %v", err)
		} else {
			s.tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
		}
	}
	return s
}

// compressedConn wraps a net.Conn with DEFLATE compression (RFC 8054).
type compressedConn struct {
	net.Conn
	r io.Reader
	w *flate.Writer
}

func (c *compressedConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *compressedConn) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if err != nil {
		return n, err
	}
	return n, c.w.Flush()
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
func (s *Server) handleConn(rawConn net.Conn) {
	conn := rawConn // may be replaced by STARTTLS or COMPRESS
	remoteAddr := rawConn.RemoteAddr().String()
	defer func() {
		s.log.Log(config.LogInfo, "Connection from %s closed", remoteAddr)
		s.connMu.Lock()
		delete(s.conns, rawConn)
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
				s.send(conn, "501 AUTHINFO GENERIC is deprecated (RFC 4643); use AUTHINFO USER/PASS")
			default:
				s.send(conn, "501 Bad or unknown argument")
			}

		case "CAPABILITIES":
			s.send(conn, "101 Capability list:")
			s.send(conn, "VERSION 2")
			s.send(conn, "READER")
			s.send(conn, "POST")
			s.send(conn, "LIST ACTIVE NEWSGROUPS OVERVIEW.FMT ACTIVE.TIMES SUBSCRIPTIONS HEADERS")
			s.send(conn, "OVER")
			s.send(conn, "HDR")
			if s.authStore != nil {
				s.send(conn, "AUTHINFO USER")
			}
			if s.tlsConfig != nil {
				s.send(conn, "STARTTLS")
			}
			s.send(conn, "COMPRESS DEFLATE")
			s.send(conn, ".")

		case "STARTTLS":
			if s.tlsConfig == nil {
				s.send(conn, "580 TLS not available")
				continue
			}
			s.send(conn, "382 Continue with TLS negotiation")
			tlsConn := tls.Server(conn, s.tlsConfig)
			if err := tlsConn.Handshake(); err != nil {
				s.log.Log(config.LogError, "STARTTLS handshake failed from %s: %v", remoteAddr, err)
				return
			}
			s.log.Log(config.LogInfo, "STARTTLS completed from %s", remoteAddr)
			conn = tlsConn

		case "COMPRESS":
			if strings.ToUpper(arg1) != "DEFLATE" {
				s.send(conn, "501 Only DEFLATE is supported")
				continue
			}
			s.send(conn, "206 Compression active")
			fw, err := flate.NewWriter(conn, flate.DefaultCompression)
			if err != nil {
				s.log.Log(config.LogError, "COMPRESS init failed from %s: %v", remoteAddr, err)
				return
			}
			fr := flate.NewReader(conn)
			conn = &compressedConn{Conn: conn, r: fr, w: fw}
			s.log.Log(config.LogInfo, "COMPRESS DEFLATE active from %s", remoteAddr)

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
			namesForList := s.listGroups(session)
			listArg1 := strings.ToUpper(arg1)
			// Apply wildmat filter for LIST ACTIVE <wildmat>
			if listArg1 == "ACTIVE" && arg2 != "" {
				var filtered []string
				for _, n := range namesForList {
					if group.WildmatMatch(arg2, n) {
						filtered = append(filtered, n)
					}
				}
				namesForList = filtered
			}
			switch listArg1 {
			case "EXTENSIONS":
				s.send(conn, "202 Extensions supported:\r\nLISTGROUP\r\nMODE\r\nXOVER\r\nOVER\r\nHDR\r\nDATE\r\nCOMPRESS DEFLATE\r\n.")
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
			case "DISTRIBUTIONS":
				s.send(conn, "215 Distributions follow")
				s.send(conn, ".")
			case "DISTRIB.PATS":
				s.send(conn, "215 Distribution patterns follow")
				s.send(conn, ".")
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
			case "HEADERS":
				s.send(conn, "215 Header and metadata item names follow")
				s.send(conn, ":")
				s.send(conn, ".")
			case "COUNTS":
				s.send(conn, "215 Group counts follow")
				for _, name := range namesForList {
					var g group.Group
					if g.LoadInfoFull(s.cfg, name, s.log) != nil {
						continue
					}
					s.send(conn, fmt.Sprintf("%s %d %d %d y", g.Name, g.End, g.Start, g.Total))
				}
				s.send(conn, ".")
			default:
				s.send(conn, "501 Syntax error")
			}

		case "LISTGROUP":
			lgGroup := arg1
			lgRange := arg2
			if lgGroup != "" {
				if !auth.ValidGroupName(lgGroup) {
					s.send(conn, "501 invalid group name")
					continue
				}
				var g group.Group
				if g.LoadInfoFull(s.cfg, lgGroup, s.log) != nil {
					s.send(conn, fmt.Sprintf("411 No such newsgroup: %s", g.Errmsg))
					continue
				}
				if !s.canRead(session, lgGroup, true) {
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
			lgStart, lgEnd := curGroup.Start, curGroup.End
			if lgRange != "" {
				parseArticleRange(lgRange, curGroup.Start, curGroup.End, &lgStart, &lgEnd)
			}
			s.send(conn, fmt.Sprintf("211 %d %d %d %s list follows", curGroup.Total, curGroup.Start, curGroup.End, curGroup.Name))
			for n := lgStart; n <= lgEnd; n++ {
				if _, err := os.Stat(article.GetArticlePath(s.cfg, curGroup.Name, n)); err == nil {
					s.send(conn, strconv.FormatUint(n, 10))
				}
			}
			s.send(conn, ".")

		case "XPAT":
			if !curGroup.Valid {
				s.send(conn, "412 Not in a newsgroup")
				continue
			}
			if !s.canRead(session, curGroup.Name, true) {
				s.send(conn, "480 Authentication required")
				continue
			}
			if arg1 == "" || arg2 == "" {
				s.send(conn, "501 Syntax: XPAT header range|<msgid> pat [pat...]")
				continue
			}
			xpatField := arg1
			xpatRest := strings.SplitN(arg2, " ", 2)
			xpatRange := xpatRest[0]
			xpatPattern := ""
			if len(xpatRest) > 1 {
				xpatPattern = xpatRest[1]
			}
			if xpatPattern == "" {
				s.send(conn, "501 Syntax: XPAT header range|<msgid> pat [pat...]")
				continue
			}
			sart, eart := curGroup.Start, curGroup.End
			if len(xpatRange) > 0 && xpatRange[0] == '<' {
				artnum, findErr := curGroup.FindArticleByMessageID(s.cfg, xpatRange)
				if findErr != nil {
					s.send(conn, "430 no such article found")
					continue
				}
				sart, eart = artnum, artnum
			} else {
				parseArticleRange(xpatRange, curGroup.Start, curGroup.End, &sart, &eart)
			}
			patterns := strings.Fields(xpatPattern)
			s.send(conn, "221 Header follows")
			for n := sart; n <= eart; n++ {
				val := article.GetRawHeader(s.cfg, curGroup.Name, n, xpatField)
				if val == "" {
					continue
				}
				for _, pat := range patterns {
					if group.WildmatMatch(pat, val) {
						s.send(conn, fmt.Sprintf("%d %s", n, val))
						break
					}
				}
			}
			s.send(conn, ".")

		case "XREPLIC":
			s.send(conn, "437 'xreplic' not implemented on this server")

		case "XOVER", "OVER":
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
				parseArticleRange(arg1, curGroup.Start, curGroup.End, &sart, &eart)
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

		case "XHDR", "HDR":
			if !curGroup.Valid {
				s.send(conn, "412 Not in a newsgroup")
				continue
			}
			if !s.canRead(session, curGroup.Name, true) {
				s.send(conn, "480 Authentication required")
				continue
			}
			if arg1 == "" {
				s.send(conn, "501 Header field name required")
				continue
			}
			hdrField := arg1
			hdrRange := arg2
			sart, eart := curGroup.Start, curGroup.End
			if hdrRange != "" {
				if len(hdrRange) > 0 && hdrRange[0] == '<' {
					artnum, findErr := curGroup.FindArticleByMessageID(s.cfg, hdrRange)
					if findErr != nil {
						s.send(conn, "430 no such article found")
						continue
					}
					sart, eart = artnum, artnum
				} else {
					parseArticleRange(hdrRange, curGroup.Start, curGroup.End, &sart, &eart)
				}
			}
			s.send(conn, "225 Header information follows")
			for n := sart; n <= eart; n++ {
				val := article.GetRawHeader(s.cfg, curGroup.Name, n, hdrField)
				if val != "" {
					s.send(conn, fmt.Sprintf("%d %s", n, val))
				}
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
			s.send(conn, "ARTICLE [msg#|<msgid>]\r\n"+
				"AUTHINFO [user|pass] <value>\r\n"+
				"AUTHINFO simple\r\n"+
				"BODY [msg#|<msgid>]\r\n"+
				"CAPABILITIES\r\n"+
				"COMPRESS DEFLATE\r\n"+
				"DATE\r\n"+
				"GROUP newsgroup\r\n"+
				"HDR field [range|<msgid>]\r\n"+
				"HEAD [msg#|<msgid>]\r\n"+
				"HELP\r\n"+
				"LAST\r\n"+
				"LIST [active [wildmat]|active.times|counts|newsgroups|overview.fmt|headers|distributions|distrib.pats|subscriptions]\r\n"+
				"LISTGROUP [newsgroup [range]]\r\n"+
				"MODE reader\r\n"+
				"NEWGROUPS [YY]yymmdd hhmmss [GMT|UTC]\r\n"+
				"NEWNEWS wildmat [YY]yymmdd hhmmss [GMT|UTC]\r\n"+
				"NEXT\r\n"+
				"OVER [range]\r\n"+
				"POST\r\n"+
				"QUIT\r\n"+
				"STARTTLS\r\n"+
				"STAT [msg#|<msgid>]\r\n"+
				"XHDR field [range|<msgid>]\r\n"+
				"XOVER [range]\r\n"+
				"XPAT header range|<msgid> pat [pat...]\r\n"+
				".")
		case "NEWGROUPS":
			checkTime, err := parseNNTPDateTime(arg1, arg2)
			if err != nil {
				s.send(conn, "501 Bad or missing date/time arguments")
				continue
			}
			s.send(conn, "231 list of new newsgroups follows")
			for _, name := range s.listGroups(session) {
				var g group.Group
				if g.LoadInfoFull(s.cfg, name, s.log) != nil {
					continue
				}
				if g.Ctime >= checkTime.Unix() {
					s.send(conn, name)
				}
			}
			s.send(conn, ".")

		case "NEWNEWS":
			if arg1 == "" || arg2 == "" {
				s.send(conn, "501 Syntax: NEWNEWS wildmat date time [GMT]")
				continue
			}
			nwPat := arg1
			nwFields := strings.Fields(arg2)
			if len(nwFields) < 2 {
				s.send(conn, "501 Bad date/time arguments")
				continue
			}
			checkTime, err := parseNNTPDateTime(nwFields[0], nwFields[1])
			if err != nil {
				s.send(conn, "501 Bad date/time arguments")
				continue
			}
			s.send(conn, "230 list of new articles follows")
			for _, name := range s.listGroups(session) {
				if !group.WildmatMatch(nwPat, name) {
					continue
				}
				var g group.Group
				if g.LoadInfoFull(s.cfg, name, s.log) != nil {
					continue
				}
				for n := g.Start; n <= g.End; n++ {
					path := article.GetArticlePath(s.cfg, name, n)
					fi, ferr := os.Stat(path)
					if ferr != nil {
						continue
					}
					if fi.ModTime().After(checkTime) {
						msgid, merr := g.GetMessageID(s.cfg, n)
						if merr == nil {
							s.send(conn, msgid)
						}
					}
				}
			}
			s.send(conn, ".")
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

		case "LAST":
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
			if curArticle.Number <= curGroup.Start {
				s.send(conn, "422 no previous article in this group")
				continue
			}
			prev := curArticle.Number - 1
			restoreArt := curArticle
			if err := curArticle.Load(s.cfg, curGroup.Name, prev); err != nil {
				errmsg := curArticle.Errmsg
				curArticle = restoreArt
				s.send(conn, fmt.Sprintf("422 error retrieving article %d: %s", prev, errmsg))
				continue
			}
			s.send(conn, fmt.Sprintf("223 %d %s article retrieved - request text separately", prev, curArticle.MessageID))

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

			// CANCEL control message: Control: cancel <message-id>
			controlVal, hasControl := group.GetHeaderValue(headers, "Control:")
			if hasControl {
				controlLower := strings.ToLower(strings.TrimSpace(controlVal))
				if strings.HasPrefix(controlLower, "cancel ") {
					cancelMsgID := strings.TrimSpace(controlVal[7:])
					s.handleCancel(conn, headers, cancelMsgID, session, remoteAddr)
					continue
				}
			}

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

// parseNNTPDateTime parses NNTP date/time strings (YYMMDD or YYYYMMDD + HHMMSS).
func parseNNTPDateTime(dateStr, timeStr string) (time.Time, error) {
	if len(timeStr) < 6 {
		return time.Time{}, fmt.Errorf("bad time")
	}
	// Strip trailing GMT/UTC keywords if present in timeStr
	timePart := strings.Fields(timeStr)[0]
	if len(timePart) < 6 {
		return time.Time{}, fmt.Errorf("bad time")
	}
	var year, mon, day, hour, min, sec int
	switch len(dateStr) {
	case 6:
		if n, _ := fmt.Sscanf(dateStr, "%2d%2d%2d", &year, &mon, &day); n != 3 {
			return time.Time{}, fmt.Errorf("bad date")
		}
		if year >= 70 {
			year += 1900
		} else {
			year += 2000
		}
	case 8:
		if n, _ := fmt.Sscanf(dateStr, "%4d%2d%2d", &year, &mon, &day); n != 3 {
			return time.Time{}, fmt.Errorf("bad date")
		}
	default:
		return time.Time{}, fmt.Errorf("bad date length")
	}
	if n, _ := fmt.Sscanf(timePart, "%2d%2d%2d", &hour, &min, &sec); n != 3 {
		return time.Time{}, fmt.Errorf("bad time")
	}
	return time.Date(year, time.Month(mon), day, hour, min, sec, 0, time.UTC), nil
}

// parseArticleRange parses an article range string (N, N-, N-M) and clamps to group bounds.
func parseArticleRange(rangeStr string, gStart, gEnd uint64, sart, eart *uint64) {
	if strings.Contains(rangeStr, "-") {
		if n, _ := fmt.Sscanf(rangeStr, "%d-%d", sart, eart); n == 2 {
			// N-M
		} else if n, _ := fmt.Sscanf(rangeStr, "%d-", sart); n == 1 {
			// N-
			*eart = gEnd
		}
	} else {
		var single uint64
		fmt.Sscanf(rangeStr, "%d", &single)
		*sart, *eart = single, single
	}
	if *sart < gStart {
		*sart = gStart
	}
	if *sart > gEnd {
		*sart = gEnd
	}
	if *eart < gStart {
		*eart = gStart
	}
	if *eart > gEnd {
		*eart = gEnd
	}
	if *sart > *eart {
		*sart = *eart
	}
}

// handleCancel processes a CANCEL control message: validates the canceller and deletes the article.
func (s *Server) handleCancel(conn net.Conn, headers []string, cancelMsgID string, session *auth.Session, remoteAddr string) {
	postgroup, hasGroup := group.GetHeaderValue(headers, "Newsgroups:")
	if !hasGroup || postgroup == "" {
		s.send(conn, "441 cancel: no Newsgroups header")
		return
	}
	if !auth.ValidGroupName(postgroup) {
		s.send(conn, "441 cancel: invalid group name")
		return
	}
	if !s.canPost(session, postgroup, true) {
		s.log.LogAuth("cancel denied group=%q user=%q from %s", postgroup, sessionUsername(session), remoteAddr)
		s.send(conn, "480 Authentication required")
		return
	}

	var g group.Group
	if g.LoadInfoFull(s.cfg, postgroup, s.log) != nil {
		s.send(conn, fmt.Sprintf("441 cancel: %s", g.Errmsg))
		return
	}

	artnum, err := g.FindArticleByMessageID(s.cfg, cancelMsgID)
	if err != nil {
		s.send(conn, "441 cancel: article not found")
		return
	}

	// Validate From: matches the original article's From:
	cancelFrom, _ := group.GetHeaderValue(headers, "From:")
	origFrom := article.GetRawHeader(s.cfg, postgroup, artnum, "From")
	if cancelFrom == "" || origFrom == "" || !strings.EqualFold(strings.TrimSpace(cancelFrom), strings.TrimSpace(origFrom)) {
		s.log.LogAuth("cancel rejected msgid=%s group=%q user=%q from %s (From mismatch)", cancelMsgID, postgroup, sessionUsername(session), remoteAddr)
		s.send(conn, "441 cancel: permission denied (From does not match)")
		return
	}

	if err := g.DeleteArticle(s.cfg, artnum); err != nil {
		s.log.Log(config.LogError, "cancel: delete article %d in %s: %v", artnum, postgroup, err)
		s.send(conn, "441 cancel: failed to delete article")
		return
	}

	s.log.LogAuth("cancel ok msgid=%s group=%q artnum=%d user=%q from %s", cancelMsgID, postgroup, artnum, sessionUsername(session), remoteAddr)
	s.send(conn, "240 Article cancelled successfully")
}

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
