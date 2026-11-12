// Ready remote MCP servers for the office gateway's "add" form (ADR-277): one
// click fills name, URL and the header names; the person pastes the secret.
// Header values stay empty (placeholder only). No header = the server logs in
// with OAuth through office (ADR-091). Endpoints checked against each docs link.
export interface McpPreset {
  id: string // also the server name
  label: string
  kind: 'http'
  url: string
  headers: { key: string, placeholder: string }[]
  docs: string
}

export const MCP_PRESETS: McpPreset[] = [
  {
    id: 'composio', label: 'Composio', kind: 'http',
    url: 'https://connect.composio.dev/mcp',
    headers: [{ key: 'x-consumer-api-key', placeholder: 'ck_…' }],
    docs: 'https://docs.composio.dev/docs/composio-connect'
  },
  {
    id: 'context7', label: 'Context7', kind: 'http',
    url: 'https://mcp.context7.com/mcp',
    headers: [{ key: 'Authorization', placeholder: 'Bearer …' }],
    docs: 'https://github.com/upstash/context7'
  },
  {
    id: 'github', label: 'GitHub', kind: 'http',
    url: 'https://api.githubcopilot.com/mcp/',
    headers: [{ key: 'Authorization', placeholder: 'Bearer github_pat_…' }],
    docs: 'https://github.com/github/github-mcp-server'
  },
  {
    id: 'linear', label: 'Linear', kind: 'http',
    url: 'https://mcp.linear.app/mcp',
    headers: [],
    docs: 'https://linear.app/docs/mcp'
  },
  {
    id: 'sentry', label: 'Sentry', kind: 'http',
    url: 'https://mcp.sentry.dev/mcp',
    headers: [],
    docs: 'https://mcp.sentry.dev/'
  }
]
