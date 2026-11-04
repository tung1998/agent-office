<script setup lang="ts">
// Settings › Theme: the default look, the color themes, the person's own
// three colors, and the live wallpapers with how they move (useTheme).
import type { MessageKey } from '~/locales/vi'
import { COLOR_THEMES, WALLPAPER_THEMES, DEFAULT_CUSTOM, isHex, type Theme } from '~/utils/themes'

const { t } = useLang()
const toast = useToast()
const { saved, theme, look, setTheme, setCustom, setLook } = useTheme()
const custom = reactive({ ...DEFAULT_CUSTOM, ...saved.value.custom })
const speeds = computed(() => [0.5, 1, 2, 4].map(v => ({ label: `${v}×`, value: v })))
const cells = computed(() => ([[14, 'small'], [18, 'medium'], [22, 'large']] as const).map(([v, k]) => ({ label: t(`theme.cell.${k}`), value: v as number })))

async function run(p: Promise<void>) {
  try {
    await p
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const pick = (id: string) => saved.value.id !== id && run(setTheme(id))
function pickColor(k: keyof typeof custom, v: string) {
  custom[k] = v
  if (isHex(v)) run(setCustom({ ...custom }))
}
// dragging shows the strength at once; it is saved when let go
const preview = (v: number) => { saved.value = { ...saved.value, wall: { ...saved.value.wall, strength: v } } }
const nameOf = (id: string) => t(`theme.name.${id}` as MessageKey)
const swatch = (th: Theme) => ({ background: th.wallpaper?.sky ?? th.bg, color: th.text })
</script>

<template>
  <div class="space-y-4">
    <div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
      <button
        v-for="th in [{ id: 'default' }, ...COLOR_THEMES, { id: 'custom' }]"
        :key="th.id"
        type="button"
        class="flex h-14 flex-col justify-between rounded-lg border p-2 text-start text-xs transition"
        :class="saved.id === th.id ? 'border-(--ui-primary) ring-2 ring-(--ui-primary)/40' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
        :style="'bg' in th ? swatch(th as Theme) : undefined"
        :aria-pressed="saved.id === th.id"
        @click="pick(th.id)"
      >
        <span class="font-medium" :style="'font' in th ? { fontFamily: (th as Theme).font } : undefined">{{ nameOf(th.id) }}</span>
        <span v-if="'accent' in th" class="h-1.5 w-8 rounded-full" :style="{ background: (th as Theme).accent }" />
        <UIcon v-else :name="th.id === 'custom' ? 'i-lucide-palette' : 'i-lucide-sun-moon'" class="size-4 text-(--ui-text-muted)" />
      </button>
    </div>

    <div v-if="saved.id === 'custom'" class="grid grid-cols-3 gap-2">
      <label v-for="k in (['bg', 'text', 'accent'] as const)" :key="k" class="flex flex-col gap-1 text-xs text-(--ui-text-muted)">
        {{ t(`theme.custom.${k}`) }}
        <span class="flex items-center gap-1.5 rounded-md border border-(--ui-border) p-1">
          <input type="color" :value="custom[k]" class="size-7 shrink-0 cursor-pointer rounded border-0 bg-transparent p-0" @change="pickColor(k, ($event.target as HTMLInputElement).value)">
          <code class="truncate text-(--ui-text)">{{ custom[k] }}</code>
        </span>
      </label>
    </div>

    <div class="space-y-2">
      <h4 class="flex items-center gap-1 text-xs font-semibold text-(--ui-text-muted)">
        {{ t('theme.wallpapers') }}
        <UTooltip :text="t('theme.wallpapersHint')"><UIcon name="i-lucide-info" class="size-3.5" /></UTooltip>
      </h4>
      <div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
        <button
          v-for="th in WALLPAPER_THEMES"
          :key="th.id"
          type="button"
          class="flex h-14 flex-col justify-between rounded-lg border p-2 text-start text-xs transition"
          :class="saved.id === th.id ? 'border-(--ui-primary) ring-2 ring-(--ui-primary)/40' : 'border-white/10 hover:border-white/30'"
          :style="swatch(th)"
          :aria-pressed="saved.id === th.id"
          @click="pick(th.id)"
        >
          <span class="font-medium">{{ nameOf(th.id) }}</span>
          <span class="h-1.5 w-8 rounded-full" :style="{ background: th.accent }" />
        </button>
      </div>
    </div>

    <div v-if="theme?.wallpaper" class="space-y-3 rounded-lg border border-(--ui-border) p-3">
      <UFormField :label="t('theme.strength')">
        <USlider :model-value="look.strength" :min="0.15" :max="1" :step="0.05" @update:model-value="(v) => preview(v as number)" @change="run(setLook({ strength: look.strength }))" />
      </UFormField>
      <div class="grid grid-cols-2 gap-3">
        <UFormField :label="t('theme.speed')">
          <USelect :model-value="look.speed" :items="speeds" class="w-full" @update:model-value="(v) => run(setLook({ speed: v as number }))" />
        </UFormField>
        <UFormField :label="t('theme.cellSize')">
          <USelect :model-value="look.cell" :items="cells" class="w-full" @update:model-value="(v) => run(setLook({ cell: v as number }))" />
        </UFormField>
      </div>
      <USwitch :model-value="look.still" :label="t('theme.still')" @update:model-value="(v: boolean) => run(setLook({ still: v }))" />
    </div>
  </div>
</template>
