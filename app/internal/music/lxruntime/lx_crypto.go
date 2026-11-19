//go:build lxquickjs && cgo

package lxruntime

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/md5"
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

// lxCryptoRandomBytes mirrors crypto.randomBytes for bounded source requests.
func lxCryptoRandomBytes(size int) ([]byte, error) {
	if size < 0 || size > 1<<20 {
		return nil, fmt.Errorf("randomBytes size out of range: %d", size)
	}
	out := make([]byte, size)
	if _, err := rand.Read(out); err != nil {
		return nil, err
	}
	return out, nil
}

// lxCryptoAESEncrypt mirrors Node's createCipheriv(...).update(...)+final()
// with the default PKCS#7 padding enabled. Supported modes are CBC and ECB.
func lxCryptoAESEncrypt(plaintext []byte, mode string, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("invalid AES key: %w", err)
	}
	mode = strings.ToLower(mode)
	var encrypt cipher.BlockMode
	switch mode {
	case "aes-128-cbc", "aes-192-cbc", "aes-256-cbc":
		if len(iv) != aes.BlockSize {
			return nil, fmt.Errorf("AES-CBC IV must be %d bytes", aes.BlockSize)
		}
		encrypt = cipher.NewCBCEncrypter(block, iv)
	case "aes-128-ecb", "aes-192-ecb", "aes-256-ecb":
		if len(iv) != 0 {
			return nil, errors.New("AES-ECB does not accept an IV")
		}
		encrypt = ecbEncrypter{block}
	default:
		return nil, fmt.Errorf("unsupported AES mode %q", mode)
	}
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := make([]byte, len(plaintext)+padding)
	copy(padded, plaintext)
	for i := len(plaintext); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	encrypt.CryptBlocks(padded, padded)
	return padded, nil
}

type ecbEncrypter struct{ cipher.Block }

func (e ecbEncrypter) CryptBlocks(dst, src []byte) {
	if len(src)%e.BlockSize() != 0 || len(dst) < len(src) {
		panic("crypto: invalid buffer length")
	}
	for len(src) > 0 {
		e.Encrypt(dst[:e.BlockSize()], src[:e.BlockSize()])
		src = src[e.BlockSize():]
		dst = dst[e.BlockSize():]
	}
}
