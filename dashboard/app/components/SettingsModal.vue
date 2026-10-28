<script setup lang="ts">
// The account's own settings (from the user menu): for now the sidebar's
// projects (useProjectMenu), kept with the account.
const open = defineModel<boolean>('open', { default: false })
const { t } = useLang()
const toast = useToast()
const { max, maxLimits, sort, single, setOptions } = useProjectMenu()
const sortItems = computed(() => (['manual', 'usage', 'name'] as const).map(v => ({ label: t(`projects.menuSort.${v}`), value: v })))
async function save(o: Parameters<typeof setOptions>[0]) {
  try {
    await setOptions(o)
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
</script>

<template>
  <UModal v-model:open="open" :title="t('settings.title')">
    <template #body>
      <section class="space-y-3">
        <h3 class="text-sm font-semibold">{{ t('projects.menuOptions') }}</h3>
        <UFormField :label="t('projects.menuMax')">
          <UInputNumber :model-value="max" :min="maxLimits.min" :max="maxLimits.max" class="w-full" @update:model-value="(v: number | null) => v && v !== max && save({ max: v })" />
        </UFormField>
        <UFormField :label="t('projects.menuSort')">
          <USelect :model-value="sort" :items="sortItems" class="w-full" @update:model-value="(v) => save({ sort: v as typeof sort })" />
        </UFormField>
        <USwitch :model-value="single" :label="t('projects.menuSingle')" @update:model-value="(v: boolean) => save({ single: v })" />
      </section>
    </template>
  </UModal>
</template>
