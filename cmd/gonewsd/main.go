//
// Copyright © 2026 Runable.app. GPL-3.0.
//
// gonewsd is the main entry point for the NNTP news server (Go port of newsd).
// It parses flags and config, starts the server or runs subcommands: mailgateway,
// rotate, and auth CLI (adduser, listuser, addgroup, etc.). Handles daemonization,
// SIGHUP auth reload, and graceful shutdown.

package main

import (
	"bufio"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	"gonewsd/internal/auth"
	"gonewsd/internal/config"
	"gonewsd/internal/group"
	"gonewsd/internal/logging"
	"gonewsd/internal/nntp"
)

//go:embed resources/help.txt
var helpText string

//go:embed resources/VERSION.txt
var versionText string

// version returns the embedded version string (from resources/VERSION.txt).
func version() string { return strings.TrimSpace(versionText) }

// main parses flags and config, then either runs a subcommand (help, version, auth CLI, mailgateway, rotate) or starts the NNTP server, handles SIGHUP/SIGINT/SIGTERM, and shuts down gracefully.
func main() {
	// Handle "help" and "version" subcommands before flags (they are not flags).
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "help":
			fmt.Print(helpText)
			os.Exit(0)
		case "version":
			fmt.Println("gonewsd v" + version())
			fmt.Println("Copyright © 2026 Runable.app. GPL-3.0.")
			os.Exit(0)
		}
	}

	conffile := flag.String("c", "/etc/gonewsd.conf", "config file")
	dodebug := flag.Bool("d", false, "debug mode (log to stderr)")
	dobackground := flag.Bool("b", false, "background mode (fork); not recommended on systemd/Ubuntu")
	preservedate := flag.Bool("preserve-date", false, "preserve original Date in mailgateway/post")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, "Run 'gonewsd help' for usage.\n")
	}
	flag.Parse()
	// Subcommands: auth CLI, mailgateway, rotate
	if n := len(flag.Args()); n >= 1 {
		sub := flag.Arg(0)
		switch sub {
		case "adduser", "listuser", "deleteuser", "updateuser", "addgroup", "deletegroup", "updategroup", "listgroup":
			cfg := config.DefaultConfig()
			if err := cfg.Load(*conffile); err != nil {
				fmt.Fprintf(os.Stderr, "🛑 Error: %v\n", err)
				os.Exit(1)
			}
			runAuthCLI(cfg, sub, flag.Args()[1:])
			os.Exit(0)
		case "mailgateway":
			if len(flag.Args()) < 2 {
				fmt.Fprintf(os.Stderr, "🛑 Error: mailgateway requires a group name\n")
				fmt.Fprintf(os.Stderr, "Run 'gonewsd help' for usage.\n")
				os.Exit(1)
			}
			groupName := flag.Arg(1)
			cfg := config.DefaultConfig()
			if err := cfg.Load(*conffile); err != nil {
				fmt.Fprintf(os.Stderr, "🛑 Error: %v\n", err)
				os.Exit(1)
			}
			log := logging.NewLogger(cfg)
			if runAs(cfg) != nil {
				os.Exit(1)
			}
			if mailGateway(cfg, log, groupName, *preservedate) != nil {
				os.Exit(1)
			}
			os.Exit(0)
		case "rotate":
			cfg := config.DefaultConfig()
			if err := cfg.Load(*conffile); err != nil {
				fmt.Fprintf(os.Stderr, "🛑 Error: %v\n", err)
				os.Exit(1)
			}
			log := logging.NewLogger(cfg)
			if err := log.Init(); err != nil {
				fmt.Fprintf(os.Stderr, "🛑 Error: %v\n", err)
				os.Exit(1)
			}
			log.Lock()
			log.RotateWithLock(true)
			log.Unlock()
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "🛑 Error: unknown argument %q\n", sub)
		flag.Usage()
		os.Exit(1)
	}

	cfg := config.DefaultConfig()
	if err := cfg.Load(*conffile); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: %v\n", err)
		os.Exit(1)
	}

	log := logging.NewLogger(cfg)
	if *dodebug {
		log.SetLevel(config.LogDebug)
		cfg.ErrorLog = "stderr"
	}

	// Default is foreground. Use -b to fork into background (not recommended on systemd).
	dofork := *dobackground
	if *dodebug {
		dofork = false // debug mode always runs in foreground
	}

	if err := log.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: %v\n", err)
		os.Exit(1)
	}
	log.Log(config.LogInfo, "-- gonewsd started - V%s --", version())
	log.Log(config.LogInfo, "-- start config summary --")
	log.LogSelf(cfg)
	log.Log(config.LogInfo, "-- end config summary --")

	authStore := loadAuthStore(cfg, log)
	srv := nntp.NewServer(cfg, log, authStore)
	if err := srv.Listen(); err != nil {
		log.Log(config.LogError, "Unable to listen for connections: %v", err)
		os.Exit(1)
	}

	// Pid file when configured; used by auth CLI to send SIGHUP reload.
	if cfg.PidFile != "" {
		if err := os.WriteFile(cfg.PidFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644); err != nil {
			log.Log(config.LogError, "write pid file %q: %v", cfg.PidFile, err)
		}
		defer os.Remove(cfg.PidFile)
	}

	if runAs(cfg) != nil {
		os.Exit(1)
	}

	if dofork {
		// Daemonize: close stdio, detach from terminal
		if err := daemonize(); err != nil {
			log.Log(config.LogError, "daemonize: %v", err)
			os.Exit(1)
		}
	}

	// Register for shutdown signals before starting accept loop so Ctrl-C (SIGINT) is handled
	sigCh := make(chan os.Signal, 1)
	notifySignals(sigCh)
	defer signal.Stop(sigCh)

	// Accept loop (each connection handled in goroutine by Accept)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, err := srv.Accept()
			if err != nil {
				if !isClosed(err) {
					log.Log(config.LogError, "Accept: %v", err)
				}
				return
			}
		}
	}()

	// Wait for shutdown or SIGHUP (reload auth)
	for {
		sig := <-sigCh
		if isReloadSignal(sig) {
			log.Log(config.LogInfo, "SIGHUP: reloading auth store")
			if st := loadAuthStore(cfg, log); st != nil {
				srv.SetAuthStore(st)
			}
			continue
		}
		signal.Stop(sigCh)
		log.Log(config.LogInfo, "Received %v, shutting down gracefully", sig)
		break
	}

	// Close listener so Accept() returns and accept goroutine exits
	if err := srv.Close(); err != nil {
		log.Log(config.LogError, "Close listener: %v", err)
	}
	// Close all client connections so handleConn goroutines (e.g. blocked in POST read) exit
	srv.CloseAllConns()
	<-done

	// Flush and close log (and any open log file)
	if err := log.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: log close: %v\n", err)
	}
	os.Exit(0)
}

