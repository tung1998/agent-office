<script setup lang="ts">
// Sharing chosen projects with another office (ADR-276). Export and import
// are two tabs; importing shows what each project becomes before anything is
// written, and asks only where a project clashes with one here.
interface Project { id: string, name: string }
interface Staged { slug: string, name: string, kind: string, conflict?: { id: string, name: string, reason: 'name' | 'remote' }, new_name: string }
interface Result { name: string, action: string, ok: boolean, path?: string, note?: string }
interface Peer { id: string, name: string }

const { t } = useLang()
const toast = useToast()
const { data: projData, error: projError, refresh: refreshProjects } = useLiveFetch<{ projects: Project[] }>('/api/projects', { lazy: true })
const { data: peerData, error: peerError, refresh: refreshPeers } = useLazyFetch<{ peers: Peer[] }>('/api/peers')
const peerItems = computed(() => (peerData.value?.peers ?? []).map(p => ({ label: p.name, value: p.id })))
const projectItems = computed(() => (projData.value?.projects ?? []).map(p => ({ label: p.name, value: p.id })))

const tab = ref<'export' | 'import'>('export')
const tabs = computed(() => [
  { label: t('transfer.projects.export'), value: 'export', icon: 'i-lucide-upload' },
  { label: t('transfer.projects.import'), value: 'import', icon: 'i-lucide-download' }
])
const pass = ref('')
const passValid = ref(false)
const busy = ref('')
const mb = (n: number) => (n / 1048576).toFixed(1)
watch(tab, () => { pass.value = '' })

