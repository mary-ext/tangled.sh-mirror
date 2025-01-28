package main

import (
	"fmt"
)

func formatKeyData(repoguardPath string, data map[string]string) string {
	var result string
	for user, key := range data {
		result += fmt.Sprintf(
			`command="%s -base-dir /home/git -user %s -log-path /home/git/log ",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty %s`+"\n",
			repoguardPath, user, key)
	}
	return result
}
