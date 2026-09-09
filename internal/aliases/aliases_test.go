package aliases

import (
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "aliases.json"))
}

// Spellings a person would consider the same must collide, or the same name
// typed twice ends up pointing at two different people.
func TestKeyFoldsTheSpellingsAPersonTreatsAsOne(t *testing.T) {
	const want = "андрей бекендер"
	for _, spelling := range []string{
		"Андрей Бекендер", "  андрей   бекендер ", "@андрей бекендер", "Андрёй Бекендер",
	} {
		if got := Key(spelling); got != want {
			t.Errorf("Key(%q) = %q, want %q -- these spellings must not split into "+
				"separate aliases", spelling, got, want)
		}
	}
}

// An alias that looks like a handle would shadow the real account of that name
// for every tool, which is how a message reaches the wrong person.
func TestReservedRefusesHandlesAndSelf(t *testing.T) {
	for _, key := range []string{"", "me", "self", "durov", "some_channel"} {
		if Reserved(key) == "" {
			t.Errorf("%q should be refused as an alias", key)
		}
	}
	for _, key := range []string{"андрей бекендер", "мама", "bob"} {
		if reason := Reserved(key); reason != "" {
			t.Errorf("%q should be allowed, got %q", key, reason)
		}
	}
}

func TestSaveAndLookupRoundTrip(t *testing.T) {
	store := newStore(t)
	if _, err := store.Save("Андрей Бекендер", Entry{ID: 42, Name: "Andrey"}, false); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// The lookup normalises too, so the wording does not have to be repeated
	// exactly as it was saved.
	entry, ok := store.Lookup("  андрей бекендер ")
	if !ok || entry.ID != 42 {
		t.Errorf("expected the saved id back, got %#v (found=%v)", entry, ok)
	}
	if _, ok := store.Lookup("someone else"); ok {
		t.Error("an unsaved alias must not resolve")
	}
}

// Repointing an alias silently is how a message goes to the wrong person, so it
// takes an explicit replace.
func TestRepointingNeedsReplace(t *testing.T) {
	store := newStore(t)
	if _, err := store.Save("бекендер", Entry{ID: 1}, false); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	existing, err := store.Save("бекендер", Entry{ID: 2}, false)
	if err == nil {
		t.Fatal("repointing without replace should fail")
	}
	if existing.ID != 1 {
		t.Errorf("the error should report who the alias points at now, got %#v", existing)
	}
	if entry, _ := store.Lookup("бекендер"); entry.ID != 1 {
		t.Error("the stored alias must not have changed")
	}

	if _, err := store.Save("бекендер", Entry{ID: 2}, true); err != nil {
		t.Fatalf("replace should succeed: %v", err)
	}
	if entry, _ := store.Lookup("бекендер"); entry.ID != 2 {
		t.Error("replace did not take effect")
	}
}

// Saving the same id under several words is how tags work: either resolves.
func TestOnePersonMayHaveSeveralAliases(t *testing.T) {
	store := newStore(t)
	for _, alias := range []string{"андрей бекендер", "бекендер"} {
		if _, err := store.Save(alias, Entry{ID: 7}, false); err != nil {
			t.Fatalf("save %q failed: %v", alias, err)
		}
	}
	for _, alias := range []string{"андрей бекендер", "Бекендер"} {
		if entry, ok := store.Lookup(alias); !ok || entry.ID != 7 {
			t.Errorf("%q did not resolve to the shared id", alias)
		}
	}
}

func TestDeleteReportsWhetherThereWasOne(t *testing.T) {
	store := newStore(t)
	if _, err := store.Save("мама", Entry{ID: 3}, false); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	deleted, err := store.Delete("Мама")
	if err != nil || !deleted {
		t.Fatalf("expected a delete, got %v %v", deleted, err)
	}
	deleted, err = store.Delete("мама")
	if err != nil || deleted {
		t.Errorf("deleting a missing alias should report false, got %v %v", deleted, err)
	}
}

// This runs inside peer resolution on every call, so a damaged file must cost
// nothing more than the aliases it held.
func TestADamagedFileResolvesToNothingRatherThanFailing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aliases.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if entries := New(path).Load(); len(entries) != 0 {
		t.Errorf("expected an empty map, got %#v", entries)
	}
}

// A file written by an earlier, simpler version maps the alias straight to an
// id. Those installs must keep resolving.
func TestAFlatFileStillResolves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aliases.json")
	if err := os.WriteFile(path, []byte(`{"Мама": 99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	entry, ok := New(path).Lookup("мама")
	if !ok || entry.ID != 99 {
		t.Errorf("expected the flat id back, got %#v (found=%v)", entry, ok)
	}
}

// The file maps nicknames to real people, so it must not be world-readable.
func TestTheFileIsWrittenPrivate(t *testing.T) {
	store := newStore(t)
	if _, err := store.Save("мама", Entry{ID: 3}, false); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("expected 0600, got %o", mode)
	}
}
