package crypto

import (
	"strings"
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	SetKey("test-passphrase-for-unit-tests")
	defer func() {
		mu.Lock()
		activeKey = nil
		mu.Unlock()
	}()

	plaintext := "discord://token123/channel456"
	encrypted, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt error = %v", err)
	}
	if !strings.HasPrefix(encrypted, "enc:v1:") {
		t.Fatalf("expected enc:v1: prefix, got %q", encrypted)
	}
	if encrypted == plaintext {
		t.Fatal("encrypted value should differ from plaintext")
	}

	decrypted, err := Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt error = %v", err)
	}
	if decrypted != plaintext {
		t.Fatalf("roundtrip mismatch: got %q, want %q", decrypted, plaintext)
	}
}

func TestMaybeEncryptMaybeDecrypt(t *testing.T) {
	SetKey("another-test-key")
	defer func() {
		mu.Lock()
		activeKey = nil
		mu.Unlock()
	}()

	original := "ntfys://ntfy.sh/my-topic"
	enc, err := MaybeEncrypt(original)
	if err != nil {
		t.Fatalf("MaybeEncrypt error = %v", err)
	}
	if !IsEncrypted(enc) {
		t.Fatal("expected encrypted output")
	}

	// MaybeEncrypt on already-encrypted value is a no-op
	enc2, err := MaybeEncrypt(enc)
	if err != nil {
		t.Fatalf("MaybeEncrypt(already-encrypted) error = %v", err)
	}
	if enc2 != enc {
		t.Fatal("MaybeEncrypt should not re-encrypt")
	}

	dec, err := MaybeDecrypt(enc)
	if err != nil {
		t.Fatalf("MaybeDecrypt error = %v", err)
	}
	if dec != original {
		t.Fatalf("MaybeDecrypt roundtrip: got %q, want %q", dec, original)
	}

	// Plaintext passes through MaybeDecrypt unchanged
	pass, err := MaybeDecrypt("plain-value")
	if err != nil {
		t.Fatalf("MaybeDecrypt(plain) error = %v", err)
	}
	if pass != "plain-value" {
		t.Fatalf("MaybeDecrypt(plain) should return unchanged, got %q", pass)
	}
}

func TestNoKeyPassthrough(t *testing.T) {
	mu.Lock()
	activeKey = nil
	mu.Unlock()

	// Without a key, Encrypt returns plaintext unchanged
	enc, err := MaybeEncrypt("some-secret")
	if err != nil {
		t.Fatalf("MaybeEncrypt (no key) error = %v", err)
	}
	if enc != "some-secret" {
		t.Fatalf("expected passthrough, got %q", enc)
	}

	// Without a key, MaybeDecrypt on plaintext returns it unchanged
	dec, err := MaybeDecrypt("some-secret")
	if err != nil {
		t.Fatalf("MaybeDecrypt (no key, plain) error = %v", err)
	}
	if dec != "some-secret" {
		t.Fatalf("expected passthrough, got %q", dec)
	}

	// Without a key, MaybeDecrypt on an encrypted value returns an error
	_, err = MaybeDecrypt("enc:v1:abc123")
	if err == nil {
		t.Fatal("expected error when decrypting without key")
	}
}

func TestEncryptProducesUniqueNonces(t *testing.T) {
	SetKey("nonce-uniqueness-test")
	defer func() {
		mu.Lock()
		activeKey = nil
		mu.Unlock()
	}()
	a, _ := Encrypt("same-value")
	b, _ := Encrypt("same-value")
	if a == b {
		t.Fatal("two encryptions of the same value should produce different ciphertexts")
	}
	// Both should still decrypt to the same plaintext
	da, _ := Decrypt(a)
	db, _ := Decrypt(b)
	if da != "same-value" || db != "same-value" {
		t.Fatal("both ciphertexts should decrypt to same-value")
	}
}
