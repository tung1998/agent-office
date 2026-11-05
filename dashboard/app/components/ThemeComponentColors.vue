<script setup lang="ts">
import { COLOR_GROUPS, readComponentColors, type ColorGroup, type ComponentColors } from '~/utils/component-colors'
import { isHex, DEFAULT_CUSTOM } from '~/utils/themes'

const { t } = useLang()
const { saved, theme, saving, setColors } = useTheme()
const toast = useToast()
const colorMode = useColorMode()
const colors = computed(() => readComponentColors(saved.value.colors))
const defaults = computed(() => theme.value ?? (colorMode.value === 'dark' ? DEFAULT_CUSTOM : { bg: '#ffffff', text: '#18181b', accent: '#6366f1' }))
const hasColors = computed(() => Object.keys(colors.value).length > 0)
async function save(next: ComponentColors) {
  try { await setColors(next) } catch (e) { toast.add({ title: apiError(e), color: 'error' }) }
}
function setSurface(group: ColorGroup, key: 'bg' | 'text', value: string) {
  if (isHex(value)) save({ ...colors.value, surfaces: { ...colors.value.surfaces, [group]: { ...colors.value.surfaces?.[group], [key]: value } } })
}
function resetSurface(group: ColorGroup) {
  const surfaces = { ...colors.value.surfaces }
  delete surfaces[group]
  save({ ...colors.value, surfaces })
}
function setGlobal(key: 'border' | 'link' | 'accent', value?: string) {
  const next = { ...colors.value }
  if (value && isHex(value)) next[key] = value
  else delete next[key]
  save(next)
}
const globalDefault = (key: 'border' | 'link' | 'accent') => key === 'border' ? (colorMode.value === 'dark' ? '#3f3f46' : '#e4e4e7') : defaults.value.accent
</script>

<template>
  <details class="rounded-lg border border-(--ui-border) p-3">
    <summary class="cursor-pointer text-sm font-medium">{{ t('theme.colors.title') }}</summary>
    <div class="mt-3 space-y-4">
      <p class="text-xs text-(--ui-text-muted)">{{ t('theme.colors.hint') }}</p>
      <UButton :disabled="saving || !hasColors" size="xs" color="neutral" variant="soft" :label="t('theme.colors.resetAll')" @click="save({})" />
      <section v-for="group in COLOR_GROUPS" :key="group" class="space-y-2">
        <div class="flex items-center justify-between gap-2">
          <h5 class="text-xs font-semibold">{{ t(`theme.colors.${group}`) }}</h5>
          <UButton :disabled="saving || !colors.surfaces?.[group]" size="xs" color="neutral" variant="ghost" :label="t('theme.colors.resetGroup', { group: t(`theme.colors.${group}`) })" @click="resetSurface(group)" />
        </div>
        <div class="grid grid-cols-2 gap-2">
          <label v-for="key in (['bg', 'text'] as const)" :key="key" class="flex min-w-0 items-center gap-2 text-xs">
            <input type="color" :disabled="saving" :value="colors.surfaces?.[group]?.[key] ?? defaults[key]" :aria-label="t('theme.colors.field', { group: t(`theme.colors.${group}`), field: t(`theme.custom.${key}`) })" class="size-8 shrink-0 cursor-pointer rounded border border-(--ui-border) bg-transparent p-0.5" @change="setSurface(group, key, ($event.target as HTMLInputElement).value)">
            <span class="min-w-0">{{ t(`theme.custom.${key}`) }}<span class="block text-(--ui-text-muted)">{{ colors.surfaces?.[group]?.[key] ?? t('theme.colors.inherit') }}</span></span>
          </label>
        </div>
      </section>
      <div class="space-y-2 border-t border-(--ui-border) pt-3">
        <div v-for="key in (['border', 'link', 'accent'] as const)" :key="key" class="flex items-center justify-between gap-2">
          <label class="flex min-w-0 items-center gap-2 text-xs">
            <input type="color" :disabled="saving" :value="colors[key] ?? globalDefault(key)" :aria-label="t(`theme.colors.${key}`)" class="size-8 shrink-0 cursor-pointer rounded border border-(--ui-border) bg-transparent p-0.5" @change="setGlobal(key, ($event.target as HTMLInputElement).value)">
            <span>{{ t(`theme.colors.${key}`) }}<span class="block text-(--ui-text-muted)">{{ colors[key] ?? t('theme.colors.inherit') }}</span></span>
          </label>
          <UButton :disabled="saving || !colors[key]" size="xs" color="neutral" variant="ghost" :label="t('theme.colors.resetGroup', { group: t(`theme.colors.${key}`) })" @click="setGlobal(key)" />
        </div>
      </div>
    </div>
  </details>
</template>
