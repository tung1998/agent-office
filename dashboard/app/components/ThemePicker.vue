<script setup lang="ts">
// Settings › Theme: the default look, the color themes, the person's own
// colors (three, and muted text, surfaces and borders if they like, each with
// its contrast), the live wallpapers, and a background of their own with
// colors worked out from it (useTheme, utils/backdropColors).
import type { MessageKey } from '~/locales/vi'
import { COLOR_THEMES, WALLPAPER_THEMES, wallGlassFloor, DEFAULT_CUSTOM, EXTRA_COLORS, autoColors, contrast, isHex, type ExtraColor, type Theme, type ThemeColors } from '~/utils/themes'
import { BG_MAX, BLUR_MAX, CLEAR_MAX, GLASS_MIN, backdropFloor, backdropGlass, backdropSrc, bgKind, bgVersion, type BgItem } from '~/composables/useTheme'
import { cssHex, suggestColors } from '~/utils/backdropColors'
import { BACKGROUND_PACKS } from '~/utils/backgroundPacks'

const { t } = useLang()
const toast = useToast()
const { saved, theme, look, library, setWallGlass, previewWallGlass, setTheme, setCustom, setLook, loadLibrary, pickBackground, setBackground, clearBackground, deleteBackground, sampleSaved, setGlass, previewGlass, previewBg, keepBg, previewWall, keepWall, previewClear, keepClear, pickPackImage } = useTheme()
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
// own colors start from the look in use (the theme's colors, or the default
// look's as the page shows them), not from a fixed set
function currentColors(): ThemeColors {
  const th = theme.value
  if (th) {
    const c: ThemeColors = { bg: th.bg, text: th.text, accent: th.accent }
    for (const k of EXTRA_COLORS) if (th[k]) c[k] = th[k]
    return c
  }
  const bg = cssHex('var(--ui-bg)'), text = cssHex('var(--ui-text)'), accent = cssHex('var(--ui-primary)')
  return bg && text && accent ? { bg, text, accent } : { ...DEFAULT_CUSTOM }
}
function pick(id: string) {
  if (saved.value.id === id) return
  if (id !== 'custom') return run(setTheme(id))
  const c = currentColors()
  for (const k of EXTRA_COLORS) delete custom[k]
  Object.assign(custom, c)
  run(setCustom({ ...c }))
}
function pickColor(k: ColorKey, v: string) {
  custom[k] = v
  if (isHex(v)) run(setCustom({ ...custom }))
}
function autoColor(k: ExtraColor) {
  delete custom[k]
  run(setCustom({ ...custom }))
}
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
onMounted(() => {
  run(sampleSaved())
  run(loadLibrary())
})
// the library: the file shown now is marked; picking another shows it
const current = computed(() => saved.value.bg ? bgVersion(saved.value.bg.v) : 0)
const busy = ref(0) // the file being put on
async function pickItem(item: BgItem) {
  if (busy.value || item.version === current.value) return
  busy.value = item.version
  await run(pickBackground(item))
  busy.value = 0
}
function removeItem(item: BgItem) {
  if (confirm(t('theme.backdropDeleteConfirm'))) run(deleteBackground(item))
}
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
// the panes over a wallpaper theme: the readable glass, unless the person set theirs
const wallFloor = computed(() => theme.value ? wallGlassFloor(theme.value) : 0)
const wallGlass = computed(() => saved.value.wallGlass ?? wallFloor.value)
const keepWallGlass = () => run(setWallGlass(saved.value.wallGlass))
const keepGlass = () => { const p = setGlass(glass.value); if (p) run(p) }
// a pack of the dashboard's own images: one tile, opened to pick from it
const openPack = ref('')
const shownPack = computed(() => BACKGROUND_PACKS.find(p => p.id === openPack.value))
const packBusy = ref('')
async function pickPack(src: string) {
  if (packBusy.value) return
  packBusy.value = src
  await run(pickPackImage(src))
  packBusy.value = ''
}
const keepBackdrop = () => { const p = keepBg(); if (p) run(p) }
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

    <div v-if="saved.id === 'custom'" class="space-y-3">
    <div class="grid grid-cols-3 gap-2">
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
    <ThemeRegionColors :base="custom" />
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
      <div class="grid grid-cols-3 gap-2 sm:grid-cols-4">
        <button
          type="button"
          class="flex h-16 flex-col items-center justify-center gap-1 rounded-lg border border-dashed border-(--ui-border-accented) text-xs text-(--ui-text-muted) transition hover:border-(--ui-primary) hover:text-(--ui-text)"
          :disabled="uploading"
          @click="fileInput?.click()"
        >
          <UIcon :name="uploading ? 'i-lucide-loader-circle' : 'i-lucide-upload'" class="size-4" :class="{ 'animate-spin': uploading }" />
          {{ t('theme.backdropUpload') }}
        </button>
        <button
          v-for="pack in BACKGROUND_PACKS"
          :key="pack.id"
          type="button"
          class="relative h-16 overflow-hidden rounded-lg border transition"
          :class="openPack === pack.id || pack.images.some(i => i.src === saved.bg?.src) ? 'border-(--ui-primary) ring-2 ring-(--ui-primary)/40' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
          :aria-expanded="openPack === pack.id"
          @click="openPack = openPack === pack.id ? '' : pack.id"
        >
          <img :src="pack.images[0]!.src" alt="" loading="lazy" class="size-full object-cover">
          <span class="absolute inset-x-0 bottom-0 flex items-center gap-1 bg-black/55 px-1.5 py-0.5 text-[11px] text-white">
            <UIcon name="i-lucide-images" class="size-3" />
            <span class="truncate">{{ t('theme.pack', { name: pack.name, n: pack.images.length }) }}</span>
          </span>
        </button>
        <div v-for="item in library" :key="item.version" class="group relative h-16">
          <button
            type="button"
            class="size-full overflow-hidden rounded-lg border transition"
            :class="item.version === current ? 'border-(--ui-primary) ring-2 ring-(--ui-primary)/40' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
            :aria-pressed="item.version === current"
            @click="pickItem(item)"
          >
            <video v-if="bgKind(item.mime) === 'video'" :src="`${backdropSrc({ v: item.version })}#t=0.1`" muted preload="metadata" class="size-full object-cover" />
            <img v-else :src="backdropSrc({ v: item.version })" alt="" loading="lazy" class="size-full object-cover">
            <UIcon v-if="bgKind(item.mime) === 'video'" name="i-lucide-play" class="absolute bottom-1 left-1 size-3.5 text-white drop-shadow" />
            <span v-if="busy === item.version" class="absolute inset-0 flex items-center justify-center bg-black/30">
              <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin text-white" />
            </span>
          </button>
          <button
            type="button"
            class="absolute top-1 right-1 rounded-full bg-black/50 p-1 text-white opacity-70 transition hover:bg-black/70 hover:opacity-100 sm:opacity-0 sm:group-hover:opacity-100 sm:focus-visible:opacity-100"
            :aria-label="t('theme.backdropDelete')"
            :title="t('theme.backdropDelete')"
            @click="removeItem(item)"
          >
            <UIcon name="i-lucide-x" class="size-3" />
          </button>
        </div>
      </div>
      <div v-if="shownPack" class="rounded-lg border border-(--ui-border) p-2">
        <div class="grid grid-cols-4 gap-1.5 sm:grid-cols-5">
          <button
            v-for="img in shownPack.images"
            :key="img.id"
            type="button"
            class="relative h-12 overflow-hidden rounded-md border transition"
            :class="saved.bg?.src === img.src ? 'border-(--ui-primary) ring-2 ring-(--ui-primary)/40' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
            :title="img.name"
            :aria-label="img.name"
            :aria-pressed="saved.bg?.src === img.src"
            @click="pickPack(img.src)"
          >
            <img :src="img.src" alt="" loading="lazy" class="size-full object-cover">
            <span v-if="packBusy === img.src" class="absolute inset-0 flex items-center justify-center bg-black/30">
              <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin text-white" />
            </span>
          </button>
        </div>
      </div>
      <div v-if="saved.bg" class="flex flex-wrap items-center gap-2">
        <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-eye-off" :disabled="uploading || !!busy" :label="t('theme.backdropOff')" @click="run(clearBackground())" />
        <UTooltip v-if="suggested" :text="t('theme.backdropSuggestHint')">
          <UButton size="sm" color="neutral" variant="outline" :disabled="uploading" :label="t('theme.backdropSuggest')" @click="useSuggested">
            <template #leading>
              <span class="flex -space-x-1">
                <span v-for="c in [suggested.bg, suggested.text, suggested.accent]" :key="c" class="size-3.5 rounded-full ring-1 ring-(--ui-border)" :style="{ background: c }" />
              </span>
            </template>
          </UButton>
        </UTooltip>
      </div>
      <input ref="fileInput" type="file" accept="image/jpeg,image/png,image/webp,image/gif,video/mp4,video/webm" class="hidden" @change="upload">
      <UFormField v-if="saved.bg" :label="t('theme.backdropGlass')" :help="floor ? t('theme.backdropFloor', { n: Math.round(floor * 100) }) : undefined">
        <USlider :model-value="glass" :min="GLASS_MIN" :max="0.99" :step="0.01" @update:model-value="(v) => previewGlass(v as number)" @change="keepGlass" />
      </UFormField>
      <template v-if="saved.bg">
        <UFormField :label="t('theme.strength')">
          <USlider :model-value="saved.bg.strength ?? 1" :min="0.15" :max="1" :step="0.05" @update:model-value="(v) => previewBg({ strength: v as number })" @change="keepBackdrop" />
        </UFormField>
        <UFormField :label="t('theme.transparency')">
          <USlider :model-value="saved.clear ?? 0" :min="0" :max="CLEAR_MAX" :step="0.05" @update:model-value="(v) => previewClear(v as number)" @change="run(keepClear())" />
        </UFormField>
        <UFormField :label="t('theme.backdropBlur')">
          <USlider :model-value="saved.bg.blur ?? 0" :min="0" :max="BLUR_MAX" :step="1" @update:model-value="(v) => previewBg({ blur: v as number })" @change="keepBackdrop" />
        </UFormField>
      </template>
    </div>

    <div v-if="theme?.wallpaper && !saved.bg" class="space-y-3 rounded-lg border border-(--ui-border) p-3">
      <UFormField :label="t('theme.backdropGlass')" :help="wallFloor ? t('theme.backdropFloor', { n: Math.round(wallFloor * 100) }) : undefined">
        <USlider :model-value="wallGlass" :min="GLASS_MIN" :max="0.99" :step="0.01" @update:model-value="(v) => previewWallGlass(v as number)" @change="keepWallGlass" />
      </UFormField>
      <UFormField :label="t('theme.strength')">
        <USlider :model-value="look.strength" :min="0.15" :max="1" :step="0.05" @update:model-value="(v) => previewWall({ strength: v as number })" @change="run(keepWall())" />
      </UFormField>
      <UFormField :label="t('theme.transparency')">
        <USlider :model-value="saved.clear ?? 0" :min="0" :max="CLEAR_MAX" :step="0.05" @update:model-value="(v) => previewClear(v as number)" @change="run(keepClear())" />
      </UFormField>
      <UFormField :label="t('theme.backdropBlur')">
        <USlider :model-value="look.blur" :min="0" :max="BLUR_MAX" :step="1" @update:model-value="(v) => previewWall({ blur: v as number })" @change="run(keepWall())" />
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
