// The person's chats an agent answered since they last looked: a kind in
// "Cần xử lý" (the server's incidents), shared by every page that shows them.
export function useUnread() {
  const { data, refresh } = useLiveFetch<{ incidents: { kind: string, id: string }[] }>('/api/incidents', { key: 'incidents', lazy: true })
  const ids = computed(() => new Set((data.value?.incidents ?? []).filter(x => x.kind === 'unread').map(x => x.id)))
  async function mark(id: string, seen: boolean) {
    await $fetch(`/api/conversations/${id}/seen`, { method: 'POST', body: { seen } })
    await refresh()
  }
  return { ids, mark }
}
