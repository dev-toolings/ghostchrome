package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	v := New("test-password-123")
	plaintext := []byte(`{"cookies":[{"name":"session","value":"abc123"}]}`)

	encrypted, err := v.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if bytes.Equal(encrypted, plaintext) {
		t.Fatal("encrypted data should differ from plaintext")
	}
	if len(encrypted) < saltLen+nonceLen+len(plaintext) {
		t.Fatal("encrypted data too short")
	}

	decrypted, err := v.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("roundtrip failed: got %q", decrypted)
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	v1 := New("correct-password")
	v2 := New("wrong-password")

	encrypted, err := v1.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = v2.Decrypt(encrypted)
	if !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("expected ErrDecryptFailed, got %v", err)
	}
}

func TestDecryptCorruptedData(t *testing.T) {
	v := New("password")

	_, err := v.Decrypt([]byte("too-short"))
	if !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("expected ErrDecryptFailed for short data, got %v", err)
	}

	encrypted, err := v.Encrypt([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	encrypted[len(encrypted)-1] ^= 0xFF
	_, err = v.Decrypt(encrypted)
	if !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("expected ErrDecryptFailed for tampered data, got %v", err)
	}
}

func TestEncryptProducesDifferentOutput(t *testing.T) {
	v := New("password")
	plaintext := []byte("same input")

	e1, _ := v.Encrypt(plaintext)
	e2, _ := v.Encrypt(plaintext)

	if bytes.Equal(e1, e2) {
		t.Fatal("two encryptions of the same plaintext should produce different ciphertext (random salt+nonce)")
	}
}

func TestSaveFileLoadFile(t *testing.T) {
	v := New("file-test-pw")
	dir := t.TempDir()
	path := filepath.Join(dir, "state.enc")

	type testData struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	original := testData{Name: "test", Value: 42}
	if err := v.SaveFile(path, &original); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if json.Valid(raw) {
		t.Fatal("saved file should be encrypted, not valid JSON")
	}

	var loaded testData
	if err := v.LoadFile(path, &loaded); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if loaded.Name != original.Name || loaded.Value != original.Value {
		t.Fatalf("roundtrip mismatch: got %+v", loaded)
	}
}

func TestNewFromEnv(t *testing.T) {
	t.Setenv("GHOSTCHROME_VAULT_KEY", "env-key-123")
	v, err := NewFromEnv()
	if err != nil {
		t.Fatalf("NewFromEnv: %v", err)
	}

	encrypted, err := v.Encrypt([]byte("via env"))
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := v.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if string(decrypted) != "via env" {
		t.Fatalf("got %q", decrypted)
	}
}

func TestNewFromEnvMissing(t *testing.T) {
	t.Setenv("GHOSTCHROME_VAULT_KEY", "")
	_, err := NewFromEnv()
	if err == nil {
		t.Fatal("expected error when env var is empty")
	}
}
