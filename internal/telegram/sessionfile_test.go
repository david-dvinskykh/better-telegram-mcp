package telegram

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gotd/td/session"
)

// A session file that exists but is empty is what the server pre-creates before
// the first sign-in. It has to read as "no session", or gotd fails on an
// unmarshal error instead of starting one.
func TestEmptySessionFileReadsAsNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.session")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newSessionStorage(path).LoadSession(context.Background()); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestMissingSessionFileReadsAsNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.session")
	if _, err := newSessionStorage(path).LoadSession(context.Background()); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestStoreThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "round.session")
	storage := newSessionStorage(path)
	want := []byte(`{"dc":2}`)
	if err := storage.StoreSession(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := storage.LoadSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("session file mode is %v, want 0600", perm)
	}
}

// The bug this storage exists for: writers that truncate the file let a reader
// see a session that will not parse, and gotd answers that by asking Telegram
// for a brand-new auth key. A reader must only ever see a whole session.
func TestConcurrentWritersNeverExposeAPartialSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.session")
	storage := newSessionStorage(path)
	ctx := context.Background()

	// Two distinct whole values, both big enough that a truncating writer would
	// leave a reader holding a prefix.
	first := bytes.Repeat([]byte("a"), 64*1024)
	second := bytes.Repeat([]byte("b"), 64*1024)
	if err := storage.StoreSession(ctx, first); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			value := first
			if i%2 == 1 {
				value = second
			}
			if err := storage.StoreSession(ctx, value); err != nil {
				t.Errorf("store: %v", err)
				break
			}
		}
		close(stop)
	}()

	// A separate storage on the same path stands in for the other process the
	// shared lock allows: it shares nothing but the file and the lock.
	reader := newSessionStorage(path)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			got, err := reader.LoadSession(ctx)
			if err != nil {
				t.Errorf("load: %v", err)
				return
			}
			if !bytes.Equal(got, first) && !bytes.Equal(got, second) {
				t.Errorf("read a partial session: %d bytes", len(got))
				return
			}
		}
	}()

	wg.Wait()
}

// A write leaves nothing behind: a directory filling with temporary files would
// be its own outage.
func TestStoreLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tidy.session")
	storage := newSessionStorage(path)
	for i := 0; i < 5; i++ {
		if err := storage.StoreSession(context.Background(), []byte("session")); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".tmp" {
			t.Errorf("temporary file left behind: %s", entry.Name())
		}
	}
}
