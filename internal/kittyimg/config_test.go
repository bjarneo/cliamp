package kittyimg

import (
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
)

// pngDims builds the smallest PNG that image.DecodeConfig accepts for the
// given pixel size: signature, IHDR and IEND. No pixel data needs decoding
// for the cap check, so a huge declared size costs nothing to build.
func pngDims(width, height uint32) []byte {
	out := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	ihdr := make([]byte, 0, 21)
	ihdr = binary.BigEndian.AppendUint32(ihdr, 13)
	ihdr = append(ihdr, 'I', 'H', 'D', 'R')
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], width)
	binary.BigEndian.PutUint32(data[4:8], height)
	data[8], data[9], data[10], data[11], data[12] = 8, 6, 0, 0, 0 // 8-bit RGBA
	ihdr = append(ihdr, data...)
	ihdr = binary.BigEndian.AppendUint32(ihdr, crc32.ChecksumIEEE(append([]byte("IHDR"), data...)))
	out = append(out, ihdr...)
	return append(out, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xAE, 0x42, 0x60, 0x82)
}

func TestDecodeImageRejectsHugePixels(t *testing.T) {
	raw := pngDims(512, 1<<20) // 536,870,912 pixels, far over the cap
	_, err := decodeImage(raw)
	if err == nil {
		t.Fatal("a 512x1048576 image must be rejected before decoding")
	}
	if !strings.Contains(err.Error(), "pixel cap") {
		t.Fatalf("err = %v, want the pixel-cap message", err)
	}
}

func TestDecodeImageAcceptsNormalPixels(t *testing.T) {
	raw := pngDims(64, 16)
	_, err := decodeImage(raw)
	// Without pixel data the full decode fails, but the error must not be
	// the pixel cap: the size check passed.
	if err != nil && strings.Contains(err.Error(), "pixel cap") {
		t.Fatalf("a 64x16 image must pass the cap check: %v", err)
	}
}
