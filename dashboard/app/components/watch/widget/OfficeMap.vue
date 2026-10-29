<script setup lang="ts">
// A company's office map: the startup pack's own app (office-map/, run as one of
// the project's processes) in the box, without its header. Office only shows
// it: the app reads the company's agents and departments itself.
interface Proc { id: string, name: string, command: string, state: { status: string, port?: number } }
const { t } = useLang()
const { isAdmin } = useAuth()
const toast = useToast()
const { projectId } = useWatchBox()
const { data, error, refresh } = useLiveFetch<{ processes: Proc[] }>(() => `/api/projects/${projectId.value}/processes`, { lazy: true, server: false })
const proc = computed(() => data.value?.processes.find(p => /(office-map|ban-do)\/server\.mjs/.test(p.command)))
const url = computed(() => {
  const port = proc.value?.state.status === 'running' && proc.value.state.port
  return port && import.meta.client ? `${location.protocol}//${location.hostname}:${port}/?embed` : ''
})
const starting = ref(false)
async function start() {
  if (!proc.value) return
  starting.value = true
  try {
    await $fetch(`/api/processes/${proc.value.id}/start`, { method: 'POST' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    starting.value = false
  }
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <LoadingRows v-if="!data && !error" :n="3" class="p-3" />
    <UAlert v-else-if="error" color="error" variant="subtle" :title="apiError(error)" class="m-3" :actions="[{ label: t('common.refresh'), onClick: () => refresh() }]" />
    <WatchEmpty v-else-if="!proc" icon="i-lucide-map" :text="t('watchW.officemap.none')" />
    <WatchEmpty v-else-if="!url" icon="i-lucide-map" :text="proc.state.status === 'running' ? t('watchW.officemap.noPort') : t('watchW.officemap.stopped')">
      <UButton v-if="isAdmin && proc.state.status !== 'running'" size="sm" icon="i-lucide-play" :loading="starting" :label="t('watchW.officemap.start')" @click="start" />
    </WatchEmpty>
    <iframe v-else :src="url" :title="t('watchW.kind.officemap')" class="min-h-0 w-full flex-1 border-0" sandbox="allow-scripts allow-same-origin" />
  </div>
</template>
