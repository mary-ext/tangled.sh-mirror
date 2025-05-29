package keyfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/urfave/cli/v3"
	"tangled.sh/tangled.sh/core/log"
)

func Command() *cli.Command {
	return &cli.Command{
		Name:   "keys",
		Usage:  "fetch public keys from the knot server",
		Action: Run,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Usage:   "output format (table, json, authorized-keys)",
				Value:   "table",
			},
			&cli.StringFlag{
				Name:  "internal-api",
				Usage: "internal API endpoint",
				Value: "http://localhost:5444",
			},
			&cli.StringFlag{
				Name:  "git-dir",
				Usage: "base directory for git repos",
				Value: "/home/git",
			},
			&cli.StringFlag{
				Name:  "log-path",
				Usage: "path to log file",
				Value: "/home/git/log",
			},
		},
	}
}

func Run(ctx context.Context, cmd *cli.Command) error {
	l := log.FromContext(ctx)

	internalApi := cmd.String("internal-api")
	gitDir := cmd.String("git-dir")
	logPath := cmd.String("log-path")
	output := cmd.String("output")

	executablePath, err := os.Executable()
	if err != nil {
		l.Error("error getting path of executable", "error", err)
		return err
	}

	resp, err := http.Get(internalApi + "/keys")
	if err != nil {
		l.Error("error reaching internal API endpoint; is the knot server running?", "error", err)
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		l.Error("error reading response body", "error", err)
		return err
	}

	var data []map[string]any
	err = json.Unmarshal(body, &data)
	if err != nil {
		l.Error("error unmarshalling response body", "error", err)
		return err
	}

	switch output {
	case "json":
		prettyJSON, err := json.MarshalIndent(data, "", "    ")
		if err != nil {
			l.Error("error pretty printing JSON", "error", err)
			return err
		}

		if _, err := os.Stdout.Write(prettyJSON); err != nil {
			l.Error("error writing to stdout", "error", err)
			return err
		}
	case "authorized-keys":
		formatted := formatKeyData(executablePath, gitDir, logPath, internalApi, data)
		_, err := os.Stdout.Write([]byte(formatted))
		if err != nil {
			l.Error("error writing to stdout", "error", err)
			return err
		}
	case "table":
		fmt.Printf("%-40s %-40s\n", "DID", "KEY")
		fmt.Println(strings.Repeat("-", 80))

		for _, entry := range data {
			did, _ := entry["did"].(string)
			key, _ := entry["key"].(string)
			fmt.Printf("%-40s %-40s\n", did, key)
		}
	}
	return nil
}

func formatKeyData(executablePath, gitDir, logPath, endpoint string, data []map[string]any) string {
	var result string
	for _, entry := range data {
		result += fmt.Sprintf(
			`command="%s guard -git-dir %s -user %s -log-path %s -internal-api %s",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty %s`+"\n",
			executablePath, gitDir, entry["did"], logPath, endpoint, entry["key"])
	}
	return result
}
