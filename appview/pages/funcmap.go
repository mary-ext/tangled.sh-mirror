package pages

import (
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/dustin/go-humanize"
)

func funcMap() template.FuncMap {
	return template.FuncMap{
		"split": func(s string) []string {
			return strings.Split(s, "\n")
		},
		"truncateAt30": func(s string) string {
			if len(s) <= 30 {
				return s
			}
			return s[:30] + "…"
		},
		"splitOn": func(s, sep string) []string {
			return strings.Split(s, sep)
		},
		"add": func(a, b int) int {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"cond": func(cond interface{}, a, b string) string {
			if cond == nil {
				return b
			}

			if boolean, ok := cond.(bool); boolean && ok {
				return a
			}

			return b
		},
		"didOrHandle": func(did, handle string) string {
			if handle != "" {
				return fmt.Sprintf("@%s", handle)
			} else {
				return did
			}
		},
		"assoc": func(values ...string) ([][]string, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("invalid assoc call, must have an even number of arguments")
			}
			pairs := make([][]string, 0)
			for i := 0; i < len(values); i += 2 {
				pairs = append(pairs, []string{values[i], values[i+1]})
			}
			return pairs, nil
		},
		"append": func(s []string, values ...string) []string {
			s = append(s, values...)
			return s
		},
		"timeFmt": humanize.Time,
		"byteFmt": humanize.Bytes,
		"length": func(slice any) int {
			v := reflect.ValueOf(slice)
			if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
				return v.Len()
			}
			return 0
		},
		"splitN": func(s, sep string, n int) []string {
			return strings.SplitN(s, sep, n)
		},
		"escapeHtml": func(s string) template.HTML {
			if s == "" {
				return template.HTML("<br>")
			}
			return template.HTML(s)
		},
		"unescapeHtml": func(s string) string {
			return html.UnescapeString(s)
		},
		"nl2br": func(text string) template.HTML {
			return template.HTML(strings.Replace(template.HTMLEscapeString(text), "\n", "<br>", -1))
		},
		"unwrapText": func(text string) string {
			paragraphs := strings.Split(text, "\n\n")

			for i, p := range paragraphs {
				lines := strings.Split(p, "\n")
				paragraphs[i] = strings.Join(lines, " ")
			}

			return strings.Join(paragraphs, "\n\n")
		},
		"sequence": func(n int) []struct{} {
			return make([]struct{}, n)
		},
		"subslice": func(slice any, start, end int) any {
			v := reflect.ValueOf(slice)
			if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
				return nil
			}
			if start < 0 || start > v.Len() || end > v.Len() || start > end {
				return nil
			}
			return v.Slice(start, end).Interface()
		},
		"markdown": func(text string) template.HTML {
			return template.HTML(renderMarkdown(text))
		},
		"isNil": func(t any) bool {
			// returns false for other "zero" values
			return t == nil
		},
		"list": func(args ...any) []any {
			return args
		},
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, errors.New("invalid dict call")
			}
			dict := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, errors.New("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
		"i": func(name string, classes ...string) template.HTML {
			data, err := icon(name, classes)
			if err != nil {
				log.Printf("icon %s does not exist", name)
				data, _ = icon("airplay", classes)
			}
			return template.HTML(data)
		},
	}
}

func icon(name string, classes []string) (template.HTML, error) {
	iconPath := filepath.Join("static", "icons", name)

	if filepath.Ext(name) == "" {
		iconPath += ".svg"
	}

	data, err := Files.ReadFile(iconPath)
	if err != nil {
		return "", fmt.Errorf("icon %s not found: %w", name, err)
	}

	// Convert SVG data to string
	svgStr := string(data)

	svgTagEnd := strings.Index(svgStr, ">")
	if svgTagEnd == -1 {
		return "", fmt.Errorf("invalid SVG format for icon %s", name)
	}

	classTag := ` class="` + strings.Join(classes, " ") + `"`

	modifiedSVG := svgStr[:svgTagEnd] + classTag + svgStr[svgTagEnd:]
	return template.HTML(modifiedSVG), nil
}
