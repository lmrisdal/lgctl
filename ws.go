package main

import (
	"bufio"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// wsConn is a minimal RFC 6455 websocket client built on the standard library
// so that lgctl compiles to a single dependency-free static binary. webOS only
// exchanges small single-frame JSON text messages, which is all this supports
// (plus ping/pong and close control frames).
type wsConn struct {
	c net.Conn
	r *bufio.Reader
}

func wsDial(host string, port int, useTLS bool, timeout time.Duration) (*wsConn, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	raw, err := (&net.Dialer{Timeout: timeout}).Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	var conn net.Conn = raw
	if useTLS {
		// LG TVs present a self-signed certificate, so verification is skipped.
		tconn := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, ServerName: host})
		_ = tconn.SetDeadline(time.Now().Add(timeout))
		if err := tconn.Handshake(); err != nil {
			raw.Close()
			return nil, fmt.Errorf("tls handshake: %w", err)
		}
		_ = tconn.SetDeadline(time.Time{})
		conn = tconn
	}

	key := make([]byte, 16)
	_, _ = rand.Read(key)
	req := "GET / HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(key) + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"

	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}

	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !strings.Contains(status, "101") {
		conn.Close()
		return nil, fmt.Errorf("websocket upgrade rejected: %s", strings.TrimSpace(status))
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, err
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	_ = conn.SetDeadline(time.Time{})
	return &wsConn{c: conn, r: br}, nil
}

func (w *wsConn) setReadDeadline(d time.Duration) {
	_ = w.c.SetReadDeadline(time.Now().Add(d))
}

// writeText sends a masked text frame (clients must mask per the spec).
func (w *wsConn) writeText(s string) error {
	return w.writeFrame(0x1, []byte(s))
}

func (w *wsConn) writeFrame(opcode byte, payload []byte) error {
	frame := []byte{0x80 | opcode} // FIN + opcode
	n := len(payload)
	switch {
	case n < 126:
		frame = append(frame, 0x80|byte(n))
	case n < 65536:
		frame = append(frame, 0x80|126, byte(n>>8), byte(n))
	default:
		frame = append(frame, 0x80|127)
		for i := 7; i >= 0; i-- {
			frame = append(frame, byte(n>>(8*i)))
		}
	}
	var maskKey [4]byte
	_, _ = rand.Read(maskKey[:])
	frame = append(frame, maskKey[:]...)
	for i := 0; i < n; i++ {
		frame = append(frame, payload[i]^maskKey[i%4])
	}
	_ = w.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := w.c.Write(frame)
	return err
}

// readText returns the next complete text message, transparently handling
// fragmentation and replying to ping frames.
func (w *wsConn) readText() (string, error) {
	var data []byte
	for {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(w.r, hdr); err != nil {
			return "", err
		}
		fin := hdr[0]&0x80 != 0
		opcode := hdr[0] & 0x0f
		masked := hdr[1]&0x80 != 0
		n := int(hdr[1] & 0x7f)
		switch n {
		case 126:
			ext := make([]byte, 2)
			if _, err := io.ReadFull(w.r, ext); err != nil {
				return "", err
			}
			n = int(ext[0])<<8 | int(ext[1])
		case 127:
			ext := make([]byte, 8)
			if _, err := io.ReadFull(w.r, ext); err != nil {
				return "", err
			}
			n = 0
			for i := 0; i < 8; i++ {
				n = n<<8 | int(ext[i])
			}
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(w.r, mask[:]); err != nil {
				return "", err
			}
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(w.r, payload); err != nil {
			return "", err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch opcode {
		case 0x8: // close
			return "", io.EOF
		case 0x9: // ping -> pong
			_ = w.writeFrame(0xA, payload)
		case 0xA: // pong, ignore
		default: // 0x0 continuation, 0x1 text, 0x2 binary
			data = append(data, payload...)
			if fin {
				return string(data), nil
			}
		}
	}
}

func (w *wsConn) Close() error {
	_ = w.writeFrame(0x8, nil) // best-effort close frame
	return w.c.Close()
}
