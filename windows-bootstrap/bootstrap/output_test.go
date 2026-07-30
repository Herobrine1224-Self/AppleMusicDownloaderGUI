package bootstrap

import (
	"encoding/binary"
	"reflect"
	"testing"
	"unicode/utf16"
)

func TestDecodeWindowsOutput(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "utf8", data: []byte("Ubuntu\r\n"), want: "Ubuntu\r\n"},
		{name: "utf8 bom", data: append([]byte{0xef, 0xbb, 0xbf}, []byte("Ubuntu")...), want: "Ubuntu"},
		{name: "utf16 no bom", data: utf16Bytes("Ubuntu\r\n", false), want: "Ubuntu\r\n"},
		{name: "utf16 bom", data: utf16Bytes("AppleMusic", true), want: "AppleMusic"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := DecodeWindowsOutput(test.data); got != test.want {
				t.Fatalf("DecodeWindowsOutput() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseDistroList(t *testing.T) {
	data := utf16Bytes("Ubuntu\r\nAppleMusic-Runtime-aabbccdd\r\n", true)
	want := []string{"Ubuntu", "AppleMusic-Runtime-aabbccdd"}
	if got := parseDistroList(data); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDistroList() = %#v, want %#v", got, want)
	}
}

func utf16Bytes(value string, bom bool) []byte {
	words := utf16.Encode([]rune(value))
	if bom {
		words = append([]uint16{0xfeff}, words...)
	}
	data := make([]byte, len(words)*2)
	for i, word := range words {
		binary.LittleEndian.PutUint16(data[i*2:], word)
	}
	return data
}
