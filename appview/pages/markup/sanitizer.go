package markup

import (
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/microcosm-cc/bluemonday"
)

type Sanitizer struct {
	defaultPolicy     *bluemonday.Policy
	descriptionPolicy *bluemonday.Policy
}

func NewSanitizer() Sanitizer {
	return Sanitizer{
		defaultPolicy:     defaultPolicy(),
		descriptionPolicy: descriptionPolicy(),
	}
}

func defaultPolicy() *bluemonday.Policy {
	policy := bluemonday.UGCPolicy()

	// Allow generally safe attributes
	generalSafeAttrs := []string{
		"abbr", "accept", "accept-charset",
		"accesskey", "action", "align", "alt",
		"aria-describedby", "aria-hidden", "aria-label", "aria-labelledby",
		"axis", "border", "cellpadding", "cellspacing", "char",
		"charoff", "charset", "checked",
		"clear", "cols", "colspan", "color",
		"compact", "coords", "datetime", "dir",
		"disabled", "enctype", "for", "frame",
		"headers", "height", "hreflang",
		"hspace", "ismap", "label", "lang",
		"maxlength", "media", "method",
		"multiple", "name", "nohref", "noshade",
		"nowrap", "open", "prompt", "readonly", "rel", "rev",
		"rows", "rowspan", "rules", "scope",
		"selected", "shape", "size", "span",
		"start", "summary", "tabindex", "target",
		"title", "type", "usemap", "valign", "value",
		"vspace", "width", "itemprop",
	}

	generalSafeElements := []string{
		"h1", "h2", "h3", "h4", "h5", "h6", "h7", "h8", "br", "b", "i", "strong", "em", "a", "pre", "code", "img", "tt",
		"div", "ins", "del", "sup", "sub", "p", "ol", "ul", "table", "thead", "tbody", "tfoot", "blockquote", "label",
		"dl", "dt", "dd", "kbd", "q", "samp", "var", "hr", "ruby", "rt", "rp", "li", "tr", "td", "th", "s", "strike", "summary",
		"details", "caption", "figure", "figcaption",
		"abbr", "bdo", "cite", "dfn", "mark", "small", "span", "time", "video", "wbr",
	}

	policy.AllowAttrs(generalSafeAttrs...).OnElements(generalSafeElements...)

	// video
	policy.AllowAttrs("src", "autoplay", "controls").OnElements("video")

	// checkboxes
	policy.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	policy.AllowAttrs("checked", "disabled", "data-source-position").OnElements("input")

	// for code blocks
	policy.AllowAttrs("class").Matching(regexp.MustCompile(`chroma`)).OnElements("pre")
	policy.AllowAttrs("class").Matching(regexp.MustCompile(`anchor|footnote-ref|footnote-backref`)).OnElements("a")
	policy.AllowAttrs("class").Matching(regexp.MustCompile(`heading`)).OnElements("h1", "h2", "h3", "h4", "h5", "h6", "h7", "h8")
	policy.AllowAttrs("class").Matching(regexp.MustCompile(strings.Join(slices.Collect(maps.Values(chroma.StandardTypes)), "|"))).OnElements("span")

	// centering content
	policy.AllowElements("center")

	policy.AllowAttrs("align", "style", "width", "height").Globally()
	policy.AllowStyles(
		"margin",
		"padding",
		"text-align",
		"font-weight",
		"text-decoration",
		"padding-left",
		"padding-right",
		"padding-top",
		"padding-bottom",
		"margin-left",
		"margin-right",
		"margin-top",
		"margin-bottom",
	)

	return policy
}

func descriptionPolicy() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowStandardURLs()

	// allow italics and bold.
	policy.AllowElements("i", "b", "em", "strong")

	// allow code.
	policy.AllowElements("code")

	// allow links
	policy.AllowAttrs("href", "target", "rel").OnElements("a")

	return policy
}
