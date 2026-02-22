//
// Copyright © 2026 Runable.app. GPL-3.0.
//
// utf8test posts an article with Korean UTF-8 content and reads it back.
// Usage:
//
//	utf8test [group]
//	NNTP_ADDR=host:port (default 127.0.0.1:1119)
//	AUTH_USER=email  AUTH_PASS=password  (optional)
//

package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

func main() {
	addr := "127.0.0.1:1119"
	if a := os.Getenv("NNTP_ADDR"); a != "" {
		addr = a
	}

	group := "test.public"
	if len(os.Args) >= 2 && os.Args[1] != "" {
		group = os.Args[1]
	}

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	r := bufio.NewReader(conn)

	// Helper functions
	readLine := func() string {
		line, _ := r.ReadString('\n')
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		fmt.Printf("< %s\n", line)
		return line
	}

	writeLine := func(s string) {
		fmt.Printf("> %s\n", s)
		conn.Write([]byte(s + "\r\n"))
	}

	// Korean test content
	koreanSubject := "한글 테스트 - UTF-8 Test"
	koreanBody := "안녕하세요! 이것은 한글 테스트입니다.\nHello! This is a Korean UTF-8 test."
	msgID := fmt.Sprintf("<utf8-test-%d@gotest>", time.Now().Unix())

	fmt.Println("=== UTF-8 Test: Posting Korean content ===")
	fmt.Println()
	fmt.Printf("Subject bytes: %x\n", []byte(koreanSubject))
	fmt.Printf("Body bytes (first line): %x\n", []byte("안녕하세요!"))
	fmt.Println()

	// Read greeting
	readLine()

	// MODE READER
	writeLine("MODE READER")
	readLine()

	// Auth if provided
	user := os.Getenv("AUTH_USER")
	pass := os.Getenv("AUTH_PASS")
	if user != "" && pass != "" {
		writeLine("AUTHINFO USER " + user)
		resp := readLine()
		if strings.HasPrefix(resp, "381") {
			writeLine("AUTHINFO PASS " + pass)
			readLine()
		}
	}

	// POST
	writeLine("POST")
	resp := readLine()
	if !strings.HasPrefix(resp, "340") {
		fmt.Fprintf(os.Stderr, "POST failed: %s\n", resp)
		os.Exit(1)
	}

	// Send article with UTF-8 content
	writeLine("From: utf8test@test.local")
	writeLine("Newsgroups: " + group)
	writeLine("Subject: " + koreanSubject)
	writeLine("Message-ID: " + msgID)
	writeLine("Content-Type: text/plain; charset=UTF-8")
	writeLine("")
	for _, line := range strings.Split(koreanBody, "\n") {
		writeLine(line)
	}
	writeLine(".")

	resp = readLine()
	if strings.HasPrefix(resp, "240") {
		fmt.Println("\n✓ POST succeeded!")
	} else {
		fmt.Printf("\n✗ POST failed: %s\n", resp)
		os.Exit(1)
	}

	// Read it back
	fmt.Println("\n=== Reading article back ===")
	writeLine("GROUP " + group)
	readLine()

	writeLine("ARTICLE " + msgID)
	resp = readLine()
	if strings.HasPrefix(resp, "220") {
		fmt.Println("\n=== Article content (check for Korean): ===")
		for {
			line := readLine()
			if line == "." {
				break
			}
			// Check if line contains Korean
			if strings.Contains(line, "한글") || strings.Contains(line, "안녕") {
				fmt.Println("✓ Korean text preserved!")
			}
			if strings.Contains(line, "???") {
				fmt.Println("✗ Found '???' - UTF-8 was corrupted!")
			}
		}
	} else {
		fmt.Printf("Could not retrieve article: %s\n", resp)
	}

	writeLine("QUIT")
	readLine()

	fmt.Println("\n=== Test complete ===")
}
