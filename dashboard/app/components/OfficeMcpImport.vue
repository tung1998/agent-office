<script setup lang="ts">
// Moving the MCP servers already set up on this machine (Claude Code user/
// local/project, Codex) into office (ADR-093): pick one or many, rename,
// and optionally take them out of the source (a copy kept in trash).
interface Ref { kind: string, name: string, type: string, path: string, project_path?: string }
interface Candidate {
  ref: Ref, name: string, suggested: string, source: string, transport: string, target: string,
  movable: boolean, taken: boolean, moved_as?: string, problem?: string
}
interface Result { name: string, source: string, ok: boolean, error?: string, taken_out: boolean, missing?: string[] }

const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ done: [] }>()
const { t } = useLang()

const loading = ref(false)
const error = ref('')
const items = ref<Candidate[]>([])
const picked = ref<Record<number, boolean>>({})
const names = ref<Record<number, string>>({})
const takeOut = ref(true)
const moving = ref(false)
const results = ref<Result[] | null>(null)

async function load() {
  loading.value = true
  error.value = ''
  results.value = null
  try {
    const r = await $fetch<{ candidates: Candidate[] }>('/api/mcp/import')
    items.value = r.candidates
    picked.value = {}
    names.value = Object.fromEntries(r.candidates.map((c, i) => [i, c.suggested]))
  } catch (e) {
    error.value = apiError(e)
  } finally {
    loading.value = false
  }
}
watch(open, (v) => { if (v) load() }, { immediate: true })

const usable = (c: Candidate) => !c.problem && !c.moved_as
const chosen = computed(() => items.value.map((c, i) => ({ c, i })).filter(x => picked.value[x.i] && usable(x.c)))

async function move() {
  moving.value = true
  error.value = ''
  try {
    const r = await $fetch<{ results: Result[] }>('/api/mcp/import', {
      method: 'POST',
      body: { take_out: takeOut.value, items: chosen.value.map(x => ({ ref: x.c.ref, name: names.value[x.i] })) }
    })
    results.value = r.results
    emit('done')
  } catch (e) {
    error.value = apiError(e)
  } finally {
    moving.value = false
  }
}
</script>

<template>
  <UModal v-model:open="open" :title="t('tools.gwImportTitle')" :ui="{ content: 'sm:max-w-2xl' }">
    <template #body>
      <div class="space-y-3">
        <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />
        <template v-if="results">
          <ul class="space-y-2 text-sm">
            <li v-for="r in results" :key="r.name" class="flex items-start gap-2">
              <UIcon :name="r.ok ? 'i-lucide-circle-check' : 'i-lucide-circle-x'" class="mt-0.5 size-4 shrink-0" :class="r.ok ? 'text-(--ui-success)' : 'text-(--ui-error)'" />
              <div class="min-w-0">
                <p><span class="font-mono font-medium">{{ r.name }}</span> <span class="text-(--ui-text-muted)">· {{ r.source }}</span>
                  <UBadge v-if="r.taken_out" class="ms-1" color="neutral" variant="subtle" size="sm" :label="t('tools.gwTakenOut')" />
                </p>
                <p v-if="r.error" class="text-xs text-(--ui-error)">{{ r.error }}</p>
                <p v-if="r.missing?.length" class="text-xs text-(--ui-warning)">{{ t('tools.gwMissingVars', { vars: r.missing.join(', ') }) }}</p>
              </div>
            </li>
          </ul>
        </template>
        <template v-else>
          <p v-if="loading" class="text-sm text-(--ui-text-muted)">{{ t('common.loading') }}</p>
          <p v-else-if="!items.length" class="text-sm text-(--ui-text-muted)">{{ t('tools.gwImportEmpty') }}</p>
          <div v-else class="divide-y divide-(--ui-border) rounded-lg border border-(--ui-border)">
            <div v-for="(c, i) in items" :key="i" class="flex items-center gap-3 px-3 py-2" :class="usable(c) ? '' : 'opacity-60'">
              <UCheckbox :model-value="!!picked[i]" :disabled="!usable(c)" @update:model-value="v => picked = { ...picked, [i]: !!v }" />
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm"><span class="font-mono font-medium">{{ c.name }}</span> <span class="text-xs text-(--ui-text-muted)">· {{ c.source }}</span></p>
                <p class="truncate font-mono text-xs text-(--ui-text-muted)">{{ c.target }}</p>
                <p v-if="c.problem === 'sse'" class="text-xs text-(--ui-warning)">{{ t('tools.gwImportSse') }}</p>
                <p v-else-if="c.moved_as" class="text-xs text-(--ui-text-muted)">{{ t('tools.gwImportMoved', { name: c.moved_as }) }}</p>
                <p v-else-if="!c.movable" class="text-xs text-(--ui-text-muted)">{{ t('tools.gwImportCopyOnly') }}</p>
              </div>
              <UInput v-if="picked[i]" v-model="names[i]" size="sm" class="w-40 font-mono" :color="c.taken && names[i] === c.suggested ? 'warning' : undefined" />
            </div>
          </div>
          <UCheckbox v-model="takeOut" :label="t('tools.gwTakeOut')">
            <template #description>
              <span class="text-xs text-(--ui-text-muted)">{{ t('tools.gwTakeOutInfo') }}</span>
            </template>
          </UCheckbox>
        </template>
      </div>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton color="neutral" variant="ghost" :label="results ? t('common.close') : t('common.cancel')" @click="open = false" />
        <UButton v-if="!results" icon="i-lucide-arrow-right-to-line" :disabled="!chosen.length" :loading="moving"
                 :label="t('tools.gwImportGo', { n: chosen.length })" @click="move" />
      </div>
    </template>
  </UModal>
</template>
