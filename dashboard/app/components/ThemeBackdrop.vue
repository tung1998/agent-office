<script setup lang="ts">
// The person's own background image or video, behind the whole dashboard
// (useTheme paints the panes over it as glass). A video plays muted on a
// loop, and stays on its first frame when the machine asks for less motion.
import { backdropSrc, type Backdrop } from '~/composables/useTheme'

const props = defineProps<{ bg: Backdrop }>()
const src = computed(() => backdropSrc(props.bg))
// a blurred background is scaled up a little so its edges stay covered
const style = computed(() => props.bg.blur ? { filter: `blur(${props.bg.blur}px)`, transform: `scale(${1 + props.bg.blur / 100})` } : undefined)
const still = ref(false)
onMounted(() => { still.value = window.matchMedia('(prefers-reduced-motion: reduce)').matches })
</script>

<template>
  <video
    v-if="bg.kind === 'video'"
    :key="src"
    :src="src"
    :autoplay="!still"
    muted
    loop
    playsinline
    aria-hidden="true"
    class="pointer-events-none fixed inset-0 -z-10 size-full object-cover"
    :style="style"
  />
  <img
    v-else
    :src="src"
    alt=""
    aria-hidden="true"
    class="pointer-events-none fixed inset-0 -z-10 size-full object-cover"
    :style="style"
  >
</template>
