<script setup lang="ts">
// The quick on/off switch of an agent (admin): off = paused, out of chat,
// anyone calling it gets a notice instead of an answer.
const props = defineProps<{ agent: Pick<Agent, 'id' | 'name' | 'enabled'> }>()
const emit = defineEmits<{ changed: [enabled: boolean] }>()
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()
const busy = ref(false)
async function toggle(on: boolean) {
  busy.value = true
  try {
    await $fetch(`/api/agents/${props.agent.id}/enabled`, { method: 'PATCH', body: { enabled: on } })
    emit('changed', on)
    toast.add({ title: on ? t('org.editor.resumedToast', { name: props.agent.name }) : t('org.editor.pausedToast', { name: props.agent.name }), color: on ? 'success' : 'neutral' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <USwitch
    :model-value="agent.enabled !== false" :disabled="!isAdmin" :loading="busy" size="sm"
    :aria-label="t('org.editor.pauseHint')" :title="t('org.editor.pauseHint')" @update:model-value="toggle" @click.stop
  />
</template>
