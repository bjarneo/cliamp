package httpclient

import (
	"io"
	"net"
)

// icyConn translates SHOUTcast's nonstandard "ICY 200 OK" status line to
// HTTP/1.0 before net/http parses it.
type icyConn struct {
	net.Conn
	prefix []byte
	err    error
}

func newICYConn(conn net.Conn) *icyConn {
	return &icyConn{Conn: conn}
}

func (c *icyConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c.prefix == nil {
		var prefix [4]byte
		n, err := io.ReadFull(c.Conn, prefix[:])
		c.prefix = prefix[:n]
		c.err = err
		if string(c.prefix) == "ICY " {
			c.prefix = []byte("HTTP/1.0 ")
		}
	}
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	if c.err != nil {
		err := c.err
		c.err = nil
		return 0, err
	}
	return c.Conn.Read(p)
}
