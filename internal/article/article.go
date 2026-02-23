// Copyright © 2026 Runable.app. GPL-3.0.
//
// Package article handles NNTP article storage and retrieval. It loads articles
// from the spool by group and number, parses headers (From, Date, Message-ID,
// Subject, etc.), and can send header/body with dot-stuffing and CRLF. Also
// provides overview lines for XOVER and path resolution for spool layout.
package article

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"gonewsd/internal/config"
	"gonewsd/internal/logging"
)

const (
	lineLen  = 4096
	fieldMax = 1024
)

// Article represents a news article and its parsed headers.
type Article struct {
	Group      string
	Filename   string
	Number     uint64
	Valid      bool
	From       string
	Date       string
	MessageID  string
	Subject    string
	References string
	Xref       string
	Lines      int
	Errmsg     string
}

// GetArticlePath returns the filesystem path for an article in a group.
func GetArticlePath(cfg *config.Config, group string, artnum uint64) string {
	pathGroup := strings.ReplaceAll(group, ".", string(filepath.Separator))
	base := filepath.Join(cfg.SpoolDir, pathGroup)
	if cfg.MsgModDirs {
		mod := (artnum / 1000) * 1000
		return filepath.Join(base, strconv.FormatUint(mod, 10), strconv.FormatUint(artnum, 10))
	}
	return filepath.Join(base, strconv.FormatUint(artnum, 10))
}

// Load reads an article from disk and parses headers.
func (a *Article) Load(cfg *config.Config, group string, num uint64) error {
	a.Group = group
	a.Number = num
	a.Valid = false
	a.From = ""
	a.Date = ""
	a.MessageID = ""
	a.Subject = ""
	a.References = ""
	a.Xref = ""
	a.Lines = 0
	a.Errmsg = ""

	if len(group) >= 1024 {
		a.Errmsg = "group name too long"
		return fmt.Errorf("%s", a.Errmsg)
	}

	a.Filename = GetArticlePath(cfg, group, num)
	f, err := os.Open(a.Filename)
	if err != nil {
		a.Errmsg = fmt.Sprintf("article %d no longer exists", num)
		return err
	}
	defer f.Close()

	var key, val string
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, lineLen*2)

	for scanner.Scan() {
		line := scanner.Text()
		// Truncate at first CR or LF
		if i := strings.IndexAny(line, "\r\n"); i >= 0 {
			line = line[:i]
		}

		if len(line) == 0 {
			if key != "" {
				a.parseHeader(key, val)
			}
			break
		}

		first := rune(line[0])
		if first == '\t' || first == ' ' {
			val += line
			if len(val) > fieldMax {
				val = val[:fieldMax]
			}
			continue
		}

		if key != "" {
			a.parseHeader(key, val)
		}
		key, val = splitKeyValue(line)
	}

	if err := scanner.Err(); err != nil {
		a.Errmsg = err.Error()
		return err
	}

	if a.MessageID == "" {
		a.Errmsg = "No 'Message-ID' field"
		return fmt.Errorf("%s", a.Errmsg)
	}
	if a.From == "" {
		a.Errmsg = "No 'From' field"
		return fmt.Errorf("%s", a.Errmsg)
	}

	a.Valid = true
	return nil
}

// splitKeyValue splits a "Key: value" header line into key (including ":") and trimmed value.
func splitKeyValue(s string) (key, val string) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return "", ""
	}
	key = s[:i+1]
	val = strings.TrimLeftFunc(s[i+1:], unicode.IsSpace)
	return key, val
}

// parseHeader applies a single header key/value to the Article (Subject, From, Date, Xref, Message-ID, References, Lines).
func (a *Article) parseHeader(key, val string) {
	keyLower := strings.ToLower(strings.TrimSuffix(key, ":")) + ":"
	switch keyLower {
	case "subject:":
		a.Subject = val
	case "from:":
		a.From = val
	case "date:":
		a.Date = val
	case "xref:":
		a.Xref = val
	case "message-id:":
		a.MessageID = val
	case "references:":
		a.References = val
	case "lines:":
		a.Lines, _ = strconv.Atoi(val)
	}
}