// ---- export ----
const chosen = ref<string[]>([])
const withChats = ref(true)
const withMemory = ref(true)
const download = ref<{ name: string, size: number } | null>(null)
async function exportChosen() {
  busy.value = 'export'
  download.value = null
  try {
    download.value = await $fetch('/api/transfer/projects-export', { method: 'POST', body: { project_ids: chosen.value, include: includes(), passphrase: pass.value } })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}

// ---- through a paired office: let it fetch once, or sync now ----
const sendTo = ref('')
const synced = ref<Result[] | null>(null)
const peerName = (id: string) => peerData.value?.peers.find(p => p.id === id)?.name ?? ''
const includes = () => [...(withChats.value ? ['chats'] : []), ...(withMemory.value ? ['memory'] : [])]
async function offer() {
  busy.value = 'offer'
  try {
    const r = await $fetch<{ size: number }>(`/api/peers/${sendTo.value}/projects-offer`, { method: 'POST', body: { project_ids: chosen.value, include: includes(), passphrase: pass.value } })
    toast.add({ title: t('transfer.office.sent', { name: peerName(sendTo.value), mb: mb(r.size) }), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
async function syncNow() {
  busy.value = 'sync'
  synced.value = null
  try {
    synced.value = (await $fetch<{ results: Result[] }>(`/api/peers/${sendTo.value}/projects-sync`, { method: 'POST', body: { project_ids: chosen.value, include: includes() } })).results
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}

// ---- import: file or paired office → preview → choices → import ----
const source = ref<'file' | 'peer'>('file')
const sources = computed(() => [
  { label: t('transfer.office.fromFile'), value: 'file' },
  { label: t('transfer.office.fromPeer'), value: 'peer', disabled: !peerItems.value.length }
])
const fromPeer = ref('')
const fileInput = ref<HTMLInputElement | null>(null)
const file = ref<File | null>(null)
const token = ref('')
const staged = ref<Staged[]>([])
const choice = ref<Record<string, 'new' | 'overwrite' | 'skip'>>({})
const results = ref<Result[] | null>(null)
const choices = computed(() => [
  { label: t('transfer.projects.new'), value: 'new' },
  { label: t('transfer.projects.overwrite'), value: 'overwrite' },
  { label: t('transfer.projects.skip'), value: 'skip' }
])
function onFile(e: Event) {
  file.value = (e.target as HTMLInputElement).files?.[0] ?? null
  staged.value = []
  results.value = null
}
async function preview() {
  busy.value = 'preview'
  try {
    let body: { token: string, projects: Staged[] }
    if (source.value === 'peer') {
      body = await $fetch(`/api/peers/${fromPeer.value}/projects-fetch`, { method: 'POST', body: { passphrase: pass.value } })
    } else {
      const res = await fetch('/api/transfer/projects-import', {
        method: 'POST', body: file.value, credentials: 'same-origin',
        headers: { 'Content-Type': 'application/octet-stream', 'X-Office-Passphrase': pass.value }
      })
      const raw = await res.json()
      if (!res.ok) throw new Error(raw?.error || res.statusText)
      body = raw
    }
    token.value = body.token
    staged.value = body.projects
    choice.value = Object.fromEntries(staged.value.map(p => [p.slug, 'new'])) // a clash becomes a copy unless the person says otherwise
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
const outcome = (p: Staged) => {
  const c = choice.value[p.slug]
  if (c === 'skip') return t('transfer.projects.willSkip')
  if (c === 'overwrite') return t('transfer.projects.willOverwrite', { name: p.conflict?.name ?? p.name })
  return t('transfer.projects.willAdd', { name: p.new_name })
}
const overwritten = computed(() => staged.value.filter(p => choice.value[p.slug] === 'overwrite'))
const importing = computed(() => staged.value.filter(p => choice.value[p.slug] !== 'skip').length)
const confirming = ref(false)
function startImport() {
  if (overwritten.value.length) confirming.value = true
  else apply()
}
async function apply() {
  confirming.value = false
  busy.value = 'apply'
  try {
    const r = await $fetch<{ results: Result[] }>('/api/transfer/projects-import/apply', { method: 'POST', body: { token: token.value, decisions: choice.value } })
    results.value = r.results
    staged.value = []
    file.value = null
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
</script>

<template>
  <div class="space-y-5 rounded-lg border border-(--ui-border) p-4 sm:p-5">
    <UTabs v-model="tab" :items="tabs" :content="false" class="w-full sm:max-w-md" :ui="{ list: 'w-full', trigger: 'flex-1 min-w-0', label: 'truncate' }" />

    <div v-if="tab === 'export'" class="space-y-5">
      <UFormField :label="t('transfer.projects.which')">
        <UAlert v-if="projError" color="error" variant="subtle" :description="apiError(projError)">
          <template #actions><UButton size="xs" color="neutral" variant="outline" :label="t('common.refresh')" @click="refreshProjects()" /></template>
        </UAlert>
        <USkeleton v-else-if="!projData" class="h-8 w-full" />
        <USelectMenu v-else v-model="chosen" :items="projectItems" value-key="value" multiple :placeholder="t('transfer.projects.pick')" class="w-full" />
      </UFormField>
      <div class="flex flex-wrap gap-x-6 gap-y-2">
        <UCheckbox v-model="withChats" :label="t('transfer.projects.chats')" />
        <UCheckbox v-model="withMemory" :label="t('transfer.projects.memory')" />
      </div>
      <PassphraseField v-model="pass" v-model:valid="passValid" confirm />
      <div class="flex flex-wrap items-center gap-2">
        <UButton icon="i-lucide-file-archive" :label="t('transfer.projects.makeFile', { n: chosen.length })" :loading="busy === 'export'"
                 :disabled="!chosen.length || !passValid || !!busy" @click="exportChosen" />
        <UButton v-if="download" icon="i-lucide-download" color="success" variant="soft" :to="`/api/transfer/full-export/${download.name}`" external
                 :label="t('transfer.download', { mb: mb(download.size) })" />
      </div>
      <div class="space-y-3 border-t border-(--ui-border) pt-4">
        <p class="text-sm font-medium">{{ t('transfer.office.orPeer') }}</p>
        <UAlert v-if="peerError" color="error" variant="subtle" :description="apiError(peerError)">
          <template #actions><UButton size="xs" color="neutral" variant="outline" :label="t('common.refresh')" @click="refreshPeers()" /></template>
        </UAlert>
        <USkeleton v-else-if="!peerData" class="h-8 w-64" />
        <p v-else-if="!peerItems.length" class="text-sm text-(--ui-text-muted)">{{ t('transfer.office.noPeers') }}</p>
        <template v-else>
          <div class="flex flex-wrap items-end gap-2">
            <UFormField :label="t('transfer.office.peer')">
              <USelect v-model="sendTo" :items="peerItems" :placeholder="t('transfer.office.pickPeer')" class="min-w-56" />
            </UFormField>
            <UButton icon="i-lucide-refresh-cw" :label="t('transfer.projects.syncNow')" :loading="busy === 'sync'" :disabled="!sendTo || !chosen.length || !!busy" @click="syncNow" />
            <UButton icon="i-lucide-send" color="neutral" variant="outline" :label="t('transfer.office.sendPeer')" :loading="busy === 'offer'"
                     :disabled="!sendTo || !chosen.length || !passValid || !!busy" @click="offer" />
          </div>
          <ul class="space-y-1 text-xs text-(--ui-text-muted)">
            <li><span class="font-medium text-(--ui-text)">{{ t('transfer.projects.syncNow') }}:</span> {{ t('transfer.projects.syncHint') }}</li>
            <li><span class="font-medium text-(--ui-text)">{{ t('transfer.office.sendPeer') }}:</span> {{ t('transfer.projects.offerHint') }}</li>
          </ul>
        </template>
        <UAlert v-if="synced" :color="synced.every(r => r.ok) ? 'success' : 'warning'" variant="subtle"
                :icon="synced.every(r => r.ok) ? 'i-lucide-circle-check' : 'i-lucide-triangle-alert'" :title="t('transfer.projects.synced', { name: peerName(sendTo) })">
          <template #description>
            <ul class="space-y-1 text-xs">
              <li v-for="r in synced" :key="r.name" class="flex items-start gap-1.5">
                <UIcon :name="r.ok ? 'i-lucide-check' : 'i-lucide-alert-circle'" class="mt-0.5 size-3.5 shrink-0" :aria-label="r.ok ? t('transfer.ok') : t('transfer.failed')" />
                <span>{{ r.name }} · {{ r.action === 'overwrite' ? t('transfer.projects.updated') : r.action === 'new' ? t('transfer.projects.added') : r.action }}<span v-if="r.note"> · {{ r.note }}</span></span>
              </li>
            </ul>
          </template>
        </UAlert>
      </div>
    </div>

    <div v-else class="space-y-5">
      <template v-if="!staged.length">
        <UFormField :label="t('transfer.office.source')">
          <URadioGroup v-model="source" :items="sources" orientation="horizontal" />
        </UFormField>
        <div v-if="source === 'file'">
          <input ref="fileInput" type="file" accept=".age" class="hidden" @change="onFile">
          <UButton icon="i-lucide-file-up" color="neutral" variant="outline" :label="file ? file.name : t('transfer.chooseFile')" @click="fileInput?.click()" />
        </div>
        <UFormField v-else :label="t('transfer.office.peer')" :hint="t('transfer.projects.fetchHint')">
          <USelect v-model="fromPeer" :items="peerItems" :placeholder="t('transfer.office.pickPeer')" class="min-w-56" />
        </UFormField>
        <PassphraseField v-model="pass" v-model:valid="passValid" />
        <UButton icon="i-lucide-list-checks" :label="t('transfer.projects.preview')" :loading="busy === 'preview'"
                 :disabled="(source === 'file' ? !file : !fromPeer) || !passValid || !!busy" @click="preview" />
      </template>

      <template v-else>
        <p class="text-sm text-(--ui-text-muted)">{{ t('transfer.projects.previewHint') }}</p>
        <ul class="divide-y divide-(--ui-border) rounded-md border border-(--ui-border)">
          <li v-for="p in staged" :key="p.slug" class="space-y-2 px-3 py-3">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-medium">{{ p.name }}</span>
              <UBadge v-if="p.conflict" color="warning" variant="subtle" icon="i-lucide-copy"
                      :label="p.conflict.reason === 'name' ? t('transfer.projects.clashName', { name: p.conflict.name }) : t('transfer.projects.clashRemote', { name: p.conflict.name })" />
            </div>
            <URadioGroup v-if="p.conflict" v-model="choice[p.slug]" :items="choices" orientation="horizontal" size="sm"
                         :aria-label="t('transfer.projects.choiceFor', { name: p.name })" />
            <p class="text-xs text-(--ui-text-muted)">{{ outcome(p) }}</p>
          </li>
        </ul>
        <div class="flex flex-wrap justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="staged = []" />
          <UButton icon="i-lucide-check" :label="t('transfer.projects.importBtn', { n: importing })" :loading="busy === 'apply'" :disabled="!importing || !!busy" @click="startImport" />
        </div>
      </template>

      <UAlert v-if="results" :color="results.every(r => r.ok) ? 'success' : 'warning'" variant="subtle"
              :icon="results.every(r => r.ok) ? 'i-lucide-circle-check' : 'i-lucide-triangle-alert'" :title="t('transfer.projects.done')">
        <template #description>
          <ul class="space-y-1 text-xs">
            <li v-for="r in results" :key="r.name" class="flex items-start gap-1.5">
              <UIcon :name="r.ok ? 'i-lucide-check' : 'i-lucide-alert-circle'" class="mt-0.5 size-3.5 shrink-0" :aria-label="r.ok ? t('transfer.ok') : t('transfer.failed')" />
              <span>{{ r.name }}<span v-if="r.path"> → <code>{{ r.path }}</code></span><span v-if="r.note"> · {{ r.note }}</span></span>
            </li>
          </ul>
        </template>
      </UAlert>
    </div>

    <UModal v-model:open="confirming" :title="t('transfer.projects.confirmTitle', { n: overwritten.length })">
      <template #body>
        <ul class="list-disc space-y-1 pl-5 text-sm">
          <li v-for="p in overwritten" :key="p.slug" class="font-medium">{{ p.conflict?.name }}</li>
        </ul>
        <p class="mt-4 text-sm text-(--ui-text-muted)">{{ t('transfer.projects.confirmBody') }}</p>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="confirming = false" />
          <UButton color="warning" :label="t('transfer.projects.confirmBtn')" @click="apply" />
        </div>
      </template>
    </UModal>
  </div>
</template>
