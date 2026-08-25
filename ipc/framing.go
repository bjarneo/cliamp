package ipc

import (
	"bufio"
	"io"
)

// maxIPCFrameSize bounds every NDJSON request, response, and event frame.
const maxIPCFrameSize = 1 << 20

func newFrameScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxIPCFrameSize)
	return scanner
}