// SendArticle writes the article to w with dot-stuffing and CRLF.
// If log != nil, logs each line sent (same as newsd Article::SendArticle L_DEBUG "SEND: %s").
func (a *Article) SendArticle(w io.Writer, sendHead, sendBody bool, log *logging.Logger) error {
	f, err := os.Open(a.Filename)
	if err != nil {
		a.Errmsg = fmt.Sprintf("article %d no longer exists", a.Number)
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, lineLen*2)

	mode := "head"
	for scanner.Scan() {
		line := scanner.Text()
		if i := strings.IndexAny(line, "\r\n"); i >= 0 {
			line = line[:i]
		}
		if len(line) > lineLen-4 {
			line = line[:lineLen-4]
		}

		if mode == "sep" {
			mode = "body"
		}
		if mode == "head" && line == "" {
			mode = "sep"
			if !sendBody {
				break
			}
			if sendHead && sendBody {
				if _, err := w.Write([]byte("\r\n")); err != nil {
					return err
				}
			}
			continue
		}

		send := (mode == "head" && sendHead) || (mode == "body" && sendBody) || (mode == "sep" && sendHead && sendBody)
		if !send {
			continue
		}

		// Line to write (dot-stuff if line starts with .)
		sw := line
		if strings.HasPrefix(line, ".") {
			sw = "." + line
		}
		if log != nil {
			log.Log(config.LogDebug, "SEND: %s", sw)
		}
		out := sw + "\r\n"
		if _, err := w.Write([]byte(out)); err != nil {
			return err
		}
	}

	return scanner.Err()
}

// SendHead writes only the header to w.
func (a *Article) SendHead(w io.Writer, log *logging.Logger) error {
	return a.SendArticle(w, true, false, log)
}

// SendBody writes only the body to w.
func (a *Article) SendBody(w io.Writer, log *logging.Logger) error {
	return a.SendArticle(w, false, true, log)
}

// sanitizeOverview replaces tabs in overview fields with spaces for XOVER output.
func sanitizeOverview(s string) string {
	return strings.ReplaceAll(s, "\t", " ")
}

// Overview returns a tab-separated overview line for XOVER (same as newsd Article::Overview).
// Only outputs tab+value for fields newsd handles; Reply-To has no branch in newsd so we output nothing for it.
func (a *Article) Overview(overviewFormat []string) string {
	var b strings.Builder
	b.WriteString(strconv.FormatUint(a.Number, 10))
	for _, name := range overviewFormat {
		nameLower := strings.ToLower(strings.TrimSuffix(name, ":"))
		switch nameLower {
		case "subject":
			b.WriteByte('\t')
			b.WriteString(sanitizeOverview(a.Subject))
		case "from":
			b.WriteByte('\t')
			b.WriteString(sanitizeOverview(a.From))
		case "date":
			b.WriteByte('\t')
			b.WriteString(sanitizeOverview(a.Date))
		case "message-id":
			b.WriteByte('\t')
			b.WriteString(sanitizeOverview(a.MessageID))
		case "references":
			b.WriteByte('\t')
			b.WriteString(sanitizeOverview(a.References))
		case "lines":
			b.WriteByte('\t')
			if a.Lines > 0 {
				b.WriteString(strconv.Itoa(a.Lines))
			}
		case "bytes":
			b.WriteByte('\t')
		case "xref:full":
			b.WriteByte('\t')
			if a.Xref != "" {
				b.WriteString("Xref: ")
				b.WriteString(sanitizeOverview(a.Xref))
			}
		case "reply-to":
			// newsd has no branch for Reply-To:; output nothing
		}
	}
	return b.String()
}

// GetRawHeader reads an article file and returns the value of the given header field.
// Field name matching is case-insensitive. Returns empty string if not found.
func GetRawHeader(cfg *config.Config, group string, artnum uint64, field string) string {
	path := GetArticlePath(cfg, group, artnum)
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	target := strings.ToLower(field)
	if !strings.HasSuffix(target, ":") {
		target += ":"
	}

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, lineLen*2)

	var val string
	var matched bool
	for scanner.Scan() {
		line := scanner.Text()
		if i := strings.IndexAny(line, "\r\n"); i >= 0 {
			line = line[:i]
		}
		if line == "" {
			break
		}
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			if matched {
				val += " " + strings.TrimSpace(line)
			}
			continue
		}
		if matched {
			return val
		}
		if len(line) >= len(target) && strings.ToLower(line[:len(target)]) == target {
			val = strings.TrimSpace(line[len(target):])
			matched = true
		}
	}
	if matched {
		return val
	}
	return ""
}
