//go:build lxquickjs

package lxruntime

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"strconv"
	"testing"
	"time"
)

// This exercises the real QuickJS -> C callback -> cgo -> Go RSA path.
// The result is checked against an independent raw RSA public operation.
func TestLXHostRSAEncryptUsesNativeCallbackAndNodeBuffer(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: x509.MarshalPKCS1PublicKey(&privateKey.PublicKey),
	})
	input := []byte("LX RSA host contract")
	inputHex := hex.EncodeToString(input)
	pemText := string(publicPEM)

	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "rsa-host-contract"}); err != nil {
		t.Fatal(err)
	}

	script := `
const input = Buffer.from(` + strconv.Quote(inputHex) + `, 'hex');
const publicKey = Buffer.from(` + strconv.Quote(pemText) + `, 'utf8');
const encrypted = lx.utils.crypto.rsaEncrypt(input, publicKey);
if (!(encrypted instanceof Uint8Array)) throw new Error('rsaEncrypt did not return Uint8Array');
if (encrypted.length !== 128) throw new Error('rsaEncrypt returned ' + encrypted.length + ' bytes');
Buffer.from(encrypted).toString('hex');
`
	value, err := rt.Eval(context.Background(), script, "lx-rsa-host-contract.js")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("RSA host result is not hex: %v", err)
	}
	if len(encrypted) != lxRSABlockSize {
		t.Fatalf("RSA host output length = %d, want %d", len(encrypted), lxRSABlockSize)
	}

	// RSA_NO_PADDING decrypts the public-operation result to the exact
	// 128-byte left-padded input block.
	recovered := new(big.Int).Exp(new(big.Int).SetBytes(encrypted), privateKey.D, privateKey.N)
	recoveredBlock := make([]byte, lxRSABlockSize)
	recovered.FillBytes(recoveredBlock)
	want := make([]byte, lxRSABlockSize)
	copy(want[len(want)-len(input):], input)
	if string(recoveredBlock) != string(want) {
		t.Fatalf("RSA host raw operation mismatch: got %x, want %x", recoveredBlock, want)
	}
}
