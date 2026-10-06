<script setup lang="ts">
// Finishes an MCP login whose callback went to localhost (useMcpLogin): the
// person pastes the address the browser landed on after signing in.
const { t } = useLang()
const toast = useToast()
const { pasteFor } = useMcpLogin()
// mounted only while a login waits (v-if in OfficeMcpServers): fresh state each time
const link = ref('')
const error = ref('')
const busy = ref(false)
const close = () => { pasteFor.value = null }

async function finish() {
  error.value = ''
  busy.value = true
  try {
    const r = await $fetch<{ server: { name: string } }>('/api/mcp/oauth/finish', { method: 'POST', body: { url: link.value } })
    close()
    toast.add({ title: t('tools.gwPasteDone', { name: r.server.name }), color: 'success' })
  } catch (e) {
    error.value = apiError(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UModal :open="true" :title="t('tools.gwPasteTitle')" @update:open="(v: boolean) => { if (!v) close() }">
    <template #body>
      <form id="mcp-paste" class="space-y-3" @submit.prevent="finish">
        <UFormField :label="t('tools.gwPasteLabel')">
          <template #hint>
            <UTooltip :text="t('tools.gwPasteInfo')"><UIcon name="i-lucide-info" class="text-(--ui-text-dimmed)" /></UTooltip>
          </template>
          <UInput v-model="link" placeholder="http://localhost:…/api/mcp/oauth/callback?code=…&state=…" class="w-full font-mono" required />
        </UFormField>
        <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />
      </form>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="close" />
        <UButton type="submit" form="mcp-paste" :label="t('tools.gwPasteSubmit')" :loading="busy" :disabled="!link.trim()" />
      </div>
    </template>
  </UModal>
</template>
