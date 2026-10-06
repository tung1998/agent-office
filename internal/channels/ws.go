package channels

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// wsConn is a minimal websocket client (RFC 6455) for the Discord gateway:
// text messages, ping/pong and close; no extensions. Kept here so office
// needs no extra dependency.
type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
	wmu  sync.Mutex
}

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func dialWS(ctx context.Context, rawURL string) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	host := u.Host
	d := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	switch u.Scheme {
	case "wss":
		if u.Port() == "" {
			host += ":443"
		}
		conn, err = (&tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", host)
	case "ws":
		if u.Port() == "" {
			host += ":80"
		}
		conn, err = d.DialContext(ctx, "tcp", host)
	default:
		return nil, fmt.Errorf("websocket: scheme %q", u.Scheme)
	}
	if err != nil {
		return nil, err
	}
	keyRaw := make([]byte, 16)
	_, _ = rand.Read(keyRaw)
	key := base64.StdEncoding.EncodeToString(keyRaw)
	path := u.RequestURI()
	req := "GET " + path + " HTTP/1.1\r\nHost: " + u.Host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\nUser-Agent: agent-office\r\n\r\n"
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := io.WriteString(conn, req); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		conn.Close()
		return nil, err
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		conn.Close()
		return nil, fmt.Errorf("websocket: handshake failed (%s)", resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	return &wsConn{conn: conn, br: br}, nil
}

// Read returns the next text message (answering pings on the way).
func (w *wsConn) Read() (string, error) {
	var msg []byte
	for {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(w.br, hdr); err != nil {
			return "", err
		}
		fin, op := hdr[0]&0x80 != 0, hdr[0]&0x0f
		n := uint64(hdr[1] & 0x7f)
		switch n {
		case 126:
			b := make([]byte, 2)
			if _, err := io.ReadFull(w.br, b); err != nil {
				return "", err
			}
			n = uint64(binary.BigEndian.Uint16(b))
		case 127:
			b := make([]byte, 8)
			if _, err := io.ReadFull(w.br, b); err != nil {
				return "", err
			}
			n = binary.BigEndian.Uint64(b)
		}
		if n > 16<<20 || uint64(len(msg))+n > 16<<20 { // a frame, or fragments together
			return "", errors.New("websocket: frame too large")
		}
		var mask []byte
		if hdr[1]&0x80 != 0 {
			mask = make([]byte, 4)
			if _, err := io.ReadFull(w.br, mask); err != nil {
				return "", err
			}
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(w.br, p); err != nil {
			return "", err
		}
		if mask != nil {
			for i := range p {
				p[i] ^= mask[i%4]
			}
		}
		switch op {
		case 0x8: // close: its code says why (Discord's 4004 = bad token…)
			if len(p) >= 2 {
				return "", &CloseError{Code: int(binary.BigEndian.Uint16(p))}
			}
			return "", io.EOF
		case 0x9: // ping
			_ = w.write(0xA, p)
			continue
		case 0xA: // pong
			continue
		case 0x1, 0x2, 0x0:
			msg = append(msg, p...)
			if fin {
				return string(msg), nil
			}
		}
	}
}

// CloseError is a close frame from the server.
type CloseError struct{ Code int }

func (e *CloseError) Error() string { return fmt.Sprintf("websocket close %d", e.Code) }

// WriteText sends a text message.
func (w *wsConn) WriteText(s string) error { return w.write(0x1, []byte(s)) }

func (w *wsConn) write(op byte, p []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	frame := []byte{0x80 | op}
	switch n := len(p); {
	case n < 126:
		frame = append(frame, 0x80|byte(n))
	case n < 65536:
		frame = append(frame, 0x80|126, byte(n>>8), byte(n))
	default:
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(n))
		frame = append(append(frame, 0x80|127), b...)
	}
	mask := make([]byte, 4)
	_, _ = rand.Read(mask)
	frame = append(frame, mask...)
	masked := make([]byte, len(p))
	for i := range p {
		masked[i] = p[i] ^ mask[i%4]
	}
	_ = w.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	_, err := w.conn.Write(append(frame, masked...))
	return err
}

// Close ends the connection.
func (w *wsConn) Close() error {
	_ = w.write(0x8, []byte{0x03, 0xe8}) // 1000: normal
	return w.conn.Close()
}
