<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'

definePageMeta({ admin: true, manager: true }) // a manager runs the members (ADR-239)

const toast = useToast()
const { user: me, isOfficeAdmin } = useAuth()
const { t, when: fmtWhen } = useLang()
// offices: the other offices (whole, or projects of them) this account may give (ADR-239)
interface OfficeChoice { id: string, name: string, whole: boolean, projects: { id: string, name: string }[] }
const { data, refresh, status } = await useLiveFetch<{ users: OfficeUser[], offices: OfficeChoice[] }>('/api/users')
const users = computed(() => data.value?.users ?? [])
// the projects an account other than an admin sees (ADR-224)
const { data: projData } = useLiveFetch<{ projects: Project[] }>('/api/projects', { lazy: true })
const projectItems = computed(() => (projData.value?.projects ?? []).map(p => ({ label: p.name, value: p.id })))
const projectName = (id: string) => projData.value?.projects.find(p => p.id === id)?.name ?? id
// a manager makes members only
const roleItems = computed(() => [
  { label: t('admin.usersRoleMember'), value: 'member' },
  ...(isOfficeAdmin.value
    ? [{ label: t('admin.usersRoleManager'), value: 'manager' }, { label: t('admin.usersRoleAdmin'), value: 'admin' }]
    : [])
])
// one list for the offices: "<peer>/*" an office whole, "<peer>/<project>" one of its projects
const officeItems = computed(() => (data.value?.offices ?? []).flatMap(o => [
  ...(o.whole ? [{ label: t('admin.usersOfficeWhole', { office: o.name }), value: `${o.id}/*` }] : []),
  ...o.projects.map(p => ({ label: `${o.name}/${p.name}`, value: `${o.id}/${p.id}` }))
]))
const toOfficeList = (o?: Record<string, string[]>) => Object.entries(o ?? {}).flatMap(([peer, list]) => list.map(p => `${peer}/${p}`))
function toOffices(list: string[]) {
  const out: Record<string, string[]> = {}
  for (const v of list) {
    const i = v.indexOf('/')
    ;(out[v.slice(0, i)] ??= []).push(v.slice(i + 1))
  }
  return out
}
const officeLabel = (peer: string, p: string) => officeItems.value.find(x => x.value === `${peer}/${p}`)?.label ?? `${peer}/${p}`
const roleColor = (r: string) => r === 'admin' ? 'primary' : r === 'manager' ? 'info' : 'neutral'

const columns = computed<TableColumn<OfficeUser>[]>(() => [
  { accessorKey: 'email', header: t('admin.usersEmail') },
  { accessorKey: 'name', header: t('admin.usersName') },
  { accessorKey: 'role', header: t('admin.usersRole') },
  { id: 'chat_ids', header: t('admin.usersChatIds') },
  { accessorKey: 'disabled', header: t('admin.usersStatus') },
  { accessorKey: 'last_login_at', header: t('admin.usersLastLogin') },
  { id: 'actions' }
])

function fmt(d: string | null) {
  return d ? fmtWhen(d) : '—'
}

// ---- create ----
const createOpen = ref(false)
const createState = reactive({ email: '', name: '', role: 'member', password: '', projects: [] as string[], offices: [] as string[] })
const createError = ref('')
const creating = ref(false)

async function createUser() {
  createError.value = ''
  creating.value = true
  try {
    await $fetch('/api/users', { method: 'POST', body: { ...createState, offices: toOffices(createState.offices) } })
    toast.add({ title: t('admin.usersCreated', { email: createState.email }), color: 'success' })
    Object.assign(createState, { email: '', name: '', role: 'member', password: '', projects: [], offices: [] })
    createOpen.value = false
    await refresh()
  } catch (e) {
    createError.value = apiError(e)
  } finally {
    creating.value = false
  }
}

// ---- disable / enable ----
const togglingId = ref<string | null>(null)