// loadAuthStore opens the auth DB (if configured), loads users and ACLs into memory, and returns the store.
// If auth.db is set but the file does not exist, it is created with the proper schema (empty users/ACLs).
func loadAuthStore(cfg *config.Config, log *logging.Logger) *auth.Store {
	if cfg.AuthDB == "" {
		return nil
	}
	_, err := os.Stat(cfg.AuthDB)
	createIfMissing := err != nil && os.IsNotExist(err)
	if err != nil && !os.IsNotExist(err) {
		log.Log(config.LogError, "auth: stat DB %q: %v", cfg.AuthDB, err)
		return nil
	}

	readOnly := !createIfMissing
	db, err := auth.OpenDB(cfg.AuthDB, readOnly)
	if err != nil {
		log.Log(config.LogError, "auth: open DB %q: %v", cfg.AuthDB, err)
		return nil
	}
	defer db.Close()

	if createIfMissing {
		if err := auth.EnsureSchema(db); err != nil {
			log.Log(config.LogError, "auth: ensure schema: %v", err)
			return nil
		}
	}

	st := auth.NewStore(cfg.AuthMode)
	if err := auth.LoadStoreFromDB(db, st); err != nil {
		log.Log(config.LogError, "auth: load from DB: %v", err)
		return nil
	}
	return st
}

// isClosed returns true if the error is from a closed listener (e.g. after Close()).
func isClosed(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, net.ErrClosed) ||
		strings.Contains(err.Error(), "use of closed network connection") ||
		strings.Contains(err.Error(), "closed")
}

// createSpoolDir creates the spool directory and chowns it to Config.User when running as root.
func createSpoolDir(cfg *config.Config) error {
	if err := os.MkdirAll(cfg.SpoolDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: mkdir %q: %v\n", cfg.SpoolDir, err)
		return err
	}
	if isRootUser() && !cfg.BadUser {
		if err := os.Chown(cfg.SpoolDir, int(cfg.UID), int(cfg.GID)); err != nil {
			fmt.Fprintf(os.Stderr, "🛑 Error: chown %q: %v\n", cfg.SpoolDir, err)
			return err
		}
	}
	return nil
}

