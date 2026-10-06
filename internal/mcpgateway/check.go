package mcpgateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// CheckTimeout bounds a whole check (initialize + tools/list).
const CheckTimeout = 15 * time.Second

const protocolVersion = "2025-06-18"

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// client is one MCP session over streamable HTTP, as Claude Code opens it.
type client struct {
	http    *http.Client
	url     string
	headers map[string]string
	session string
	nextID  int
}

// Check connects to an HTTP MCP server like a client does (initialize,
// notifications/initialized, tools/list) and returns its tools. Its errors
// say what to fix and never carry the server's secrets.
func Check(ctx context.Context, hc *http.Client, rawURL string, headers map[string]string) ([]storage.MCPTool, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	if u, err := url.Parse(rawURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("URL không hợp lệ: cần dạng https://…")
	}
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	c := &client{http: hc, url: rawURL, headers: headers}
	if _, err := c.call(ctx, "initialize", initParams()); err != nil {
		return nil, err
	}
	if err := c.notify(ctx, "notifications/initialized"); err != nil {
		return nil, err
	}
	tools, err := listTools(ctx, c.call)
	if err != nil {
		return nil, err
	}
	c.close()
	return tools, nil
}

// callHTTP opens a session with an HTTP server and calls one tool (office
// calling on behalf of an API run, or after a person approved the call).
func callHTTP(ctx context.Context, hc *http.Client, rawURL string, headers map[string]string, tool string, args json.RawMessage) (json.RawMessage, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	c := &client{http: hc, url: rawURL, headers: headers}
	defer c.close()
	if _, err := c.call(ctx, "initialize", initParams()); err != nil {
		return nil, err
	}
	if err := c.notify(ctx, "notifications/initialized"); err != nil {
		return nil, err
	}
	return c.call(ctx, "tools/call", toolParams(tool, args))
}

func toolParams(tool string, args json.RawMessage) map[string]any {
	if len(bytes.TrimSpace(args)) == 0 || string(bytes.TrimSpace(args)) == "null" {
		args = json.RawMessage(`{}`)
	}
	return map[string]any{"name": tool, "arguments": args}
}

// initParams is what office sends as initialize, as a client.
func initParams() map[string]any {
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "agent-office", "version": "1"},
	}
}

// listTools pages through tools/list.
func listTools(ctx context.Context, call func(ctx context.Context, method string, params any) (json.RawMessage, error)) ([]storage.MCPTool, error) {
	tools := []storage.MCPTool{}
	cursor := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var res struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
				Annotations struct {
					ReadOnlyHint *bool `json:"readOnlyHint"`
				} `json:"annotations"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, errors.New("danh sách tool không đọc được")
		}
		for _, t := range res.Tools {
			schema := t.InputSchema
			if len(schema) > 32<<10 { // a schema this big is not worth keeping per tool
				schema = nil
			}
			tools = append(tools, storage.MCPTool{Name: t.Name, Description: firstLine(t.Description, 300), ReadOnly: t.Annotations.ReadOnlyHint, InputSchema: schema})
		}
		if cursor = res.NextCursor; cursor == "" {
			break
		}
	}
	return tools, nil
}

func (c *client) post(ctx context.Context, body any) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("URL không hợp lệ")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", protocolVersion)
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, connError(ctx, err)
	}
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		c.session = s
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, &StatusError{Code: resp.StatusCode, WWWAuthenticate: resp.Header.Get("WWW-Authenticate")}
	}
	return resp, nil
}

func (c *client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.nextID++
	id := c.nextID
	resp, err := c.post(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	msg, err := readReply(resp, id)
	if err != nil {
		if ctx.Err() != nil {
			return nil, connError(ctx, ctx.Err())
		}
		return nil, err
	}
	if msg.Error != nil {
		return nil, fmt.Errorf("MCP báo lỗi khi %s: %s", method, firstLine(msg.Error.Message, 200))
	}
	return msg.Result, nil
}

func (c *client) notify(ctx context.Context, method string) error {
	resp, err := c.post(ctx, map[string]any{"jsonrpc": "2.0", "method": method})
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	return nil
}

// close ends the session, when the server gave one (best effort).
func (c *client) close() {
	if c.session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Mcp-Session-Id", c.session)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if resp, err := c.http.Do(req); err == nil {
		resp.Body.Close()
	}
}

// readReply reads the answer to request id, from a JSON body or an SSE stream.
func readReply(resp *http.Response, id int) (rpcMessage, error) {
	want := fmt.Sprint(id)
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mt == "text/event-stream" {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 8<<20)
		var data strings.Builder
		flush := func() (rpcMessage, bool) {
			defer data.Reset()
			var m rpcMessage
			if data.Len() == 0 || json.Unmarshal([]byte(data.String()), &m) != nil {
				return m, false
			}
			return m, string(m.ID) == want && (m.Result != nil || m.Error != nil)
		}
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if m, ok := flush(); ok {
					return m, nil
				}
				continue
			}
			if v, ok := strings.CutPrefix(line, "data:"); ok {
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimPrefix(v, " "))
			}
		}
		if m, ok := flush(); ok {
			return m, nil
		}
		return rpcMessage{}, errors.New("MCP không trả lời trong luồng SSE")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return rpcMessage{}, errors.New("đọc phản hồi của MCP bị lỗi")
	}
	var one rpcMessage
	if json.Unmarshal(raw, &one) == nil && (one.Result != nil || one.Error != nil) {
		return one, nil
	}
	var batch []rpcMessage
	if json.Unmarshal(raw, &batch) == nil {
		for _, m := range batch {
			if string(m.ID) == want {
				return m, nil
			}
		}
	}
	return rpcMessage{}, errors.New("phản hồi không phải MCP: kiểm tra URL")
}

// StatusError is the server answering an HTTP error; a 401 carries where to
// log in (WWW-Authenticate).
type StatusError struct {
	Code            int
	WWWAuthenticate string
}

func (e *StatusError) Error() string {
	switch e.Code {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Sprintf("MCP từ chối (HTTP %d): token sai hoặc hết hạn, nhập lại token", e.Code)
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return fmt.Sprintf("không thấy MCP ở địa chỉ này (HTTP %d): kiểm tra URL", e.Code)
	}
	return fmt.Sprintf("MCP trả lỗi HTTP %d", e.Code)
}

// connError says why the server could not be reached, without the URL (it
// may carry a key in its query).
func connError(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("MCP không trả lời trong %d giây: kiểm tra URL", int(CheckTimeout.Seconds()))
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("không kết nối được: kiểm tra URL (%s)", firstLine(err.Error(), 160))
}

func firstLine(s string, n int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}
