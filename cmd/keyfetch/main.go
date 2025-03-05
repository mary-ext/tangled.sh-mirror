// This program must be configured to run as the sshd AuthorizedKeysCommand.
// The format looks something like this:
//   Match User git
//     AuthorizedKeysCommand /keyfetch -internal-api http://localhost:5444 -repoguard-path /home/git/repoguard
//     AuthorizedKeysCommandUser nobody
//
// The command and its parent directories must be owned by root and set to 0755. Hence, the ideal location for this is
// somewhere already owned by root so you don't have to mess with directory perms.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
)

func main() {
	endpoint := flag.String("internal-api", "http://localhost:5444", "Internal API endpoint")
	repoguardPath := flag.String("repoguard-path", "/home/git/repoguard", "Path to the repoguard binary")
	gitDir := flag.String("git-dir", "/home/git", "Path to the git directory")
	logPath := flag.String("log-path", "/home/git/log", "Path to log file")
	flag.Parse()

	resp, err := http.Get(*endpoint + "/keys")
	if err != nil {
		log.Fatalf("error fetching keys: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("error reading response body: %v", err)
	}

	var data []map[string]interface{}
	err = json.Unmarshal(body, &data)
	if err != nil {
		log.Fatalf("error unmarshalling response body: %v", err)
	}

	fmt.Print(formatKeyData(*repoguardPath, *gitDir, *logPath, data))
}
