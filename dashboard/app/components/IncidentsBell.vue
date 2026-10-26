<script setup lang="ts">
// The header's "cần xử lý": its count opens the list, each item with its
// buttons right there (a card: Duyệt / Bỏ qua; a chat answered: Đã xem…) or
// opened where it is. Browser notifications are turned on from here (plugins/notify.client.ts).
import { actOnIncident, alreadyDecided, alreadyDecidedText, incidentActs, incidentText, isApproval, useBrowserNotify, type Incident, type IncidentAct } from '~/composables/useApprovals'

const { t } = useLang()
const toast = useToast()
const { data, error, refresh } = useLiveFetch<{ incidents: Incident[], count: number }>('/api/incidents', { key: 'incidents', lazy: true })
const { supported, permission, enable } = useBrowserNotify()
const open = ref(false)
const n = computed(() => data.value?.count ?? 0)
// approvals first: what can be done from here
const list = computed(() => [...(data.value?.incidents ?? [])].sort((a, b) => Number(isApproval(b)) - Number(isApproval(a))).slice(0, 12))
const busy = reactive(new Set<string>())
async function act(x: Incident, what: IncidentAct) {
  busy.add(x.key)
  try {
    await actOnIncident(x, what) // done: it leaves the list, nothing more to say
    await refresh()
  } catch (e) {
    const d = alreadyDecided(e) // decided elsewhere first: said so, and the list catches up
    toast.add(d ? { title: alreadyDecidedText(d, t), color: 'info' } : { title: apiError(e), color: 'error' })
    if (d) void refresh()
  } finally {
    busy.delete(x.key)
  }
}
// every chat answered since the person looked, not only the ones shown
const unread = computed(() => (data.value?.incidents ?? []).filter(x => x.kind === 'unread'))
const clearing = ref(false)
async function seenAll() {
  clearing.value = true
  try {
    await $fetch('/api/conversations/seen', { method: 'POST' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    clearing.value = false
  }
}
function go(link: string) {
  open.value = false
  navigateTo(link || '/')
}
</script>

<template>
  <UPopover v-model:open="open" :content="{ align: 'end' }">
    <button
      type="button" :title="error ? t('hdr.incidentsError') : t('hdr.incidents', { n })" :aria-label="t('hdr.incidents', { n })"
      class="flex items-center gap-1 rounded-md px-1.5 py-1 text-xs tabular-nums transition hover:bg-(--ui-bg-elevated)"
      :class="error ? 'text-(--ui-warning)' : n ? 'text-(--ui-error)' : 'text-(--ui-text-muted) hover:text-(--ui-text)'"
    >
      <UIcon :name="error ? 'i-lucide-triangle-alert' : n ? 'i-lucide-bell-ring' : 'i-lucide-bell'" class="size-3.5" />{{ error ? '?' : n }}
    </button>
    <template #content>
      <div class="w-[min(24rem,calc(100vw-1rem))]">
        <div class="max-h-[60vh] divide-y divide-(--ui-border) overflow-y-auto">
          <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-triangle-alert" class="m-2" :title="t('common.loadError')" :actions="[{ label: t('common.refresh'), onClick: () => { refresh() } }]" />
          <LoadingRows v-else-if="!data" class="p-2" />
          <p v-else-if="!list.length" class="p-3 text-sm text-(--ui-text-muted)">{{ t('bell.empty') }}</p>
          <div v-for="x in list" :key="x.key" class="space-y-1.5 p-2.5">
            <button type="button" class="block w-full rounded text-start outline-none focus-visible:ring-2 focus-visible:ring-primary" @click="go(x.link)">
              <p class="line-clamp-2 break-words text-sm" :class="x.severity === 'error' ? 'text-(--ui-error)' : ''" :title="x.title">{{ incidentText(x, (n, l) => t('bell.files', { n, list: l })) }}</p>
              <p class="truncate text-xs text-(--ui-text-muted)">{{ [x.project_name, x.detail].filter(Boolean).join(' · ') }}</p>
            </button>
            <div class="flex flex-wrap gap-1.5">
              <UButton
                v-for="(a, i) in incidentActs(x)" :key="a" size="xs" :color="i ? 'neutral' : 'primary'" :variant="i ? 'outline' : 'solid'"
                :icon="a === 'approve' ? 'i-lucide-check' : undefined" :label="t(`bell.act.${a}`)" :loading="busy.has(x.key) && !i" :disabled="busy.has(x.key)" @click="act(x, a)"
              />
              <UButton size="xs" color="neutral" variant="ghost" :label="t('bell.act.open')" @click="go(x.link)" />
            </div>
          </div>
        </div>
        <div class="flex items-center gap-2 border-t border-(--ui-border) p-2">
          <UButton v-if="supported && permission === 'default'" size="xs" color="neutral" variant="ghost" icon="i-lucide-bell-plus" :label="t('bell.enable')" @click="enable" />
          <span v-else-if="supported && permission === 'denied'" class="text-xs text-(--ui-text-muted)">{{ t('bell.denied') }}</span>
          <UButton v-if="unread.length > 1" size="xs" color="neutral" variant="ghost" icon="i-lucide-check-check" :label="t('bell.seenAll', { n: unread.length })" :loading="clearing" class="ms-auto" @click="seenAll" />
          <UButton size="xs" color="neutral" variant="link" :label="t('bell.all')" :class="unread.length > 1 ? '' : 'ms-auto'" @click="go('/')" />
        </div>
      </div>
    </template>
  </UPopover>
</template>
