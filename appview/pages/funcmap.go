package pages

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/dustin/go-humanize"
	"github.com/go-enry/go-enry/v2"
	"tangled.org/core/appview/filetree"
	"tangled.org/core/appview/pages/markup"
	"tangled.org/core/crypto"
)

func (p *Pages) funcMap() template.FuncMap {
	return template.FuncMap{
		"split": func(s string) []string {
			return strings.Split(s, "\n")
		},
		"trimPrefix": func(s, prefix string) string {
			return strings.TrimPrefix(s, prefix)
		},
		"join": func(elems []string, sep string) string {
			return strings.Join(elems, sep)
		},
		"contains": func(s string, target string) bool {
			return strings.Contains(s, target)
		},
		"stripPort": func(hostname string) string {
			if strings.Contains(hostname, ":") {
				return strings.Split(hostname, ":")[0]
			}
			return hostname
		},
		"mapContains": func(m any, key any) bool {
			mapValue := reflect.ValueOf(m)
			if mapValue.Kind() != reflect.Map {
				return false
			}
			keyValue := reflect.ValueOf(key)
			return mapValue.MapIndex(keyValue).IsValid()
		},
		"resolve": func(s string) string {
			identity, err := p.resolver.ResolveIdent(context.Background(), s)

			if err != nil {
				return s
			}

			if identity.Handle.IsInvalidHandle() {
				return "handle.invalid"
			}

			return identity.Handle.String()
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
		"string": func(v any) string {
			return fmt.Sprint(v)
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
		"cond": func(cond any, a, b string) string {
			if cond == nil {
				return b
			}

			if boolean, ok := cond.(bool); boolean && ok {
				return a
			}

			return b
		},
		"didOrHandle": func(did, handle string) string {
			if handle != "" && handle != syntax.HandleInvalid.String() {
				return handle
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
		"commaFmt":   humanize.Comma,
		"relTimeFmt": humanize.Time,
		"shortRelTimeFmt": func(t time.Time) string {
			return humanize.CustomRelTime(t, time.Now(), "", "", []humanize.RelTimeMagnitude{
				{D: time.Second, Format: "now", DivBy: time.Second},
				{D: 2 * time.Second, Format: "1s %s", DivBy: 1},
				{D: time.Minute, Format: "%ds %s", DivBy: time.Second},
				{D: 2 * time.Minute, Format: "1min %s", DivBy: 1},
				{D: time.Hour, Format: "%dmin %s", DivBy: time.Minute},
				{D: 2 * time.Hour, Format: "1hr %s", DivBy: 1},
				{D: humanize.Day, Format: "%dhrs %s", DivBy: time.Hour},
				{D: 2 * humanize.Day, Format: "1d %s", DivBy: 1},
				{D: 20 * humanize.Day, Format: "%dd %s", DivBy: humanize.Day},
				{D: 8 * humanize.Week, Format: "%dw %s", DivBy: humanize.Week},
				{D: humanize.Year, Format: "%dmo %s", DivBy: humanize.Month},
				{D: 18 * humanize.Month, Format: "1y %s", DivBy: 1},
				{D: 2 * humanize.Year, Format: "2y %s", DivBy: 1},
				{D: humanize.LongTime, Format: "%dy %s", DivBy: humanize.Year},
				{D: math.MaxInt64, Format: "a long while %s", DivBy: 1},
			})
		},
		"longTimeFmt": func(t time.Time) string {
			return t.Format("Jan 2, 2006, 3:04 PM MST")
		},
		"iso8601DateTimeFmt": func(t time.Time) string {
			return t.Format("2006-01-02T15:04:05-07:00")
		},
		"iso8601DurationFmt": func(duration time.Duration) string {
			days := int64(duration.Hours() / 24)
			hours := int64(math.Mod(duration.Hours(), 24))
			minutes := int64(math.Mod(duration.Minutes(), 60))
			seconds := int64(math.Mod(duration.Seconds(), 60))
			return fmt.Sprintf("P%dD%dH%dM%dS", days, hours, minutes, seconds)
		},
		"durationFmt": func(duration time.Duration) string {
			return durationFmt(duration, [4]string{"d", "hr", "min", "s"})
		},
		"longDurationFmt": func(duration time.Duration) string {
			return durationFmt(duration, [4]string{"days", "hours", "minutes", "seconds"})
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
			return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(text), "\n", "<br>"))
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
			return v.Slice(0, min(n, v.Len())).Interface()
		},
		"markdown": func(text string) template.HTML {
			p.rctx.RendererType = markup.RendererTypeDefault
			htmlString := p.rctx.RenderMarkdown(text)
			sanitized := p.rctx.SanitizeDefault(htmlString)
			return template.HTML(sanitized)
		},
		"description": func(text string) template.HTML {
			p.rctx.RendererType = markup.RendererTypeDefault
			htmlString := p.rctx.RenderMarkdown(text)
			sanitized := p.rctx.SanitizeDescription(htmlString)
			return template.HTML(sanitized)
		},
		"trimUriScheme": func(text string) string {
			text = strings.TrimPrefix(text, "https://")
			text = strings.TrimPrefix(text, "http://")
			return text
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
			data, err := p.icon(name, classes)
			if err != nil {
				log.Printf("icon %s does not exist", name)
				data, _ = p.icon("airplay", classes)
			}
			return template.HTML(data)
		},
		"cssContentHash": p.CssContentHash,
		"fileTree":       filetree.FileTree,
		"pathEscape": func(s string) string {
			return url.PathEscape(s)
		},
		"pathUnescape": func(s string) string {
			u, _ := url.PathUnescape(s)
			return u
		},
		"safeUrl": func(s string) template.URL {
			return template.URL(s)
		},
		"tinyAvatar": func(handle string) string {
			return p.AvatarUrl(handle, "tiny")
		},
		"fullAvatar": func(handle string) string {
			return p.AvatarUrl(handle, "")
		},
		"langColor": enry.GetColor,
		"layoutSide": func() string {
			return "col-span-1 md:col-span-2 lg:col-span-3"
		},
		"layoutCenter": func() string {
			return "col-span-1 md:col-span-8 lg:col-span-6"
		},

		"normalizeForHtmlId": func(s string) string {
			normalized := strings.ReplaceAll(s, ":", "_")
			normalized = strings.ReplaceAll(normalized, ".", "_")
			return normalized
		},
		"sshFingerprint": func(pubKey string) string {
			fp, err := crypto.SSHFingerprint(pubKey)
			if err != nil {
				return "error"
			}
			return fp
		},
	}
}

func (p *Pages) AvatarUrl(handle, size string) string {
	handle = strings.TrimPrefix(handle, "@")

	secret := p.avatar.SharedSecret
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(handle))
	signature := hex.EncodeToString(h.Sum(nil))

	sizeArg := ""
	if size != "" {
		sizeArg = fmt.Sprintf("size=%s", size)
	}
	return fmt.Sprintf("%s/%s/%s?%s", p.avatar.Host, signature, handle, sizeArg)
}

func (p *Pages) icon(name string, classes []string) (template.HTML, error) {
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

func durationFmt(duration time.Duration, names [4]string) string {
	days := int64(duration.Hours() / 24)
	hours := int64(math.Mod(duration.Hours(), 24))
	minutes := int64(math.Mod(duration.Minutes(), 60))
	seconds := int64(math.Mod(duration.Seconds(), 60))

	chunks := []struct {
		name   string
		amount int64
	}{
		{names[0], days},
		{names[1], hours},
		{names[2], minutes},
		{names[3], seconds},
	}

	parts := []string{}

	for _, chunk := range chunks {
		if chunk.amount != 0 {
			parts = append(parts, fmt.Sprintf("%d%s", chunk.amount, chunk.name))
		}
	}

	return strings.Join(parts, " ")
}
