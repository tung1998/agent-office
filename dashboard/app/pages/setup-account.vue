<script setup lang="ts">
// The default admin (admin / admin) of a first run sets its real email and
// password here before anything else (the auth middleware keeps it on this page).
const { user, logout } = useAuth()
const { t } = useLang()

const MIN_PASSWORD = 10 // auth.MinPasswordLen
const state = reactive({ name: user.value?.name ?? '', email: '', password: '', confirm: '' })
const loading = ref(false)
const error = ref('')

async function onSubmit() {
  error.value = ''
  if (state.password !== state.confirm) {
    error.value = t('firstRun.mismatch')
    return
  }
  loading.value = true
  try {
    const res = await $fetch<{ user: OfficeUser }>('/api/auth/setup', {
      method: 'POST',
      body: { name: state.name, email: state.email, password: state.password }
    })
    user.value = res.user
    await navigateTo('/')
  } catch (e) {
    error.value = apiError(e)
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="flex min-h-screen items-center justify-center bg-(--ui-bg-muted) px-4">
    <UCard class="w-full max-w-sm">
      <template #header>
        <div class="flex items-center gap-2">
          <UIcon name="i-lucide-shield-check" class="size-6 text-primary" />
          <div>
            <h1 class="text-lg font-semibold">{{ t('firstRun.title') }}</h1>
            <p class="text-sm text-(--ui-text-muted)">{{ t('firstRun.desc') }}</p>
          </div>
          <PrefSwitcher class="ms-auto" />
        </div>
      </template>

      <form class="space-y-4" @submit.prevent="onSubmit">
        <UFormField :label="t('firstRun.name')" name="name">
          <UInput v-model="state.name" autocomplete="name" class="w-full" />
        </UFormField>
        <UFormField :label="t('firstRun.email')" name="email" required>
          <UInput v-model="state.email" type="email" autocomplete="username" placeholder="you@company.com" class="w-full" autofocus />
        </UFormField>
        <UFormField :label="t('firstRun.password')" name="password" :hint="t('firstRun.passwordHint', { n: MIN_PASSWORD })" required>
          <UInput v-model="state.password" type="password" autocomplete="new-password" :minlength="MIN_PASSWORD" class="w-full" />
        </UFormField>
        <UFormField :label="t('firstRun.confirm')" name="confirm" required>
          <UInput v-model="state.confirm" type="password" autocomplete="new-password" class="w-full" />
        </UFormField>

        <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />

        <UButton type="submit" block :loading="loading" :label="t('firstRun.submit')" />
        <UButton block color="neutral" variant="ghost" :label="t('firstRun.logout')" @click="logout" />
      </form>
    </UCard>
  </div>
</template>
