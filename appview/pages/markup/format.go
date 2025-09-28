package markup

import (
	"regexp"
)

type Format string

const (
	FormatMarkdown Format = "markdown"
	FormatText     Format = "text"
)

var FileTypes map[Format][]string = map[Format][]string{
	FormatMarkdown: {".md", ".markdown", ".mdown", ".mkdn", ".mkd"},
}

var FileTypePatterns = map[Format]*regexp.Regexp{
	FormatMarkdown: regexp.MustCompile(`(?i)\.(md|markdown|mdown|mkdn|mkd)$`),
}

var ReadmePattern = regexp.MustCompile(`(?i)^readme(\.(md|markdown|txt))?$`)

func IsReadmeFile(filename string) bool {
	return ReadmePattern.MatchString(filename)
}

func GetFormat(filename string) Format {
	for format, pattern := range FileTypePatterns {
		if pattern.MatchString(filename) {
			return format
		}
	}
	// default format
	return FormatText
}
