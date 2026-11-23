//go:build lxquickjs || lxproduction

package lxruntime

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/md5"
	"crypto/rsa"
	"crypto/x509"
	"bytes"
	"encoding/pem"
	"math/big"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// lxCryptoMD5 mirrors crypto.createHash('md5').update(str).digest('hex').
func lxCryptoMD5(value []byte) string {
	sum := md5.Sum(value)
	return hex.EncodeToString(sum[:])
}

// lxCryptoRandomBytes mirrors randomBytes for bounded source requests.
func lxCryptoRandomBytes(size int) ([]byte, error) {
	if size < 0 || size > 1<<20 { return nil, fmt.Errorf("randomBytes size out of range: %d", size) }
	out := make([]byte, size)
	if _, err := rand.Read(out); err != nil { return nil, err }
	return out, nil
}

// lxCryptoAESEncrypt mirrors Node's createCipheriv with default PKCS#7 padding.
func lxCryptoAESEncrypt(plaintext []byte, mode string, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil { return nil, fmt.Errorf("invalid AES key: %w", err) }
	mode = strings.ToLower(mode)
	var encrypt cipher.BlockMode
	switch mode {
	case "aes-128-cbc", "aes-192-cbc", "aes-256-cbc":
		if len(iv) != aes.BlockSize { return nil, fmt.Errorf("AES-CBC IV must be %d bytes", aes.BlockSize) }
		encrypt = cipher.NewCBCEncrypter(block, iv)
	case "aes-128-ecb", "aes-192-ecb", "aes-256-ecb":
		if len(iv) != 0 { return nil, errors.New("AES-ECB does not accept an IV") }
		encrypt = ecbEncrypter{block}
	default:
		return nil, fmt.Errorf("unsupported AES mode %q", mode)
	}
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := make([]byte, len(plaintext)+padding)
	copy(padded, plaintext)
	for i := len(plaintext); i < len(padded); i++ { padded[i] = byte(padding) }
	encrypt.CryptBlocks(padded, padded)
	return padded, nil
}

type ecbEncrypter struct{ cipher.Block }
func (e ecbEncrypter) CryptBlocks(dst, src []byte) {
	if len(src)%e.BlockSize() != 0 || len(dst) < len(src) { panic("crypto: invalid buffer length") }
	for len(src) > 0 {
		e.Encrypt(dst[:e.BlockSize()], src[:e.BlockSize()])
		src = src[e.BlockSize():]
		dst = dst[e.BlockSize():]
	}
}

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
