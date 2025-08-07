package nixery

import (
	"io"

	"regexp"
)

// regex to match ANSI escape codes (e.g., color codes, cursor moves)
const ansi = "[\u001B\u009B][[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;[a-zA-Z\\d]*)*)?\u0007)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))"

var re = regexp.MustCompile(ansi)

type ansiStrippingWriter struct {
	underlying io.Writer
}

func (w *ansiStrippingWriter) Write(p []byte) (int, error) {
	clean := re.ReplaceAll(p, []byte{})
	return w.underlying.Write(clean)
}
