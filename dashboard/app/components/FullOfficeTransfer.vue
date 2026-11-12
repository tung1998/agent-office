<script setup lang="ts">
// Moving the whole office to another machine (ADR-274/275). One machine
// sends, the other receives: each sees only its own steps. Receiving
// replaces the database at the next start, so it ends on a summary to
// confirm, never on a bare button.
interface Peer { id: string, name: string, admin?: boolean }
interface ImportResult { projects: { name: string, to: string, ok: boolean, note?: string }[], warnings: string[], pending: boolean }

const { t } = useLang()
const toast = useToast()
const { data: peerData, error: peerError, refresh: refreshPeers } = useLazyFetch<{ peers: Peer[] }>('/api/peers')
const peers = computed(() => peerData.value?.peers ?? [])
const peerItems = computed(() => peers.value.map(p => ({ label: p.name, value: p.id })))

const side = ref<'send' | 'receive'>('send')
const sides = computed(() => [
  { label: t('transfer.office.send'), value: 'send', icon: 'i-lucide-upload' },
  { label: t('transfer.office.receive'), value: 'receive', icon: 'i-lucide-download' }
])
const pass = ref('')
const passValid = ref(false)
const busy = ref('')
const mb = (n: number) => (n / 1048576).toFixed(1)
watch(side, () => { pass.value = '' })

