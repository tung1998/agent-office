<script setup lang="ts">
// Settings › Theme: the default look, the color themes, the person's own
// three colors, and the live wallpapers with how they move (useTheme).
import { BG_MAX, GLASS_MIN, backdropFloor, backdropGlass, backdropSrc, bgKind, bgVersion, type BgItem } from '~/composables/useTheme'
import { suggestColors } from '~/utils/backdropColors'
import type { WallLook } from '~/composables/useTheme'
import { MEDIA_BACKGROUNDS, mediaFileKind, validMediaSource, type MediaBackground } from '~/utils/backgrounds'
import { storeBackgroundFile, removeBackgroundFile } from '~/utils/localBackground'
import type { MessageKey } from '~/locales/vi'
import { COLOR_THEMES, WALLPAPER_THEMES, wallGlassFloor, DEFAULT_CUSTOM, EXTRA_COLORS, autoColors, contrast, isHex, type ExtraColor, type ThemeColors, type Theme } from '~/utils/themes'

const { t } = useLang()
const toast = useToast()
const { saved, theme, look, background, saving, setTheme, setCustom, setLook, library, loadLibrary, pickBackground, deleteBackground, setWallGlass, previewWallGlass, setMediaBackground, setBackground, clearBackground, sampleSaved, setGlass, previewGlass, previewLook } = useTheme()
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
const pick = (id: string) => (saved.value.id !== id || background.value) && run(setTheme(id))
function pickColor(k: ColorKey, v: string) {
  custom[k] = v
  if (isHex(v)) run(setCustom({ ...custom }))
}
function autoColor(k: ExtraColor) {
  delete custom[k]
  run(setCustom({ ...custom }))
}
// Media files stay on this device; only their selection is saved to the account.
const busy = ref(false)
const mediaUrl = ref('')
const mediaKind = ref<'image' | 'video'>('image')
const kinds = computed(() => [{ label: t('theme.image'), value: 'image' }, { label: t('theme.video'), value: 'video' }])
const mediaError = ref('')
async function chooseMedia(next?: MediaBackground) {
  busy.value = true
  try {
    await setMediaBackground(next)
  } catch (e) { toast.add({ title: apiError(e), color: 'error' }) }
  finally { busy.value = false }
}
async function chooseFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  const kind = mediaFileKind(file)
  if (!kind) { mediaError.value = t('theme.fileError'); return }
  mediaError.value = ''
  const src = `local:${crypto.randomUUID()}`
  busy.value = true
  try {
    await storeBackgroundFile(src, file)
    try { await setMediaBackground({ kind, src, name: file.name }) }
    catch (e) { await removeBackgroundFile(src); throw e }
  } catch (e) { toast.add({ title: apiError(e), color: 'error' }) }
  finally { busy.value = false }
}
function chooseUrl() {
  const src = mediaUrl.value.trim()
  if (!/^https?:\/\//i.test(src) || !validMediaSource(src)) { mediaError.value = t('theme.urlError'); return }
  mediaError.value = ''
  chooseMedia({ kind: mediaKind.value, src, name: new URL(src).hostname })
}
async function commitLook(key: keyof WallLook) {
  // Slider's keyboard commit can precede its model update in the same tick.
  await nextTick()
  await run(setLook({ [key]: look.value[key] }))
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
const libraryBusy = ref(0) // the file being put on
async function pickItem(item: BgItem) {
  if (libraryBusy.value || item.version === current.value) return
  libraryBusy.value = item.version
  await run(pickBackground(item))
  libraryBusy.value = 0
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
const wallGlassDraft = ref<number>()
function updateWallGlass(v: number) { wallGlassDraft.value = v; previewWallGlass(v) }
async function keepWallGlass() { await nextTick(); await run(setWallGlass(wallGlassDraft.value ?? wallGlass.value)); wallGlassDraft.value = undefined }
const glassDraft = ref<number>()
function updateGlass(v: number) { glassDraft.value = v; previewGlass(v) }
async function keepGlass() { await nextTick(); const p = setGlass(glassDraft.value ?? glass.value); if (p) await run(p); glassDraft.value = undefined }
const nameOf = (id: string) => t(`theme.name.${id}` as MessageKey)
const swatch = (th: Theme) => ({ background: th.wallpaper?.sky ?? th.bg, color: th.text })
</script>

<template>
  <fieldset :disabled="saving || busy || uploading || !!libraryBusy" class="min-w-0 space-y-4">
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

    <div class="space-y-3 border-t border-(--ui-border) pt-4">
      <div class="flex items-center justify-between gap-2">
        <h4 class="text-xs font-semibold text-(--ui-text-muted)">{{ t('theme.media') }}</h4>
        <UButton v-if="background" size="xs" color="neutral" variant="ghost" :label="t('theme.removeMedia')" :disabled="saving || busy" @click="saved.bg ? run(clearBackground()) : chooseMedia()" />
      </div>
      <div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
        <button v-for="b in MEDIA_BACKGROUNDS" :key="b.src" type="button" :disabled="saving || busy"
          class="relative overflow-hidden rounded-lg border text-start text-xs transition disabled:opacity-50"
          :class="background?.src === b.src ? 'border-(--ui-primary) ring-2 ring-(--ui-primary)/40' : 'border-(--ui-border)'"
          :aria-pressed="background?.src === b.src" @click="chooseMedia(b)">
          <img :src="b.poster ?? b.src" alt="" loading="lazy" class="aspect-video w-full object-cover">
          <span class="flex items-center gap-1 p-2"><UIcon v-if="b.kind === 'video'" name="i-lucide-film" class="size-3 shrink-0" />{{ b.name }}</span>
        </button>
      </div>
      <UFormField :label="t('theme.file')" :help="t('theme.localHint')">
        <input type="file" accept="image/jpeg,image/png,image/webp,image/gif,image/bmp,image/avif,video/mp4" :disabled="saving || busy" class="block w-full min-w-0 text-xs file:me-2 file:rounded file:border file:border-(--ui-border) file:px-3 file:py-2" @change="chooseFile">
      </UFormField>
      <form class="space-y-2" @submit.prevent="chooseUrl">
        <UFormField :label="t('theme.url')" :help="t('theme.urlHint')">
          <UInput v-model="mediaUrl" type="url" class="w-full" :placeholder="t('theme.urlPlaceholder')" :disabled="saving || busy" />
        </UFormField>
        <div class="flex gap-2">
          <USelect v-model="mediaKind" :items="kinds" :disabled="saving || busy" :aria-label="t('theme.mediaKind')" />
          <UButton type="submit" :label="t('theme.apply')" :loading="busy" :disabled="!mediaUrl.trim()" />
        </div>
      </form>
      <p v-if="mediaError" role="alert" class="text-xs text-(--ui-error)">{{ mediaError }}</p>
      <p v-if="background" class="break-anywhere text-xs text-(--ui-text-muted)">{{ background.name }}</p>
    </div>

    <div v-if="theme?.wallpaper || background" class="space-y-3 rounded-lg border border-(--ui-border) p-3">
      <UFormField :label="t('theme.strength')">
        <USlider :disabled="saving || busy" :aria-label="t('theme.strength')" :tooltip="true" :model-value="look.strength" :min="0" :max="1" :step="0.05" @update:model-value="(v) => previewLook({ strength: v as number })" @change="commitLook('strength')" />
      </UFormField>
      <UFormField v-if="background && !saved.bg" :label="t('theme.transparency')">
        <USlider :disabled="saving || busy" :aria-label="t('theme.transparency')" :tooltip="true" :model-value="look.transparency" :min="0" :max="0.9" :step="0.05" @update:model-value="(v) => previewLook({ transparency: v as number })" @change="commitLook('transparency')" />
      </UFormField>
      <UFormField v-if="theme?.wallpaper && !background" :label="t('theme.backdropGlass')" :help="t('theme.backdropFloor', { n: Math.round(wallFloor * 100) })">
        <USlider :disabled="saving" :aria-label="t('theme.backdropGlass')" :model-value="wallGlassDraft ?? wallGlass" :min="GLASS_MIN" :max="1" :step="0.01" @update:model-value="(v) => updateWallGlass(v as number)" @change="keepWallGlass" />
        <UButton v-if="saved.wallGlass !== undefined" size="xs" color="neutral" variant="ghost" :label="t('theme.custom.reset')" @click="run(setWallGlass(undefined))" />
      </UFormField>
      <UFormField :label="t('theme.blur')">
        <USlider :disabled="saving || busy" :aria-label="t('theme.blur')" :tooltip="true" :model-value="look.blur" :min="0" :max="30" :step="1" @update:model-value="(v) => previewLook({ blur: v as number })" @change="commitLook('blur')" />
      </UFormField>
      <div v-if="theme?.wallpaper && !background" class="grid grid-cols-2 gap-3">
        <UFormField :label="t('theme.speed')">
          <USelect :disabled="saving || busy" :model-value="look.speed" :items="speeds" class="w-full" @update:model-value="(v) => run(setLook({ speed: v as number }))" />
        </UFormField>
        <UFormField :label="t('theme.cellSize')">
          <USelect :disabled="saving || busy" :model-value="look.cell" :items="cells" class="w-full" @update:model-value="(v) => run(setLook({ cell: v as number }))" />
        </UFormField>
      </div>
      <USwitch v-if="background?.kind === 'video' || (theme?.wallpaper && !background)" :disabled="saving || busy" :model-value="look.still" :label="t('theme.still')" @update:model-value="(v: boolean) => run(setLook({ still: v }))" />
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
        <div v-for="(item, index) in library" :key="item.version" class="group relative h-16">
          <button
            type="button"
            class="size-full overflow-hidden rounded-lg border transition"
            :class="item.version === current ? 'border-(--ui-primary) ring-2 ring-(--ui-primary)/40' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
            :aria-pressed="item.version === current" :aria-label="t('theme.libraryItem', { n: index + 1 })"
            @click="pickItem(item)"
          >
            <video v-if="bgKind(item.mime) === 'video'" :src="`${backdropSrc({ v: item.version })}#t=0.1`" muted preload="metadata" class="size-full object-cover" />
            <img v-else :src="backdropSrc({ v: item.version })" alt="" loading="lazy" class="size-full object-cover">
            <UIcon v-if="bgKind(item.mime) === 'video'" name="i-lucide-play" class="absolute bottom-1 left-1 size-3.5 text-white drop-shadow" />
            <span v-if="libraryBusy === item.version" class="absolute inset-0 flex items-center justify-center bg-black/30">
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
        <USlider :aria-label="t('theme.backdropGlass')" :model-value="glassDraft ?? glass" :min="GLASS_MIN" :max="0.99" :step="0.01" @update:model-value="(v) => updateGlass(v as number)" @change="keepGlass" />
      </UFormField>
    </div>

    <ThemeComponentColors />
  </fieldset>
</template>
