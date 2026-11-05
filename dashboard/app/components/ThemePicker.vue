<script setup lang="ts">
// Settings › Theme: the default look, the color themes, the person's own
// colors (three, and muted text, surfaces and borders if they like, each with
// its contrast), the live wallpapers, and a background of their own with
// colors worked out from it (useTheme, utils/backdropColors).
import type { MessageKey } from '~/locales/vi'
import { COLOR_THEMES, WALLPAPER_THEMES, DEFAULT_CUSTOM, EXTRA_COLORS, autoColors, contrast, isHex, type ExtraColor, type Theme, type ThemeColors } from '~/utils/themes'
import { BG_MAX, backdropFloor, backdropGlass, backdropSrc } from '~/composables/useTheme'
import { suggestColors } from '~/utils/backdropColors'

const { t } = useLang()
const toast = useToast()
const { saved, theme, look, setTheme, setCustom, setLook, setBackground, clearBackground, sampleSaved, setGlass, previewGlass } = useTheme()
const colorMode = useColorMode()
const custom = reactive<ThemeColors>({ ...DEFAULT_CUSTOM, ...saved.value.custom })
const auto = computed(() => autoColors(custom))
const BASE = ['bg', 'text', 'accent'] as const
type ColorKey = typeof BASE[number] | ExtraColor
const colorOf = (k: ColorKey) => custom[k] ?? auto.value[k as ExtraColor]
// the contrast each color is read at (WCAG): text, muted and accent on the
// background, text on cards; borders and the background itself have none
const ratio = (k: ColorKey) => k === 'text' || k === 'muted' || k === 'accent'
  ? contrast(colorOf(k)!, custom.bg)
  : k === 'surface' ? contrast(custom.text, colorOf(k)!) : undefined
const tone = (r: number) => r >= 4.5 ? 'text-(--ui-success)' : r >= 3 ? 'text-(--ui-warning)' : 'text-(--ui-error)'
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
function pickColor(k: ColorKey, v: string) {
  custom[k] = v
  if (isHex(v)) run(setCustom({ ...custom }))
}
function autoColor(k: ExtraColor) {
  delete custom[k]
  run(setCustom({ ...custom }))
}
// dragging shows the strength at once; it is saved when let go
const preview = (v: number) => { saved.value = { ...saved.value, wall: { ...saved.value.wall, strength: v } } }
// a background image or video of the person's own, on top of any theme
const fileInput = ref<HTMLInputElement>()
const uploading = ref(false)
// the least glass this background needs for the theme's muted text (the
// page is painted by then: the colors are read from it)
const floor = computed(() => {
  void colorMode.value
  return saved.value.bg ? backdropFloor(saved.value.bg) : 0
})
const glass = computed(() => saved.value.bg ? backdropGlass(saved.value.bg, floor.value) : 0)
const suggested = computed(() => suggestColors(saved.value.bg?.swatch ?? []))
function useSuggested() {
  if (!suggested.value) return
  Object.assign(custom, suggested.value)
  for (const k of EXTRA_COLORS) delete custom[k]
  run(setCustom({ ...suggested.value }))
}
onMounted(() => run(sampleSaved()))
async function upload(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (file.size > BG_MAX) {
    toast.add({ title: t('theme.backdropTooBig'), color: 'error' })
    return
  }
  uploading.value = true
  await run(setBackground(file))
  uploading.value = false
}
const keepGlass = () => { const p = setGlass(glass.value); if (p) run(p) }
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
      <label v-for="k in ([...BASE, ...EXTRA_COLORS] as ColorKey[])" :key="k" class="flex min-w-0 flex-col gap-1 text-xs text-(--ui-text-muted)">
        <span class="flex items-center gap-1">
          <span class="truncate">{{ t(`theme.custom.${k}`) }}</span>
          <UTooltip v-if="ratio(k) !== undefined" :text="t('theme.contrast', { n: ratio(k)!.toFixed(1) })">
            <span class="ms-auto tabular-nums" :class="tone(ratio(k)!)">{{ ratio(k)!.toFixed(1) }}</span>
          </UTooltip>
        </span>
        <span class="flex items-center gap-1.5 rounded-md border border-(--ui-border) p-1">
          <input type="color" :value="colorOf(k)" class="size-7 shrink-0 cursor-pointer rounded border-0 bg-transparent p-0" @change="pickColor(k, ($event.target as HTMLInputElement).value)">
          <code v-if="custom[k]" class="min-w-0 truncate text-(--ui-text)">{{ custom[k] }}</code>
          <span v-else class="min-w-0 truncate italic">{{ t('theme.custom.auto') }}</span>
          <button v-if="custom[k] && (EXTRA_COLORS as readonly string[]).includes(k)" type="button" class="ms-auto shrink-0 rounded p-0.5 hover:bg-(--ui-bg-elevated)" :aria-label="t('theme.custom.reset')" :title="t('theme.custom.reset')" @click.prevent="autoColor(k as ExtraColor)">
            <UIcon name="i-lucide-rotate-ccw" class="size-3.5" />
          </button>
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

    <div class="space-y-2">
      <h4 class="flex items-center gap-1 text-xs font-semibold text-(--ui-text-muted)">
        {{ t('theme.backdrop') }}
        <UTooltip :text="t('theme.backdropHint')"><UIcon name="i-lucide-info" class="size-3.5" /></UTooltip>
      </h4>
      <div class="flex flex-wrap items-center gap-2">
        <template v-if="saved.bg">
          <video v-if="saved.bg.kind === 'video'" :src="backdropSrc(saved.bg)" muted class="h-12 w-20 rounded-md border border-(--ui-border) object-cover" />
          <img v-else :src="backdropSrc(saved.bg)" alt="" class="h-12 w-20 rounded-md border border-(--ui-border) object-cover">
        </template>
        <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-upload" :loading="uploading" :label="saved.bg ? t('theme.backdropChange') : t('theme.backdropUpload')" @click="fileInput?.click()" />
        <UButton v-if="saved.bg" size="sm" color="neutral" variant="ghost" icon="i-lucide-trash-2" :disabled="uploading" :label="t('theme.backdropRemove')" @click="run(clearBackground())" />
        <UTooltip v-if="suggested" :text="t('theme.backdropSuggestHint')">
          <UButton size="sm" color="neutral" variant="outline" :disabled="uploading" :label="t('theme.backdropSuggest')" @click="useSuggested">
            <template #leading>
              <span class="flex -space-x-1">
                <span v-for="c in [suggested.bg, suggested.text, suggested.accent]" :key="c" class="size-3.5 rounded-full ring-1 ring-(--ui-border)" :style="{ background: c }" />
              </span>
            </template>
          </UButton>
        </UTooltip>
        <input ref="fileInput" type="file" accept="image/jpeg,image/png,image/webp,image/gif,video/mp4,video/webm" class="hidden" @change="upload">
      </div>
      <UFormField v-if="saved.bg" :label="t('theme.backdropGlass')" :help="floor ? t('theme.backdropFloor', { n: Math.round(floor * 100) }) : undefined">
        <USlider :model-value="glass" :min="floor || 0.65" :max="0.99" :step="0.01" @update:model-value="(v) => previewGlass(v as number)" @change="keepGlass" />
      </UFormField>
    </div>

    <div v-if="theme?.wallpaper && !saved.bg" class="space-y-3 rounded-lg border border-(--ui-border) p-3">
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
