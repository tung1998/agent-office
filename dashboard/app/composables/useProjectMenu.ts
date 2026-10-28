// The sidebar's projects, kept with the account (/api/me/prefs/projects) so
// they follow it to any browser: the order set on the Projects page (the
// first 5 show) and the projects hidden from the menu (still listed there).
interface Saved { order: string[], hidden: string[] }
const PREF_URL = '/api/me/prefs/projects'

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
      prefs.value = { order: v?.order ?? [], hidden: v?.hidden ?? [] }
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

  // list in the account's order; projects it never placed keep theirs, after
  function arrange<T extends { id: string }>(list: T[]): T[] {
    const pos = new Map(prefs.value.order.map((id, i) => [id, i]))
    return list.map((p, i) => ({ p, i })).sort((a, b) =>
      (pos.get(a.p.id) ?? Infinity) - (pos.get(b.p.id) ?? Infinity) || a.i - b.i).map(x => x.p)
  }

  return { prefs, isHidden, setHidden, setOrder, hasOrder, arrange }
}
