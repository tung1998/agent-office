<script setup lang="ts">
const route = useRoute()
const { login } = useAuth()
const { t } = useLang()

const state = reactive({ email: '', password: '' })
const loading = ref(false)
const error = ref('')

const { data: status } = await useLiveFetch<{ has_users: boolean, default_admin?: boolean }>('/api/auth/status')
// the default account of a first run (auth.DefaultAdminEmail / DefaultAdminPassword)
const DEFAULT = { user: 'admin', pass: 'admin' }

async function onSubmit() {
  error.value = ''
  loading.value = true
  try {
    await login(state.email, state.password)
    const r = route.query.redirect
    // the default admin goes to set its email and password (the middleware sees to it)
    await navigateTo(typeof r === 'string' && r.startsWith('/') && !r.startsWith('//') ? r : '/')
  } catch (e) {
    error.value = apiError(e, t('login.failed'))
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
          <UIcon name="i-lucide-building-2" class="size-6 text-primary" />
          <div>
            <h1 class="text-lg font-semibold">Agent Office</h1>
            <p class="text-sm text-(--ui-text-muted)">{{ t('login.subtitle') }}</p>
          </div>
          <PrefSwitcher class="ms-auto" />
        </div>
      </template>

      <UAlert
        v-if="status && !status.has_users"
        class="mb-4"
        color="warning"
        variant="subtle"
        icon="i-lucide-info"
        :title="t('login.noUsersTitle')"
        :description="t('login.noUsersDesc', { cmd: 'office user create --email you@company.com --role admin' })"
      />

      <UAlert
        v-else-if="status?.default_admin"
        class="mb-4"
        color="info"
        variant="subtle"
        icon="i-lucide-key-round"
        :title="t('login.defaultTitle')"
        :description="t('login.defaultDesc', DEFAULT)"
      />

      <form class="space-y-4" @submit.prevent="onSubmit">
        <UFormField :label="t('login.email')" name="email" required>
          <!-- text, not email: the default account is just "admin" -->
          <UInput v-model="state.email" type="text" inputmode="email" autocomplete="username" placeholder="you@company.com" class="w-full" autofocus />
        </UFormField>
        <UFormField :label="t('login.password')" name="password" required>
          <UInput v-model="state.password" type="password" autocomplete="current-password" class="w-full" />
        </UFormField>

        <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />

        <UButton type="submit" block :loading="loading" :label="t('login.submit')" />
      </form>
    </UCard>
  </div>
</template>
