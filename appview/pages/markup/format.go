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
