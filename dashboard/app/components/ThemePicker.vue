<script setup lang="ts">
// Settings › Theme: the default look, the color themes, the person's own
// three colors, and the live wallpapers with how they move (useTheme).
import type { WallLook } from '~/composables/useTheme'
import { MEDIA_BACKGROUNDS, mediaFileKind, validMediaSource, type MediaBackground } from '~/utils/backgrounds'
import { storeBackgroundFile, removeBackgroundFile } from '~/utils/localBackground'
import type { MessageKey } from '~/locales/vi'
import { COLOR_THEMES, WALLPAPER_THEMES, DEFAULT_CUSTOM, isHex, type Theme } from '~/utils/themes'

const { t } = useLang()
const toast = useToast()
const { saved, theme, look, background, saving, setTheme, setCustom, setLook, setBackground, previewLook } = useTheme()
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
const pick = (id: string) => (saved.value.id !== id || background.value) && run(setTheme(id))
function pickColor(k: keyof typeof custom, v: string) {
  custom[k] = v
  if (isHex(v)) run(setCustom({ ...custom }))
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
    await setBackground(next)
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
    try { await setBackground({ kind, src, name: file.name }) }
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
const nameOf = (id: string) => t(`theme.name.${id}` as MessageKey)
const swatch = (th: Theme) => ({ background: th.wallpaper?.sky ?? th.bg, color: th.text })
</script>

<template>
  <fieldset :disabled="saving || busy" class="min-w-0 space-y-4">
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

    <div class="space-y-3 border-t border-(--ui-border) pt-4">
      <div class="flex items-center justify-between gap-2">
        <h4 class="text-xs font-semibold text-(--ui-text-muted)">{{ t('theme.media') }}</h4>
        <UButton v-if="background" size="xs" color="neutral" variant="ghost" :label="t('theme.removeMedia')" :disabled="saving || busy" @click="chooseMedia()" />
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
      <UFormField :label="t('theme.transparency')">
        <USlider :disabled="saving || busy" :aria-label="t('theme.transparency')" :tooltip="true" :model-value="look.transparency" :min="0" :max="0.9" :step="0.05" @update:model-value="(v) => previewLook({ transparency: v as number })" @change="commitLook('transparency')" />
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
  </fieldset>
</template>
