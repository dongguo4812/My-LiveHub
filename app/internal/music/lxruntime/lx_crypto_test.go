//go:build lxquickjs

package lxruntime

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"testing"
)

func TestLXCryptoMD5MatchesNodeDigestFormat(t *testing.T) {
	if got := lxCryptoMD5([]byte("abc")); got != "900150983cd24fb0d6963f7d28e17f72" {
		t.Fatalf("md5 = %q", got)
	}
}

func TestLXCryptoRandomBytesBoundsAndLength(t *testing.T) {
	value, err := lxCryptoRandomBytes(48)
	if err != nil {
		t.Fatal(err)
	}
	if len(value) != 48 {
		t.Fatalf("random byte length = %d", len(value))
	}
	if _, err := lxCryptoRandomBytes(-1); err == nil {
		t.Fatal("expected negative size rejection")
	}
	if _, err := lxCryptoRandomBytes((1 << 20) + 1); err == nil {
		t.Fatal("expected oversized request rejection")
	}
}

func TestLXCryptoAESEncryptCBCUsesPKCS7Padding(t *testing.T) {
	key, _ := hex.DecodeString("2b7e151628aed2a6abf7158809cf4f3c")
	iv, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	plain := []byte("hello LX source")
	encrypted, err := lxCryptoAESEncrypt(plain, "aes-128-cbc", key, iv)
	if err != nil {
		t.Fatal(err)
	}
	if len(encrypted) != aes.BlockSize {
		t.Fatalf("ciphertext length = %d, want one padded block", len(encrypted))
	}
	block, _ := aes.NewCipher(key)
	decrypted := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(decrypted, encrypted)
	padding := int(decrypted[len(decrypted)-1])
	if padding < 1 || padding > aes.BlockSize || !bytes.Equal(decrypted[:len(decrypted)-padding], plain) {
		t.Fatalf("decrypted plaintext/padding mismatch: %x", decrypted)
	}
	for _, b := range decrypted[len(decrypted)-padding:] {
		if int(b) != padding {
			t.Fatalf("invalid PKCS7 padding: %x", decrypted)
		}
	}
}

func TestLXCryptoAESEncryptECBAndRejectsBadIV(t *testing.T) {
	key := []byte("0123456789abcdef")
	encrypted, err := lxCryptoAESEncrypt([]byte("abc"), "aes-128-ecb", key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(encrypted) != aes.BlockSize {
		t.Fatalf("ECB ciphertext length = %d", len(encrypted))
	}
	if _, err := lxCryptoAESEncrypt([]byte("abc"), "aes-128-cbc", key, nil); err == nil {
		t.Fatal("expected missing CBC IV to fail")
	}
}
