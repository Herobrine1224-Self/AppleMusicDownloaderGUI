package bootstrap

import (
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

func DecodeWindowsOutput(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if isUTF16LE(data) {
		if len(data)%2 != 0 {
			data = data[:len(data)-1]
		}
		words := make([]uint16, 0, len(data)/2)
		for i := 0; i+1 < len(data); i += 2 {
			word := binary.LittleEndian.Uint16(data[i : i+2])
			if len(words) == 0 && word == 0xfeff {
				continue
			}
			words = append(words, word)
		}
		return strings.TrimPrefix(string(utf16.Decode(words)), "\ufeff")
	}
	return strings.TrimPrefix(string(data), "\ufeff")
}

func isUTF16LE(data []byte) bool {
	if len(data) >= 2 && data[0] == 0xff && data[1] == 0xfe {
		return true
	}
	if len(data) < 4 {
		return false
	}
	nulsAtOdd := 0
	pairs := len(data) / 2
	for i := 1; i < len(data); i += 2 {
		if data[i] == 0 {
			nulsAtOdd++
		}
	}
	return nulsAtOdd*4 >= pairs*3
}

func parseDistroList(data []byte) []string {
	text := strings.ReplaceAll(DecodeWindowsOutput(data), "\x00", "")
	lines := strings.FieldsFunc(text, func(r rune) bool { return r == '\r' || r == '\n' })
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}
