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
	"github.com/cpanato/pr-inbox/internal/gitlab"
	"github.com/cpanato/pr-inbox/internal/inbox"
	"github.com/cpanato/pr-inbox/internal/provider"
	"github.com/cpanato/pr-inbox/internal/server"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("value cannot be empty")
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
	flags.Var(&orgs, "org", "limit GitHub discovery to an organization; may be repeated")
	var gitlabHosts stringList
	flags.Var(&gitlabHosts, "gitlab", "also show merge requests from a GitLab host such as gitlab.com or gitlab.example.com; may be repeated")
	noGitHub := flags.Bool("no-github", false, "do not connect to GitHub")
	noGitLab := flags.Bool("no-gitlab", false, "do not connect to gitlab.com automatically")
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

	if *demoMode && (len(gitlabHosts) > 0 || *noGitHub || *noGitLab) {
		return errors.New("--demo cannot be combined with --gitlab, --no-github or --no-gitlab")
	}
	if *noGitHub && *noGitLab && len(gitlabHosts) == 0 {
		return errors.New("--no-github and --no-gitlab leave nothing to show; add a --gitlab host or drop a flag")
	}

	var services []*inbox.Service
	if *demoMode {
		services = append(services, inbox.New(demo.New(), orgs), inbox.New(demo.NewGitLab(), nil))
	} else {
		var err error
		services, err = connectProviders(context.Background(), orgs, gitlabHosts, *noGitHub, *noGitLab, *doctor)
		if err != nil {
			return err
		}
	}
	if *doctor {
		return nil
	}
	primary := services[0].Info()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen on loopback: %w", err)
	}
	handler, launchURL, err := server.New(services, *readOnly, listener.Addr().String())
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

	fmt.Printf("PR Inbox for @%s: %s\nPress Ctrl+C to stop.\n", primary.Viewer.Login, launchURL)
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

func connectProviders(ctx context.Context, orgs, gitlabHosts []string, noGitHub, noGitLab, doctor bool) ([]*inbox.Service, error) {
	var ready, missing []*inbox.Service
	add := func(service *inbox.Service, unavailable *provider.Unavailable) {
		if unavailable == nil {
			ready = append(ready, service)
			return
		}
		log.Printf("warning: %s", unavailable.Reason())
		missing = append(missing, inbox.New(unavailable, nil))
	}

	if !noGitHub {
		info := provider.Info{ID: "github", Name: "GitHub", Kind: "github", Host: "github.com"}
		token, err := auth.Token(ctx)
		if err != nil {
			add(nil, provider.NewUnavailable(info, "auth_required: "+err.Error()))
		} else {
			client := github.NewClient(token)
			viewer, err := client.Viewer(ctx)
			if err != nil {
				add(nil, provider.NewUnavailable(info, fmt.Sprintf("GitHub connectivity: %v", err)))
			} else {
				if doctor {
					fmt.Printf("GitHub account: %s\nGitHub API: connected\n", viewer.Login)
				}
				add(inbox.New(github.NewProvider(client, viewer), orgs), nil)
			}
		}
	}

	hosts := make([]string, 0, len(gitlabHosts)+1)
	if !noGitLab {
		hosts = append(hosts, gitlab.DefaultHost)
	}
	seen := make(map[string]bool)
	for _, value := range gitlabHosts {
		host, err := gitlab.NormalizeHost(value)
		if err != nil {
			return nil, err
		}
		hosts = append(hosts, host)
	}
	for _, host := range hosts {
		if seen[host] {
			continue
		}
		seen[host] = true
		info := provider.Info{ID: "gitlab:" + host, Name: host, Kind: "gitlab", Host: host}
		if host == gitlab.DefaultHost {
			info.Name = "GitLab"
		}
		token, err := gitlab.Token(ctx, host)
		if err != nil {
			add(nil, provider.NewUnavailable(info, "auth_required: "+err.Error()))
			continue
		}
		client := gitlab.NewClient(host, token)
		viewer, err := client.Viewer(ctx)
		if err != nil {
			add(nil, provider.NewUnavailable(info, fmt.Sprintf("GitLab %s: %v", host, err)))
			continue
		}
		if doctor {
			fmt.Printf("GitLab %s account: %s\nGitLab %s API: connected\n", host, viewer.Login, host)
		}
		add(inbox.New(gitlab.NewProvider(client, viewer), nil), nil)
	}

	if len(ready) == 0 {
		return nil, errors.New("no provider is available; run `gh auth login` and/or `glab auth login`, or set GITHUB_TOKEN / GITLAB_TOKEN")
	}
	return append(ready, missing...), nil
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
