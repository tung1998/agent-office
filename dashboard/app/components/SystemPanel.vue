<script setup lang="ts">
// The machine office runs on and what office runs now (admin, on the
// overview): agents' answers, automations, project processes, each with the
// processes under it, to stop or kill.
type Group = SysGroup
const { t } = useLang()
const toast = useToast()

const { stats: data } = useSystemStats('full') // pushed by the server: no asking

const m = computed(() => data.value?.machine)
const pct = (used: number, total: number) => total ? Math.round(used / total * 100) : 0
const bytes = (b: number) => b >= 1 << 30 ? `${(b / (1 << 30)).toFixed(1)} GB` : b >= 1 << 20 ? `${Math.round(b / (1 << 20))} MB` : `${Math.round(b / 1024)} KB`
const rate = (b: number) => `${bytes(b)}/s`
function span(sec: number) {
  const d = Math.floor(sec / 86400), h = Math.floor(sec % 86400 / 3600), mi = Math.floor(sec % 3600 / 60)
  return d ? `${d}d ${h}h` : h ? `${h}h ${mi}m` : `${mi}m`
}
const since = (iso: string) => span(Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000))
const tone = (p: number) => p >= 90 ? 'bg-(--ui-error)' : p >= 70 ? 'bg-(--ui-warning)' : 'bg-primary'
const officeCount = computed(() => (data.value?.groups ?? []).reduce((n, g) => n + g.procs.length, 0))
const tiles = computed(() => {
  const x = m.value
  if (!x || !data.value) return []
  return [
    { key: 'cpu', label: t('sys.cpu'), value: `${Math.round(x.cpu_percent)}%`, sub: t('sys.cores', { n: x.cores.length }), bar: Math.round(x.cpu_percent) },
    { key: 'ram', label: t('sys.ram'), value: `${pct(x.mem_used, x.mem_total)}%`, sub: `${bytes(x.mem_used)} / ${bytes(x.mem_total)}`, bar: pct(x.mem_used, x.mem_total) },
    { key: 'disk', label: t('sys.disk'), value: `${pct(x.disk_used, x.disk_total)}%`, sub: `${bytes(x.disk_used)} / ${bytes(x.disk_total)}`, bar: pct(x.disk_used, x.disk_total) },
    { key: 'load', label: t('sys.load'), value: x.load?.length ? x.load[0]!.toFixed(2) : '—', sub: x.load?.length ? `${x.load[1]!.toFixed(2)} · ${x.load[2]!.toFixed(2)}` : '' },
    { key: 'net', label: t('sys.net'), value: `↓ ${rate(x.net_rx)}`, sub: `↑ ${rate(x.net_tx)}` },
    { key: 'office', label: t('sys.office'), value: bytes(data.value.office.mem), sub: t('sys.officeSub', { n: officeCount.value }) }
  ]
})

