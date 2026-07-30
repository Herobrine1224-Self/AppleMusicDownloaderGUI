package mp4tag

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestItunesAlbumIDUsesFull64BitPayload(t *testing.T) {
	const albumID int64 = 6784529612
	path := filepath.Join(t.TempDir(), "plid.bin")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeItunesAlbumID(f, albumID); err != nil {
		f.Close()
		t.Fatalf("writeItunesAlbumID returned an error: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x00, 0x00, 0x00, 0x20, 'p', 'l', 'I', 'D',
		0x00, 0x00, 0x00, 0x18, 'd', 'a', 't', 'a',
		0x00, 0x00, 0x00, 0x15, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x01, 0x94, 0x63, 0xb4, 0xcc,
	}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("encoded plID = % x, want % x", encoded, want)
	}

	f, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	mp4 := MP4{f: f}
	boxes := MP4Boxes{Boxes: []*MP4Box{{
		StartOffset: 8,
		BoxSize:     24,
		Path:        "moov.udta.meta.ilst.plID.data",
	}}}
	decoded, err := mp4.readITAlbumID(boxes)
	if err != nil {
		t.Fatalf("readITAlbumID returned an error: %v", err)
	}
	if decoded != albumID {
		t.Fatalf("decoded plID = %d, want %d", decoded, albumID)
	}
}

func TestItunesAlbumIDRoundTripsThroughPublicAPI(t *testing.T) {
	const albumID int64 = 6784529612
	path := filepath.Join(t.TempDir(), "track.m4a")
	if err := os.WriteFile(path, minimalMP4(), 0600); err != nil {
		t.Fatal(err)
	}

	mp4, err := Open(path)
	if err != nil {
		t.Fatalf("Open returned an error: %v", err)
	}
	defer mp4.Close()
	if err := mp4.Write(&MP4Tags{ItunesAlbumID: albumID}, nil); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	tags, err := mp4.Read()
	if err != nil {
		t.Fatalf("Read returned an error: %v", err)
	}
	if tags.ItunesAlbumID != albumID {
		t.Fatalf("ItunesAlbumID = %d, want %d", tags.ItunesAlbumID, albumID)
	}
}

func minimalMP4() []byte {
	stco := testBox("stco", make([]byte, 8))
	stbl := testBox("stbl", stco)
	minf := testBox("minf", stbl)
	mdia := testBox("mdia", minf)
	trak := testBox("trak", mdia)
	ilst := testBox("ilst", nil)
	meta := testBox("meta", append(make([]byte, 4), ilst...))
	udta := testBox("udta", meta)
	moov := testBox("moov", append(trak, udta...))
	ftyp := testBox("ftyp", []byte("M4A "))
	mdat := testBox("mdat", nil)

	fixture := append(ftyp, moov...)
	return append(fixture, mdat...)
}

func testBox(name string, payload []byte) []byte {
	box := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(box[:4], uint32(8+len(payload)))
	copy(box[4:], name)
	return append(box, payload...)
}
