<script setup lang="ts">
// The account's own settings (from the user menu), a panel on the right: the
// sidebar's projects and which of its groups show (useProjectMenu), kept with
// the account.
const open = defineModel<boolean>('open', { default: false })
const { t } = useLang()
const toast = useToast()
const { max, maxLimits, sort, single, setOptions, showsGroup, setGroup } = useProjectMenu()
const { isOfficeAdmin, canCreateProject } = useAuth()
// administration is only in an admin's menu
const groups = computed(() => MENU_GROUPS.filter(g => g !== 'admin' || isOfficeAdmin.value || canCreateProject.value)) // a manager has its members
const sortItems = computed(() => (['manual', 'usage', 'name'] as const).map(v => ({ label: t(`projects.menuSort.${v}`), value: v })))
async function save(o: Parameters<typeof setOptions>[0] | (() => Promise<void>)) {
  try {
    await (typeof o === 'function' ? o() : setOptions(o))
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
</script>

<template>
  <USlideover v-model:open="open" side="right" :title="t('settings.title')">
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
      <section class="mt-6 space-y-3">
        <h3 class="text-sm font-semibold">{{ t('settings.menuGroups') }}</h3>
        <USwitch
          v-for="g in groups"
          :key="g"
          :model-value="showsGroup(g)"
          :label="t(`nav.${g}`)"
          @update:model-value="(v: boolean) => save(() => setGroup(g, v))"
        />
      </section>
    </template>
  </USlideover>
</template>
