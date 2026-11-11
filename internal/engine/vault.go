package engine

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// ParseKey reads the 32-byte key that encrypts stored credentials, base64 or
// base64url, padded or not.
func ParseKey(v string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if k, err := enc.DecodeString(v); err == nil && len(k) == 32 {
			return k, nil
		}
	}
	return nil, errors.New("the coach key must be 32 bytes, base64 encoded")
}

// ErrVaultUnreadable means a stored credential exists but cannot be read: wrong
// key, a damaged file, or another person's file moved under this name. The cause
// is never surfaced, since it could quote file content.
var ErrVaultUnreadable = errors.New("stored credential is unreadable")

// Vault keeps one encrypted file per person. The file name is a hash of the
// person and the person is bound into the ciphertext as associated data, so a
// file copied to another person's name does not decrypt. Callers serialize
// writes per person.
type Vault struct {
	dir string
	gcm cipher.AEAD
}

func NewVault(dir string, key []byte) (*Vault, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{dir: dir, gcm: gcm}, nil
}

// subject is what binds a file to a person. User ids are this server's own
// accounts, so the id is the subject.
func subject(userID int64) []byte {
	return []byte("training-mcp/user/" + strconv.FormatInt(userID, 10))
}

func (v *Vault) path(userID int64) string {
	sum := sha256.Sum256(subject(userID))
	return filepath.Join(v.dir, hex.EncodeToString(sum[:])+".json")
}

type vaultFile struct {
	V    int    `json:"v"`
	IV   string `json:"iv"`
	Data string `json:"data"`
}

// Read returns nil, nil when the person has no stored credential.
func (v *Vault) Read(userID int64) (*Credential, error) {
	raw, err := os.ReadFile(v.path(userID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrVaultUnreadable
	}
	var f vaultFile
	if json.Unmarshal(raw, &f) != nil || f.V != 1 {
		return nil, ErrVaultUnreadable
	}
	iv, errIV := base64.StdEncoding.DecodeString(f.IV)
	data, errData := base64.StdEncoding.DecodeString(f.Data)
	if errIV != nil || errData != nil || len(iv) != v.gcm.NonceSize() {
		return nil, ErrVaultUnreadable
	}
	plain, err := v.gcm.Open(nil, iv, data, subject(userID))
	if err != nil {
		return nil, ErrVaultUnreadable
	}
	var c Credential
	if json.Unmarshal(plain, &c) != nil || !c.valid() {
		return nil, ErrVaultUnreadable
	}
	return &c, nil
}

// Write stores a credential with mode 0600, through a temporary file and a
// rename, so a crash leaves either the old file or the new one.
func (v *Vault) Write(userID int64, c *Credential) error {
	if err := os.MkdirAll(v.dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(v.dir, 0o700); err != nil {
		return err
	}
	plain, err := json.Marshal(c)
	if err != nil {
		return err
	}
	iv := make([]byte, v.gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return err
	}
	body, err := json.Marshal(vaultFile{V: 1, IV: base64.StdEncoding.EncodeToString(iv), Data: base64.StdEncoding.EncodeToString(v.gcm.Seal(nil, iv, plain, subject(userID)))})
	if err != nil {
		return err
	}
	target := v.path(userID)
	tmp, err := os.CreateTemp(v.dir, filepath.Base(target)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, target); err != nil {
		os.Remove(name)
		return fmt.Errorf("storing credential: %w", err)
	}
	return nil
}

func (v *Vault) Delete(userID int64) error {
	err := os.Remove(v.path(userID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
