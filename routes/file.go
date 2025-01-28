package routes

import (
	"bytes"
	"html/template"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/icyphox/bild/git"
)

func (h *Handle) listFiles(files []git.NiceTree, data map[string]any, w http.ResponseWriter) {
	data["files"] = files
	data["meta"] = h.c.Meta

	if err := h.t.ExecuteTemplate(w, "repo/tree", data); err != nil {
		log.Println(err)
		return
	}
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

func (h *Handle) showFileWithHighlight(name, content string, data map[string]any, w http.ResponseWriter) {
	lexer := lexers.Get(name)
	if lexer == nil {
		lexer = lexers.Get(".txt")
	}

	style := styles.Get(h.c.Meta.SyntaxHighlight)
	if style == nil {
		style = styles.Get("monokailight")
	}

	formatter := html.New(
		html.WithLineNumbers(true),
		html.WithLinkableLineNumbers(true, "L"),
	)

	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		h.Write500(w)
		return
	}

	var code bytes.Buffer
	err = formatter.Format(&code, style, iterator)
	if err != nil {
		h.Write500(w)
		return
	}

	data["content"] = template.HTML(code.String())
	data["meta"] = h.c.Meta
	data["chroma"] = true

	if err := h.t.ExecuteTemplate(w, "repo/file", data); err != nil {
		log.Println(err)
		return
	}
}

func (h *Handle) showFile(content string, data map[string]any, w http.ResponseWriter) {
	lc, err := countLines(strings.NewReader(content))
	if err != nil {
		// Non-fatal, we'll just skip showing line numbers in the template.
		log.Printf("counting lines: %s", err)
	}

	lines := make([]int, lc)
	if lc > 0 {
		for i := range lines {
			lines[i] = i + 1
		}
	}

	data["linecount"] = lines
	data["content"] = content
	data["meta"] = h.c.Meta
	data["chroma"] = false

	if err := h.t.ExecuteTemplate(w, "repo/file", data); err != nil {
		log.Println(err)
		return
	}
}

func (h *Handle) showRaw(content string, w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(content))
	return
}