// ---- send ----
const download = ref<{ name: string, size: number } | null>(null)
async function makeFile() {
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
const sendTo = ref('')
const sendPeer = computed(() => peers.value.find(p => p.id === sendTo.value))
async function sendToPeer() {
  busy.value = 'offer'
  try {
    const r = await $fetch<{ size: number }>(`/api/peers/${sendTo.value}/full-offer`, { method: 'POST', body: { passphrase: pass.value } })
    toast.add({ title: t('transfer.office.sent', { name: sendPeer.value?.name ?? '', mb: mb(r.size) }), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}

// ---- receive ----
const source = ref<'file' | 'peer'>('file')
const sources = computed(() => [
  { label: t('transfer.office.fromFile'), value: 'file' },
  { label: t('transfer.office.fromPeer'), value: 'peer', disabled: !peers.value.length }
])
const fileInput = ref<HTMLInputElement | null>(null)
const file = ref<File | null>(null)
const fromPeer = ref('')
const mode = ref<'move' | 'clone' | undefined>(undefined)
const modes = computed(() => [
  { label: t('transfer.office.move'), description: t('transfer.office.moveHint'), value: 'move' },
  { label: t('transfer.office.clone'), description: t('transfer.office.cloneHint'), value: 'clone' }
])
const ready = computed(() => passValid.value && !!mode.value && (source.value === 'file' ? !!file.value : !!fromPeer.value))
const sourceName = computed(() => source.value === 'file' ? file.value?.name : peers.value.find(p => p.id === fromPeer.value)?.name)
const confirming = ref(false)
const result = ref<ImportResult | null>(null)

async function receive() {
  confirming.value = false
  busy.value = 'import'
  result.value = null
  try {
    if (source.value === 'file') {
      const res = await fetch(`/api/transfer/full-import?mode=${mode.value}`, {
        method: 'POST', body: file.value, credentials: 'same-origin',
        headers: { 'Content-Type': 'application/octet-stream', 'X-Office-Passphrase': pass.value }
      })
      const body = await res.json()
      if (!res.ok) throw new Error(body?.error || res.statusText)
      result.value = body
    } else {
      result.value = await $fetch<ImportResult>(`/api/peers/${fromPeer.value}/full-pull`, { method: 'POST', body: { passphrase: pass.value, mode: mode.value } })
    }
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
</script>

<template>
  <div class="space-y-5 rounded-lg border border-(--ui-border) p-4 sm:p-5">
    <UTabs v-model="side" :items="sides" :content="false" class="w-full sm:max-w-md" :ui="{ list: 'w-full', trigger: 'flex-1 min-w-0', label: 'truncate' }" />

    <!-- this machine sends -->
    <div v-if="side === 'send'" class="space-y-5">
      <PassphraseField v-model="pass" v-model:valid="passValid" confirm />
      <div class="flex flex-wrap items-center gap-2">
        <UButton icon="i-lucide-file-archive" :label="t('transfer.office.makeFile')" :loading="busy === 'export'" :disabled="!passValid || !!busy" @click="makeFile" />
        <UButton v-if="download" icon="i-lucide-download" color="success" variant="soft" :to="`/api/transfer/full-export/${download.name}`" external
                 :label="t('transfer.download', { mb: mb(download.size) })" />
      </div>
      <div class="space-y-2 border-t border-(--ui-border) pt-4">
        <p class="text-sm font-medium">{{ t('transfer.office.orPeer') }}</p>
        <UAlert v-if="peerError" color="error" variant="subtle" :description="apiError(peerError)">
          <template #actions><UButton size="xs" variant="outline" :label="t('common.refresh')" @click="refreshPeers()" /></template>
        </UAlert>
        <USkeleton v-else-if="!peerData" class="h-8 w-64" />
        <p v-else-if="!peers.length" class="text-sm text-(--ui-text-muted)">{{ t('transfer.office.noPeers') }}</p>
        <div v-else class="flex flex-wrap items-end gap-2">
          <UFormField :label="t('transfer.office.peer')">
            <USelect v-model="sendTo" :items="peerItems" :placeholder="t('transfer.office.pickPeer')" class="min-w-56" />
          </UFormField>
          <UButton icon="i-lucide-send" color="neutral" variant="outline" :label="t('transfer.office.sendPeer')" :loading="busy === 'offer'"
                   :disabled="!sendPeer?.admin || !passValid || !!busy" @click="sendToPeer" />
        </div>
        <p class="text-xs text-(--ui-text-muted)">
          {{ sendPeer && !sendPeer.admin ? t('transfer.office.needAdmin') : t('transfer.office.peerHow') }}
        </p>
      </div>
    </div>

    <!-- this machine receives -->
    <div v-else class="space-y-5">
      <UFormField :label="t('transfer.office.source')">
        <URadioGroup v-model="source" :items="sources" orientation="horizontal" />
      </UFormField>
      <div v-if="source === 'file'">
        <input ref="fileInput" type="file" accept=".age" class="hidden" @change="file = ($event.target as HTMLInputElement).files?.[0] ?? null">
        <UButton icon="i-lucide-file-up" color="neutral" variant="outline" :label="file ? file.name : t('transfer.chooseFile')" @click="fileInput?.click()" />
      </div>
      <UFormField v-else :label="t('transfer.office.peer')" :hint="t('transfer.office.fetchHint')">
        <USelect v-model="fromPeer" :items="peerItems" :placeholder="t('transfer.office.pickPeer')" class="min-w-56" />
      </UFormField>
      <PassphraseField v-model="pass" v-model:valid="passValid" />
      <UFormField :label="t('transfer.office.mode')">
        <URadioGroup v-model="mode" :items="modes" variant="card" class="w-full" :ui="{ fieldset: 'grid gap-2 sm:grid-cols-2' }" />
      </UFormField>
      <UButton icon="i-lucide-hard-drive-download" :label="t('transfer.office.receiveBtn')" :loading="busy === 'import'" :disabled="!ready || !!busy" @click="confirming = true" />

      <UAlert v-if="result" :color="result.projects.every(p => p.ok) ? 'success' : 'warning'" variant="subtle"
              :icon="result.projects.every(p => p.ok) ? 'i-lucide-circle-check' : 'i-lucide-triangle-alert'" :title="t('transfer.office.received')">
        <template #description>
          <ul class="space-y-1 text-xs">
            <li v-for="p in result.projects" :key="p.name" class="flex items-start gap-1.5">
              <UIcon :name="p.ok ? 'i-lucide-check' : 'i-lucide-alert-circle'" class="mt-0.5 size-3.5 shrink-0" :aria-label="p.ok ? t('transfer.ok') : t('transfer.failed')" />
              <span>{{ p.name }} → <code>{{ p.to }}</code><span v-if="p.note"> · {{ p.note }}</span></span>
            </li>
            <li v-for="w in result.warnings" :key="w" class="text-(--ui-text-muted)">{{ w }}</li>
          </ul>
          <p v-if="result.pending" class="mt-3 text-sm font-medium">{{ t('transfer.office.restart') }}</p>
        </template>
      </UAlert>
    </div>

    <UModal v-model:open="confirming" :title="t('transfer.office.confirmTitle')">
      <template #body>
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
          <dt class="text-(--ui-text-muted)">{{ t('transfer.office.source') }}</dt><dd class="font-medium">{{ sourceName }}</dd>
          <dt class="text-(--ui-text-muted)">{{ t('transfer.office.mode') }}</dt><dd class="font-medium">{{ mode === 'move' ? t('transfer.office.move') : t('transfer.office.clone') }}</dd>
        </dl>
        <ul class="mt-4 list-disc space-y-1 pl-5 text-sm">
          <li>{{ t('transfer.office.confirmDb') }}</li>
          <li>{{ t('transfer.office.confirmLost') }}</li>
          <li>{{ mode === 'move' ? t('transfer.office.moveHint') : t('transfer.office.cloneHint') }}</li>
        </ul>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="confirming = false" />
          <UButton color="warning" icon="i-lucide-hard-drive-download" :label="t('transfer.office.confirmBtn')" @click="receive" />
        </div>
      </template>
    </UModal>
  </div>
</template>
