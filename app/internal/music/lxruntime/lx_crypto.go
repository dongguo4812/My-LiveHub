//go:build lxquickjs && cgo

package lxruntime

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
)

const lxRSABlockSize = 128

// lxCryptoRSAEncrypt mirrors the desktop preload's RSA_NO_PADDING operation:
// the input is left-padded to 128 bytes, then exponentiated with the public key.
func lxCryptoRSAEncrypt(input, encodedKey []byte) ([]byte, error) {
	if len(input) > lxRSABlockSize {
		return nil, fmt.Errorf("RSA input exceeds %d bytes", lxRSABlockSize)
	}
	key, err := parseLXRSAPublicKey(encodedKey)
	if err != nil {
		return nil, err
	}
	if key.Size() != lxRSABlockSize {
		return nil, fmt.Errorf("RSA key must be exactly %d bytes (1024-bit)", lxRSABlockSize)
	}
	block := make([]byte, lxRSABlockSize)
	copy(block[len(block)-len(input):], input)
	message := new(big.Int).SetBytes(block)
	modulus := key.N
	if message.Cmp(modulus) >= 0 {
		return nil, errors.New("RSA input representative is greater than or equal to modulus")
	}
	result := new(big.Int).Exp(message, big.NewInt(int64(key.E)), modulus)
	out := make([]byte, lxRSABlockSize)
	result.FillBytes(out)
	return out, nil
}

func parseLXRSAPublicKey(data []byte) (*rsa.PublicKey, error) {
	der := bytes.TrimSpace(data)
	if block, _ := pem.Decode(der); block != nil {
		der = block.Bytes
	}
	if key, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err == nil {
		if key, ok := parsed.(*rsa.PublicKey); ok {
			return key, nil
		}
		return nil, errors.New("RSA public key is not an RSA key")
	}
	return nil, fmt.Errorf("invalid RSA public key (expected PEM, PKCS#1 DER, or PKIX DER): %w", err)
}
