package channels

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// wsServe is a tiny websocket server for tests: handshake, then handle gets
// each text message the client sends; send pushes text frames.
func wsServe(t *testing.T, onOpen func(send func(string)), handle func(msg string, send func(string))) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Sec-WebSocket-Key")
		h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(h[:]) + "\r\n\r\n")
		rw.Flush()
		send := func(s string) {
			if code, ok := strings.CutPrefix(s, "CLOSE:"); ok { // a close frame with that code
				var n int
				fmt.Sscan(code, &n)
				conn.Write([]byte{0x88, 2, byte(n >> 8), byte(n)})
				return
			}
			frame := []byte{0x81}
			switch n := len(s); {
			case n < 126:
				frame = append(frame, byte(n))
			case n < 65536:
				frame = append(frame, 126, byte(n>>8), byte(n))
			default:
				b := make([]byte, 8)
				binary.BigEndian.PutUint64(b, uint64(n))
				frame = append(append(frame, 127), b...)
			}
			conn.Write(append(frame, s...))
		}
		if onOpen != nil {
			onOpen(send)
		}
		br := bufio.NewReader(conn)
		for {
			hdr := make([]byte, 2)
			if _, err := io.ReadFull(br, hdr); err != nil {
				return
			}
			n := int(hdr[1] & 0x7f)
			if n == 126 {
				b := make([]byte, 2)
				io.ReadFull(br, b)
				n = int(binary.BigEndian.Uint16(b))
			}
			mask := make([]byte, 4)
			io.ReadFull(br, mask)
			p := make([]byte, n)
			io.ReadFull(br, p)
			for i := range p {
				p[i] ^= mask[i%4]
			}
			if hdr[0]&0x0f == 8 {
				return
			}
			if hdr[0]&0x0f == 1 {
				handle(string(p), send)
			}
		}
	}))
}

func TestWebsocketRoundTrip(t *testing.T) {
	srv := wsServe(t, func(send func(string)) { send(`{"op":10}`) }, func(msg string, send func(string)) { send("echo:" + msg) })
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dialWS(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/gateway")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if msg, err := c.Read(); err != nil || msg != `{"op":10}` {
		t.Fatalf("hello = %q %v", msg, err)
	}
	long := strings.Repeat("x", 300) // a 16-bit length frame
	if err := c.WriteText(long); err != nil {
		t.Fatal(err)
	}
	if msg, err := c.Read(); err != nil || msg != "echo:"+long {
		t.Fatalf("echo = %d %v", len(msg), err)
	}
	var _ net.Conn
}
