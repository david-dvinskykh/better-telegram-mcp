package tools

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/docs"
)

var helpTopics = []string{"chats", "contacts", "folders", "media", "messages", "profile"}

// HandleHelp returns the documentation for one topic, or all of it.
//
// It answers with markdown, not a result object: the whole point is text the
// model can read. Only the error case is JSON, so a caller can tell a failure
// from a document.
func HandleHelp(topic string) string {
	if topic == "" || topic == "all" || topic == "telegram" {
		parts := make([]string, 0, len(helpTopics))
		for _, name := range helpTopics {
			if doc := docs.Load(name); doc != "" {
				parts = append(parts, doc)
			}
		}
		if len(parts) == 0 {
			return helpError("No documentation found.")
		}
		return strings.Join(parts, "\n\n---\n\n")
	}

	if !slicesContains(helpTopics, topic) {
		valid := append(append([]string(nil), helpTopics...), "all", "telegram")
		sort.Strings(valid)
		suggestion := ""
		if closest := closestMatch(topic, valid); closest != "" {
			suggestion = " Did you mean '" + closest + "'?"
		}
		return helpError("Unknown topic '" + topic + "'." + suggestion +
			" Valid: telegram|messages|chats|media|contacts|profile|folders|all")
	}

	if doc := docs.Load(topic); doc != "" {
		return doc
	}
	return helpError("Documentation for '" + topic + "' not found.")
}

func helpError(message string) string {
	encoded, err := json.Marshal(map[string]string{"error": message})
	if err != nil {
		return `{"error": "Documentation lookup failed."}`
	}
	return string(encoded)
}

func slicesContains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
