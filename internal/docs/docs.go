// Package docs embeds the per-topic tool documentation the `help` tool serves.
// Embedding keeps the binary self-contained: a deployment is one file.
package docs

import (
	"embed"
	"strings"
)

//go:embed *.md
var files embed.FS

// Load returns the document for a topic, or "" when there is none.
func Load(topic string) string {
	raw, err := files.ReadFile(topic + ".md")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
