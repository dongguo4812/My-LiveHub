//go:build lxquickjs

package lxruntime

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"testing"
)

func TestLXCryptoRSAEncryptMatchesRawRSAPublicOperation(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&privateKey.PublicKey)})
	input := []byte("lx-source-rsa-test")
	encrypted, err := lxCryptoRSAEncrypt(input, publicPEM)
	if err != nil {
		t.Fatal(err)
	}
	if len(encrypted) != lxRSABlockSize {
		t.Fatalf("RSA output length = %d, want %d", len(encrypted), lxRSABlockSize)
	}
	recovered := new(big.Int).Exp(new(big.Int).SetBytes(encrypted), privateKey.D, privateKey.N)
	recoveredBytes := make([]byte, lxRSABlockSize)
	recovered.FillBytes(recoveredBytes)
	want := make([]byte, lxRSABlockSize)
	copy(want[len(want)-len(input):], input)
	if !bytes.Equal(recoveredBytes, want) {
		t.Fatalf("raw RSA round-trip mismatch: got %x want %x", recoveredBytes, want)
	}
}

func TestLXCryptoRSAEncryptAcceptsPKIXDERAndRejectsInvalidInputs(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := lxCryptoRSAEncrypt([]byte{1, 2, 3}, der); err != nil || len(output) != lxRSABlockSize {
		t.Fatalf("PKIX DER RSA result length=%d err=%v", len(output), err)
	}
	if _, err := lxCryptoRSAEncrypt(make([]byte, lxRSABlockSize+1), der); err == nil {
		t.Fatal("expected oversized input rejection")
	}
	if _, err := lxCryptoRSAEncrypt([]byte("x"), []byte("not a key")); err == nil {
		t.Fatal("expected invalid key rejection")
	}
	wideKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lxCryptoRSAEncrypt([]byte("x"), x509.MarshalPKCS1PublicKey(&wideKey.PublicKey)); err == nil {
		t.Fatal("expected non-1024-bit key rejection")
	}
}
