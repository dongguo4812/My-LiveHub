package lxruntime

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf16"
)

// lxBufferFromString implements the string-encoding portion of Node's
// Buffer.from(value, encoding). The caller supplies UTF-8 bytes produced by
// QuickJS's JS_ToCStringLen.
func lxBufferFromString(value []byte, encoding string) ([]byte, error) {
	switch strings.ToLower(encoding) {
	case "", "utf8", "utf-8":
		return append([]byte(nil), value...), nil
	case "hex":
		n := len(value) / 2
		out := make([]byte, 0, n)
		for i := 0; i+1 < len(value); i += 2 {
			decoded := make([]byte, 1)
			if _, err := hex.Decode(decoded, value[i:i+2]); err != nil {
				break
			}
			out = append(out, decoded[0])
		}
		return out, nil
	case "base64", "base64url":
		// Node accepts both alphabets for both encodings and ignores
		// whitespace and non-alphabet characters while decoding.
		text := strings.Map(func(r rune) rune {
			switch {
			case r == '-':
				return '+'
			case r == '_':
				return '/'
			case r == '+' || r == '/' || r == '=':
				return r
			case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				return r
			default:
				return -1
			}
		}, string(value))
		text = strings.TrimRight(text, "=")
		if rem := len(text) % 4; rem == 1 {
			text = text[:len(text)-1]
		}
		out, err := base64.RawStdEncoding.DecodeString(text)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 input: %w", err)
		}
		return out, nil
	case "latin1", "binary", "ascii":
		// Node's single-byte encodings consume UTF-16 code units, not UTF-8
		// bytes or Unicode scalar values. Keep the low byte of each unit.
		units := utf16.Encode([]rune(string(value)))
		out := make([]byte, len(units))
		for i, unit := range units {
			out[i] = byte(unit)
		}
		return out, nil
	case "ucs2", "ucs-2", "utf16le", "utf-16le":
		units := utf16.Encode([]rune(string(value)))
		out := make([]byte, len(units)*2)
		for i, unit := range units {
			binary.LittleEndian.PutUint16(out[i*2:], unit)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown encoding: %s", encoding)
	}
}

func lxBufferToString(value []byte, encoding string) (string, error) {
	switch strings.ToLower(encoding) {
	case "", "utf8", "utf-8":
		return strings.ToValidUTF8(string(value), "\uFFFD"), nil
	case "hex":
		return hex.EncodeToString(value), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(value), nil
	case "base64url":
		return base64.RawURLEncoding.EncodeToString(value), nil
	case "latin1", "binary":
		runes := make([]rune, len(value))
		for i, b := range value {
			runes[i] = rune(b)
		}
		return string(runes), nil
	case "ascii":
		runes := make([]rune, len(value))
		for i, b := range value {
			runes[i] = rune(b & 0x7f)
		}
		return string(runes), nil
	case "ucs2", "ucs-2", "utf16le", "utf-16le":
		units := make([]uint16, len(value)/2)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(value[i*2:])
		}
		return string(utf16.Decode(units)), nil
	default:
		return "", fmt.Errorf("unknown encoding: %s", encoding)
	}
}
