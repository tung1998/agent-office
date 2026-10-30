<script setup lang="ts">
// The office assistant's settings: its rights (ADR-059), and which AI it uses
// (the assistant is the one agent of its hidden project, saved as any agent).
const props = defineProps<{ projectId: string }>()
const { t } = useLang()
const toast = useToast()
const open = ref(false)

const { data: provData } = useLiveFetch<{ providers: Provider[] }>('/api/providers', { lazy: true })
const providers = computed(() => provData.value?.providers ?? [])
const defaultProvider = computed(() => providers.value.find(p => p.is_default))
const agent = ref<Agent | null>(null)
type Mode = 'answer' | 'manage' | 'admin'
const mode = ref<Mode>('manage')
const modeItems = computed(() => (['answer', 'manage', 'admin'] as const).map(v => ({ value: v, label: t(`assistant.mode.${v}`), description: t(`assistant.modeDesc.${v}`) })))
const form = reactive<{ provider_id: string, fallback_provider_ids: string[], model_tier: 'strong' | 'balanced' | 'fast', llm_model: string }>({ provider_id: '', fallback_provider_ids: [], model_tier: 'balanced', llm_model: '' })
watch(open, async (o) => {
  if (!o) return
  try {
    mode.value = (await $fetch<{ mode: Mode }>('/api/assistant')).mode ?? 'manage'
    const list = (await $fetch<{ agents: Agent[] }>(`/api/projects/${props.projectId}/chat/agents`)).agents
    const a = list[0]
    if (!a) return
    agent.value = (await $fetch<{ agent: Agent }>(`/api/agents/${a.id}`)).agent
    Object.assign(form, { provider_id: agent.value.provider_id ?? '', fallback_provider_ids: [...(agent.value.fallback_provider_ids ?? [])], model_tier: (agent.value.model_tier || 'balanced') as 'strong' | 'balanced' | 'fast', llm_model: agent.value.llm_model ?? '' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
})
const chosen = computed(() => providers.value.find(p => p.id === form.provider_id) ?? defaultProvider.value)
const tierItems = computed(() => (['strong', 'balanced', 'fast'] as const).map(v => ({
  label: `${t(`tier.model.${v}`)}${chosen.value?.tier_models?.[v] ? ` · ${chosen.value.tier_models[v]}` : ''}`, value: v
})))

const saving = ref(false)
async function save() {
  const a = agent.value
  if (!a) return
  saving.value = true
  try {
    await $fetch('/api/assistant/mode', { method: 'PUT', body: { mode: mode.value } })
    await $fetch(`/api/agents/${a.id}`, {
      method: 'PATCH',
      body: {
        key: a.key, name: a.name, role: a.role, description: a.description,
        provider_id: form.provider_id, fallback_provider_ids: form.fallback_provider_ids.filter(id => id !== chosen.value?.id), model_tier: form.model_tier, llm_model: form.llm_model.trim(), instructions: a.instructions, permissions: a.permissions
      }
    })
    toast.add({ title: t('assistant.settingsSaved'), color: 'success' })
    open.value = false
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UButton icon="i-lucide-settings-2" color="neutral" variant="ghost" :aria-label="t('assistant.settings')" :label="t('assistant.settings')" :ui="{ label: 'max-sm:hidden' }" @click="open = true" />
  <UModal v-model:open="open" :title="t('assistant.settings')" :description="t('assistant.settingsHint')">
    <template #body>
      <LoadingRows v-if="!agent" :n="2" :icon="false" />
      <div v-else class="space-y-4">
        <UFormField :label="t('assistant.rights')">
          <URadioGroup v-model="mode" :items="modeItems" variant="card" class="w-full" />
        </UFormField>
        <UAlert
          v-if="mode === 'admin'" color="error" variant="subtle" icon="i-lucide-shield-alert"
          :title="t('assistant.adminWarnTitle')" :description="t('assistant.adminWarn')"
        />
        <UFormField :label="t('org.form.modelTier')">
          <USelect v-model="form.model_tier" :items="tierItems" class="w-full" />
        </UFormField>
        <ConnectionChain v-model:provider-id="form.provider_id" v-model:llm-model="form.llm_model" v-model:fallbacks="form.fallback_provider_ids" :providers="providers" :tier="form.model_tier" />

      </div>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="open = false" />
        <UButton icon="i-lucide-save" :label="t('auto.save')" :loading="saving" :disabled="!agent" @click="save" />
      </div>
    </template>
  </UModal>
</template>
