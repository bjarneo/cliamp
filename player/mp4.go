package player

import (
	"encoding/binary"
	"io"
)

// mp4HeadSize limits how much of a file readMdatFirst reads. An MP4 file puts
// its ftyp box and the header of its first large box well inside this range.
const mp4HeadSize = 4096

// mp4MdatFirst reports whether head, the first bytes of a file, starts an MP4
// file whose media data box (mdat) comes before its movie box (moov). ffmpeg
// needs the moov box to decode, so it cannot play such a file from a pipe.
//
// It walks the top-level boxes from the ftyp box. A box size of 1 means a
// 64-bit size follows the type. ok is false when head ends before the answer
// is known. Data that is not MP4 and a damaged box give false.
func mp4MdatFirst(head []byte) (mdatFirst, ok bool) {
	if len(head) < 8 {
		return false, false
	}
	if string(head[4:8]) != "ftyp" {
		return false, true
	}
	for off := 0; ; {
		box := head[off:]
		if len(box) < 8 {
			return false, false
		}
		switch string(box[4:8]) {
		case "mdat":
			return true, true
		case "moov":
			return false, true
		}
		size, headerLen := uint64(binary.BigEndian.Uint32(box)), uint64(8)
		if size == 1 {
			if len(box) < 16 {
				return false, false
			}
			size, headerLen = binary.BigEndian.Uint64(box[8:]), 16
		}
		// A size of 0 means that the box runs to the end of the file, so no
		// box follows it.
		if size < headerLen {
			return false, true
		}
		if size > uint64(len(box)) {
			return false, false
		}
		off += int(size)
	}
}

// readMdatFirst reads r until mp4MdatFirst knows the answer, or until it has
// read mp4HeadSize bytes. A read error before the answer is known gives false.
func readMdatFirst(r io.Reader) bool {
	head := make([]byte, 0, mp4HeadSize)
	for len(head) < cap(head) {
		n, err := r.Read(head[len(head):cap(head)])
		head = head[:len(head)+n]
		if mdatFirst, ok := mp4MdatFirst(head); ok || err != nil {
			return mdatFirst
		}
	}
	return false
}
