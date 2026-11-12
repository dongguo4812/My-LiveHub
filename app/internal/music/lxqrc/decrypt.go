package qrc

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// TripleDesKey 是 QQ 音乐 QRC 歌词使用的静态 3DES 密钥（24 字节）。
const TripleDesKey = "!@#)(*$%123ZXC!@!@#)(NHL"

// MaxSupportedVersion 是已知可以直接解析的 QRC 格式版本。
// QrcHeadInfo 的 Version="100" 即 QRC v1；更高的版本可能引入新编码。
const MaxSupportedVersion = 100

var (
	// ErrInvalidFile 输入不是 QRC 歌词文件（或文件为空/被截断）。
	ErrInvalidFile = errors.New("不是有效的 QRC 文件")
	// ErrUnsupportedVersion QRC 版本高于本工具支持的版本。
	ErrUnsupportedVersion = errors.New("不支持的 QRC 版本")
	// ErrDecryptFailed 数据看起来是 QRC，但所有解密链路都失败了。
	ErrDecryptFailed = errors.New("QRC 解密失败")
	// ErrEncoding 文件编码异常（如 UTF-16、非法字节序列）。
	ErrEncoding = errors.New("歌词文件编码异常")
)

// qmcStaticBox 是 QQ 音乐 QMC1 静态密码本。客户端把歌词写进
// LyricNew\QQMusicLyricNew 目录时，整份文件会先经过这一层异或。
var qmcStaticBox = [256]byte{
	0x77, 0x48, 0x32, 0x73, 0xDE, 0xF2, 0xC0, 0xC8, 0x95, 0xEC, 0x30, 0xB2, 0x51, 0xC3, 0xE1, 0xA0,
	0x9E, 0xE6, 0x9D, 0xCF, 0xFA, 0x7F, 0x14, 0xD1, 0xCE, 0xB8, 0xDC, 0xC3, 0x4A, 0x67, 0x93, 0xD6,
	0x28, 0xC2, 0x91, 0x70, 0xCA, 0x8D, 0xA2, 0xA4, 0xF0, 0x08, 0x61, 0x90, 0x7E, 0x6F, 0xA2, 0xE0,
	0xEB, 0xAE, 0x3E, 0xB6, 0x67, 0xC7, 0x92, 0xF4, 0x91, 0xB5, 0xF6, 0x6C, 0x5E, 0x84, 0x40, 0xF7,
	0xF3, 0x1B, 0x02, 0x7F, 0xD5, 0xAB, 0x41, 0x89, 0x28, 0xF4, 0x25, 0xCC, 0x52, 0x11, 0xAD, 0x43,
	0x68, 0xA6, 0x41, 0x8B, 0x84, 0xB5, 0xFF, 0x2C, 0x92, 0x4A, 0x26, 0xD8, 0x47, 0x6A, 0x7C, 0x95,
	0x61, 0xCC, 0xE6, 0xCB, 0xBB, 0x3F, 0x47, 0x58, 0x89, 0x75, 0xC3, 0x75, 0xA1, 0xD9, 0xAF, 0xCC,
	0x08, 0x73, 0x17, 0xDC, 0xAA, 0x9A, 0xA2, 0x16, 0x41, 0xD8, 0xA2, 0x06, 0xC6, 0x8B, 0xFC, 0x66,
	0x34, 0x9F, 0xCF, 0x18, 0x23, 0xA0, 0x0A, 0x74, 0xE7, 0x2B, 0x27, 0x70, 0x92, 0xE9, 0xAF, 0x37,
	0xE6, 0x8C, 0xA7, 0xBC, 0x62, 0x65, 0x9C, 0xC2, 0x08, 0xC9, 0x88, 0xB3, 0xF3, 0x43, 0xAC, 0x74,
	0x2C, 0x0F, 0xD4, 0xAF, 0xA1, 0xC3, 0x01, 0x64, 0x95, 0x4E, 0x48, 0x9F, 0xF4, 0x35, 0x78, 0x95,
	0x7A, 0x39, 0xD6, 0x6A, 0xA0, 0x6D, 0x40, 0xE8, 0x4F, 0xA8, 0xEF, 0x11, 0x1D, 0xF3, 0x1B, 0x3F,
	0x3F, 0x07, 0xDD, 0x6F, 0x5B, 0x19, 0x30, 0x19, 0xFB, 0xEF, 0x0E, 0x37, 0xF0, 0x0E, 0xCD, 0x16,
	0x49, 0xFE, 0x53, 0x47, 0x13, 0x1A, 0xBD, 0xA4, 0xF1, 0x40, 0x19, 0x60, 0x0E, 0xED, 0x68, 0x09,
	0x06, 0x5F, 0x4D, 0xCF, 0x3D, 0x1A, 0xFE, 0x20, 0x77, 0xE4, 0xD9, 0xDA, 0xF9, 0xA4, 0x2B, 0x76,
	0x1C, 0x71, 0xDB, 0x00, 0xBC, 0xFD, 0x0C, 0x6C, 0xA5, 0x47, 0xF7, 0xF6, 0x00, 0x79, 0x4A, 0x11,
}

