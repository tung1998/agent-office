import type { Attachment } from '~/components/PromptInput.vue'

// What was typed in a chat but not sent, kept in the browser per chat: another
// tab (or a reload) and back, it is still in the box. The 10 chats written in
// last; an 11th drops the oldest.
interface ChatDraft { key: string, text: string, files: Attachment[] }
const STORE = 'office.chatDrafts'
const MAX = 10

function read(): ChatDraft[] {
  try {
    const v = JSON.parse(localStorage.getItem(STORE) ?? '[]')
    return Array.isArray(v) ? v : []
  } catch { return [] }
}
function write(list: ChatDraft[]) {
  try { localStorage.setItem(STORE, JSON.stringify(list)) } catch { /* full or private mode: not kept */ }
}

export function useChatDrafts() {
  function load(key: string): { text: string, files: Attachment[] } {
    const d = import.meta.client ? read().find(x => x.key === key) : undefined
    return { text: d?.text ?? '', files: d?.files ?? [] }
  }
  // an empty box takes its chat out; written last goes first
  function save(key: string, text: string, files: Attachment[]) {
    if (!import.meta.client) return
    const rest = read().filter(x => x.key !== key)
    write(text.trim() || files.length ? [{ key, text, files }, ...rest].slice(0, MAX) : rest)
  }
  return { load, save }
}
