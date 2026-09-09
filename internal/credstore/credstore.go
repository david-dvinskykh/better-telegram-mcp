// Package credstore persists the single-user credentials written by the `auth`
// CLI subcommand: an AES-256-GCM blob on disk, keyed either by an operator
// secret from the environment or by a machine key generated next to it.
package credstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	configFile = "config.enc"
	secretFile = ".secret"
	// PBKDF2 rounds for an operator-supplied secret. The blob is local and
	// already behind file permissions, so this is defence in depth rather than
	// the only barrier.
	pbkdf2Rounds = 200_000
	keyLen       = 32
	saltLen      = 16
)

// Store reads and writes the encrypted credential blob under a data directory.
type Store struct {
	dir string
}

// New returns a store rooted at dir (typically ~/.better-telegram-mcp).
func New(dir string) *Store { return &Store{dir: dir} }

type envelope struct {
	Version int    `json:"v"`
	Salt    string `json:"salt"`
	Nonce   string `json:"nonce"`
	Data    string `json:"data"`
}

// Load returns the saved credentials, or nil when nothing has been saved.
func (s *Store) Load() (map[string]string, error) {
	raw, err := os.ReadFile(s.path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("credential store is corrupt: %w", err)
	}
	salt, err := base64.StdEncoding.DecodeString(env.Salt)
	if err != nil {
		return nil, fmt.Errorf("credential store is corrupt: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, fmt.Errorf("credential store is corrupt: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return nil, fmt.Errorf("credential store is corrupt: %w", err)
	}

	gcm, err := s.cipher(salt)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return nil, errors.New(
			"cannot decrypt the saved credentials: the master secret changed. " +
				"Run `better-telegram-mcp logout` then `auth` to re-create them")
	}

	var out map[string]string
	if err := json.Unmarshal(plain, &out); err != nil {
		return nil, fmt.Errorf("credential store is corrupt: %w", err)
	}
	return out, nil
}

// Save replaces the stored credentials.
func (s *Store) Save(creds map[string]string) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	plain, err := json.Marshal(creds)
	if err != nil {
		return err
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	gcm, err := s.cipher(salt)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}

	blob, err := json.Marshal(envelope{
		Version: 1,
		Salt:    base64.StdEncoding.EncodeToString(salt),
		Nonce:   base64.StdEncoding.EncodeToString(nonce),
		Data:    base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, plain, nil)),
	})
	if err != nil {
		return err
	}
	return writeFilePrivate(s.path(), blob)
}

// Clear removes the stored credentials. A store that holds nothing is not an
// error: `logout` should be safe to run twice.
func (s *Store) Clear() error {
	err := os.Remove(s.path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Exists reports whether a credential blob has been written.
func (s *Store) Exists() bool {
	_, err := os.Stat(s.path())
	return err == nil
}

func (s *Store) path() string { return filepath.Join(s.dir, configFile) }

func (s *Store) cipher(salt []byte) (cipher.AEAD, error) {
	secret, err := s.secret()
	if err != nil {
		return nil, err
	}
	key, err := pbkdf2.Key(sha256.New, secret, salt, pbkdf2Rounds, keyLen)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// secret resolves the master secret: an operator-supplied one from the
// environment, else a machine key generated once next to the blob.
func (s *Store) secret() (string, error) {
	for _, key := range []string{
		"CREDENTIAL_SECRET",
		"MCP_DCR_SERVER_SECRET",
		"DCR_SERVER_SECRET",
		"MASTER_SECRET",
	} {
		if v := os.Getenv(key); v != "" {
			return v, nil
		}
	}
	return s.machineSecret()
}

func (s *Store) machineSecret() (string, error) {
	path := filepath.Join(s.dir, secretFile)
	raw, err := os.ReadFile(path)
	if err == nil && len(raw) > 0 {
		return string(raw), nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	generated := base64.RawURLEncoding.EncodeToString(buf)
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", err
	}
	if err := writeFilePrivate(path, []byte(generated)); err != nil {
		return "", err
	}
	return generated, nil
}

// writeFilePrivate writes atomically with 0600 permissions, so a half-written
// blob never replaces a good one and no other user can read it.
func writeFilePrivate(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
