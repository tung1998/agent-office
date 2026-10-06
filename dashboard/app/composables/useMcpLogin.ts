// OAuth login of an office MCP server (ADR-092), shared by the office MCP
// gateway and the machine MCP list ("Đăng nhập qua office"). Works for any
// server with standard discovery; one whose authorization server registers no
// client answers needs_client, and the caller opens the client form.
// Office opened over plain http on another host (Tailscale, LAN): the login
// comes back to localhost, which this browser cannot reach, so the person
// pastes the address it lands on (McpLoginPaste).
export function useMcpLogin() {
  const toast = useToast()
  const connecting = ref<Record<string, boolean>>({})
  // the server whose login waits for the pasted address
  const pasteFor = useState<string | null>('mcp-login-paste', () => null)

  // connect opens the login page of server id. target may first make the
  // server (an async step returning its id, or null to give up): the tab is
  // opened before it, as a tab opened after an await is blocked as a popup.
  async function connect(key: string, target: string | (() => Promise<string | null>), onNeedsClient?: (id: string, msg: string) => void) {
    const tab = window.open('', '_blank')
    connecting.value = { ...connecting.value, [key]: true }
    let id: string | null = null
    try {
      id = typeof target === 'string' ? target : await target()
      if (!id) {
        tab?.close()
        return false
      }
      const r = await $fetch<{ url: string, paste?: boolean }>(`/api/mcp/servers/${id}/oauth/start`, { method: 'POST', body: { origin: location.origin } })
      if (r.paste) pasteFor.value = id
      if (tab) tab.location.href = r.url
      else location.href = r.url
      return true
    } catch (e) {
      tab?.close()
      if (id && onNeedsClient && (e as { data?: { code?: string } }).data?.code === 'needs_client') onNeedsClient(id, apiError(e))
      else toast.add({ title: apiError(e), color: 'error' })
      return false
    } finally {
      connecting.value = { ...connecting.value, [key]: false }
    }
  }
  return { connect, connecting, pasteFor }
}
