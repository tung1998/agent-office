package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/tung1998/agent-office/internal/storage"
)

// FoundServer is catalog metadata, never credentials or connection settings.
type FoundServer struct {
	Name          string `json:"name"`
	Available     bool   `json:"available"`
	Status        string `json:"status,omitempty"`
	ToolCount     int    `json:"tool_count"`
	CatalogStatus string `json:"catalog_status"` // unchecked | empty | ready | stale
}

type FoundTool struct {
	Server      string          `json:"server"`
	Tool        string          `json:"tool"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ToolSearch struct {
	Servers    []FoundServer `json:"servers"`
	Tools      []FoundTool   `json:"tools"`
	NextOffset *int          `json:"next_offset,omitempty"`
	Hint       string        `json:"hint,omitempty"`
}

// FindTools searches the last checked catalog locally. An empty query only
// lists servers; a search returns at most five schemas. It never starts an
// upstream connection, grants access, or exposes schemas of unassigned servers.
func (g *Gateway) FindTools(ctx context.Context, c Caller, query string, offset int) (ToolSearch, error) {
	out := ToolSearch{Servers: []FoundServer{}, Tools: []FoundTool{}}
	query = strings.TrimSpace(query)
	if offset < 0 || len(query) > 512 {
		return out, errors.New("query tối đa 512 byte; offset phải từ 0")
	}
	query = searchWords(query)
	list, err := g.Store.MCPServers().List(ctx)
	if err != nil {
		return out, err
	}
	terms := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if query != "" && len(terms) == 0 {
		return out, errors.New("query cần có từ khóa hoặc tên MCP/tool")
	}
	type hit struct {
		tool  FoundTool
		score int
	}
	var hits []hit
	for _, m := range list {
		if !catalogServer(m) {
			continue
		}
		available := Assigned(m, c)
		if query == "" || strings.Contains(searchWords(m.Name), query) {
			catalog := "ready"
			switch {
			case m.LastCheckAt == nil:
				catalog = "unchecked"
			case len(m.LastTools) == 0:
				catalog = "empty"
			case m.LastCheckStatus != "ok":
				catalog = "stale"
			}
			out.Servers = append(out.Servers, FoundServer{Name: m.Name, Available: available, Status: m.LastCheckStatus, ToolCount: len(m.LastTools), CatalogStatus: catalog})
		}
		if query == "" || !available {
			continue
		}
		for _, t := range m.LastTools {
			name := searchWords(m.Name + " " + t.Name)
			desc := searchWords(t.Description)
			score := 0
			if query == searchWords(t.Name) || query == searchWords(m.Name+"/"+t.Name) {
				score += 100
			}
			for _, term := range terms {
				if strings.Contains(name, term) {
					score += 4
				} else if strings.Contains(desc, term) {
					score++
				}
			}
			if score == 0 {
				continue
			}
			schema := t.InputSchema
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			hits = append(hits, hit{tool: FoundTool{Server: m.Name, Tool: t.Name, Description: t.Description, InputSchema: schema}, score: score})
		}
	}
	sort.Slice(out.Servers, func(i, j int) bool { return out.Servers[i].Name < out.Servers[j].Name })
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if hits[i].tool.Server != hits[j].tool.Server {
			return hits[i].tool.Server < hits[j].tool.Server
		}
		return hits[i].tool.Tool < hits[j].tool.Tool
	})
	if offset >= len(hits) {
		if query != "" && len(hits) == 0 {
			out.Hint = "No matching tools. Retry with English keywords or the server name; an unchecked or empty catalog needs an MCP check in office. Not-assigned servers need the person's permission."
		}
		return out, nil
	}
	end := offset + min(5, len(hits)-offset)
	for _, h := range hits[offset:end] {
		out.Tools = append(out.Tools, h.tool)
	}
	if end < len(hits) {
		out.NextOffset = &end
	}
	return out, nil
}

// Match Vietnamese descriptions with or without accents. This is lexical
// search, not translation: English descriptions still need English keywords.
func searchWords(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		if r == 'đ' {
			return 'd'
		}
		return r
	}, norm.NFD.String(strings.ToLower(s)))
}

func catalogServer(m storage.MCPServer) bool {
	return m.Enabled && m.Scope == "machine" && (m.Kind == "http" || m.Kind == "stdio")
}

// InvokeFound checks today's catalog before using the existing gateway policy,
// proposal, logging and result truncation path. No API function-name length
// restriction applies to the server/tool carried as arguments.
func (g *Gateway) InvokeFound(ctx context.Context, c Caller, server, tool string, args json.RawMessage) (string, bool) {
	return resultText(g.invokeFound(ctx, c, server, tool, args, false))
}

// InvokeFoundResult is the content-preserving form used by the office MCP.
func (g *Gateway) InvokeFoundResult(ctx context.Context, c Caller, server, tool string, args json.RawMessage) json.RawMessage {
	return g.invokeFound(ctx, c, server, tool, args, true)
}

func (g *Gateway) invokeFound(ctx context.Context, c Caller, server, tool string, args json.RawMessage, rich bool) json.RawMessage {
	m, err := g.Store.MCPServers().GetByName(ctx, server)
	if err != nil || !catalogServer(m) || !Assigned(m, c) {
		return toolResult("MCP không có hoặc chưa được cấp cho lượt này; dùng find_tool để kiểm tra", true)
	}
	for _, t := range m.LastTools {
		if t.Name == tool {
			return g.invoke(ctx, c, server, tool, args, rich)
		}
	}
	if len(m.LastTools) == 0 {
		return toolResult("danh mục MCP chưa được kiểm tra hoặc đang rỗng; kiểm tra MCP trong office rồi dùng find_tool lại", true)
	}
	return toolResult("tool không có trong danh mục MCP; kiểm tra lại MCP trong office", true)
}
