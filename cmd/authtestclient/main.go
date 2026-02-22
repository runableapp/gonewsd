//
// Copyright © 2026 Runable.app. GPL-3.0.
//
// authtestclient runs NNTP commands against a server for auth testing.
// Optional AUTHINFO USER/PASS via env AUTH_USER and AUTH_PASS.
// Usage:
//
//	authtestclient [group_to_select [group_to_post]]
//	NNTP_ADDR=host:port (default 127.0.0.1:1119)
//	AUTH_USER=email  AUTH_PASS=password  (optional)
//
// If group_to_select is set, runs GROUP then ARTICLE 1 (or HEAD 1).
// If group_to_post is set, runs POST with a minimal article to that group.
// Prints each response code line to stdout; exits 0 on success.

package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// main connects to NNTP_ADDR, optionally authenticates with AUTH_USER/AUTH_PASS, runs LIST ACTIVE, GROUP, ARTICLE 1, and optionally POST to a group; prints response lines to stdout.
func main() {
	// The default NNTP address "127.0.0.1:1119" is used here for local testing.
	// It is NOT loaded from a config file; instead, it can be overridden via the NNTP_ADDR environment variable.
	// This client does not read any config file—it's intended for quick, direct testing against a running server.
	addr := "127.0.0.1:1119"
	if a := os.Getenv("NNTP_ADDR"); a != "" {
		addr = a
	}

	groupSelect := "test.public"
	if len(os.Args) >= 2 && os.Args[1] != "" {
		groupSelect = os.Args[1]
	}
	groupPost := ""
	if len(os.Args) >= 3 {
		groupPost = os.Args[2]
	}

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))

	c := &client{conn: conn, w: os.Stdout}
	if err := c.run(groupSelect, groupPost); err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}
}

type client struct {
	conn net.Conn
	w    io.Writer
	r    *bufio.Reader
}

// writeln writes a line to the connection with CRLF termination.
func (c *client) writeln(s string) {
	c.conn.Write([]byte(s + "\r\n"))
}

// readLine reads a line from the connection (up to newline); strips \r\n.
func (c *client) readLine() (string, error) {
	if c.r == nil {
		c.r = bufio.NewReader(c.conn)
	}
	line, err := c.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line, nil
}

// readResponse reads the first line and, for multi-line responses (215, 220, 221, 222), reads until "."; prints each line to c.w.
func (c *client) readResponse() (first string, err error) {
	first, err = c.readLine()
	if err != nil {
		return first, err
	}
	fmt.Fprintln(c.w, first)
	code := ""
	if len(first) >= 3 && first[0] >= '0' && first[0] <= '9' && first[1] >= '0' && first[1] <= '9' && first[2] >= '0' && first[2] <= '9' {
		code = first[:3]
	}
	// Multi-line responses ending with ".": 215 list, 220 article, 221 head, 222 body. 211 GROUP etc. are single-line.
	if len(code) == 3 && (code == "215" || code == "220" || code == "221" || code == "222") {
		for {
			raw, err := c.readLine()
			if err != nil {
				return first, err
			}
			// Terminator is single "."; ".." is dot-stuffed content line
			if raw == "." {
				fmt.Fprintln(c.w, raw)
				break
			}
			if strings.HasPrefix(raw, "..") {
				raw = raw[1:]
			}
			fmt.Fprintln(c.w, raw)
		}
	}
	return first, nil
}

// cmd sends a command and reads the response; returns the first response line.
func (c *client) cmd(s string) (string, error) {
	c.writeln(s)
	return c.readResponse()
}

// run runs the auth test flow: greeting, MODE READER, optional AUTHINFO, LIST ACTIVE, GROUP, ARTICLE 1, optional POST to groupPost, then QUIT.
func (c *client) run(groupSelect, groupPost string) error {
	// Greeting
	if _, err := c.readResponse(); err != nil {
		return err
	}

	// MODE READER
	if _, err := c.cmd("MODE READER"); err != nil {
		return err
	}

	// Optional auth
	user := os.Getenv("AUTH_USER")
	pass := os.Getenv("AUTH_PASS")
	if user != "" && pass != "" {
		first, err := c.cmd("AUTHINFO USER " + user)
		if err != nil {
			return err
		}
		if strings.HasPrefix(first, "381") {
			first, err = c.cmd("AUTHINFO PASS " + pass)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(first, "281") && !strings.HasPrefix(first, "250") {
				return fmt.Errorf("auth failed: %s", first)
			}
		}
	}

	// LIST ACTIVE
	first, err := c.cmd("LIST ACTIVE")
	if err != nil {
		return err
	}
	if strings.HasPrefix(first, "480") {
		// Auth required for LIST in private mode
		return nil
	}
	if !strings.HasPrefix(first, "215") {
		return fmt.Errorf("LIST ACTIVE: %s", first)
	}

	// GROUP
	first, err = c.cmd("GROUP " + groupSelect)
	if err != nil {
		return err
	}
	if strings.HasPrefix(first, "480") {
		return nil
	}
	if !strings.HasPrefix(first, "211") {
		return fmt.Errorf("GROUP %s: %s", groupSelect, first)
	}

	// ARTICLE 1 (or HEAD 1)
	first, err = c.cmd("ARTICLE 1")
	if err != nil {
		return err
	}
	if strings.HasPrefix(first, "423") || strings.HasPrefix(first, "430") {
		// No article 1 in group
	} else if strings.HasPrefix(first, "480") {
		return nil
	} else if !strings.HasPrefix(first, "220") {
		// Allow 220 (article follows) or 423/430
	}

	// POST to groupPost if requested
	if groupPost != "" {
		first, err = c.cmd("POST")
		if err != nil {
			return err
		}
		if !strings.HasPrefix(first, "340") {
			return fmt.Errorf("POST: %s", first)
		}
		postBody := []string{
			"From: authtest@test",
			"Newsgroups: " + groupPost,
			"Subject: auth test",
			"Message-ID: <auth-test-" + groupPost + "@test>",
			"Date: Mon, 01 Jan 2025 00:00:00 +0000",
			"",
			"auth test body",
			".",
		}
		for _, line := range postBody {
			if line == "." {
				c.writeln(".") // terminator: line containing only period
			} else if strings.HasPrefix(line, ".") {
				c.writeln("." + line) // dot-stuff content lines that start with .
			} else {
				c.writeln(line)
			}
		}
		first, err = c.readResponse()
		if err != nil {
			return err
		}
		if strings.HasPrefix(first, "480") {
			return nil
		}
		if !strings.HasPrefix(first, "240") {
			return fmt.Errorf("POST result: %s", first)
		}
	}

	// QUIT
	c.writeln("QUIT")
	c.readResponse()
	return nil
}
