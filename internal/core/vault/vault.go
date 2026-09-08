package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
)

const (
	saltLen   = 16
	nonceLen  = 12
	keyLen    = 32
	argonTime = 1
	argonMem  = 64 * 1024
	argonPar  = 4
)

var ErrDecryptFailed = errors.New("decryption failed: wrong password or corrupted data")

type Vault struct {
	password string
}

func New(password string) *Vault {
	return &Vault{password: password}
}

func NewFromEnv() (*Vault, error) {
	key := os.Getenv("GHOSTCHROME_VAULT_KEY")
	if key == "" {
		return nil, fmt.Errorf("GHOSTCHROME_VAULT_KEY is not set")
	}
	return New(key), nil
}

func (v *Vault) Encrypt(plaintext []byte) ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(v.password), salt, argonTime, argonMem, argonPar, keyLen)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Format: salt (16) || nonce (12) || ciphertext+tag
	out := make([]byte, 0, saltLen+nonceLen+len(ciphertext))
	out = append(out, salt...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return out, nil
}

func (v *Vault) Decrypt(data []byte) ([]byte, error) {
	minLen := saltLen + nonceLen + aes.BlockSize
	if len(data) < minLen {
		return nil, ErrDecryptFailed
	}

	salt := data[:saltLen]
	nonce := data[saltLen : saltLen+nonceLen]
	ciphertext := data[saltLen+nonceLen:]

	key := argon2.IDKey([]byte(v.password), salt, argonTime, argonMem, argonPar, keyLen)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plaintext, nil
}

func (v *Vault) SaveFile(path string, data any) error {
	clean := filepath.Clean(path)
	plaintext, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	encrypted, err := v.Encrypt(plaintext)
	if err != nil {
		return err
	}
	return os.WriteFile(clean, encrypted, 0o600)
}

func (v *Vault) LoadFile(path string, dst any) error {
	clean := filepath.Clean(path)
	data, err := os.ReadFile(clean)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	plaintext, err := v.Decrypt(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(plaintext, dst)
}
