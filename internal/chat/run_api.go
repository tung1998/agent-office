package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/tung1998/agent-office/internal/officetools"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// The read-only tools office offers to API-based agents.
var workspaceTools = []struct {
	Name, Description string
	Schema            map[string]any
}{
	{"list_dir", "Liệt kê file và thư mục con trong một thư mục của project (đường dẫn tương đối, '.' là gốc).",
		map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}},
	{"read_file", "Đọc một file văn bản của project, có đánh số dòng. Dùng start/end để đọc một đoạn.",
		map[string]any{"type": "object", "properties": map[string]any{
			"path": map[string]any{"type": "string"}, "start": map[string]any{"type": "integer"}, "end": map[string]any{"type": "integer"}},
			"required": []string{"path"}}},
	{"search_text", "Tìm các dòng khớp biểu thức chính quy (không phân biệt hoa thường) trong project. glob tùy chọn, ví dụ *.vue.",
		map[string]any{"type": "object", "properties": map[string]any{"pattern": map[string]any{"type": "string"}, "glob": map[string]any{"type": "string"}},
			"required": []string{"pattern"}}},
}

// The tools that write, offered when the run may edit files.
var writeTools = []struct {
	Name, Description string
	Schema            map[string]any
}{
	{"write_file", "Tạo mới hoặc ghi đè toàn bộ một file (đường dẫn tương đối từ gốc).",
		map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}},
			"required": []string{"path", "content"}}},
	{"edit_file", "Sửa một file: thay đoạn old (phải khớp đúng một chỗ, kể cả khoảng trắng) bằng new. Đọc file trước khi sửa.",
		map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "old": map[string]any{"type": "string"}, "new": map[string]any{"type": "string"}},
			"required": []string{"path", "old", "new"}}},
}

// fileTools are the workspace tools of a run: reading, and writing when allowed.
func fileTools(req RunRequest) []struct {
	Name, Description string
	Schema            map[string]any
} {
	if req.NoTools {
		return nil
	}
	if !req.Write {
		return workspaceTools
	}
	return append(slices.Clone(workspaceTools), writeTools...)
}

func officeTools(req RunRequest) []officetools.Tool {
	if req.Office == nil || req.Office.Tools == nil || req.NoTools {
		return nil
	}
	return req.Office.Tools.ToolsFor(req.Office.Scope)
}

// gatewayTools are the office MCP servers' tools the run's agent gets
// (ADR-093); office calls them itself.
func gatewayTools(ctx context.Context, req RunRequest) []GatewayTool {
	if req.Office == nil || req.Office.GatewayTools == nil || req.NoTools {
		return nil
	}
	return req.Office.GatewayTools.List(ctx)
}

// dispatchTool runs an office tool, a gateway MCP tool or a workspace tool.
func dispatchTool(ctx context.Context, req RunRequest, w Workspace, name string, raw json.RawMessage) (string, bool) {
	if req.Office != nil && req.Office.Tools != nil && req.Office.Tools.Has(req.Office.Scope, name) {
		return req.Office.Tools.Call(ctx, req.Office.Scope, name, raw)
	}
	if strings.HasPrefix(name, "mcp__") && req.Office != nil && req.Office.GatewayTools != nil && !req.NoTools {
		return req.Office.GatewayTools.Call(ctx, name, raw)
	}
	return callTool(w, name, raw)
}

