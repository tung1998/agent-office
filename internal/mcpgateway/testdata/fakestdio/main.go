// fakestdio is an MCP server over stdio for the gateway's tests. It answers
// each request in its own goroutine (so answers come back out of order),
// counts initialize, and has tools to sleep, crash, ask the client back
// (sampling) and report its pid.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var (
	out     sync.Mutex
	inits   atomic.Int64
	waiting sync.Map // id of a request office got from us → chan of its answer
)

func send(v any) {
	raw, _ := json.Marshal(v)
	out.Lock()
	defer out.Unlock()
	os.Stdout.Write(append(raw, '\n'))
}

func text(id json.RawMessage, s string) {
	send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": s}}}})
}

func main() {
	fmt.Fprintln(os.Stderr, "fakestdio starting")
	fmt.Println("this line is a log, not JSON") // servers do this; office must skip it
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string `json:"name"`
				Arguments struct {
					Text string `json:"text"`
					MS   int    `json:"ms"`
				} `json:"arguments"`
			} `json:"params"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		line := append([]byte(nil), sc.Bytes()...)
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		if m.Method == "" { // an answer to our own request
			if ch, ok := waiting.Load(string(m.ID)); ok {
				msg := "ok"
				if m.Error != nil {
					msg = m.Error.Message
				}
				ch.(chan string) <- msg
			}
			continue
		}
		if len(m.ID) == 0 {
			continue // notification
		}
		go func() {
			switch m.Method {
			case "initialize":
				inits.Add(1)
				send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{
					"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}},
					"serverInfo": map[string]any{"name": "fakestdio", "version": "1"}}})
			case "tools/list":
				send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"tools": []any{
					map[string]any{"name": "echo", "description": "Echo"},
					map[string]any{"name": "slow"}, map[string]any{"name": "crash"},
					map[string]any{"name": "ask"}, map[string]any{"name": "pid"}, map[string]any{"name": "inits"},
					map[string]any{"name": "env"},
				}}})
			case "tools/call":
				switch m.Params.Name {
				case "slow":
					time.Sleep(time.Duration(m.Params.Arguments.MS) * time.Millisecond)
					text(m.ID, m.Params.Arguments.Text)
				case "crash":
					fmt.Fprintln(os.Stderr, "boom: crashed on purpose")
					os.Exit(3)
				case "ask":
					ch := make(chan string, 1)
					waiting.Store(`"s1"`, ch)
					send(map[string]any{"jsonrpc": "2.0", "id": "s1", "method": "sampling/createMessage", "params": map[string]any{}})
					text(m.ID, <-ch)
				case "pid":
					text(m.ID, strconv.Itoa(os.Getpid()))
				case "inits":
					text(m.ID, strconv.FormatInt(inits.Load(), 10))
				case "env":
					text(m.ID, os.Getenv("FAKE_TOKEN"))
				default:
					text(m.ID, m.Params.Arguments.Text)
				}
			default:
				send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "no " + m.Method}})
			}
		}()
	}
}
