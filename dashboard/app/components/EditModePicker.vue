<script setup lang="ts">
// Where a chat or a task changes code: its own git worktree (you merge the
// diff) or right in the project folder like the CLI (admins only).
type EditMode = 'worktree' | 'direct'
const mode = defineModel<EditMode>({ default: 'worktree' })
const { isAdmin } = useAuth()
const { t } = useLang()

const options = computed(() => [
  { value: 'worktree' as const, icon: 'i-lucide-git-branch', label: t('policy.editWorktree'), description: t('policy.editWorktreeDesc') },
  { value: 'direct' as const, icon: 'i-lucide-pencil', label: t('policy.editDirect'), description: isAdmin.value ? t('policy.editDirectDesc') : t('mode.adminOnly') }
])
const current = computed(() => options.value.find(o => o.value === mode.value) ?? options.value[0]!)
const items = computed(() => [options.value.map(o => ({
  label: o.label, description: o.description, icon: o.icon, active: mode.value === o.value,
  disabled: o.value === 'direct' && !isAdmin.value,
  onSelect: () => { mode.value = o.value }
}))])
</script>

<template>
  <UDropdownMenu :items="items" :content="{ align: 'end', side: 'top' }" :ui="{ content: 'w-80' }" class="min-w-0">
    <!-- the label is cut (…) when the row is narrow; on a phone only the icon -->
    <UButton
      size="sm" :color="mode === 'direct' ? 'warning' : 'neutral'" :variant="mode === 'direct' ? 'soft' : 'ghost'" class="min-w-0 max-w-full"
      :icon="current.icon" :title="`${current.label}: ${current.description}`" :aria-label="current.label"
      :ui="{ label: 'hidden truncate sm:inline', trailingIcon: 'hidden sm:inline-flex' }" :label="current.label" trailing-icon="i-lucide-chevron-up"
    />
  </UDropdownMenu>
</template>
