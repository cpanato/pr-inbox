package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/cpanato/pr-inbox/internal/auth"
	"github.com/cpanato/pr-inbox/internal/demo"
	"github.com/cpanato/pr-inbox/internal/github"
	"github.com/cpanato/pr-inbox/internal/inbox"
	"github.com/cpanato/pr-inbox/internal/server"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("organization cannot be empty")
	}
	*s = append(*s, value)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pr-inbox:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	doctorCommand := len(args) > 0 && args[0] == "doctor"
	if doctorCommand {
		args = args[1:]
	}
	flags := flag.NewFlagSet("pr-inbox", flag.ContinueOnError)
	noOpen := flags.Bool("no-open", false, "print the local URL without opening a browser")
	readOnly := flags.Bool("read-only", false, "disable write operations (the initial inbox is read-only)")
	doctor := flags.Bool("doctor", false, "check GitHub CLI authentication and GitHub connectivity")
	var orgs stringList
	flags.Var(&orgs, "org", "limit discovery to an organization; may be repeated")
	demoMode := flags.Bool("demo", false, "use built-in sample data instead of GitHub (no authentication needed)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	*doctor = *doctor || doctorCommand
	if *demoMode && *doctor {
		return errors.New("--demo cannot be combined with doctor")
	}

	var searcher inbox.Searcher
	var viewer github.Viewer
	if *demoMode {
		searcher = demo.New()
		viewer = github.Viewer{ID: 1, Login: demo.Login}
	} else {
		token, err := auth.Token(context.Background())
		if err != nil {
			if *doctor {
				return err
			}
			return fmt.Errorf("%w; run `gh auth login` and try again", err)
		}
		client := github.NewClient(token)
		viewer, err = client.Viewer(context.Background())
		if err != nil {
			if *doctor {
				return fmt.Errorf("GitHub connectivity: %w", err)
			}
			return err
		}
		searcher = client
	}
	if *doctor {
		fmt.Printf("GitHub CLI: available\nGitHub account: %s\nGitHub API: connected\n", viewer.Login)
		return nil
	}

	inboxService := inbox.New(searcher, viewer, orgs)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen on loopback: %w", err)
	}
	handler, launchURL, err := server.New(inboxService, *readOnly, listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		return err
	}

	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()

	fmt.Printf("PR Inbox for @%s: %s\nPress Ctrl+C to stop.\n", viewer.Login, launchURL)
	if !*noOpen {
		if err := openBrowser(launchURL); err != nil {
			log.Printf("could not open browser automatically: %v", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("stop server: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve local inbox: %w", err)
	}
}

func openBrowser(url string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "open", []string{url}
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		command, args = "xdg-open", []string{url}
	}
	cmd := exec.Command(command, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("browser launch command failed: %v", err)
		}
	}()
	return nil
}
