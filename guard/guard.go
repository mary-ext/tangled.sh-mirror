package guard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/bluesky-social/indigo/atproto/identity"
	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/urfave/cli/v3"
	"tangled.org/core/idresolver"
	"tangled.org/core/knotserver/config"
	"tangled.org/core/log"
)

func Command() *cli.Command {
	return &cli.Command{
		Name:   "guard",
		Usage:  "role-based access control for git over ssh (not for manual use)",
		Action: Run,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "user",
				Usage:    "allowed git user",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "git-dir",
				Usage: "base directory for git repos",
				Value: "/home/git",
			},
			&cli.StringFlag{
				Name:  "log-path",
				Usage: "path to log file",
				Value: "/home/git/guard.log",
			},
			&cli.StringFlag{
				Name:  "internal-api",
				Usage: "internal API endpoint",
				Value: "http://localhost:5444",
			},
			&cli.StringFlag{
				Name:  "motd-file",
				Usage: "path to message of the day file",
				Value: "/home/git/motd",
			},
		},
	}
}

func Run(ctx context.Context, cmd *cli.Command) error {
	l := log.FromContext(ctx)

	c, err := config.Load(ctx)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	incomingUser := cmd.String("user")
	gitDir := cmd.String("git-dir")
	logPath := cmd.String("log-path")
	endpoint := cmd.String("internal-api")
	motdFile := cmd.String("motd-file")

	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		l.Error("failed to open log file", "error", err)
		return err
	} else {
		fileHandler := slog.NewJSONHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo})
		l = slog.New(fileHandler)
	}

	var clientIP string
	if connInfo := os.Getenv("SSH_CONNECTION"); connInfo != "" {
		parts := strings.Fields(connInfo)
		if len(parts) > 0 {
			clientIP = parts[0]
		}
	}

	if incomingUser == "" {
		l.Error("access denied: no user specified")
		fmt.Fprintln(os.Stderr, "access denied: no user specified")
		os.Exit(-1)
	}

	sshCommand := os.Getenv("SSH_ORIGINAL_COMMAND")

	l.Info("connection attempt",
		"user", incomingUser,
		"command", sshCommand,
		"client", clientIP)

	if sshCommand == "" {
		l.Info("access denied: no interactive shells", "user", incomingUser)
		fmt.Fprintf(os.Stderr, "Hi @%s! You've successfully authenticated.\n", incomingUser)
		os.Exit(-1)
	}

	cmdParts := strings.Fields(sshCommand)
	if len(cmdParts) < 2 {
		l.Error("invalid command format", "command", sshCommand)
		fmt.Fprintln(os.Stderr, "invalid command format")
		os.Exit(-1)
	}

	gitCommand := cmdParts[0]

	// did:foo/repo-name or
	// handle/repo-name or
	// any of the above with a leading slash (/)

	components := strings.Split(strings.TrimPrefix(strings.Trim(cmdParts[1], "'"), "/"), "/")
	l.Info("command components", "components", components)

	if len(components) != 2 {
		l.Error("invalid repo format", "components", components)
		fmt.Fprintln(os.Stderr, "invalid repo format, needs <user>/<repo> or /<user>/<repo>")
		os.Exit(-1)
	}

	didOrHandle := components[0]
	identity := resolveIdentity(ctx, c, l, didOrHandle)
	did := identity.DID.String()
	repoName := components[1]
	qualifiedRepoName, _ := securejoin.SecureJoin(did, repoName)

	validCommands := map[string]bool{
		"git-receive-pack":   true,
		"git-upload-pack":    true,
		"git-upload-archive": true,
	}
	if !validCommands[gitCommand] {
		l.Error("access denied: invalid git command", "command", gitCommand)
		fmt.Fprintln(os.Stderr, "access denied: invalid git command")
		return fmt.Errorf("access denied: invalid git command")
	}

	if gitCommand != "git-upload-pack" {
		if !isPushPermitted(l, incomingUser, qualifiedRepoName, endpoint) {
			l.Error("access denied: user not allowed",
				"did", incomingUser,
				"reponame", qualifiedRepoName)
			fmt.Fprintln(os.Stderr, "access denied: user not allowed")
			os.Exit(-1)
		}
	}

	fullPath, _ := securejoin.SecureJoin(gitDir, qualifiedRepoName)

	l.Info("processing command",
		"user", incomingUser,
		"command", gitCommand,
		"repo", repoName,
		"fullPath", fullPath,
		"client", clientIP)

	var motdReader io.Reader
	if reader, err := os.Open(motdFile); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			l.Error("failed to read motd file", "error", err)
		}
		motdReader = strings.NewReader("Welcome to this knot!\n")
	} else {
		motdReader = reader
	}
	if gitCommand == "git-upload-pack" {
		io.WriteString(os.Stderr, "\x02")
	}
	io.Copy(os.Stderr, motdReader)

	gitCmd := exec.Command(gitCommand, fullPath)
	gitCmd.Stdout = os.Stdout
	gitCmd.Stderr = os.Stderr
	gitCmd.Stdin = os.Stdin
	gitCmd.Env = append(os.Environ(),
		fmt.Sprintf("GIT_USER_DID=%s", incomingUser),
		fmt.Sprintf("GIT_USER_PDS_ENDPOINT=%s", identity.PDSEndpoint()),
	)

	if err := gitCmd.Run(); err != nil {
		l.Error("command failed", "error", err)
		fmt.Fprintf(os.Stderr, "command failed: %v\n", err)
		return fmt.Errorf("command failed: %v", err)
	}

	l.Info("command completed",
		"user", incomingUser,
		"command", gitCommand,
		"repo", repoName,
		"success", true)

	return nil
}

func resolveIdentity(ctx context.Context, c *config.Config, l *slog.Logger, didOrHandle string) *identity.Identity {
	resolver := idresolver.DefaultResolver(c.Server.PlcUrl)
	ident, err := resolver.ResolveIdent(ctx, didOrHandle)
	if err != nil {
		l.Error("Error resolving handle", "error", err, "handle", didOrHandle)
		fmt.Fprintf(os.Stderr, "error resolving handle: %v\n", err)
		os.Exit(1)
	}
	if ident.Handle.IsInvalidHandle() {
		l.Error("Error resolving handle", "invalid handle", didOrHandle)
		fmt.Fprintf(os.Stderr, "error resolving handle: invalid handle\n")
		os.Exit(1)
	}
	return ident
}

func isPushPermitted(l *slog.Logger, user, qualifiedRepoName, endpoint string) bool {
	u, _ := url.Parse(endpoint + "/push-allowed")
	q := u.Query()
	q.Add("user", user)
	q.Add("repo", qualifiedRepoName)
	u.RawQuery = q.Encode()

	req, err := http.Get(u.String())
	if err != nil {
		l.Error("Error verifying permissions", "error", err)
		fmt.Fprintf(os.Stderr, "error verifying permissions: %v\n", err)
		os.Exit(1)
	}

	l.Info("Checking push permission",
		"url", u.String(),
		"status", req.Status)

	return req.StatusCode == http.StatusNoContent
}