async function setDisabled(u: OfficeUser, disabled: boolean) {
  if (togglingId.value) return
  togglingId.value = u.id
  try {
    await $fetch(`/api/users/${u.id}`, { method: 'PATCH', body: { disabled } })
    toast.add({ title: disabled ? t('admin.usersDisabledToast', { email: u.email }) : t('admin.usersEnabledToast', { email: u.email }), color: 'success' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    togglingId.value = null
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

// ---- Discord / Telegram ids ----
const linkTarget = ref<OfficeUser | null>(null)
const linkState = reactive({ discord_id: '', telegram_id: '' })
const linkError = ref('')
const linking = ref(false)

function openLink(u: OfficeUser) {
  Object.assign(linkState, { discord_id: u.discord_id ?? '', telegram_id: u.telegram_id ?? '' })
  linkError.value = ''
  linkTarget.value = u
}

async function saveLink() {
  if (!linkTarget.value || linking.value) return
  linkError.value = ''
  linking.value = true
  try {
    await $fetch(`/api/users/${linkTarget.value.id}`, { method: 'PATCH', body: { ...linkState } })
    toast.add({ title: t('admin.usersChatIdsSaved', { email: linkTarget.value.email }), color: 'success' })
    linkTarget.value = null
    await refresh()
  } catch (e) {
    linkError.value = apiError(e)
  } finally {
    linking.value = false
  }
}

// ---- role and projects ----
const accessTarget = ref<OfficeUser | null>(null)
const accessState = reactive({ role: 'member' as OfficeUser['role'], projects: [] as string[], offices: [] as string[] })
const accessError = ref('')
const savingAccess = ref(false)

function openAccess(u: OfficeUser) {
  Object.assign(accessState, { role: u.role, projects: [...(u.projects ?? [])], offices: toOfficeList(u.offices) })
  accessError.value = ''
  accessTarget.value = u
}

async function saveAccess() {
  const u = accessTarget.value
  if (!u || savingAccess.value) return
  accessError.value = ''
  savingAccess.value = true
  try {
    const admin = accessState.role === 'admin'
    const body: { role?: string, projects?: string[], offices?: Record<string, string[]> } = { projects: admin ? [] : accessState.projects, offices: admin ? {} : toOffices(accessState.offices) }
    if (accessState.role !== u.role) body.role = accessState.role
    await $fetch(`/api/users/${u.id}`, { method: 'PATCH', body })
    toast.add({ title: t('admin.usersAccessSaved', { email: u.email }), color: 'success' })
    accessTarget.value = null
    await refresh()
  } catch (e) {
    accessError.value = apiError(e)
  } finally {
    savingAccess.value = false
  }
}

const { viewAs } = useAuth()
async function startViewAs(u: OfficeUser) {
  try {
    await viewAs(u.id)
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

function rowActions(u: OfficeUser) {
  const busy = togglingId.value === u.id
  const items = [[
    ...(u.id !== me.value?.id ? [{ label: t('admin.usersAccessEdit'), icon: 'i-lucide-shield', onSelect: () => openAccess(u) }] : []),
    { label: t('admin.usersResetPassword'), icon: 'i-lucide-key-round', onSelect: () => { resetTarget.value = u } },
    { label: t('admin.usersChatIdsEdit'), icon: 'i-lucide-link', onSelect: () => openLink(u) },
    // read-only, as that account sees the office: not another admin
    ...(isOfficeAdmin.value && u.role !== 'admin' && !u.disabled && !u.must_change ? [{ label: t('admin.usersViewAs'), icon: 'i-lucide-eye', onSelect: () => startViewAs(u) }] : [])
  ]]
  if (u.id !== me.value?.id) {
    items.push([u.disabled
      ? { label: t('admin.usersEnable'), icon: 'i-lucide-user-check', disabled: busy, onSelect: () => setDisabled(u, false) }
      : { label: t('admin.usersDisable'), icon: 'i-lucide-user-x', color: 'error', disabled: busy, onSelect: () => setDisabled(u, true) }] as never)
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
        <div class="flex flex-col items-start gap-1">
          <UBadge :label="row.original.role" :color="roleColor(row.original.role)" variant="subtle" />
          <span class="text-xs text-(--ui-text-muted)">
            {{ row.original.role === 'admin' ? t('admin.usersAllProjects') : row.original.projects?.length ? row.original.projects.map(projectName).join(', ') : t('admin.usersNoProject') }}
          </span>
          <span v-if="row.original.role !== 'admin' && toOfficeList(row.original.offices).length" class="flex items-center gap-1 text-xs text-(--ui-text-muted)">
            <UIcon name="i-lucide-building-2" class="size-3.5 shrink-0" />
            {{ Object.entries(row.original.offices ?? {}).flatMap(([peer, list]) => list.map(p => officeLabel(peer, p))).join(', ') }}
          </span>
        </div>
      </template>
      <template #disabled-cell="{ row }">
        <UBadge
          :label="row.original.disabled ? t('admin.usersDisabled') : t('admin.usersActive')"
          :color="row.original.disabled ? 'error' : 'success'"
          variant="subtle"
        />
      </template>
      <template #chat_ids-cell="{ row }">
        <div class="flex flex-col gap-0.5 text-xs">
          <span v-if="row.original.discord_id" class="flex items-center gap-1"><UIcon :name="sourceIcon.discord" class="size-3.5 shrink-0 text-(--ui-text-muted)" /><span class="font-mono">{{ row.original.discord_id }}</span></span>
          <span v-if="row.original.telegram_id" class="flex items-center gap-1"><UIcon :name="sourceIcon.telegram" class="size-3.5 shrink-0 text-(--ui-text-muted)" /><span class="font-mono">{{ row.original.telegram_id }}</span></span>
          <span v-if="!row.original.discord_id && !row.original.telegram_id" class="text-(--ui-text-dimmed)">—</span>
        </div>
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
          <UFormField v-if="roleItems.length > 1" :label="t('admin.usersRole')">
            <USelect v-model="createState.role" :items="roleItems" class="w-full" />
          </UFormField>
          <UFormField v-if="createState.role !== 'admin'" :label="t('admin.usersProjects')" :hint="createState.role === 'manager' ? t('admin.usersProjectsHint') : undefined">
            <USelectMenu v-model="createState.projects" :items="projectItems" value-key="value" multiple class="w-full" />
          </UFormField>
          <UFormField v-if="createState.role !== 'admin' && officeItems.length" :label="t('admin.usersOffices')" :hint="t('admin.usersOfficesHint')">
            <USelectMenu v-model="createState.offices" :items="officeItems" value-key="value" multiple class="w-full" />
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

    <UModal :open="!!accessTarget" :title="t('admin.usersAccessModal', { email: accessTarget?.email ?? '' })" @update:open="v => { if (!v) accessTarget = null }">
      <template #body>
        <form id="user-access" class="space-y-4" @submit.prevent="saveAccess">
          <UFormField v-if="isOfficeAdmin" :label="t('admin.usersRole')" :hint="accessTarget && accessState.role !== accessTarget.role ? t('admin.usersRoleChangeWarn') : undefined">
            <USelect v-model="accessState.role" :items="roleItems" class="w-full" />
          </UFormField>
          <UFormField v-if="accessState.role !== 'admin'" :label="t('admin.usersProjects')">
            <USelectMenu v-model="accessState.projects" :items="projectItems" value-key="value" multiple class="w-full" />
          </UFormField>
          <UFormField v-if="accessState.role !== 'admin' && officeItems.length" :label="t('admin.usersOffices')" :hint="t('admin.usersOfficesHint')">
            <USelectMenu v-model="accessState.offices" :items="officeItems" value-key="value" multiple class="w-full" />
          </UFormField>
          <UAlert v-if="accessError" color="error" variant="subtle" :description="accessError" />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="accessTarget = null" />
          <UButton type="submit" form="user-access" :label="t('common.save')" :loading="savingAccess" :disabled="savingAccess" />
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

    <UModal :open="!!linkTarget" :title="t('admin.usersChatIdsModal', { email: linkTarget?.email ?? '' })" @update:open="v => { if (!v) linkTarget = null }">
      <template #body>
        <form id="link-ids" class="space-y-4" @submit.prevent="saveLink">
          <UFormField :label="t('source.discord')" :hint="t('admin.usersDiscordHint')">
            <UInput v-model="linkState.discord_id" inputmode="numeric" placeholder="123456789012345678" class="w-full font-mono" />
          </UFormField>
          <UFormField :label="t('source.telegram')" :hint="t('admin.usersTelegramHint')">
            <UInput v-model="linkState.telegram_id" inputmode="numeric" placeholder="1725044104" class="w-full font-mono" />
          </UFormField>
          <UAlert v-if="linkError" color="error" variant="subtle" :description="linkError" />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="linkTarget = null" />
          <UButton type="submit" form="link-ids" :label="t('common.save')" :loading="linking" :disabled="linking" />
        </div>
      </template>
    </UModal>
  </PageShell>
</template>
