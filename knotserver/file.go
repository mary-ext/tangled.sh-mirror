package knotserver

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/sotangled/tangled/types"
)

func (h *Handle) listFiles(files []types.NiceTree, data map[string]any, w http.ResponseWriter) {
	data["files"] = files

	writeJSON(w, data)
	return
}

func countLines(r io.Reader) (int, error) {
	buf := make([]byte, 32*1024)
	bufLen := 0
	count := 0
	nl := []byte{'\n'}

	for {
		c, err := r.Read(buf)
		if c > 0 {
			bufLen += c
		}
		count += bytes.Count(buf[:c], nl)

		switch {
		case err == io.EOF:
			/* handle last line not having a newline at the end */
			if bufLen >= 1 && buf[(bufLen-1)%(32*1024)] != '\n' {
				count++
			}
			return count, nil
		case err != nil:
			return 0, err
		}
	}
}

func (h *Handle) showFile(resp types.RepoBlobResponse, w http.ResponseWriter, l *slog.Logger) {
	lc, err := countLines(strings.NewReader(resp.Contents))
	if err != nil {
		// Non-fatal, we'll just skip showing line numbers in the template.
		l.Warn("counting lines", "error", err)
	}

	resp.Lines = lc
	writeJSON(w, resp)
	return
}
