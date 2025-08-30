package markup

import "strings"

type Format string

const (
	FormatMarkdown Format = "markdown"
	FormatText     Format = "text"
)

var FileTypes map[Format][]string = map[Format][]string{
	FormatMarkdown: []string{".md", ".markdown", ".mdown", ".mkdn", ".mkd"},
}

// ReadmeFilenames contains the list of common README filenames to search for,
// in order of preference. Only includes well-supported formats.
var ReadmeFilenames = []string{
	"README.md", "readme.md",
	"README",
	"readme",
	"README.markdown",
	"readme.markdown",
	"README.txt",
	"readme.txt",
}

func GetFormat(filename string) Format {
	for format, extensions := range FileTypes {
		for _, extension := range extensions {
			if strings.HasSuffix(filename, extension) {
				return format
			}
		}
	}
	// default format
	return FormatText
}
