package telegram

import (
	"fmt"
	"strings"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/aliases"
)

// AliasHolder is implemented by a backend that carries the local alias map, so
// the tools can read and edit it without a second path to the file.
type AliasHolder interface {
	Aliases() *aliases.Store
}

// Aliases returns the store behind a backend, or an empty one when the backend
// has none. It never returns nil, so a caller can list aliases on a server that
// was built without a data directory.
func AliasesOf(backend Backend) *aliases.Store {
	if holder, ok := backend.(AliasHolder); ok {
		if store := holder.Aliases(); store != nil {
			return store
		}
	}
	return aliases.New("")
}

// looksLikeHandle reports whether Telegram can resolve a reference on its own.
// Anything else -- "андрей бекендер", "мама" -- has to come from the alias map.
func looksLikeHandle(reference string) bool {
	trimmed := strings.TrimPrefix(strings.TrimSpace(reference), "@")
	if len(trimmed) < 5 {
		return false
	}
	for _, r := range trimmed {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			continue
		}
		return false
	}
	return true
}

// unknownReference is what a name nobody has explained yet gets back. It is
// written as an instruction because the way out is a question to the person
// asking, not another guess by the model.
func unknownReference(reference string, cause error) error {
	if looksLikeHandle(reference) {
		return cause
	}
	return fmt.Errorf(
		"Telegram does not know %q, and no alias for it is saved. "+
			"Ask who that is (a @username, a phone, or a chat from `chat` list), then "+
			"save the answer with contact(action=\"alias_set\", alias=%q, chat_id=<theirs>) "+
			"and retry -- that wording will resolve on its own from then on.",
		reference, reference)
}
