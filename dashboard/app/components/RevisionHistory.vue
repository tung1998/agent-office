<script setup lang="ts">
interface Revision {
  id: string
  action: string
  actor: string
  agent_count: number
  created_at: string
}
interface Snapshot {
  name: string
  kind: string
  agents: { key: string, name: string, tier: AgentTier, llm_model?: string, model_tier: ModelTier }[]
}

const props = defineProps<{ modelId: string }>()
const emit = defineEmits<{ restored: [] }>()
const open = defineModel<boolean>('open', { default: false })

const toast = useToast()
const { isAdmin } = useAuth()
const { t, dateLocale } = useLang()
const revisions = ref<Revision[]>([])
const loading = ref(false)
const expanded = ref<Record<string, Snapshot | null>>({})

async function load() {
  loading.value = true
  try {
    revisions.value = (await $fetch<{ revisions: Revision[] }>(`/api/org-models/${props.modelId}/revisions`)).revisions
  } finally {
    loading.value = false
  }
}
watch(open, (v) => { if (v) load() })

async function toggle(r: Revision) {
  if (expanded.value[r.id] !== undefined) {
    const { [r.id]: _, ...rest } = expanded.value
    expanded.value = rest
    return
  }
  expanded.value = { ...expanded.value, [r.id]: null }
  const res = await $fetch<{ snapshot: Snapshot }>(`/api/revisions/${r.id}`)
  expanded.value = { ...expanded.value, [r.id]: res.snapshot }
}

async function restore(r: Revision) {
  if (!confirm(t('rev.restoreConfirm'))) return
  try {
    await $fetch(`/api/revisions/${r.id}/restore`, { method: 'POST', body: {} })
    toast.add({ title: t('rev.restored'), color: 'success' })
    emit('restored')
    await load()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

// A revision is the state *before* the action it names.
function describe(action: string) {
  const [kind, arg] = action.split(':')
  const map: Record<string, string> = {
    'model.update': t('rev.action.modelUpdate'),
    'agent.update': t('rev.action.agentUpdate', { arg: arg ?? '' }),
    'agent.create': t('rev.action.agentCreate', { arg: arg ?? '' }),
    'agent.delete': t('rev.action.agentDelete', { arg: arg ?? '' }),
    'model.replace': t('rev.action.modelReplace'),
    'template.reset': t('rev.action.templateReset'),
    'restore': t('rev.action.restore'),
    'import': t('rev.action.import')
  }
  return t('rev.beforeAction', { action: map[kind!] ?? action })
}
const who = (a: string) => a.replace(/^human:/, '')
useLive(['org_revisions', 'org_models', 'agents'], () => { if (open.value) load() })
</script>

<template>
  <USlideover v-model:open="open" :title="t('rev.title')" :ui="{ content: 'max-w-lg' }">
    <template #body>
      <p class="mb-4 text-sm text-(--ui-text-muted)">
        {{ t('rev.intro') }}
      </p>
      <p v-if="loading" class="text-sm text-(--ui-text-muted)">{{ t('rev.loading') }}</p>
      <p v-else-if="!revisions.length" class="text-sm text-(--ui-text-muted)">{{ t('rev.empty') }}</p>
      <ol class="space-y-2">
        <li v-for="r in revisions" :key="r.id" class="rounded-lg border border-(--ui-border) p-3">
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <p class="text-sm font-medium">{{ describe(r.action) }}</p>
              <p class="text-xs text-(--ui-text-muted)">
                {{ new Date(r.created_at).toLocaleString(dateLocale) }} · {{ who(r.actor) }} · {{ t('rev.agentCount', { n: r.agent_count }) }}
              </p>
            </div>
            <div class="flex shrink-0 gap-1">
              <UButton size="xs" color="neutral" variant="ghost" :label="expanded[r.id] !== undefined ? t('rev.hide') : t('rev.view')" @click="toggle(r)" />
              <UButton v-if="isAdmin" size="xs" variant="soft" icon="i-lucide-rotate-ccw" :label="t('rev.restore')" @click="restore(r)" />
            </div>
          </div>
          <div v-if="expanded[r.id] !== undefined" class="mt-2 rounded bg-(--ui-bg-muted) p-2 text-xs">
            <p v-if="!expanded[r.id]">{{ t('rev.loading') }}</p>
            <template v-else>
              <p class="font-medium">{{ expanded[r.id]!.name }}</p>
              <ul class="mt-1 space-y-0.5">
                <li v-for="a in expanded[r.id]!.agents" :key="a.key">
                  <span class="text-(--ui-text-muted)">{{ tierLabel[a.tier] }}</span> · {{ a.name }}
                  <code class="text-(--ui-text-muted)">{{ a.key }}</code>
                </li>
              </ul>
            </template>
          </div>
        </li>
      </ol>
    </template>
  </USlideover>
</template>
