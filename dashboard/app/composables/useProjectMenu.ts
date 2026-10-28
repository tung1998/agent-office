// The sidebar's projects, kept with the account (/api/me/prefs/projects) so
// they follow it to any browser: the order set on the Projects page, the
// projects hidden from the menu (still listed there) and the menu's options:
// how many show, in which order, and whether opening one closes the others.
type MenuSort = 'manual' | 'usage' | 'name'
interface Saved { order: string[], hidden: string[], max?: number, sort?: MenuSort, single?: boolean }
const PREF_URL = '/api/me/prefs/projects'
const MENU_MAX = { min: 1, max: 30, def: 5 }

export function useProjectMenu() {
  const { user } = useAuth()
  const prefs = useState<Saved>('project-menu', () => ({ order: [], hidden: [] }))
  const loadedFor = useState<string>('project-menu:user', () => '')

  async function load() {
    const id = user.value?.id ?? ''
    if (!id || loadedFor.value === id) return
    loadedFor.value = id
    try {
      const v = (await $fetch<{ value: Partial<Saved> | null }>(PREF_URL)).value
      prefs.value = { ...v, order: v?.order ?? [], hidden: v?.hidden ?? [] }
    } catch {
      loadedFor.value = '' // try again on the next page
    }
  }
  if (import.meta.client) void load()

  async function save(next: Saved) {
    const before = prefs.value
    prefs.value = next
    try {
      await $fetch(PREF_URL, { method: 'PUT', body: { value: next } })
    } catch (e) {
      prefs.value = before
      throw e
    }
  }

  const isHidden = (id: string) => prefs.value.hidden.includes(id)
  const setHidden = (id: string, hidden: boolean) => save({
    ...prefs.value,
    hidden: hidden ? [...new Set([...prefs.value.hidden, id])] : prefs.value.hidden.filter(x => x !== id)
  })
  const setOrder = (ids: string[]) => save({ ...prefs.value, order: ids })
  const hasOrder = computed(() => prefs.value.order.length > 0)
  const max = computed(() => Math.min(MENU_MAX.max, Math.max(MENU_MAX.min, Math.round(prefs.value.max ?? MENU_MAX.def))))
  const sort = computed<MenuSort>(() => prefs.value.sort ?? 'manual')
  const single = computed(() => !!prefs.value.single)
  const setOptions = (o: Pick<Saved, 'max' | 'sort' | 'single'>) => save({ ...prefs.value, ...o })

  // list in the account's order; projects it never placed keep theirs, after
  function arrange<T extends { id: string }>(list: T[]): T[] {
    const pos = new Map(prefs.value.order.map((id, i) => [id, i]))
    return list.map((p, i) => ({ p, i })).sort((a, b) =>
      (pos.get(a.p.id) ?? Infinity) - (pos.get(b.p.id) ?? Infinity) || a.i - b.i).map(x => x.p)
  }

  // the menu's projects: the first `max` not hidden, by the chosen order
  // (manual before any drag is the most opened, as before); `cur`, the open
  // one, always shows, for its sections
  const { top } = useProjectUsage()
  function menu<T extends { id: string, name: string }>(list: T[], cur?: string): T[] {
    const visible = list.filter(p => !isHidden(p.id))
    const n = max.value
    const out = sort.value === 'name'
      ? [...visible].sort((a, b) => a.name.localeCompare(b.name)).slice(0, n)
      : sort.value === 'manual' && hasOrder.value ? arrange(visible).slice(0, n) : top(visible, n)
    const open = cur ? list.find(p => p.id === cur) : undefined
    if (open && !out.includes(open)) {
      if (out.length < n) out.push(open)
      else out[n - 1] = open
    }
    return out
  }

  return { prefs, isHidden, setHidden, setOrder, hasOrder, arrange, max, maxLimits: MENU_MAX, sort, single, setOptions, menu }
}
