package insights

import (
	"regexp"
	"strings"
)

// template.go is the one placeholder syntax: {{key}}, where key is a payload
// field of the insight being narrated. The deterministic templates in types.go
// and the model's narration (internal/insights/narrate) are both rendered by
// RenderTemplate, so a number reaches prose by exactly one road — substitution
// of a payload value — whoever wrote the words around it.

var placeholderRE = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_]*)\s*\}\}`)

// Placeholders returns the keys a text references, in order of appearance.
func Placeholders(text string) []string {
	var out []string
	for _, m := range placeholderRE.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

// StripPlaceholders removes every {{key}}, leaving the words around them.
func StripPlaceholders(text string) string {
	return placeholderRE.ReplaceAllString(text, " ")
}

// RenderTemplate substitutes payload values for placeholders. An unknown key
// renders as "?" — never as an empty string, which would read as a sentence
// with a number deliberately left out. Callers that must not render an
// unknown key (the narrator) check Placeholders against the payload first.
func RenderTemplate(text string, payload map[string]string) string {
	return placeholderRE.ReplaceAllStringFunc(text, func(m string) string {
		k := strings.TrimSpace(strings.Trim(m, "{}"))
		if v, ok := payload[k]; ok {
			return v
		}
		return "?"
	})
}
