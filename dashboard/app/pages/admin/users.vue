<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'

definePageMeta({ admin: true })

const toast = useToast()
const { user: me } = useAuth()
const { t, dateLocale } = useLang()
const { data, refresh, status } = await useLiveFetch<{ users: OfficeUser[] }>('/api/users')
const users = computed(() => data.value?.users ?? [])

const columns = computed<TableColumn<OfficeUser>[]>(() => [
  { accessorKey: 'email', header: t('admin.usersEmail') },
  { accessorKey: 'name', header: t('admin.usersName') },
  { accessorKey: 'role', header: t('admin.usersRole') },
  { accessorKey: 'disabled', header: t('admin.usersStatus') },
  { accessorKey: 'last_login_at', header: t('admin.usersLastLogin') },
  { id: 'actions' }
])

function fmt(d: string | null) {
  return d ? new Date(d).toLocaleString(dateLocale.value) : '—'
}

// ---- create ----
const createOpen = ref(false)
const createState = reactive({ email: '', name: '', role: 'member', password: '' })
const createError = ref('')
const creating = ref(false)

async function createUser() {
  createError.value = ''
  creating.value = true
  try {
    await $fetch('/api/users', { method: 'POST', body: { ...createState } })
    toast.add({ title: t('admin.usersCreated', { email: createState.email }), color: 'success' })
    Object.assign(createState, { email: '', name: '', role: 'member', password: '' })
    createOpen.value = false
    await refresh()
  } catch (e) {
    createError.value = apiError(e)
  } finally {
    creating.value = false
  }
}

// ---- disable / enable ----
async function setDisabled(u: OfficeUser, disabled: boolean) {
  try {
    await $fetch(`/api/users/${u.id}`, { method: 'PATCH', body: { disabled } })
    toast.add({ title: disabled ? t('admin.usersDisabledToast', { email: u.email }) : t('admin.usersEnabledToast', { email: u.email }), color: 'success' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

// ---- reset password ----
const resetTarget = ref<OfficeUser | null>(null)
const resetPassword = ref('')
const resetError = ref('')
const resetting = ref(false)

async function doReset() {
  if (!resetTarget.value || resetting.value) return
  resetError.value = ''
  resetting.value = true
  try {
    await $fetch(`/api/users/${resetTarget.value.id}/reset-password`, { method: 'POST', body: { password: resetPassword.value } })
    toast.add({ title: t('admin.usersResetDone', { email: resetTarget.value.email }), color: 'success' })
    resetTarget.value = null
    resetPassword.value = ''
  } catch (e) {
    resetError.value = apiError(e)
  } finally {
    resetting.value = false
  }
}

function rowActions(u: OfficeUser) {
  const items = [[{ label: t('admin.usersResetPassword'), icon: 'i-lucide-key-round', onSelect: () => { resetTarget.value = u } }]]
  if (u.id !== me.value?.id) {
    items.push([u.disabled
      ? { label: t('admin.usersEnable'), icon: 'i-lucide-user-check', onSelect: () => setDisabled(u, false) }
      : { label: t('admin.usersDisable'), icon: 'i-lucide-user-x', color: 'error', onSelect: () => setDisabled(u, true) }] as never)
  }
  return items
}
</script>

<template>
  <PageShell :title="t('admin.usersTitle')">
    <template #actions>
      <UButton icon="i-lucide-user-plus" :label="t('admin.usersAdd')" @click="createOpen = true" />
    </template>

    <UTable :data="users" :columns="columns" :loading="status === 'pending'">
      <template #role-cell="{ row }">
        <UBadge :label="row.original.role" :color="row.original.role === 'admin' ? 'primary' : 'neutral'" variant="subtle" />
      </template>
      <template #disabled-cell="{ row }">
        <UBadge
          :label="row.original.disabled ? t('admin.usersDisabled') : t('admin.usersActive')"
          :color="row.original.disabled ? 'error' : 'success'"
          variant="subtle"
        />
      </template>
      <template #last_login_at-cell="{ row }">
        {{ fmt(row.original.last_login_at) }}
      </template>
      <template #actions-cell="{ row }">
        <div class="text-right">
          <UDropdownMenu :items="rowActions(row.original)">
            <UButton icon="i-lucide-ellipsis-vertical" color="neutral" variant="ghost" />
          </UDropdownMenu>
        </div>
      </template>
    </UTable>

    <UModal v-model:open="createOpen" :title="t('admin.usersAddModal')">
      <template #body>
        <form id="create-user" class="space-y-4" @submit.prevent="createUser">
          <UFormField :label="t('admin.usersEmail')" required>
            <UInput v-model="createState.email" type="email" class="w-full" />
          </UFormField>
          <UFormField :label="t('admin.usersDisplayName')">
            <UInput v-model="createState.name" class="w-full" />
          </UFormField>
          <UFormField :label="t('admin.usersRole')">
            <USelect v-model="createState.role" :items="[{ label: t('admin.usersRoleMember'), value: 'member' }, { label: t('admin.usersRoleAdmin'), value: 'admin' }]" class="w-full" />
          </UFormField>
          <UFormField :label="t('admin.usersTempPassword')" :hint="t('admin.usersPasswordHint')" required>
            <UInput v-model="createState.password" type="password" autocomplete="new-password" class="w-full" />
          </UFormField>
          <UAlert v-if="createError" color="error" variant="subtle" :description="createError" />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="createOpen = false" />
          <UButton type="submit" form="create-user" :loading="creating" :label="t('common.create')" />
        </div>
      </template>
    </UModal>

    <UModal :open="!!resetTarget" :title="t('admin.usersResetModal', { email: resetTarget?.email ?? '' })" @update:open="v => { if (!v) resetTarget = null }">
      <template #body>
        <form id="reset-pw" class="space-y-4" @submit.prevent="doReset">
          <p class="text-sm text-(--ui-text-muted)">{{ t('admin.usersResetWarn') }}</p>
          <UFormField :label="t('admin.usersNewPassword')" :hint="t('admin.usersPasswordHint')" required>
            <UInput v-model="resetPassword" type="password" autocomplete="new-password" class="w-full" />
          </UFormField>
          <UAlert v-if="resetError" color="error" variant="subtle" :description="resetError" />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="resetTarget = null" />
          <UButton type="submit" form="reset-pw" :label="t('admin.usersResetPassword')" :loading="resetting" :disabled="resetting" />
        </div>
      </template>
    </UModal>
  </PageShell>
</template>
