// Copyright © 2026 Runable.app. GPL-3.0.
//
// nntpclient runs a fixed sequence of NNTP client commands against a server
// and prints all responses to stdout (for diff against newsd vs gonewsd).
//
// Usage:
//
//	nntpclient [options]
//	NNTP_ADDR=host:port  (default 127.0.0.1:1119)
//	NNTP_OUT=path        write output to file instead of stdout
//	-no-post             skip POST and NOTACOMMAND (read-only comparison)
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// main parses flags and env (NNTP_ADDR, NNTP_OUT), connects to the server, runs a fixed NNTP command sequence, and writes responses to stdout or a file.
func main() {
	noPost := flag.Bool("no-post", false, "skip POST and NOTACOMMAND for read-only comparison")
	flag.Parse()

	addr := "127.0.0.1:1119"
	if a := os.Getenv("NNTP_ADDR"); a != "" {
		addr = a
	}
	out := os.Stdout
	if path := os.Getenv("NNTP_OUT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create %s: %v\n", path, err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer conn.Close()
	conn.SetDeadline(time.Time{}) // no global deadline; use per-read where needed

	c := &client{conn: conn, out: out, noPost: *noPost}
	if err := c.run(); err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}
}

type client struct {
	conn   net.Conn
	out    io.Writer
	buf    []byte // collect responses for comparison
	noPost bool
}

// writeln writes a line to the connection with CRLF termination.
func (c *client) writeln(s string) {
	c.conn.Write([]byte(s + "\r\n"))
}

// readLine reads bytes until newline (consumes \r); returns the line without terminator.
func (c *client) readLine() (string, error) {
	var line []byte
	for {
		b := make([]byte, 1)
		if _, err := c.conn.Read(b); err != nil {
			return string(line), err
		}
		if b[0] == '\n' {
			break
		}
		if b[0] != '\r' {
			line = append(line, b[0])
		}
		if len(line) >= 4096 {
			break
		}
	}
	return string(line), nil
}

// readResponse reads one response (single line or multi-line until ".").
// Appends normalized lines to c.buf (one line per entry, no \r).
func (c *client) readResponse() error {
	line, err := c.readLine()
	if err != nil {
		return err
	}
	line = strings.TrimSuffix(line, "\r")
	c.buf = append(c.buf, line...)
	c.buf = append(c.buf, '\n')

	code := ""
	if len(line) >= 3 && line[0] >= '0' && line[0] <= '9' && line[1] >= '0' && line[1] <= '9' && line[2] >= '0' && line[2] <= '9' {
		code = line[:3]
	}
	// Multi-line: 100, 215, 224, 231, 202, etc.
	if len(code) == 3 && (code[0] == '1' || code[0] == '2' && (code == "215" || code == "224" || code == "231" || code == "202")) {
		for {
			line, err = c.readLine()
			if err != nil {
				return err
			}
			line = strings.TrimSuffix(line, "\r")
			if line == "." {
				c.buf = append(c.buf, ".\n"...)
				break
			}
			// Dot-stuffing: leading . becomes ..
			if strings.HasPrefix(line, "..") {
				line = line[1:]
			}
			c.buf = append(c.buf, line...)
			c.buf = append(c.buf, '\n')
		}
	}
	return nil
}

// cmd sends a command line and reads the full response (single or multi-line until ".").
func (c *client) cmd(cmd string) error {
	c.writeln(cmd)
	return c.readResponse()
}

// flush writes accumulated response lines to the output writer and clears the buffer.
func (c *client) flush() {
	c.out.Write(c.buf)
	c.buf = c.buf[:0]
}

// run performs the fixed NNTP command sequence: greeting, MODE, LIST variants, LISTGROUP, XOVER, GROUP, HEAD, NEXT, HELP, NEWGROUPS, DATE; optionally POST and NOTACOMMAND; then QUIT. Responses are buffered and flushed at the end.
func (c *client) run() error {
	// Greeting
	if err := c.readResponse(); err != nil {
		return err
	}

	// Fixed sequence of commands to match server-request-handling-comparison
	cmds := []struct {
		cmd string
	}{
		{"MODE READER"},
		{"MODE STREAM"},
		{"LIST EXTENSIONS"},
		{"LIST ACTIVE"},
		{"LIST ACTIVE foo.*"},
		{"LIST ACTIVE.TIMES"},
		{"LIST NEWSGROUPS"},
		{"LIST OVERVIEW.FMT"},
		{"LIST SUBSCRIPTIONS"},
		{"LIST BADSUB"},
		{"LISTGROUP"},
		{"LISTGROUP test.group1"},
		{"XREPLIC"},
		{"XOVER 1-"},
		{"GROUP test.group1"},
		{"LISTGROUP"},
		{"XOVER 1-"},
		{"HEAD 1"},
		{"NEXT"},
		{"HELP"},
		{"NEWGROUPS"},
		{"NEWGROUPS 1 2"},
		{"NEWGROUPS 250101 000000"},
		{"NEWNEWS"},
		{"DATE"},
	}
	if !c.noPost {
		cmds = append(cmds, struct{ cmd string }{"POST"})
	}
	for _, t := range cmds {
		if err := c.cmd(t.cmd); err != nil {
			return fmt.Errorf("after %q: %w", t.cmd, err)
		}
	}

	if !c.noPost {
		// POST body (after 340): send line by line, end with \r\n.\r\n
		postLines := []string{
			"From: test@localhost",
			"Newsgroups: test.group1",
			"Subject: test",
			"Message-ID: <test-client-1@localhost>",
			"Date: Mon, 01 Jan 2025 00:00:00 +0000",
			"",
			"test body",
			".",
		}
		for _, l := range postLines {
			c.writeln(l)
		}
		if err := c.readResponse(); err != nil {
			return fmt.Errorf("after POST body: %w", err)
		}

		// Unhandled command
		if err := c.cmd("NOTACOMMAND"); err != nil {
			return fmt.Errorf("after NOTACOMMAND: %w", err)
		}
	}

	// QUIT
	c.writeln("QUIT")
	if err := c.readResponse(); err != nil && err != io.EOF {
		return fmt.Errorf("after QUIT: %w", err)
	}
	// May get 205 then connection close; ensure we have 205 in buffer
	if len(c.buf) > 0 && !bytes.HasSuffix(c.buf, []byte("goodbye.\n")) {
		line, _ := c.readLine()
		line = strings.TrimSuffix(line, "\r")
		c.buf = append(c.buf, line...)
		c.buf = append(c.buf, '\n')
	}

	c.flush()
	return nil
}
