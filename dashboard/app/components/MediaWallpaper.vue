<script setup lang="ts">
import type { MediaBackground } from '~/utils/backgrounds'
import type { WallLook } from '~/composables/useTheme'
import { loadBackgroundFile } from '~/utils/localBackground'

const props = defineProps<{ background: MediaBackground, look: WallLook }>()
const { t } = useLang()
const source = ref('')
const video = ref<HTMLVideoElement>()
const failed = ref(false)
const blocked = ref(false)
const visible = ref(true)
const reduced = ref(false)
let objectUrl = ''
let generation = 0
let motion: MediaQueryList | undefined
const moving = computed(() => visible.value && !reduced.value && !props.look.still && !failed.value)
const style = computed(() => ({ opacity: props.look.strength, filter: `blur(${props.look.blur}px)`, transform: props.look.blur ? `scale(${1 + props.look.blur / 100})` : undefined }))

async function resolveSource() {
  const current = ++generation
  failed.value = blocked.value = false
  source.value = ''
  video.value?.pause()
  if (objectUrl) URL.revokeObjectURL(objectUrl)
  objectUrl = ''
  const src = props.background.src
  if (!src.startsWith('local:')) { source.value = src; return }
  try {
    const file = await loadBackgroundFile(src)
    if (current !== generation) return
    if (!file) { failed.value = true; return }
    source.value = objectUrl = URL.createObjectURL(file)
  } catch { if (current === generation) failed.value = true }
}

async function playback() {
  const el = video.value
  if (!el) return
  if (!moving.value) { el.pause(); blocked.value = false; return }
  try { await el.play(); blocked.value = false }
  catch { if (el === video.value && moving.value) blocked.value = true }
}
function updateVisibility() { visible.value = !document.hidden }
function updateMotion() { reduced.value = motion?.matches ?? false }
onMounted(() => {
  updateVisibility()
  motion = matchMedia('(prefers-reduced-motion: reduce)')
  updateMotion()
  motion.addEventListener('change', updateMotion)
  document.addEventListener('visibilitychange', updateVisibility)
  resolveSource()
})
watch(() => [props.background.src, props.background.kind], resolveSource)
watch([moving, video], playback)
onBeforeUnmount(() => {
  generation++
  video.value?.pause()
  motion?.removeEventListener('change', updateMotion)
  document.removeEventListener('visibilitychange', updateVisibility)
  if (objectUrl) URL.revokeObjectURL(objectUrl)
})
</script>

<template>
  <div class="pointer-events-none fixed inset-0 -z-10 overflow-hidden" aria-hidden="true">
    <video
      v-if="source && background.kind === 'video' && !failed" :key="source" ref="video"
      :src="source" :poster="background.poster" muted loop playsinline preload="metadata"
      class="h-full w-full object-cover" :style="style" @loadeddata="playback" @error="failed = true"
    />
    <img v-else-if="source && !failed" :key="`image:${source}`" :src="source" alt="" class="h-full w-full object-cover" :style="style" @error="failed = true">
  </div>
  <div v-if="failed || blocked" class="fixed bottom-3 end-3 z-50 max-w-72 rounded-lg border border-(--ui-border) bg-(--ui-bg-elevated) p-3 text-xs shadow" role="status">
    {{ t(failed ? (background.src.startsWith('local:') ? 'theme.localMissing' : 'theme.mediaError') : 'theme.playBlocked') }}
    <UButton v-if="blocked && !failed" size="xs" variant="soft" :label="t('theme.play')" class="mt-2" @click="playback" />
  </div>
</template>