// QMC1Decrypt 解开 QMC1 静态异或层。
//
// 掩码只跟绝对偏移有关：key(i) = box[(i*i+27) & 0xFF]，
// 其中 i 超过 0x7FFF 后按 0x7FFF 取模。整个过程不需要外部密钥。
func QMC1Decrypt(data []byte) []byte {
	out := make([]byte, len(data))
	for i := range data {
		o := i
		if o > 0x7FFF {
			o %= 0x7FFF
		}
		out[i] = data[i] ^ qmcStaticBox[(o*o+27)&0xFF]
	}
	return out
}

// qrcMarkers 用来判断一段字节流是否已经是解码后的 QRC 文本。
var qrcMarkers = [][]byte{
	[]byte("<Lyric_1"),
	[]byte("LyricContent"),
	[]byte("<QrcInfos"),
	[]byte("<QrcHeadInfo"),
}

// LooksLikeQRC 报告 b 是否看起来是解码后的 QRC 歌词文本。
func LooksLikeQRC(b []byte) bool {
	for _, m := range qrcMarkers {
		if bytes.Contains(b, m) {
			return true
		}
	}
	return false
}

// stripBOM 去掉 UTF-8 BOM。
func stripBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
}

// Decrypt 把 .qrc 文件的原始字节解码成 QRC XML 文本。
//
// QQ 音乐有几种不同的封装，这里按可能性依次尝试：
//
//  1. 明文 QRC XML（已解密 / 手工整理过的文件）
//  2. QMC1 静态异或层（客户端 LyricNew 缓存里的 .qm.qrc）
//  3. 内层载荷：自定义 3DES(ECB) → zlib → QRC XML
//
// 每一步失败都不会中断，只要有一条链路产出带 QRC 标记的文本就返回它。
func Decrypt(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: 文件为空", ErrInvalidFile)
	}
	if isUTF16(raw) {
		return nil, fmt.Errorf("%w: 检测到 UTF-16 BOM，QRC 歌词应为 UTF-8", ErrEncoding)
	}

	raw = stripBOM(raw)

	// 1. 已经是明文。
	if LooksLikeQRC(raw) {
		return raw, nil
	}

	// 2/3. 先剥掉 QMC1 层，再解内层载荷；同时也保留原始数据作为后备。
	unwrapped := QMC1Decrypt(raw)
	if LooksLikeQRC(unwrapped) {
		return unwrapped, nil
	}
	for _, base := range [][]byte{unwrapped, raw} {
		if out, ok := decodeInner(base); ok {
			return out, nil
		}
	}

	return nil, fmt.Errorf("%w: 未能从数据中解出 QRC 歌词（文件可能已损坏，也可能是尚未支持的新格式）", ErrDecryptFailed)
}

