package git

import (
	"bufio"
	"io"
	"strings"
)

type PostReceiveLine struct {
	OldSha string // old sha of reference being updated
	NewSha string // new sha of reference being updated
	Ref    string // the reference being updated
}

func ParsePostReceive(buf io.Reader) ([]PostReceiveLine, error) {
	scanner := bufio.NewScanner(buf)
	var lines []PostReceiveLine
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, " ", 3)
		if len(parts) != 3 {
			continue
		}

		oldSha := parts[0]
		newSha := parts[1]
		ref := parts[2]

		lines = append(lines, PostReceiveLine{
			OldSha: oldSha,
			NewSha: newSha,
			Ref:    ref,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return lines, nil
}
