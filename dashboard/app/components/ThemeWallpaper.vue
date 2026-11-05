<script setup lang="ts">
// A wallpaper theme's live scene, on a canvas behind the whole dashboard.
// Used as <LazyThemeWallpaper> only while such a theme is on, so this, the
// engine and the scene are their own chunks: no theme, no cost.
import type { WallLook } from '~/composables/useTheme'

const props = defineProps<{ scene: string, look: WallLook }>()
const canvas = ref<HTMLCanvasElement>()
const scenes = import.meta.glob('~/wallpaper/scenes/*.js')
type Wall = { stop: () => void, set: (o: Record<string, unknown>) => void }
let wall: Wall | undefined
let gen = 0
let most = 99 // a scene drawn at its own scale caps the cell size

const opts = () => ({ speed: props.look.speed, still: props.look.still })
async function start() {
  const my = ++gen
  wall?.stop()
  wall = undefined
  const load = Object.entries(scenes).find(([p]) => p.endsWith(`/${props.scene}.js`))?.[1]
  if (!load || !canvas.value) return
  const [{ startWallpaper }, mod] = await Promise.all([import('~/wallpaper/engine.js'), load() as Promise<{ default: { size?: number } }>])
  if (my !== gen || !canvas.value) return
  const scene = mod.default
  most = scene.size ?? 99
  wall = startWallpaper(canvas.value, scene, {
    ...opts(),
    cell: Math.min(props.look.cell, most),
    sidebar: () => document.querySelector('.office-sidebar')
  }) as Wall
}

onMounted(start)
watch(() => props.scene, start)
watch(() => [props.look.speed, props.look.still], () => wall?.set(opts()))
watch(() => props.look.cell, c => wall?.set({ cell: Math.min(c, most) }))
onBeforeUnmount(() => { gen++; wall?.stop() })
</script>

<template>
  <canvas
    ref="canvas"
    aria-hidden="true"
    class="pointer-events-none fixed inset-0 -z-10 h-screen w-screen"
    :style="{ opacity: look.strength, filter: look.blur ? `blur(${look.blur}px)` : undefined, transform: look.blur ? `scale(${1 + look.blur / 100})` : undefined }"
  />
</template>
