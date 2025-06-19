package pages

import (
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"math"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/microcosm-cc/bluemonday"
	"tangled.sh/tangled.sh/core/appview/filetree"
	"tangled.sh/tangled.sh/core/appview/pages/markup"
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
		"int64": func(a int) int64 {
			return int64(a)
		},
		"add": func(a, b int) int {
			return a + b
		},
		"now": func() time.Time {
			return time.Now()
		},
		// the absolute state of go templates
		"add64": func(a, b int64) int64 {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"f64": func(a int) float64 {
			return float64(a)
		},
		"addf64": func(a, b float64) float64 {
			return a + b
		},
		"subf64": func(a, b float64) float64 {
			return a - b
		},
		"mulf64": func(a, b float64) float64 {
			return a * b
		},
		"divf64": func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"negf64": func(a float64) float64 {
			return -a
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
		"longTimeFmt": func(t time.Time) string {
			return t.Format("2006-01-02 * 3:04 PM")
		},
		"commaFmt": humanize.Comma,
		"shortTimeFmt": func(t time.Time) string {
			return humanize.CustomRelTime(t, time.Now(), "", "", []humanize.RelTimeMagnitude{
				{time.Second, "now", time.Second},
				{2 * time.Second, "1s %s", 1},
				{time.Minute, "%ds %s", time.Second},
				{2 * time.Minute, "1min %s", 1},
				{time.Hour, "%dmin %s", time.Minute},
				{2 * time.Hour, "1hr %s", 1},
				{humanize.Day, "%dhrs %s", time.Hour},
				{2 * humanize.Day, "1d %s", 1},
				{20 * humanize.Day, "%dd %s", humanize.Day},
				{8 * humanize.Week, "%dw %s", humanize.Week},
				{humanize.Year, "%dmo %s", humanize.Month},
				{18 * humanize.Month, "1y %s", 1},
				{2 * humanize.Year, "2y %s", 1},
				{humanize.LongTime, "%dy %s", humanize.Year},
				{math.MaxInt64, "a long while %s", 1},
			})
		},
		"durationFmt": func(duration time.Duration) string {
			days := int64(duration.Hours() / 24)
			hours := int64(math.Mod(duration.Hours(), 24))
			minutes := int64(math.Mod(duration.Minutes(), 60))
			seconds := int64(math.Mod(duration.Seconds(), 60))

			chunks := []struct {
				name   string
				amount int64
			}{
				{"d", days},
				{"hr", hours},
				{"min", minutes},
				{"s", seconds},
			}

			parts := []string{}

			for _, chunk := range chunks {
				if chunk.amount != 0 {
					parts = append(parts, fmt.Sprintf("%d%s", chunk.amount, chunk.name))
				}
			}

			return strings.Join(parts, " ")
		},
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
		// take atmost N items from this slice
		"take": func(slice any, n int) any {
			v := reflect.ValueOf(slice)
			if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
				return nil
			}
			if v.Len() == 0 {
				return nil
			}
			return v.Slice(0, min(n, v.Len()-1)).Interface()
		},

		"markdown": func(text string) template.HTML {
			rctx := &markup.RenderContext{RendererType: markup.RendererTypeDefault}
			return template.HTML(bluemonday.UGCPolicy().Sanitize(rctx.RenderMarkdown(text)))
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
		"deref": func(v any) any {
			val := reflect.ValueOf(v)
			if val.Kind() == reflect.Ptr && !val.IsNil() {
				return val.Elem().Interface()
			}
			return nil
		},
		"i": func(name string, classes ...string) template.HTML {
			data, err := icon(name, classes)
			if err != nil {
				log.Printf("icon %s does not exist", name)
				data, _ = icon("airplay", classes)
			}
			return template.HTML(data)
		},
		"cssContentHash": CssContentHash,
		"fileTree":       filetree.FileTree,
		"pathUnescape": func(s string) string {
			u, _ := url.PathUnescape(s)
			return u
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
