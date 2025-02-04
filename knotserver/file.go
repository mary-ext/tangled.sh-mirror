package knotserver

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/sotangled/tangled/knotserver/git"
)

func (h *Handle) listFiles(files []git.NiceTree, data map[string]any, w http.ResponseWriter) {
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

func (h *Handle) showFile(content string, data map[string]any, w http.ResponseWriter, l *slog.Logger) {
	lc, err := countLines(strings.NewReader(content))
	if err != nil {
		// Non-fatal, we'll just skip showing line numbers in the template.
		l.Warn("counting lines", "error", err)
	}

	lines := make([]int, lc)
	if lc > 0 {
		for i := range lines {
			lines[i] = i + 1
		}
	}

	data["linecount"] = lines
	data["content"] = content

	writeJSON(w, data)
	return
}

func (h *Handle) showRaw(content string, w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(content))
	return
}
