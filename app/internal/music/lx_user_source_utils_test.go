package music

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"testing"
)

func TestLXAESEncryptSupportsECBAndCBC(t *testing.T) {
	key := []byte("0123456789abcdef")
	plain := []byte("music source")
	ecb, err := lxAESEncrypt(plain, "aes-128-ecb", key, nil)
	if err != nil { t.Fatal(err) }
	if len(ecb) != aes.BlockSize { t.Fatalf("ECB output length = %d", len(ecb)) }
	block, err := aes.NewCipher(key)
	if err != nil { t.Fatal(err) }
	decrypted := make([]byte, len(ecb))
	block.Decrypt(decrypted, ecb)
	padding := int(decrypted[len(decrypted)-1])
	if padding < 1 || padding > aes.BlockSize { t.Fatalf("invalid PKCS7 padding %d", padding) }
	if got := string(decrypted[:len(decrypted)-padding]); got != string(plain) {
		t.Fatalf("ECB decrypted text = %q", got)
	}

	iv := []byte("abcdef0123456789")
	cbc, err := lxAESEncrypt(plain, "aes-128-cbc", key, iv)
	if err != nil { t.Fatal(err) }
	if len(cbc) != aes.BlockSize { t.Fatalf("CBC output length = %d", len(cbc)) }
}

func TestLXRSAEncryptNoPadding(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil { t.Fatal(err) }
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil { t.Fatal(err) }
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	message := []byte{42}
	paddedMessage := make([]byte, privateKey.Size())
	paddedMessage[len(paddedMessage)-1] = 42

	encrypted, err := lxRSAEncryptNoPadding(message, publicPEM)
	if err != nil { t.Fatal(err) }
	ciphertext := new(big.Int).SetBytes(encrypted)
	decrypted := new(big.Int).Exp(ciphertext, privateKey.D, privateKey.N).FillBytes(make([]byte, privateKey.Size()))
	if !bytes.Equal(decrypted, paddedMessage) { t.Fatal("RSA_NO_PADDING round trip mismatch") }
}

func TestLXZlibRoundTrip(t *testing.T) {
	input := bytes.Repeat([]byte("music-source-"), 100)
	compressed, err := lxZlibDeflate(input)
	if err != nil { t.Fatal(err) }
	restored, err := lxZlibInflate(compressed)
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(restored, input) { t.Fatal("zlib round trip mismatch") }
}

func TestRunLXSourceWorkerSupportsAsyncZlibUtilities(t *testing.T) {
	script := `/*
 * @name LX Utility Promise Test
 */
lx.on(lx.EVENT_NAMES.request, async function () {
  const input = lx.utils.buffer.from("resolved-audio-url", "utf-8");
  const compressed = await lx.utils.zlib.deflate(input);
  const restored = await lx.utils.zlib.inflate(compressed);
  return lx.utils.buffer.bufToString(restored, "utf-8");
});
lx.send(lx.EVENT_NAMES.inited, {
  sources: { wy: { name: "NetEase", type: "music", actions: ["musicUrl"], qualitys: ["320k"] } }
});
`
	payload := json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"320k","musicInfo":{"id":"test"}}}`)
	input, err := json.Marshal(lxWorkerRequest{Script: script, Operation: "resolve", Payload: payload})
	if err != nil { t.Fatal(err) }
	var output bytes.Buffer
	if code := runLXSourceWorker(bytes.NewReader(input), &output, nil); code != 0 {
		t.Fatalf("worker returned %d: %s", code, output.String())
	}
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if response.Error != "" { t.Fatal(response.Error) }
	var value string
	if err := json.Unmarshal(response.Value, &value); err != nil { t.Fatal(err) }
	if value != "resolved-audio-url" { t.Fatalf("utility result = %q", value) }
}
