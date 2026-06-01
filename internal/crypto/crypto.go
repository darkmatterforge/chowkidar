package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

const prefix = "enc:v1:"

var (
	mu              sync.RWMutex
	activeKey       []byte
	decryptFailures atomic.Int32
)

// SetKey derives a 32-byte AES key from the passphrase using PBKDF2-HMAC-SHA256
// and stores it for use by Encrypt/Decrypt. Call once at startup.
func SetKey(passphrase string) {
	if passphrase == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	activeKey = pbkdf2Key([]byte(passphrase), []byte("chowkidar-enc-v1"), 100_000)
	decryptFailures.Store(0)
}

// ClearKey removes the active key, disabling encryption/decryption.
// Primarily used in tests to reset state between cases.
func ClearKey() {
	mu.Lock()
	defer mu.Unlock()
	activeKey = nil
	decryptFailures.Store(0)
}

// KeyConfigured returns true if a key has been set.
func KeyConfigured() bool {
	mu.RLock()
	defer mu.RUnlock()
	return len(activeKey) > 0
}

// HasDecryptionFailures reports whether any MaybeDecrypt calls have failed since
// the last SetKey call — indicating the key has changed and secrets need recovery.
func HasDecryptionFailures() bool {
	return decryptFailures.Load() > 0
}

// ResetDecryptionFailures clears the failure counter after a successful re-key.
func ResetDecryptionFailures() {
	decryptFailures.Store(0)
}

// IsEncrypted reports whether value was produced by Encrypt.
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, prefix)
}

// Encrypt encrypts plaintext with AES-256-GCM and returns enc:v1:<base64>.
// Returns plaintext unchanged if no key is configured or the value is empty.
func Encrypt(plaintext string) (string, error) {
	mu.RLock()
	key := activeKey
	mu.RUnlock()
	if len(key) == 0 || plaintext == "" {
		return plaintext, nil
	}
	return encryptWithKey(plaintext, key)
}

// MaybeEncrypt encrypts value if a key is configured and value is not already encrypted.
func MaybeEncrypt(value string) (string, error) {
	if !KeyConfigured() || value == "" || IsEncrypted(value) {
		return value, nil
	}
	return Encrypt(value)
}

// Decrypt decrypts a value produced by Encrypt using the active key.
func Decrypt(value string) (string, error) {
	if !IsEncrypted(value) {
		return value, nil
	}
	mu.RLock()
	key := activeKey
	mu.RUnlock()
	if len(key) == 0 {
		return "", fmt.Errorf("encrypted value found but CHOWKIDAR_SECRET_KEY is not set")
	}
	return decryptWithKey(value, key)
}

// MaybeDecrypt decrypts value if it is encrypted, otherwise returns it unchanged.
// On key mismatch, increments the failure counter and returns an error so callers
// can clear the field and keep running — the user should use the re-key API to recover.
func MaybeDecrypt(value string) (string, error) {
	if !IsEncrypted(value) {
		return value, nil
	}
	mu.RLock()
	hasKey := len(activeKey) > 0
	mu.RUnlock()
	if !hasKey {
		return "", fmt.Errorf("encrypted value found but CHOWKIDAR_SECRET_KEY is not set")
	}
	plain, err := Decrypt(value)
	if err != nil {
		decryptFailures.Add(1)
		return "", fmt.Errorf("decrypt failed — CHOWKIDAR_SECRET_KEY may have changed: %w", err)
	}
	return plain, nil
}

// encryptWithKey encrypts plaintext with AES-256-GCM using the given key.
func encryptWithKey(plaintext string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptWithKey decrypts an enc:v1: value using the given key.
func decryptWithKey(value string, key []byte) (string, error) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil {
		return "", fmt.Errorf("decode encrypted value: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create gcm: %w", err)
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt failed (wrong key?): %w", err)
	}
	return string(plain), nil
}

// pbkdf2Key derives a 32-byte key using PBKDF2-HMAC-SHA256 (stdlib only).
func pbkdf2Key(password, salt []byte, iterations int) []byte {
	mac := hmac.New(sha256.New, password)
	var counter [4]byte
	binary.BigEndian.PutUint32(counter[:], 1)
	mac.Write(salt)
	mac.Write(counter[:])
	U := mac.Sum(nil)
	T := make([]byte, sha256.Size)
	copy(T, U)
	for i := 1; i < iterations; i++ {
		mac.Reset()
		mac.Write(U)
		U = mac.Sum(nil)
		for j := range T {
			T[j] ^= U[j]
		}
	}
	return T
}
