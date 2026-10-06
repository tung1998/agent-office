// The watch screen's boxes (like iTerm's panes): a tree of splits, each leaf a
// chat of one project. Kept in this browser (localStorage), per user's machine.
export type WatchDir = 'row' | 'col' // row: side by side, col: one above the other
export interface WatchPane { kind: 'pane', id: string, projectId: string, conversationId?: string }
export interface WatchSplit { kind: 'split', id: string, dir: WatchDir, children: WatchNode[], sizes: number[] }
export type WatchNode = WatchPane | WatchSplit

const KEY = 'office.watch.layout'
const newId = () => Math.random().toString(36).slice(2, 10)
const even = (n: number) => Array.from({ length: n }, () => 100 / n)

export function useWatchLayout() {
  const root = useState<WatchNode | null>('watch-layout', () => null)
  const loaded = useState('watch-layout-loaded', () => false)

  if (import.meta.client && !loaded.value) {
    loaded.value = true
    try {
      const raw = localStorage.getItem(KEY)
      if (raw) root.value = JSON.parse(raw) as WatchNode
    } catch { /* broken or private mode: start empty */ }
    watch(root, (v) => {
      try {
        if (v) localStorage.setItem(KEY, JSON.stringify(v))
        else localStorage.removeItem(KEY)
      } catch { /* private mode */ }
    }, { deep: true })
  }

  // the node and its parent split
  function find(id: string, node = root.value, parent: WatchSplit | null = null): { node: WatchNode, parent: WatchSplit | null } | null {
    if (!node) return null
    if (node.id === id) return { node, parent }
    if (node.kind === 'split') {
      for (const c of node.children) {
        const r = find(id, c, node)
        if (r) return r
      }
    }
    return null
  }

  function replace(old: WatchNode, next: WatchNode, parent: WatchSplit | null) {
    if (!parent) root.value = next
    else parent.children[parent.children.indexOf(old)] = next
  }

  // a new box next to this one (right or below), empty: it asks for a project or a recent chat
  function split(paneId: string, dir: WatchDir) {
    const r = find(paneId)
    if (!r || r.node.kind !== 'pane') return
    const pane = r.node
    const fresh: WatchPane = { kind: 'pane', id: newId(), projectId: '' }
    const p = r.parent
    if (p && p.dir === dir) {
      // same direction: one more box in that split, the space shared again
      const i = p.children.indexOf(pane)
      p.children.splice(i + 1, 0, fresh)
      p.sizes = even(p.children.length)
      return
    }
    replace(pane, { kind: 'split', id: newId(), dir, children: [pane, fresh], sizes: [50, 50] }, p)
  }

  // the first box, or one more to the right of the whole screen
  function add(projectId: string, conversationId?: string) {
    const fresh: WatchPane = { kind: 'pane', id: newId(), projectId, conversationId }
    const r = root.value
    if (!r) root.value = fresh
    else if (r.kind === 'split' && r.dir === 'row') {
      r.children.push(fresh)
      r.sizes = even(r.children.length)
    } else root.value = { kind: 'split', id: newId(), dir: 'row', children: [r, fresh], sizes: [50, 50] }
  }

  function close(id: string) {
    const r = find(id)
    if (!r) return
    const p = r.parent
    if (!p) {
      root.value = null
      return
    }
    const i = p.children.indexOf(r.node)
    p.children.splice(i, 1)
    p.sizes.splice(i, 1)
    if (p.children.length === 1) {
      // one box left: it takes its split's place
      const pp = find(p.id)
      if (pp) replace(p, p.children[0]!, pp.parent)
    } else {
      const sum = p.sizes.reduce((a, b) => a + b, 0) || 1
      p.sizes = p.sizes.map(s => s * 100 / sum)
    }
  }

  function setPane(id: string, patch: Partial<Omit<WatchPane, 'kind' | 'id'>>) {
    const r = find(id)
    if (r?.node.kind === 'pane') Object.assign(r.node, patch)
  }

  function resize(splitId: string, sizes: number[]) {
    const r = find(splitId)
    if (r?.node.kind === 'split') r.node.sizes = sizes
  }

  function clear() {
    root.value = null
  }

  const count = computed(() => {
    const n = (x: WatchNode | null): number => !x ? 0 : x.kind === 'pane' ? 1 : x.children.reduce((a, c) => a + n(c), 0)
    return n(root.value)
  })

  return { root, count, split, add, close, setPane, resize, clear }
}
