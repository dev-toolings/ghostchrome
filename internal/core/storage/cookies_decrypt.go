package storage

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"crypto/sha1"
	"golang.org/x/crypto/pbkdf2"

	_ "modernc.org/sqlite"
)

// macOSKeychainPassword retrieves the "Chrome Safe Storage" password from
// macOS Keychain via the `security` CLI. This is the symmetric secret Chrome
// uses to derive the AES key that encrypts cookies on macOS.
func macOSKeychainPassword() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("keychain decrypt only supported on darwin")
	}
	cmd := exec.Command("security", "find-generic-password", "-wa", "Chrome")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("keychain query: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// chromeAESKey derives the 16-byte AES key from the Keychain password using
// the well-known constants Chrome uses on macOS: salt "saltysalt", 1003
// iterations of PBKDF2-SHA1.
func chromeAESKey(password string) []byte {
	return pbkdf2.Key([]byte(password), []byte("saltysalt"), 1003, 16, sha1.New)
}

// decryptChromeCookieMac decrypts a v10-tagged Chrome cookie value on macOS.
// Returns the plaintext string (which may have a 32-byte SHA-256 hash prefix
// for newer Chrome versions — we strip it heuristically).
func decryptChromeCookieMac(encrypted []byte, key []byte) (string, error) {
	if len(encrypted) < 3 {
		return "", fmt.Errorf("encrypted value too short")
	}
	if !bytes.HasPrefix(encrypted, []byte("v10")) {
		return "", fmt.Errorf("unsupported encryption prefix %q", string(encrypted[:3]))
	}
	ciphertext := encrypted[3:]
	if len(ciphertext)%aes.BlockSize != 0 || len(ciphertext) == 0 {
		return "", fmt.Errorf("ciphertext length %d not multiple of %d", len(ciphertext), aes.BlockSize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	iv := bytes.Repeat([]byte{' '}, aes.BlockSize)
	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)
	plaintext = pkcs7Unpad(plaintext)
	// Newer Chrome (v10 on macOS recently) prepends a 32-byte SHA256 of the
	// host_key as integrity prefix. Best-effort strip: if first 32 bytes are
	// non-printable noise and remainder is printable, drop it.
	if len(plaintext) > 32 && !isProbablyPrintable(plaintext[:8]) && isProbablyPrintable(plaintext[32:40]) {
		plaintext = plaintext[32:]
	}
	return string(plaintext), nil
}

func pkcs7Unpad(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	pad := int(data[len(data)-1])
	if pad <= 0 || pad > aes.BlockSize || pad > len(data) {
		return data
	}
	for i := len(data) - pad; i < len(data); i++ {
		if int(data[i]) != pad {
			return data
		}
	}
	return data[:len(data)-pad]
}

func isProbablyPrintable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// CookieRecord holds the subset of fields ghostchrome injects via CDP at
// session start. Mirrors the relevant columns of Chrome's Cookies SQLite
// schema, plus the decrypted plaintext value.
type CookieRecord struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"` // CDP unix seconds, 0 = session
	Secure   bool    `json:"secure"`
	HTTPOnly bool    `json:"httpOnly"`
	SameSite string  `json:"sameSite,omitempty"` // "Strict"|"Lax"|"None"|""
}

// ExportDecryptedCookies opens the SQLite Cookies db at path, decrypts every
// v10-tagged encrypted_value via macOS Keychain, and returns the list as
// portable CookieRecord values. Source file is not modified.
func ExportDecryptedCookies(cookiesPath string) ([]CookieRecord, error) {
	password, err := macOSKeychainPassword()
	if err != nil {
		return nil, err
	}
	key := chromeAESKey(password)

	db, err := sql.Open("sqlite", cookiesPath+"?mode=ro&immutable=1")
	if err != nil {
		return nil, fmt.Errorf("open cookies db: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT name, host_key, path, expires_utc, is_secure, is_httponly, samesite, value, encrypted_value FROM cookies`)
	if err != nil {
		return nil, fmt.Errorf("query cookies: %w", err)
	}
	defer rows.Close()

	out := make([]CookieRecord, 0, 1024)
	for rows.Next() {
		var (
			name, host, path string
			expiresUTC       int64
			isSecure         int
			isHTTPOnly       int
			sameSite         int
			plainValue       string
			encValue         []byte
		)
		if err := rows.Scan(&name, &host, &path, &expiresUTC, &isSecure, &isHTTPOnly, &sameSite, &plainValue, &encValue); err != nil {
			continue
		}
		value := plainValue
		if len(encValue) > 0 {
			if dec, derr := decryptChromeCookieMac(encValue, key); derr == nil {
				value = dec
			}
		}
		// Chrome stores expires_utc as microseconds since 1601-01-01.
		// Convert to unix seconds.
		expires := 0.0
		if expiresUTC > 0 {
			expires = float64(expiresUTC)/1e6 - 11644473600
		}
		ss := ""
		switch sameSite {
		case 0:
			ss = "None"
		case 1:
			ss = "Lax"
		case 2:
			ss = "Strict"
		}
		out = append(out, CookieRecord{
			Name:     name,
			Value:    value,
			Domain:   host,
			Path:     path,
			Expires:  expires,
			Secure:   isSecure != 0,
			HTTPOnly: isHTTPOnly != 0,
			SameSite: ss,
		})
	}
	return out, nil
}

// DecryptCookiesInPlace opens the SQLite Cookies database at path, decrypts
// every v10-tagged encrypted_value using the macOS Keychain Chrome key, and
// rewrites the row with the plaintext in `value` (encrypted_value cleared).
// Kept for backward compat with profiles imported before the JSON export
// scheme. Returns counts: total, decrypted, failed.
func DecryptCookiesInPlace(cookiesPath string) (total, decrypted, failed int, err error) {
	password, err := macOSKeychainPassword()
	if err != nil {
		return 0, 0, 0, err
	}
	key := chromeAESKey(password)

	db, err := sql.Open("sqlite", cookiesPath)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("open cookies db: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT rowid, encrypted_value FROM cookies WHERE encrypted_value IS NOT NULL AND length(encrypted_value) > 0`)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("query cookies: %w", err)
	}
	defer rows.Close()

	type pending struct {
		rowid int64
		plain string
	}
	var todo []pending
	for rows.Next() {
		var rowid int64
		var enc []byte
		if err := rows.Scan(&rowid, &enc); err != nil {
			continue
		}
		total++
		plain, derr := decryptChromeCookieMac(enc, key)
		if derr != nil {
			failed++
			continue
		}
		todo = append(todo, pending{rowid, plain})
	}
	rows.Close()

	tx, err := db.Begin()
	if err != nil {
		return total, 0, failed, err
	}
	stmt, err := tx.Prepare(`UPDATE cookies SET value = ?, encrypted_value = x'' WHERE rowid = ?`)
	if err != nil {
		_ = tx.Rollback()
		return total, 0, failed, err
	}
	defer stmt.Close()
	for _, p := range todo {
		if _, err := stmt.Exec(p.plain, p.rowid); err != nil {
			failed++
			continue
		}
		decrypted++
	}
	if err := tx.Commit(); err != nil {
		return total, decrypted, failed, err
	}
	return total, decrypted, failed, nil
}
