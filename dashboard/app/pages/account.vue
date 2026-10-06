<script setup lang="ts">
const toast = useToast()
const { t } = useLang()
const state = reactive({ old_password: '', new_password: '', confirm: '' })
const loading = ref(false)
const error = ref('')

async function onSubmit() {
  error.value = ''
  if (state.new_password !== state.confirm) {
    error.value = t('account.mismatch')
    return
  }
  loading.value = true
  try {
    await $fetch('/api/auth/password', {
      method: 'POST',
      body: { old_password: state.old_password, new_password: state.new_password }
    })
    Object.assign(state, { old_password: '', new_password: '', confirm: '' })
    toast.add({ title: t('account.saved'), description: t('account.savedDesc'), color: 'success' })
  } catch (e) {
    error.value = apiError(e)
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <PageShell :title="t('account.title')">
    <UCard class="max-w-md">
      <form class="space-y-4" @submit.prevent="onSubmit">
        <UFormField :label="t('account.current')" required>
          <UInput v-model="state.old_password" type="password" autocomplete="current-password" class="w-full" />
        </UFormField>
        <UFormField :label="t('account.new')" :hint="t('account.newHint')" required>
          <UInput v-model="state.new_password" type="password" autocomplete="new-password" class="w-full" />
        </UFormField>
        <UFormField :label="t('account.confirm')" required>
          <UInput v-model="state.confirm" type="password" autocomplete="new-password" class="w-full" />
        </UFormField>
        <UAlert v-if="error" color="error" variant="subtle" :description="error" />
        <UButton type="submit" :loading="loading" :label="t('common.save')" />
      </form>
    </UCard>
    <CliTokens class="mt-4" />
  </PageShell>
</template>
