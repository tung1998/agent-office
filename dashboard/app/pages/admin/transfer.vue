<script setup lang="ts">
interface Change { kind: 'provider' | 'template' | 'project', name: string, op: 'create' | 'update' | 'unchanged' | 'skip', detail?: string }

definePageMeta({ admin: true })
const toast = useToast()
const { t } = useLang()

// ---- import ----
const fileInput = ref<HTMLInputElement | null>(null)
const bundle = ref<unknown>(null)
const fileName = ref('')
const plan = ref<Change[] | null>(null)
const importing = ref(false)

async function onFile(e: Event) {
  const f = (e.target as HTMLInputElement).files?.[0]
  if (!f) return
  fileName.value = f.name
  plan.value = null
  try {
    bundle.value = JSON.parse(await f.text())
    const res = await $fetch<{ changes: Change[] }>('/api/transfer/import', { method: 'POST', body: { bundle: bundle.value, dry_run: true } })
    plan.value = res.changes
  } catch (err) {
    bundle.value = null
    toast.add({ title: t('admin.importFailed'), description: apiError(err, t('admin.importFailedFallback')), color: 'error' })
  } finally {
    if (fileInput.value) fileInput.value.value = ''
  }
}

const pending = computed(() => plan.value?.filter(c => c.op === 'create' || c.op === 'update').length ?? 0)

async function applyImport() {
  importing.value = true
  try {
    await $fetch('/api/transfer/import', { method: 'POST', body: { bundle: bundle.value, dry_run: false } })
    toast.add({ title: t('admin.importDone', { n: pending.value }), color: 'success' })
    plan.value = null
    bundle.value = null
  } catch (err) {
    toast.add({ title: apiError(err), color: 'error' })
  } finally {
    importing.value = false
  }
}

// ---- backup ----
const backingUp = ref(false)
const backupPath = ref('')
async function backup() {
  backingUp.value = true
  try {
    backupPath.value = (await $fetch<{ path: string }>('/api/transfer/backup', { method: 'POST', body: {} })).path
  } catch (err) {
    toast.add({ title: apiError(err), color: 'error' })
  } finally {
    backingUp.value = false
  }
}

const opMeta = computed<Record<string, { label: string, color: 'success' | 'info' | 'neutral' | 'warning' }>>(() => ({
  create: { label: t('admin.opCreate'), color: 'success' },
  update: { label: t('admin.opUpdate'), color: 'info' },
  unchanged: { label: t('admin.opUnchanged'), color: 'neutral' },
  skip: { label: t('admin.opSkip'), color: 'warning' }
}))
const kindLabelOf = computed<Record<string, string>>(() => ({ provider: t('admin.kindProvider'), template: t('admin.kindTemplate'), project: t('admin.kindProject') }))
</script>

<template>
  <PageShell :title="t('admin.transferTitle')">
    <div class="max-w-4xl space-y-6">
      <UCard>
        <template #header>
          <p class="font-medium">{{ t('admin.exportTitle') }}</p>
          <p class="text-sm text-(--ui-text-muted)">{{ t('admin.exportDesc') }}</p>
        </template>
        <div class="space-y-3">
          <UButton icon="i-lucide-download" :label="t('admin.downloadConfig')" to="/api/transfer/export" external target="_blank" />
          <p class="text-sm text-(--ui-text-muted)">{{ t('admin.exportCliHint') }}</p>
          <pre class="rounded-md bg-(--ui-bg-muted) p-3 text-xs">office export ./office-config --commit</pre>
        </div>
      </UCard>

      <UCard>
        <template #header>
          <p class="font-medium">{{ t('admin.importTitle') }}</p>
          <p class="text-sm text-(--ui-text-muted)">{{ t('admin.importDesc') }}</p>
        </template>
        <div class="space-y-4">
          <input ref="fileInput" type="file" accept="application/json,.json" class="hidden" @change="onFile">
          <UButton icon="i-lucide-upload" :label="t('admin.chooseFile')" color="neutral" variant="outline" @click="fileInput?.click()" />
          <template v-if="plan">
            <p class="text-sm">{{ t('admin.importPreviewPrefix') }} <code>{{ fileName }}</code>: {{ t('admin.importPreviewSuffix', { n: pending }) }}</p>
            <div class="divide-y divide-(--ui-border) rounded-lg border border-(--ui-border)">
              <div v-for="(c, i) in plan" :key="i" class="flex flex-wrap items-center gap-3 px-3 py-2 text-sm">
                <UBadge :label="opMeta[c.op]!.label" :color="opMeta[c.op]!.color" variant="subtle" class="w-24 justify-center" />
                <span class="w-28 text-(--ui-text-muted)">{{ kindLabelOf[c.kind] }}</span>
                <span class="font-medium">{{ c.name }}</span>
                <span v-if="c.detail" class="text-xs text-(--ui-text-muted)">{{ c.detail }}</span>
              </div>
            </div>
            <div class="flex justify-end gap-2">
              <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="plan = null; bundle = null" />
              <UButton icon="i-lucide-check" :label="t('admin.applyChanges', { n: pending })" :disabled="!pending" :loading="importing" @click="applyImport" />
            </div>
          </template>
        </div>
      </UCard>

      <UCard>
        <template #header>
          <p class="font-medium">{{ t('admin.backupTitle') }}</p>
          <p class="text-sm text-(--ui-text-muted)">
            {{ t('admin.backupDesc') }}
          </p>
        </template>
        <div class="space-y-3">
          <UButton icon="i-lucide-archive" :label="t('admin.createBackup')" :loading="backingUp" @click="backup" />
          <UAlert v-if="backupPath" color="success" variant="subtle" icon="i-lucide-circle-check" :title="t('admin.backupDone')">
            <template #description><code class="text-xs">{{ backupPath }}</code></template>
          </UAlert>
          <pre class="rounded-md bg-(--ui-bg-muted) p-3 text-xs">office backup --out ~/office-backups/$(date +%F)</pre>
        </div>
      </UCard>
    </div>
  </PageShell>
</template>
