package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	logger   *log.Logger
	logFile  *os.File
	clientIP string

	// Command line flags
	allowedUser = flag.String("user", "", "Allowed git user")
	baseDirFlag = flag.String("base-dir", "/home/git", "Base directory for git repositories")
	logPathFlag = flag.String("log-path", "/var/log/git-wrapper.log", "Path to log file")
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

	if *allowedUser == "" {
		exitWithLog("access denied: no user specified")
	}

	sshCommand := os.Getenv("SSH_ORIGINAL_COMMAND")

	logEvent("Connection attempt", map[string]interface{}{
		"user":    *allowedUser,
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
	repoName := strings.Trim(cmdParts[1], "'")

	validCommands := map[string]bool{
		"git-receive-pack":   true,
		"git-upload-pack":    true,
		"git-upload-archive": true,
	}
	if !validCommands[gitCommand] {
		exitWithLog("access denied: invalid git command")
	}

	if !isAllowedUser(*allowedUser, repoName) {
		exitWithLog("access denied: user not allowed")
	}

	fullPath := filepath.Join(*baseDirFlag, repoName)
	fullPath = filepath.Clean(fullPath)

	logEvent("Processing command", map[string]interface{}{
		"user":     *allowedUser,
		"command":  gitCommand,
		"repo":     repoName,
		"fullPath": fullPath,
		"client":   clientIP,
	})

	cmd := exec.Command(gitCommand, fullPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		exitWithLog(fmt.Sprintf("command failed: %v", err))
	}

	logEvent("Command completed", map[string]interface{}{
		"user":    *allowedUser,
		"command": gitCommand,
		"repo":    repoName,
		"success": true,
	})
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

func isAllowedUser(user, repoPath string) bool {
	fullPath := filepath.Join(*baseDirFlag, repoPath)
	didPath := filepath.Join(fullPath, "did")

	didBytes, err := os.ReadFile(didPath)
	if err != nil {
		return false
	}

	allowedUser := strings.TrimSpace(string(didBytes))
	return allowedUser == user
}
