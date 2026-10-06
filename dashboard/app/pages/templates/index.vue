<script setup lang="ts">
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()
const { data, refresh } = await useLiveFetch<{ templates: OrgModel[] }>('/api/templates')
const templates = computed(() => data.value?.templates ?? [])

const cloneOpen = ref(false)
const cloneSource = ref<OrgModel | null>(null)
const cloneForm = reactive({ key: '', name: '' })
const cloneError = ref('')

function openClone(tpl: OrgModel) {
  cloneSource.value = tpl
  cloneForm.key = `${tpl.key}-copy`
  cloneForm.name = `${tpl.name}${t('tpl.cloneSuffix')}`
  cloneError.value = ''
  cloneOpen.value = true
}

async function doClone() {
  try {
    const res = await $fetch<{ model: OrgModel }>('/api/templates', {
      method: 'POST', body: { source_id: cloneSource.value!.id, key: cloneForm.key, name: cloneForm.name }
    })
    cloneOpen.value = false
    await navigateTo(`/templates/${res.model.id}`)
  } catch (e) {
    const d = (e as { data?: { error?: string, problems?: string[] } }).data
    cloneError.value = d?.problems?.join('; ') ?? d?.error ?? t('org.editor.genericError')
  }
}

async function reset(tpl: OrgModel) {
  if (!confirm(t('tpl.resetConfirm', { name: tpl.name }))) return
  try {
    await $fetch(`/api/templates/${tpl.key}/reset`, { method: 'POST' })
    await refresh()
    toast.add({ title: t('tpl.resetDone', { name: tpl.name }), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

async function remove(tpl: OrgModel) {
  if (!confirm(t('tpl.deleteConfirm', { name: tpl.name }))) return
  try {
    await $fetch(`/api/org-models/${tpl.id}`, { method: 'DELETE' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

function menu(tpl: OrgModel) {
  const items: { label: string, icon: string, color?: 'error', onSelect: () => void }[] = [
    { label: t('tpl.clone'), icon: 'i-lucide-copy', onSelect: () => openClone(tpl) },
    { label: t('tpl.downloadJson'), icon: 'i-lucide-download', onSelect: () => window.open(`/api/org-models/${tpl.id}/export`, '_blank') }
  ]
  if (tpl.builtin) items.push({ label: t('tpl.resetDefault'), icon: 'i-lucide-rotate-ccw', onSelect: () => reset(tpl) })
  else items.push({ label: t('tpl.delete'), icon: 'i-lucide-trash', color: 'error', onSelect: () => remove(tpl) })
  return [items]
}
</script>

<template>
  <PageShell :title="t('tpl.pageTitle')">
    <template v-if="isAdmin" #actions>
      <UButton icon="i-lucide-plus" :label="t('tplNew.button')" to="/templates/new" />
    </template>
    <div class="space-y-4">
      <p class="max-w-3xl text-sm text-(--ui-text-muted)">
        {{ t('tpl.pageIntro', { init: 'init' }) }}
      </p>
      <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        <UCard v-for="tpl in templates" :key="tpl.id" class="flex flex-col">
          <div class="flex items-start justify-between gap-2">
            <NuxtLink :to="`/templates/${tpl.id}`" class="flex min-w-0 items-center gap-2 hover:text-primary">
              <UIcon :name="kindIcon[tpl.kind]" class="size-5 shrink-0 text-primary" />
              <span class="truncate font-semibold">{{ tpl.name }}</span>
            </NuxtLink>
            <div class="flex items-center gap-1">
              <UBadge v-if="tpl.builtin" :label="t('org.editor.builtin')" color="neutral" variant="outline" size="sm" />
              <UDropdownMenu v-if="isAdmin" :items="menu(tpl)">
                <UButton icon="i-lucide-ellipsis-vertical" color="neutral" variant="ghost" size="sm" />
              </UDropdownMenu>
            </div>
          </div>
          <p class="mt-2 line-clamp-3 text-sm text-(--ui-text-muted)">{{ tpl.description }}</p>
          <div class="mt-3 flex flex-wrap gap-1.5">
            <UBadge :label="kindLabel[tpl.kind]" variant="subtle" size="sm" />
            <UBadge v-for="(n, tier) in tpl.tiers" :key="tier" :label="`${n} ${tierLabel[tier as AgentTier]}`" color="neutral" variant="soft" size="sm" />
          </div>
          <div class="mt-4">
            <UButton :to="`/templates/${tpl.id}`" :label="t('tpl.viewEdit')" variant="soft" size="sm" trailing-icon="i-lucide-arrow-right" />
          </div>
        </UCard>
      </div>
    </div>

    <UModal v-model:open="cloneOpen" :title="t('tpl.cloneTitle', { name: cloneSource?.name ?? '' })">
      <template #body>
        <form id="clone-form" class="space-y-4" @submit.prevent="doClone">
          <UFormField :label="t('tpl.cloneName')" required>
            <UInput v-model="cloneForm.name" class="w-full" />
          </UFormField>
          <UFormField :label="t('tpl.cloneKey')" required :help="t('tpl.cloneKeyHelp')">
            <UInput v-model="cloneForm.key" class="w-full font-mono" />
          </UFormField>
          <UAlert v-if="cloneError" color="error" variant="subtle" :description="cloneError" />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('org.form.cancel')" @click="cloneOpen = false" />
          <UButton type="submit" form="clone-form" :label="t('tpl.cloneSubmit')" />
        </div>
      </template>
    </UModal>
  </PageShell>
</template>
