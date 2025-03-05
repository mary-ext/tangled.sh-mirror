package main

import (
	"fmt"
)

func formatKeyData(repoguardPath string, gitDir string, data []map[string]interface{}) string {
	var result string
	for _, entry := range data {
		result += fmt.Sprintf(
			`command="%s -base-dir %s -user %s -log-path /home/git/log ",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty %s`+"\n",
			repoguardPath, gitDir, entry["did"], entry["key"])
	}
	return result
}
