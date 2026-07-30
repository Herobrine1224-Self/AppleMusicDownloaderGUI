package app

import "testing"

func TestValidateAppleMusicLink(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantKind   string
		wantSingle bool
		wantError  bool
	}{
		{name: "album", input: "https://music.apple.com/cn/album/example/123456789", wantKind: "album"},
		{name: "album track", input: "https://music.apple.com/us/album/example/123456789?i=987654321", wantKind: "album", wantSingle: true},
		{name: "song", input: "https://music.apple.com/jp/song/example/123456789", wantKind: "song", wantSingle: true},
		{name: "artist", input: "https://classical.music.apple.com/de/artist/example/123456789", wantKind: "artist"},
		{name: "wrong host", input: "https://example.com/us/album/example/123456789", wantError: true},
		{name: "missing id", input: "https://music.apple.com/us/album/example", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ValidateAppleMusicLink(test.input)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v", err)
			}
			if err == nil && (got.Kind != test.wantKind || got.SingleSong != test.wantSingle) {
				t.Fatalf("unexpected result: %+v", got)
			}
		})
	}
}
