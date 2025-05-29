package hook

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/urfave/cli/v3"
)

// The hook command is nested like so:
//
//	knot hook --[flags] [hook]
func Command() *cli.Command {
	return &cli.Command{
		Name:  "hook",
		Usage: "run git hooks",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "git-dir",
				Usage: "base directory for git repos",
			},
			&cli.StringFlag{
				Name:  "user-did",
				Usage: "git user's did",
			},
			&cli.StringFlag{
				Name:  "user-handle",
				Usage: "git user's handle",
			},
			&cli.StringFlag{
				Name:  "internal-api",
				Usage: "endpoint for the internal API",
				Value: "http://localhost:5444",
			},
		},
		Commands: []*cli.Command{
			{
				Name:   "post-recieve",
				Usage:  "sends a post-recieve hook to the knot (waits for stdin)",
				Action: postRecieve,
			},
		},
	}
}

func postRecieve(ctx context.Context, cmd *cli.Command) error {
	gitDir := cmd.String("git-dir")
	userDid := cmd.String("user-did")
	userHandle := cmd.String("user-handle")
	endpoint := cmd.String("internal-api")

	payloadReader := bufio.NewReader(os.Stdin)
	payload, _ := payloadReader.ReadString('\n')

	client := &http.Client{}

	req, err := http.NewRequest("POST", "http://"+endpoint+"/hooks/post-receive", strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("X-Git-Dir", gitDir)
	req.Header.Set("X-Git-User-Did", userDid)
	req.Header.Set("X-Git-User-Handle", userHandle)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}