// callTool runs one workspace tool and returns its text result.
func callTool(w Workspace, name string, raw json.RawMessage) (string, bool) {
	var in struct {
		Path    string `json:"path"`
		Start   int    `json:"start"`
		End     int    `json:"end"`
		Pattern string `json:"pattern"`
		Glob    string `json:"glob"`
		Content string `json:"content"`
		Old     string `json:"old"`
		New     string `json:"new"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "Tham số không hợp lệ: " + err.Error(), true
	}
	var (
		out string
		err error
	)
	switch name {
	case "list_dir":
		out, err = w.ListDir(in.Path)
	case "read_file":
		out, err = w.ReadFile(in.Path, in.Start, in.End)
	case "search_text":
		out, err = w.Search(in.Pattern, in.Glob)
	case "write_file":
		out, err = w.WriteFile(in.Path, in.Content)
	case "edit_file":
		out, err = w.EditFile(in.Path, in.Old, in.New)
	default:
		return "Công cụ không tồn tại: " + name, true
	}
	if err != nil {
		return "Lỗi: " + err.Error(), true
	}
	return out, false
}

const maxToolRounds = 20

func postJSON(ctx context.Context, url string, headers map[string]string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(msg, 400))
	}
	return json.Unmarshal(raw, out)
}

// ---- Anthropic Messages API ----

type anthropicRunner struct{}

func (anthropicRunner) Run(ctx context.Context, req RunRequest, emit func(Event)) (RunResult, error) {
	base := strings.TrimRight(firstNonEmpty(req.Provider.BaseURL, "https://api.anthropic.com"), "/")
	headers := map[string]string{"x-api-key": req.APIKey, "anthropic-version": "2023-06-01"}
	tools := make([]map[string]any, 0, len(workspaceTools)+len(writeTools))
	for _, t := range fileTools(req) {
		tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Schema})
	}
	for _, t := range officeTools(req) {
		tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Schema})
	}
	for _, t := range gatewayTools(ctx, req) {
		tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Schema})
	}
	messages := []any{}
	for _, h := range req.History {
		messages = append(messages, map[string]any{"role": h.Role, "content": h.Said()})
	}
	content, err := anthropicContent(req.Prompt, req.Attachments)
	if err != nil {
		return RunResult{}, err
	}
	messages = append(messages, map[string]any{"role": "user", "content": content})
	ws := Workspace{Root: req.WorkDir, Write: req.Write, Deny: req.DenyPaths}
	res := RunResult{}
	start := time.Now()
	var answer strings.Builder
	for round := 0; round < maxToolRounds; round++ {
		var out struct {
			Model      string            `json:"model"`
			StopReason string            `json:"stop_reason"`
			Content    []json.RawMessage `json:"content"`
			Usage      struct {
				InputTokens         int `json:"input_tokens"`
				OutputTokens        int `json:"output_tokens"`
				CacheReadTokens     int `json:"cache_read_input_tokens"`
				CacheCreationTokens int `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		}
		body := map[string]any{"model": req.Model, "max_tokens": 16000, "system": req.System, "messages": messages, "tools": tools}
		if err := postJSON(ctx, base+"/v1/messages", headers, body, &out); err != nil {
			return res, err
		}
		res.Usage.Model = out.Model
		res.Usage.InputTokens += out.Usage.InputTokens + out.Usage.CacheReadTokens + out.Usage.CacheCreationTokens
		res.Usage.OutputTokens += out.Usage.OutputTokens
		// Send the assistant content back unchanged (thinking blocks must round-trip).
		messages = append(messages, map[string]any{"role": "assistant", "content": out.Content})
		var results []map[string]any
		for _, raw := range out.Content {
			var block struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			}
			if json.Unmarshal(raw, &block) != nil {
				continue
			}
			switch block.Type {
			case "text":
				if block.Text != "" {
					answer.WriteString(block.Text)
					emit(Event{Type: "text", Text: block.Text})
				}
			case "tool_use":
				tc := storage.ToolCall{Name: block.Name, Summary: toolSummary(block.Name, block.Input)}
				result, isErr := dispatchTool(ctx, req, ws, block.Name, block.Input)
				tc.Error = isErr
				res.Tools = append(res.Tools, tc)
				emit(Event{Type: "tool", Tool: &tc})
				results = append(results, map[string]any{"type": "tool_result", "tool_use_id": block.ID, "content": result, "is_error": isErr})
			}
		}
		if out.StopReason != "tool_use" || len(results) == 0 {
			break
		}
		messages = append(messages, map[string]any{"role": "user", "content": results})
		if answer.Len() > 0 && !strings.HasSuffix(answer.String(), "\n") {
			answer.WriteString("\n\n")
			emit(Event{Type: "text", Text: "\n\n"})
		}
	}
	res.Text = strings.TrimSpace(answer.String())
	res.Usage.DurationMS = time.Since(start).Milliseconds()
	return res, nil
}

// ---- OpenAI and compatible chat completions ----

type openAIRunner struct{ official bool }

func (r openAIRunner) Run(ctx context.Context, req RunRequest, emit func(Event)) (RunResult, error) {
	base := strings.TrimRight(req.Provider.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	headers := map[string]string{}
	if req.APIKey != "" {
		headers["Authorization"] = "Bearer " + req.APIKey
	}
	tools := make([]map[string]any, 0, len(workspaceTools)+len(writeTools))
	for _, t := range fileTools(req) {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Schema}})
	}
	for _, t := range officeTools(req) {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Schema}})
	}
	for _, t := range gatewayTools(ctx, req) {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Schema}})
	}
	messages := []any{map[string]any{"role": "system", "content": req.System}}
	for _, h := range req.History {
		messages = append(messages, map[string]any{"role": h.Role, "content": h.Said()})
	}
	content, err := openAIContent(req.Prompt, req.Attachments)
	if err != nil {
		return RunResult{}, err
	}
	messages = append(messages, map[string]any{"role": "user", "content": content})
	ws := Workspace{Root: req.WorkDir, Write: req.Write, Deny: req.DenyPaths}
	res := RunResult{}
	start := time.Now()
	var answer strings.Builder
	for round := 0; round < maxToolRounds; round++ {
		var out struct {
			Model   string `json:"model"`
			Choices []struct {
				Message json.RawMessage `json:"message"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		body := map[string]any{"model": req.Model, "messages": messages, "tools": tools}
		if err := postJSON(ctx, base+"/chat/completions", headers, body, &out); err != nil {
			return res, err
		}
		res.Usage.Model = out.Model
		res.Usage.InputTokens += out.Usage.PromptTokens
		res.Usage.OutputTokens += out.Usage.CompletionTokens
		if len(out.Choices) == 0 {
			break
		}
		var msg struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		}
		_ = json.Unmarshal(out.Choices[0].Message, &msg)
		messages = append(messages, out.Choices[0].Message)
		if msg.Content != "" {
			answer.WriteString(msg.Content)
			emit(Event{Type: "text", Text: msg.Content})
		}
		if len(msg.ToolCalls) == 0 {
			break
		}
		for _, call := range msg.ToolCalls {
			args := json.RawMessage(call.Function.Arguments)
			tc := storage.ToolCall{Name: call.Function.Name, Summary: toolSummary(call.Function.Name, args)}
			result, isErr := dispatchTool(ctx, req, ws, call.Function.Name, args)
			tc.Error = isErr
			res.Tools = append(res.Tools, tc)
			emit(Event{Type: "tool", Tool: &tc})
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": call.ID, "content": result})
		}
		if answer.Len() > 0 && !strings.HasSuffix(answer.String(), "\n") {
			answer.WriteString("\n\n")
			emit(Event{Type: "text", Text: "\n\n"})
		}
	}
	res.Text = strings.TrimSpace(answer.String())
	res.Usage.DurationMS = time.Since(start).Milliseconds()
	return res, nil
}