// mailGateway reads an email from stdin and posts it to the given group; optionally sends a BCC via SendMail (CCPost).
func mailGateway(cfg *config.Config, log *logging.Logger, groupname string, preservedate bool) error {
	if !auth.ValidGroupName(groupname) {
		fmt.Fprintf(os.Stderr, "🛑 Error: Invalid group name %q\n", groupname)
		return fmt.Errorf("invalid group name")
	}
	var g group.Group
	if err := g.LoadInfoFull(cfg, groupname, log); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: Unknown group %q: %s\n", groupname, g.Errmsg)
		return err
	}

	var msg strings.Builder
	msg.WriteString("Newsgroups: ")
	msg.WriteString(groupname)
	msg.WriteString("\r\n")
	msg.WriteString("X-Mail-To-News-Gateway: via gonewsd ")
	msg.WriteString(version())
	msg.WriteString("\r\n")

	linechars := 0
	linecount := 0
	toolong := false
	br := bufio.NewReader(os.Stdin)
	for {
		c, err := br.ReadByte()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if c == '\r' {
			continue
		}
		linechars++
		if linechars > 80 || c == '\n' {
			linechars = 0
			linecount++
		}
		if g.PostLimit > 0 && linecount > g.PostLimit {
			toolong = true
			continue
		}
		if c == '\n' {
			msg.WriteByte('\r')
		}
		msg.WriteByte(c)
	}

	if toolong {
		fmt.Fprintf(os.Stderr, "🛑 Error: Article not posted to %s: longer than %d lines.\n", groupname, g.PostLimit)
		return fmt.Errorf("article too long")
	}

	headers, body := group.ParseArticle(msg.String())

	// Loop detection
	for i, h := range headers {
		if len(h) >= 5 && strings.EqualFold(h[:5], "From ") {
			headers[i] = "X-Original-From: " + strings.TrimSpace(h[5:])
		}
		if (len(h) >= 14 && strings.EqualFold(h[:14], "X-Newsd-Loop:")) ||
			(len(h) >= 8 && strings.EqualFold(h[:8], "X-Loop:")) {
			fmt.Fprintf(os.Stderr, "🛑 Error: mail loop detected, message dropped\n")
			return fmt.Errorf("mail loop")
		}
	}

	group.UpdatePath(cfg, &headers)

	if err := g.Post(cfg, nntp.OverviewFormat, headers, body, "localhost", true, preservedate, log); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Error: Article not posted to %s: %s\n", groupname, g.Errmsg)
		return err
	}

	if g.IsCCPost() {
		if err := ccPostToMail(cfg, log, &g, headers, body); err != nil {
			log.Log(config.LogError, "mailgateway: ccpost: %v", err)
		}
	}
	return nil
}

// ccPostToMail runs SendMail and pipes preserved headers + body (same as newsd MailGateway IsCCPost).
func ccPostToMail(cfg *config.Config, log *logging.Logger, g *group.Group, headers, body []string) error {
	preserve := buildPreserveHeaders(headers)
	parts := strings.Fields(cfg.SendMail)
	if len(parts) == 0 {
		return fmt.Errorf("SendMail not set")
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("can't execute %q: %w", cfg.SendMail, err)
	}
	fmt.Fprintf(stdin, "To: %s\n", g.VoidEmail)
	fmt.Fprintf(stdin, "Bcc: %s\n", g.CCPost)
	stdin.Write([]byte(preserve))
	if g.IsReplyTo() {
		fmt.Fprintf(stdin, "Reply-To: %s\n", g.ReplyTo)
	}
	fmt.Fprintf(stdin, "Errors-To: %s\n", g.Creator)
	fmt.Fprintf(stdin, "\n[posted to %s]\n\n", g.Name)
	for _, line := range body {
		fmt.Fprintln(stdin, line)
	}
	if err := stdin.Close(); err != nil {
		cmd.Process.Kill()
		return err
	}
	if err := cmd.Wait(); err != nil {
		log.Log(config.LogError, "mailgateway: ccpost pclose failed for %q: %v", cfg.SendMail, err)
		return err
	}
	return nil
}

// buildPreserveHeaders returns preserved header lines (From, Subject, References, Xref, Path, Content-Type, MIME-Version, Message-ID) including continuations.
func buildPreserveHeaders(headers []string) string {
	var preserve strings.Builder
	preserveNames := []string{"From:", "Subject:", "References:", "Xref:", "Path:", "Content-Type:", "MIME-Version:", "Message-ID:"}
	pflag := false
	for _, h := range headers {
		if h == "" {
			pflag = false
			continue
		}
		first := h[0]
		if first == ' ' || first == '\t' {
			if pflag {
				preserve.WriteString(h)
				preserve.WriteByte('\n')
			}
			continue
		}
		pflag = false
		for _, name := range preserveNames {
			if len(h) >= len(name) && strings.EqualFold(h[:len(name)], name) {
				preserve.WriteString(h)
				preserve.WriteByte('\n')
				pflag = true
				break
			}
		}
	}
	return preserve.String()
}
