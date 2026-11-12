<script setup lang="ts">
// The whole office (ADR-274/275): export it to a file, bring one in, or move
// it through a paired office. Everything travels encrypted with a passphrase.
interface Peer { id: string, name: string, admin?: boolean }
interface ImportResult { projects: { name: string, to: string, ok: boolean, note?: string }[], warnings: string[], pending: boolean }

const { t } = useLang()
const toast = useToast()
const MIN = 12

const { data: peerData } = useLazyFetch<{ peers: Peer[] }>('/api/peers')
const peers = computed(() => peerData.value?.peers ?? [])

const pass = ref('')
const pass2 = ref('')
const passOk = computed(() => [...pass.value].length >= MIN)
const mode = ref<'move' | 'clone'>('move')
const modes = computed(() => [
  { label: t('fullOffice.modeMove'), value: 'move' },
  { label: t('fullOffice.modeClone'), value: 'clone' }
])
const busy = ref('')
const result = ref<ImportResult | null>(null)

// ---- export: build, then the browser downloads it ----
const download = ref<{ name: string, size: number } | null>(null)
async function exportAll() {
  if (pass.value !== pass2.value) return toast.add({ title: t('fullOffice.mismatch'), color: 'error' })
  busy.value = 'export'
  download.value = null
  try {
    download.value = await $fetch<{ name: string, size: number }>('/api/transfer/full-export', { method: 'POST', body: { passphrase: pass.value } })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
const mb = (n: number) => (n / 1048576).toFixed(1)

// ---- import from a file ----
const fileInput = ref<HTMLInputElement | null>(null)
const file = ref<File | null>(null)
function onFile(e: Event) {
  file.value = (e.target as HTMLInputElement).files?.[0] ?? null
}
async function importFile() {
  if (!file.value || !confirm(t('fullOffice.importConfirm'))) return
  busy.value = 'import'
  result.value = null
  try {
    const res = await fetch(`/api/transfer/full-import?mode=${mode.value}`, {
      method: 'POST', body: file.value, credentials: 'same-origin',
      headers: { 'Content-Type': 'application/octet-stream', 'X-Office-Passphrase': pass.value }
    })
    const body = await res.json()
    if (!res.ok) throw new Error(body?.error || res.statusText)
    result.value = body
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}

// ---- through a paired office ----
const peerId = ref('')
const peer = computed(() => peers.value.find(p => p.id === peerId.value))
async function offer() {
  if (!peer.value || pass.value !== pass2.value) return toast.add({ title: t('fullOffice.mismatch'), color: 'error' })
  busy.value = 'offer'
  try {
    const r = await $fetch<{ size: number }>(`/api/peers/${peerId.value}/full-offer`, { method: 'POST', body: { passphrase: pass.value } })
    toast.add({ title: t('fullOffice.offered', { name: peer.value.name, mb: mb(r.size) }), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
async function pull() {
  if (!peer.value || !confirm(t('fullOffice.importConfirm'))) return
  busy.value = 'pull'
  result.value = null
  try {
    result.value = await $fetch<ImportResult>(`/api/peers/${peerId.value}/full-pull`, { method: 'POST', body: { passphrase: pass.value, mode: mode.value } })
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
      <p class="font-medium"><InfoLabel :label="t('fullOffice.title')" :info="t('fullOffice.hint')" /></p>
    </template>
    <div class="space-y-4">
      <div class="grid gap-2 sm:grid-cols-2">
        <UFormField :label="t('fullOffice.passphrase')" :hint="t('fullOffice.min', { n: MIN })">
          <UInput v-model="pass" type="password" autocomplete="new-password" class="w-full" />
        </UFormField>
        <UFormField :label="t('fullOffice.passphraseAgain')">
          <UInput v-model="pass2" type="password" autocomplete="new-password" class="w-full" />
        </UFormField>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <UButton icon="i-lucide-package" :label="t('fullOffice.export')" :loading="busy === 'export'" :disabled="!passOk || !!busy" @click="exportAll" />
        <UButton v-if="download" icon="i-lucide-download" color="success" variant="soft" :to="`/api/transfer/full-export/${download.name}`" external
                 :label="t('fullOffice.download', { mb: mb(download.size) })" />
      </div>

      <div class="space-y-2 border-t border-(--ui-border) pt-4">
        <UFormField :label="t('fullOffice.mode')">
          <URadioGroup v-model="mode" :items="modes" orientation="horizontal" />
        </UFormField>
        <p class="text-xs text-(--ui-text-muted)">{{ mode === 'move' ? t('fullOffice.modeMoveHint') : t('fullOffice.modeCloneHint') }}</p>
        <div class="flex flex-wrap items-center gap-2">
          <input ref="fileInput" type="file" accept=".age" class="hidden" @change="onFile">
          <UButton icon="i-lucide-file-up" color="neutral" variant="outline" :label="file ? file.name : t('fullOffice.chooseFile')" @click="fileInput?.click()" />
          <UButton icon="i-lucide-upload" :label="t('fullOffice.import')" :loading="busy === 'import'" :disabled="!file || !passOk || !!busy" @click="importFile" />
        </div>
      </div>

      <div v-if="peers.length" class="space-y-2 border-t border-(--ui-border) pt-4">
        <p class="text-sm font-medium"><InfoLabel :label="t('fullOffice.peerTitle')" :info="t('fullOffice.peerHint')" /></p>
        <div class="flex flex-wrap items-center gap-2">
          <USelect v-model="peerId" :items="peers.map(p => ({ label: p.name, value: p.id }))" :placeholder="t('fullOffice.pickPeer')" class="min-w-48" />
          <UButton icon="i-lucide-send" color="neutral" variant="outline" :label="t('fullOffice.offer')" :loading="busy === 'offer'"
                   :disabled="!peer || !peer.admin || !passOk || !!busy" @click="offer" />
          <UButton icon="i-lucide-cloud-download" :label="t('fullOffice.pull')" :loading="busy === 'pull'" :disabled="!peer || !passOk || !!busy" @click="pull" />
        </div>
        <p v-if="peer && !peer.admin" class="text-xs text-(--ui-text-muted)">{{ t('fullOffice.needAdmin') }}</p>
      </div>

      <UAlert v-if="result" :color="result.projects.every(p => p.ok) ? 'success' : 'warning'" variant="subtle" icon="i-lucide-circle-check" :title="t('fullOffice.imported')">
        <template #description>
          <ul class="space-y-1 text-xs">
            <li v-for="p in result.projects" :key="p.name">{{ p.ok ? '✓' : '!' }} {{ p.name }} → <code>{{ p.to }}</code><span v-if="p.note"> ({{ p.note }})</span></li>
            <li v-for="w in result.warnings" :key="w">• {{ w }}</li>
          </ul>
          <p v-if="result.pending" class="mt-2 font-medium">{{ t('fullOffice.restartNeeded') }}</p>
        </template>
      </UAlert>
    </div>
  </UCard>
</template>
