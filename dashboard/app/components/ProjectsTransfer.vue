<script setup lang="ts">
// Chosen projects to and from another office (ADR-276): each one's agents,
// workflows, automations, chats, notes and code, encrypted. On import a
// project that clashes with one here is overwritten, copied as "<name> 2",
// or skipped, as picked.
interface Project { id: string, name: string }
interface Staged { slug: string, name: string, kind: string, conflict?: { id: string, name: string, reason: 'name' | 'remote' }, new_name: string }
interface Result { name: string, action: string, ok: boolean, path?: string, note?: string }

const { t } = useLang()
const toast = useToast()
const MIN = 12

const { data: projData } = useLiveFetch<{ projects: Project[] }>('/api/projects', { lazy: true })
const projectItems = computed(() => (projData.value?.projects ?? []).map(p => ({ label: p.name, value: p.id })))
const chosen = ref<string[]>([])
const withChats = ref(true)
const withMemory = ref(true)
const pass = ref('')
const passOk = computed(() => [...pass.value].length >= MIN)
const busy = ref('')
const mb = (n: number) => (n / 1048576).toFixed(1)

const download = ref<{ name: string, size: number } | null>(null)
async function exportChosen() {
  busy.value = 'export'
  download.value = null
  try {
    const include = [...(withChats.value ? ['chats'] : []), ...(withMemory.value ? ['memory'] : [])]
    download.value = await $fetch('/api/transfer/projects-export', { method: 'POST', body: { project_ids: chosen.value, include, passphrase: pass.value } })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}

// ---- import: upload → preview with clashes → apply the choices ----
const fileInput = ref<HTMLInputElement | null>(null)
const file = ref<File | null>(null)
const token = ref('')
const staged = ref<Staged[]>([])
const choice = ref<Record<string, string>>({})
const results = ref<Result[] | null>(null)
const choices = computed(() => [
  { label: t('projTransfer.new'), value: 'new' },
  { label: t('projTransfer.overwrite'), value: 'overwrite' },
  { label: t('projTransfer.skip'), value: 'skip' }
])
function onFile(e: Event) {
  file.value = (e.target as HTMLInputElement).files?.[0] ?? null
  staged.value = []
  results.value = null
}
async function preview() {
  if (!file.value) return
  busy.value = 'preview'
  try {
    const res = await fetch('/api/transfer/projects-import', {
      method: 'POST', body: file.value, credentials: 'same-origin',
      headers: { 'Content-Type': 'application/octet-stream', 'X-Office-Passphrase': pass.value }
    })
    const body = await res.json()
    if (!res.ok) throw new Error(body?.error || res.statusText)
    token.value = body.token
    staged.value = body.projects
    // a clash waits for the person: a new copy by default, never an overwrite
    choice.value = Object.fromEntries(staged.value.map(p => [p.slug, 'new']))
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
const label = (p: Staged) => choice.value[p.slug] === 'new' ? p.new_name : p.conflict?.name ?? p.name
async function apply() {
  if (Object.values(choice.value).includes('overwrite') && !confirm(t('projTransfer.overwriteConfirm'))) return
  busy.value = 'apply'
  try {
    const r = await $fetch<{ results: Result[] }>('/api/transfer/projects-import/apply', { method: 'POST', body: { token: token.value, decisions: choice.value } })
    results.value = r.results
    staged.value = []
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
</script>

<template>
  <UCard>
    <template #header>
      <p class="font-medium"><InfoLabel :label="t('projTransfer.title')" :info="t('projTransfer.hint')" /></p>
    </template>
    <div class="space-y-4">
      <UFormField :label="t('fullOffice.passphrase')" :hint="t('fullOffice.min', { n: MIN })">
        <UInput v-model="pass" type="password" autocomplete="new-password" class="w-full sm:w-80" />
      </UFormField>

      <div class="space-y-2">
        <USkeleton v-if="!projData" class="h-8 w-full" />
        <USelectMenu v-else v-model="chosen" :items="projectItems" value-key="value" multiple :placeholder="t('projTransfer.pick')" class="w-full" />
        <div class="flex flex-wrap items-center gap-4">
          <UCheckbox v-model="withChats" :label="t('projTransfer.chats')" />
          <UCheckbox v-model="withMemory" :label="t('projTransfer.memory')" />
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <UButton icon="i-lucide-package" :label="t('projTransfer.export', { n: chosen.length })" :loading="busy === 'export'" :disabled="!chosen.length || !passOk || !!busy" @click="exportChosen" />
          <UButton v-if="download" icon="i-lucide-download" color="success" variant="soft" :to="`/api/transfer/full-export/${download.name}`" external
                   :label="t('fullOffice.download', { mb: mb(download.size) })" />
        </div>
      </div>

      <div class="space-y-2 border-t border-(--ui-border) pt-4">
        <div class="flex flex-wrap items-center gap-2">
          <input ref="fileInput" type="file" accept=".age" class="hidden" @change="onFile">
          <UButton icon="i-lucide-file-up" color="neutral" variant="outline" :label="file ? file.name : t('fullOffice.chooseFile')" @click="fileInput?.click()" />
          <UButton icon="i-lucide-search" :label="t('projTransfer.preview')" :loading="busy === 'preview'" :disabled="!file || !passOk || !!busy" @click="preview" />
        </div>
        <template v-if="staged.length">
          <div class="divide-y divide-(--ui-border) rounded-lg border border-(--ui-border)">
            <div v-for="p in staged" :key="p.slug" class="flex flex-wrap items-center gap-3 px-3 py-2 text-sm">
              <span class="min-w-32 flex-1 font-medium">{{ p.name }}</span>
              <UBadge v-if="p.conflict" color="warning" variant="subtle"
                      :label="p.conflict.reason === 'name' ? t('projTransfer.clashName', { name: p.conflict.name }) : t('projTransfer.clashRemote', { name: p.conflict.name })" />
              <URadioGroup v-if="p.conflict" v-model="choice[p.slug]" :items="choices" orientation="horizontal" size="sm" />
              <span class="text-xs text-(--ui-text-muted)">→ {{ choice[p.slug] === 'skip' ? t('projTransfer.skip') : label(p) }}</span>
            </div>
          </div>
          <div class="flex justify-end">
            <UButton icon="i-lucide-check" :label="t('projTransfer.apply')" :loading="busy === 'apply'" :disabled="!!busy" @click="apply" />
          </div>
        </template>
        <UAlert v-if="results" :color="results.every(r => r.ok) ? 'success' : 'warning'" variant="subtle" :title="t('projTransfer.done')">
          <template #description>
            <ul class="space-y-1 text-xs">
              <li v-for="r in results" :key="r.name">{{ r.ok ? '✓' : '!' }} {{ r.name }} · {{ r.action }}<span v-if="r.path"> → <code>{{ r.path }}</code></span><span v-if="r.note"> ({{ r.note }})</span></li>
            </ul>
          </template>
        </UAlert>
      </div>
    </div>
  </UCard>
</template>
