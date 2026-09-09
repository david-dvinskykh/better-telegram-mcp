// Package aliases keeps the local map from the words a person actually uses for
// someone ("андрей бекендер") to the Telegram id behind them.
//
// It exists because chat ids are unusable in conversation: a model told to
// "message Andrey" has nothing to resolve unless someone once said who that is.
// Saving the answer turns a one-off clarification into a lookup that works from
// then on, and the file is local -- it never reaches Telegram.
package aliases

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Entry is what one alias resolves to.
type Entry struct {
	ID   int64  `json:"id"`
	Name string `json:"name,omitempty"`
}

// Store is the alias file. A zero path disables persistence, which is what the
// tests and an unconfigured server use.
type Store struct{ path string }

// New builds a store over the given file.
func New(path string) *Store { return &Store{path: path} }

// Path is where this store reads and writes.
func (s *Store) Path() string { return s.path }

// handlePattern matches a Telegram username. An alias that looks like one would
// shadow the real account of that name for every tool, so those are refused.
var handlePattern = regexp.MustCompile(`^@?[a-zA-Z0-9_]{5,}$`)

// selfRefs are the words the resolver reserves for the signed-in account.
var selfRefs = map[string]bool{"me": true, "self": true}

// Key normalises an alias so spellings that look identical to a person collide
// on purpose: case, surrounding space, a leading @, and the ё/е pair, which
// Russian keyboards produce interchangeably.
func Key(text string) string {
	key := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(norm.NFC.String(text)), "@"))
	key = strings.ReplaceAll(key, "ё", "е")
	return strings.Join(strings.Fields(key), " ")
}

// Reserved reports whether a key may not be used as an alias, and why.
func Reserved(key string) string {
	switch {
	case key == "":
		return "An alias must not be empty."
	case selfRefs[key]:
		return "'" + key + "' always means the signed-in account and cannot be an alias."
	case handlePattern.MatchString(key):
		return "'" + key + "' looks like a username or id, which Telegram resolves on its " +
			"own; an alias for it would shadow the real account. Use wording a person " +
			"would say instead."
	}
	return ""
}

// Load reads the file. A missing or damaged file yields an empty map rather
// than an error: this runs inside peer resolution on every call, and a bad file
// must not take the chat tools down.
func (s *Store) Load() map[string]Entry {
	out := map[string]Entry{}
	if s.path == "" {
		return out
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return out
	}
	// A pre-existing file may hold the flat {alias: id} shape, so try that
	// before giving up on it.
	if err := json.Unmarshal(raw, &out); err != nil {
		var flat map[string]int64
		if json.Unmarshal(raw, &flat) != nil {
			return map[string]Entry{}
		}
		out = map[string]Entry{}
		for alias, id := range flat {
			out[Key(alias)] = Entry{ID: id}
		}
		return out
	}
	// Re-key on read so a hand-edited file still resolves.
	normalized := make(map[string]Entry, len(out))
	for alias, entry := range out {
		normalized[Key(alias)] = entry
	}
	return normalized
}

// Lookup resolves one reference, reporting whether it was known.
func (s *Store) Lookup(text string) (Entry, bool) {
	entry, ok := s.Load()[Key(text)]
	return entry, ok
}

// Save records an alias. It refuses to repoint an existing one unless replace
// is set, because a silently redirected alias sends messages to the wrong
// person.
func (s *Store) Save(alias string, entry Entry, replace bool) (Entry, error) {
	key := Key(alias)
	if reason := Reserved(key); reason != "" {
		return Entry{}, &Error{reason}
	}
	all := s.Load()
	if existing, ok := all[key]; ok && existing.ID != entry.ID && !replace {
		return existing, &Error{"'" + alias + "' already points at another contact. " +
			"Pass replace=true to repoint it."}
	}
	all[key] = entry
	return entry, s.write(all)
}

// Delete forgets an alias, reporting whether there was one.
func (s *Store) Delete(alias string) (bool, error) {
	all := s.Load()
	key := Key(alias)
	if _, ok := all[key]; !ok {
		return false, nil
	}
	delete(all, key)
	return true, s.write(all)
}

// List returns every alias, keyed by its normalised form.
func (s *Store) List() map[string]Entry { return s.Load() }

// write replaces the file atomically at 0600: it maps nicknames to real people.
func (s *Store) write(all map[string]Entry) error {
	if s.path == "" {
		return &Error{"No alias file is configured for this server."}
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	temp := s.path + ".tmp"
	if err := os.WriteFile(temp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, s.path)
}

// Error is a message written for the caller to read, so the dispatch layer may
// pass it through instead of reporting it by type.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }
