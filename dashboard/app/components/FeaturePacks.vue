<script setup lang="ts">
// A project's feature packs: a pack's switch turns all its features on or
// off, each feature has its own below. Off hides menus, tabs, widgets and
// agent tools; nothing is deleted or stopped.
const props = defineProps<{ projectId: string }>()
const emit = defineEmits<{ changed: [] }>()
const { t } = useLang()
const toast = useToast()
const { isAdmin } = useAuth()

interface Config { packs: Record<string, boolean> | null, off: string[] | null }
interface State { config: Config, on: string[], off: Record<string, 'project' | 'no_folder'>, packs: { id: string, features: string[], default_off?: boolean }[] }
const { data, error, refresh } = useLiveFetch<State>(() => `/api/projects/${props.projectId}/features`, { lazy: true })

const packIcon: Record<string, string> = { develop: 'i-lucide-code-xml', support: 'i-lucide-headset' }
const featureIcon: Record<string, string> = { git: 'i-lucide-git-branch', ops: 'i-lucide-square-terminal', selfcheck: 'i-lucide-heart-pulse', support: 'i-lucide-inbox' }

// a pack not set is on, but one off by default (support)
const packOn = (id: string) => data.value?.config.packs?.[id] ?? !data.value?.packs.find(p => p.id === id)?.default_off
const featureOn = (f: string) => !!data.value?.on.includes(f)
const noFolder = (f: string) => data.value?.off[f] === 'no_folder'

// what still runs under Vận hành (processes, monitors): turning it off only hides it
async function stillRunning(features: string[]): Promise<string> {
  const what: string[] = []
  let n = 0
  try {
    if (features.includes('ops') && featureOn('ops')) {
      const r = await $fetch<{ processes: { state: { status: string } }[] }>(`/api/projects/${props.projectId}/processes`)
      const k = r.processes.filter(p => p.state.status === 'running').length
      if (k) { n += k; what.push(t('feat.runningProcs', { n: k })) }
    }
    if (features.includes('ops') && featureOn('ops')) {
      const r = await $fetch<{ monitors: { enabled: boolean }[] }>('/api/monitors', { query: { project: props.projectId } })
      const k = r.monitors.filter(m => m.enabled).length
      if (k) { n += k; what.push(t('feat.runningMonitors', { n: k })) }
    }
  } catch { /* unknown: nothing to warn about */ }
  return n ? t('feat.stillRunning', { n, what: what.join(', ') }) : ''
}

const saving = ref(false)
async function save(next: Config, turningOff: string[]) {
  if (saving.value) return
  const warn = turningOff.length ? await stillRunning(turningOff) : ''
  if (warn && !confirm(warn)) return
  saving.value = true
  try {
    await $fetch(`/api/projects/${props.projectId}/features`, { method: 'PUT', body: next })
    await refresh()
    emit('changed')
    toast.add({ title: t('feat.saved'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}

function current(): Config {
  return { packs: { ...(data.value?.config.packs ?? {}) }, off: [...(data.value?.config.off ?? [])] }
}
function setPack(id: string, on: boolean) {
  const c = current()
  c.packs![id] = on
  const pack = data.value?.packs.find(p => p.id === id)
  if (on) c.off = c.off!.filter(f => !pack?.features.includes(f)) // a pack turned on comes back whole
  save(c, on ? [] : pack?.features ?? [])
}
function setFeature(f: string, on: boolean) {
  const c = current()
  c.off = on ? c.off!.filter(x => x !== f) : [...c.off!, f]
  save(c, on ? [] : [f])
}
</script>

<template>
  <UCard class="max-w-4xl" :ui="{ body: 'space-y-3 sm:p-4' }">
    <p class="flex items-center gap-2 font-medium">
      <UIcon name="i-lucide-blocks" class="size-4 text-(--ui-text-muted)" />{{ t('feat.title') }}
      <InfoTip :text="t('feat.help')" icon-class="size-3.5 text-(--ui-text-dimmed)" />
    </p>
    <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="t('common.loadError')" :actions="[{ label: t('common.refresh'), onClick: () => refresh() }]" />
    <USkeleton v-else-if="!data" class="h-24 w-full" />
    <template v-else>
      <div v-for="p in data.packs" :key="p.id" class="rounded-lg border border-(--ui-border)">
        <div class="flex items-center gap-3 px-3 py-2.5">
          <UIcon :name="packIcon[p.id] ?? 'i-lucide-package'" class="size-5 shrink-0 text-primary" />
          <div class="min-w-0 flex-1">
            <p class="text-sm font-medium">{{ t(`feat.pack.${p.id}` as 'feat.pack.develop') }}</p>
            <p class="truncate text-xs text-(--ui-text-muted)">{{ t(`feat.packDesc.${p.id}` as 'feat.packDesc.develop') }}</p>
          </div>
          <USwitch :model-value="packOn(p.id)" :disabled="!isAdmin || saving" :aria-label="t(`feat.pack.${p.id}` as 'feat.pack.develop')" @update:model-value="v => setPack(p.id, v)" />
        </div>
        <div v-if="packOn(p.id)" class="divide-y divide-(--ui-border) border-t border-(--ui-border)">
          <div v-for="f in p.features" :key="f" class="flex items-center gap-3 py-2 ps-11 pe-3">
            <UIcon :name="featureIcon[f] ?? 'i-lucide-circle'" class="size-4 shrink-0 text-(--ui-text-muted)" />
            <div class="min-w-0 flex-1">
              <p class="text-sm">{{ t(`feat.f.${f}` as 'feat.f.git') }}</p>
              <p class="text-xs text-(--ui-text-muted)">{{ noFolder(f) ? t('feat.noFolder') : t(`feat.fDesc.${f}` as 'feat.fDesc.git') }}</p>
            </div>
            <USwitch :model-value="featureOn(f)" size="sm" :disabled="!isAdmin || saving || noFolder(f)" :aria-label="t(`feat.f.${f}` as 'feat.f.git')" @update:model-value="v => setFeature(f, v)" />
          </div>
        </div>
      </div>
    </template>
  </UCard>
</template>
