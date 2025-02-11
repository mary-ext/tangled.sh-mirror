package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sotangled/tangled/appview"
)

var (
	logger   *log.Logger
	logFile  *os.File
	clientIP string

	// Command line flags
	incomingUser = flag.String("user", "", "Allowed git user")
	baseDirFlag  = flag.String("base-dir", "/home/git", "Base directory for git repositories")
	logPathFlag  = flag.String("log-path", "/var/log/git-wrapper.log", "Path to log file")
	endpoint     = flag.String("internal-api", "http://localhost:5444", "Internal API endpoint")
)

func main() {
	flag.Parse()

	defer cleanup()
	initLogger()

	// Get client IP from SSH environment
	if connInfo := os.Getenv("SSH_CONNECTION"); connInfo != "" {
		parts := strings.Fields(connInfo)
		if len(parts) > 0 {
			clientIP = parts[0]
		}
	}

	if *incomingUser == "" {
		exitWithLog("access denied: no user specified")
	}

	sshCommand := os.Getenv("SSH_ORIGINAL_COMMAND")

	logEvent("Connection attempt", map[string]interface{}{
		"user":    *incomingUser,
		"command": sshCommand,
		"client":  clientIP,
	})

	if sshCommand == "" {
		exitWithLog("access denied: we don't serve interactive shells :)")
	}

	cmdParts := strings.Fields(sshCommand)
	if len(cmdParts) < 2 {
		exitWithLog("invalid command format")
	}

	gitCommand := cmdParts[0]

	// did:foo/repo-name or
	// handle/repo-name

	components := strings.Split(strings.Trim(cmdParts[1], "'"), "/")
	logEvent("Command components", map[string]interface{}{
		"components": components,
	})
	if len(components) != 2 {
		exitWithLog("invalid repo format, needs <user>/<repo>")
	}

	didOrHandle := components[0]
	did := resolveToDid(didOrHandle)
	repoName := components[1]
	qualifiedRepoName := filepath.Join(did, repoName)

	validCommands := map[string]bool{
		"git-receive-pack":   true,
		"git-upload-pack":    true,
		"git-upload-archive": true,
	}
	if !validCommands[gitCommand] {
		exitWithLog("access denied: invalid git command")
	}

	if gitCommand != "git-upload-pack" {
		if !isPushPermitted(*incomingUser, qualifiedRepoName) {
			logEvent("all infos", map[string]interface{}{
				"did":      *incomingUser,
				"reponame": qualifiedRepoName,
			})
			exitWithLog("access denied: user not allowed")
		}
	}

	fullPath := filepath.Join(*baseDirFlag, qualifiedRepoName)
	fullPath = filepath.Clean(fullPath)

	logEvent("Processing command", map[string]interface{}{
		"user":     *incomingUser,
		"command":  gitCommand,
		"repo":     repoName,
		"fullPath": fullPath,
		"client":   clientIP,
	})

	if gitCommand == "git-upload-pack" {
		fmt.Fprintf(os.Stderr, "\x02%s\n", "Welcome to this knot!")
	} else {
		fmt.Fprintf(os.Stderr, "%s\n", "Welcome to this knot!")
	}

	cmd := exec.Command(gitCommand, fullPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		exitWithLog(fmt.Sprintf("command failed: %v", err))
	}

	logEvent("Command completed", map[string]interface{}{
		"user":    *incomingUser,
		"command": gitCommand,
		"repo":    repoName,
		"success": true,
	})
}

func resolveToDid(didOrHandle string) string {
	resolver := appview.NewResolver()
	ident, err := resolver.ResolveIdent(context.Background(), didOrHandle)
	if err != nil {
		exitWithLog(fmt.Sprintf("error resolving handle: %v", err))
	}

	// did:plc:foobarbaz/repo
	return ident.DID.String()
}

func initLogger() {
	var err error
	logFile, err = os.OpenFile(*logPathFlag, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open log file: %v\n", err)
		os.Exit(1)
	}

	logger = log.New(logFile, "", 0)
}

func logEvent(event string, fields map[string]interface{}) {
	entry := fmt.Sprintf(
		"timestamp=%q event=%q",
		time.Now().Format(time.RFC3339),
		event,
	)

	for k, v := range fields {
		entry += fmt.Sprintf(" %s=%q", k, v)
	}

	logger.Println(entry)
}

func exitWithLog(message string) {
	logEvent("Access denied", map[string]interface{}{
		"error": message,
	})
	logFile.Sync()
	fmt.Fprintf(os.Stderr, "error: %s\n", message)
	os.Exit(1)
}

func cleanup() {
	if logFile != nil {
		logFile.Sync()
		logFile.Close()
	}
}

func isPushPermitted(user, qualifiedRepoName string) bool {
	u, _ := url.Parse(*endpoint + "/push-allowed")
	q := u.Query()
	q.Add("user", user)
	q.Add("repo", qualifiedRepoName)
	u.RawQuery = q.Encode()

	req, err := http.Get(u.String())
	if err != nil {
		exitWithLog(fmt.Sprintf("error verifying permissions: %v", err))
	}

	logEvent("url", map[string]interface{}{
		"url":    u.String(),
		"status": req.Status,
	})

	return req.StatusCode == http.StatusNoContent
}