// isUTF16 通过 BOM 判断是否为 UTF-16。
func isUTF16(b []byte) bool {
	if len(b) < 2 {
		return false
	}
	return (b[0] == 0xFF && b[1] == 0xFE) || (b[0] == 0xFE && b[1] == 0xFF)
}

// decodeInner 在候选载荷上尝试 “明文 / hex → 3DES → zlib” 的各种组合。
func decodeInner(b []byte) ([]byte, bool) {
	for _, cand := range payloadCandidates(b) {
		if LooksLikeQRC(cand) {
			return cand, true
		}
		// 少数情况下只有 zlib，没有 3DES。
		if out, ok := tryInflate(cand); ok {
			return out, true
		}
		// hex 编码的内层载荷（部分接口直接返回十六进制字符串）。
		if h, ok := tryHexDecode(cand); ok {
			if LooksLikeQRC(h) {
				return h, true
			}
			if out, ok := tryInflate(h); ok {
				return out, true
			}
			if out, ok := tryInflate(tripleDesECBDecrypt(h, []byte(TripleDesKey))); ok {
				return out, true
			}
		}
		d := tripleDesECBDecrypt(cand, []byte(TripleDesKey))
		if out, ok := tryInflate(d); ok {
			return out, true
		}
		if LooksLikeQRC(d) {
			return d, true
		}
	}
	return nil, false
}

// payloadCandidates 给出内层载荷可能的起始位置。
//
// 客户端的 .qm.qrc 在 QMC1 层解开后会得到一个文本前缀行 `[offset:N]`，
// 紧随其后的才是 3DES 密文；不同版本的前缀略有差异，因此这里给出几种切法。
func payloadCandidates(b []byte) [][]byte {
	out := [][]byte{b}
	head := b
	if len(head) > 64 {
		head = head[:64]
	}
	if nl := bytes.IndexByte(head, '\n'); nl >= 0 {
		out = append(out, b[nl+1:])
	}
	if bytes.HasPrefix(b, []byte("[offset:")) {
		if nl := bytes.IndexByte(b, '\n'); nl >= 0 && nl < 64 {
			out = append(out, b[nl+1:])
		}
	}
	return out
}

// tryHexDecode 把纯十六进制文本解码成字节；不满足条件时返回 false。
func tryHexDecode(b []byte) ([]byte, bool) {
	b = bytes.TrimSpace(b)
	if len(b) < 16 || len(b)%2 != 0 {
		return nil, false
	}
	// 允许尾部有少量填充空白。
	if len(b) > 1<<20 {
		b = b[:1<<20]
	}
	out := make([]byte, hex.DecodedLen(len(b)))
	n, err := hex.Decode(out, b)
	if err != nil {
		return nil, false
	}
	if n == 0 {
		return nil, false
	}
	return out[:n], true
}

// tryInflate 在 0..maxHeaderSkip 的偏移上尝试 zlib / raw deflate，
// 返回第一个带有 QRC 标记的解压结果。
func tryInflate(b []byte) ([]byte, bool) {
	const maxHeaderSkip = 8
	for skip := 0; skip <= maxHeaderSkip && skip+2 <= len(b); skip++ {
		if out, ok := readZlib(b[skip:]); ok {
			return out, true
		}
		if out, ok := readFlate(b[skip:]); ok {
			return out, true
		}
	}
	return nil, false
}

func readZlib(b []byte) ([]byte, bool) {
	r, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, false
	}
	defer r.Close()
	return drain(r)
}

func readFlate(b []byte) ([]byte, bool) {
	r := flate.NewReader(bytes.NewReader(b))
	defer r.Close()
	return drain(r)
}

func drain(r io.Reader) ([]byte, bool) {
	out, err := io.ReadAll(io.LimitReader(r, 16<<20))
	if err != nil || !LooksLikeQRC(out) {
		return nil, false
	}
	return out, true
}
