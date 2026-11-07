<script setup lang="ts">
// 👍/👎 on an agent's answer (ADR-267): optional, it outweighs office's guess
// of whether the person had to correct it. Clicking the same again clears it.
const props = defineProps<{ conversationId: string, messageId: string, value?: number, frozen?: boolean }>()
const { t } = useLang()
const toast = useToast()
const current = ref(props.value ?? 0)
watch(() => props.value, v => { current.value = v ?? 0 })
const busy = ref(false)
async function rate(v: number) {
  const next = current.value === v ? 0 : v
  busy.value = true
  try {
    await $fetch(`/api/conversations/${props.conversationId}/messages/${props.messageId}/feedback`, { method: 'PUT', body: { value: next } })
    current.value = next
  } catch (e) {
    const status = (e as { statusCode?: number }).statusCode
    toast.add({ color: status === 404 ? 'neutral' : 'error', title: status === 409 ? t('scoring.frozen') : status === 404 ? t('scoring.notScored') : apiError(e) })
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="flex items-center gap-0.5" :title="frozen ? t('scoring.frozen') : undefined">
    <UButton
      size="xs" variant="ghost" :color="current === 1 ? 'success' : 'neutral'" icon="i-lucide-thumbs-up" :disabled="busy || frozen"
      :aria-label="t('scoring.good')" :title="t('scoring.good')" :aria-pressed="current === 1" @click="rate(1)"
    />
    <UButton
      size="xs" variant="ghost" :color="current === -1 ? 'error' : 'neutral'" icon="i-lucide-thumbs-down" :disabled="busy || frozen"
      :aria-label="t('scoring.bad')" :title="t('scoring.bad')" :aria-pressed="current === -1" @click="rate(-1)"
    />
  </div>
</template>