const kindIcon: Record<Group['kind'], string> = { agent: 'i-lucide-bot', automation: 'i-lucide-zap', project: 'i-lucide-play', other: 'i-lucide-terminal' }
const open = ref<Record<number, boolean>>({})
const acting = reactive(new Set<string>())
async function act(p: { pid: number, name: string }, force: boolean) {
  if (force && !confirm(t('sys.killConfirm', { name: p.name, pid: p.pid }))) return
  const key = `${p.pid}${force}`
  if (acting.has(key)) return
  acting.add(key)
  try {
    await $fetch(`/api/system/processes/${p.pid}/${force ? 'kill' : 'stop'}`, { method: 'POST' })
    toast.add({ title: force ? t('sys.killed') : t('sys.stopped'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    acting.delete(key)
  }
}
</script>

<template>
  <UCard :ui="{ body: 'p-0 sm:p-0' }">
    <template #header>
      <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
        <p class="flex items-center gap-2 font-semibold">
          <UIcon name="i-lucide-cpu" class="size-5" />{{ t('sys.title') }}
        </p>
        <span v-if="m" class="text-xs text-(--ui-text-muted)">{{ m.host }} · {{ m.os }} · {{ t('sys.uptime', { v: span(m.uptime_s) }) }}</span>
      </div>
    </template>
    <LoadingRows v-if="!data" :n="2" />
    <template v-else>
      <div class="grid grid-cols-2 gap-px border-b border-(--ui-border) bg-(--ui-border) sm:grid-cols-3 lg:grid-cols-6">
        <div v-for="x in tiles" :key="x.key" class="bg-(--ui-bg) p-3">
          <p class="text-xs text-(--ui-text-muted)">{{ x.label }}</p>
          <p class="truncate text-lg font-semibold tabular-nums">{{ x.value }}</p>
          <p class="truncate text-xs text-(--ui-text-muted) tabular-nums">{{ x.sub }}</p>
          <div v-if="x.bar !== undefined" class="mt-1.5 h-1 overflow-hidden rounded-full bg-(--ui-bg-accented)">
            <div class="h-full rounded-full transition-all" :class="tone(x.bar)" :style="{ width: `${Math.min(100, x.bar)}%` }" />
          </div>
          <div v-if="x.key === 'cpu'" class="mt-1.5 flex h-4 items-end gap-px" :title="m!.cores.map((c, i) => `#${i + 1}: ${Math.round(c)}%`).join('\n')">
            <span v-for="(c, i) in m!.cores" :key="i" class="min-w-0.5 flex-1 rounded-sm" :class="tone(c)" :style="{ height: `${Math.max(8, Math.min(100, c))}%` }" />
          </div>
        </div>
      </div>

      <p class="px-4 pt-3 pb-1 text-xs font-medium text-(--ui-text-muted)">{{ t('sys.running') }}</p>
      <p v-if="!data.groups.length" class="flex items-center gap-2 px-4 pb-4 text-sm text-(--ui-text-muted)">
        <UIcon name="i-lucide-circle-check" class="size-4 text-(--ui-success)" />{{ t('sys.none') }}
      </p>
      <div v-else class="divide-y divide-(--ui-border)">
        <div v-for="g in data.groups" :key="g.pid">
          <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-4 py-2">
            <button type="button" class="flex min-w-0 flex-1 basis-56 items-center gap-2.5 text-left" @click="open[g.pid] = !open[g.pid]">
              <UIcon name="i-lucide-chevron-right" class="size-4 shrink-0 text-(--ui-text-dimmed) transition" :class="open[g.pid] && 'rotate-90'" />
              <UIcon :name="kindIcon[g.kind]" class="size-4 shrink-0" :class="g.kind === 'agent' ? 'text-primary' : 'text-(--ui-text-muted)'" />
              <span class="min-w-0 flex-1">
                <span class="flex items-center gap-2">
                  <span class="truncate text-sm font-medium">{{ g.label }}</span>
                  <UBadge :label="t(`sys.kind.${g.kind}`)" color="neutral" variant="subtle" size="sm" />
                </span>
                <span class="block truncate text-xs text-(--ui-text-muted)">{{ g.sub || `PID ${g.pid}` }} · {{ t('sys.procs', { n: g.procs.length }) }} · {{ since(g.started_at) }}</span>
              </span>
            </button>
            <span class="w-14 text-right text-xs tabular-nums">{{ g.cpu.toFixed(0) }}%</span>
            <span class="w-16 text-right text-xs tabular-nums">{{ bytes(g.mem) }}</span>
            <div class="flex shrink-0 items-center gap-1">
              <UButton
                v-if="g.conversation_id && g.project_id" size="xs" color="neutral" variant="ghost" icon="i-lucide-message-square" :aria-label="t('sys.openChat')" :title="t('sys.openChat')"
                :to="`/projects/${g.project_id}?tab=chat&c=${g.conversation_id}`"
              />
              <UButton size="xs" color="neutral" variant="outline" :label="t('sys.stop')" :title="t('sys.stopTip')" :loading="acting.has(`${g.pid}false`)" @click="act({ pid: g.pid, name: g.label }, false)" />
              <UButton size="xs" color="error" variant="soft" :label="t('sys.kill')" :title="t('sys.killTip')" :loading="acting.has(`${g.pid}true`)" @click="act({ pid: g.pid, name: g.label }, true)" />
            </div>
          </div>
          <div v-if="open[g.pid]" class="bg-(--ui-bg-muted)/40 pb-1">
            <div v-for="p in g.procs" :key="p.pid" class="group/p flex items-center gap-3 py-1 pe-4 text-xs" :style="{ paddingLeft: `${2.75 + p.depth * 1}rem` }">
              <span class="min-w-0 flex-1 truncate" :title="p.cmd">
                <span class="font-medium">{{ p.name }}</span>
                <span class="ms-2 text-(--ui-text-dimmed)">{{ p.pid }}</span>
                <span class="ms-2 text-(--ui-text-muted)">{{ p.cmd }}</span>
              </span>
              <span class="w-14 text-right tabular-nums">{{ p.cpu.toFixed(0) }}%</span>
              <span class="w-16 text-right tabular-nums">{{ bytes(p.mem) }}</span>
              <div class="flex w-24 shrink-0 justify-end gap-0.5">
                <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-square" :aria-label="t('sys.stop')" :title="t('sys.stopTip')" @click="act(p, false)" />
                <UButton size="xs" color="error" variant="ghost" icon="i-lucide-x" :aria-label="t('sys.kill')" :title="t('sys.killTip')" @click="act(p, true)" />
              </div>
            </div>
          </div>
        </div>
      </div>
    </template>
  </UCard>
</template>
