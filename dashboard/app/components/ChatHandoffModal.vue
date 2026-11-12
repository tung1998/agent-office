<script setup lang="ts">
// Hand a chat over to a teammate (ADR-277): it becomes theirs, a note says
// why, and its agent goes on with the same context, worktree and diffs.
const props = defineProps<{ conversation: { id: string, title: string } | null }>()
const emit = defineEmits<{ close: [], done: [owner: string] }>()
const toast = useToast()
const { t } = useLang()
const people = ref<{ email: string, name: string }[]>([])
const to = ref('')
const note = ref('')
const loading = ref(false)
const saving = ref(false)
watch(() => props.conversation?.id, async (id) => {
  people.value = []
  to.value = ''
  note.value = ''
  if (!id) return
  loading.value = true
  try {
    const r = await $fetch<{ people: { email: string, name: string }[] }>(`/api/conversations/${id}/handoff`)
    people.value = r.people
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
    emit('close')
  } finally {
    loading.value = false
  }
}, { immediate: true })
const items = computed(() => people.value.map(p => ({ label: p.name ? `${p.name} · ${p.email}` : p.email, value: p.email })))
async function submit() {
  if (!props.conversation || !to.value) return
  saving.value = true
  try {
    const r = await $fetch<{ owner: string }>(`/api/conversations/${props.conversation.id}/handoff`, { method: 'POST', body: { to: to.value, note: note.value } })
    toast.add({ title: t('handoff.done', { who: r.owner }), color: 'success' })
    emit('done', r.owner)
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UModal :open="!!conversation" :title="t('handoff.title')" :description="conversation?.title || ''" @update:open="(o) => { if (!o) emit('close') }">
    <template #body>
      <div class="space-y-3">
        <p v-if="!loading && !people.length" class="text-sm text-(--ui-text-muted)">{{ t('handoff.nobody') }}</p>
        <template v-else>
          <USelect v-model="to" :items="items" :loading="loading" :placeholder="t('handoff.to')" class="w-full" />
          <UTextarea v-model="note" :placeholder="t('handoff.note')" :rows="3" autoresize class="w-full" />
        </template>
      </div>
    </template>
    <template #footer>
      <UButton :label="t('handoff.submit')" icon="i-lucide-user-round-check" :loading="saving" :disabled="!to" @click="submit" />
    </template>
  </UModal>
</template>
