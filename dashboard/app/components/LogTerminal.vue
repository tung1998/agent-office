<script setup lang="ts">
// Terminal-like log view fed by an SSE endpoint sending `lines` events.
interface Line { seq: number, text: string, stream: 'out' | 'err' | 'sys', time: string }
const { t, dateLocale } = useLang()

const props = defineProps<{ url: string | null, title: string, subtitle?: string, empty?: string }>()
const lines = ref<Line[]>([])
const el = ref<HTMLElement | null>(null)
const follow = ref(true)
let source: EventSource | null = null

watch(() => props.url, (url) => {
  source?.close()
  lines.value = []
  follow.value = true
  if (!url) return
  source = new EventSource(url)
  source.addEventListener('lines', (ev) => {
    const add = JSON.parse((ev as MessageEvent).data) as Line[]
    lines.value = [...lines.value, ...add].slice(-3000)
    if (follow.value) nextTick(() => el.value?.scrollTo({ top: el.value.scrollHeight }))
  })
}, { immediate: true })
onBeforeUnmount(() => source?.close())

function onScroll() {
  const e = el.value
  if (e) follow.value = e.scrollHeight - e.scrollTop - e.clientHeight < 40
}
function toBottom() {
  follow.value = true
  el.value?.scrollTo({ top: el.value.scrollHeight })
}
const time = (iso: string) => new Date(iso).toLocaleTimeString(dateLocale.value, { hour12: false })
</script>

<template>
  <div class="flex h-[26rem] max-h-[60vh] flex-col self-start overflow-hidden rounded-lg border border-(--ui-border) bg-neutral-950">
    <div class="flex items-center gap-2 border-b border-white/10 px-3 py-2 text-xs text-neutral-400">
      <UIcon name="i-lucide-terminal" class="size-4 shrink-0" />
      <span class="truncate font-medium text-neutral-200">{{ title }}</span>
      <span v-if="subtitle" class="truncate">{{ subtitle }}</span>
      <slot name="actions" />
      <span class="ms-auto shrink-0">{{ t('log.lines', { n: lines.length }) }}</span>
      <button type="button" class="shrink-0 hover:text-neutral-200" @click="lines = []">{{ t('log.clear') }}</button>
    </div>
    <div ref="el" class="min-h-0 flex-1 overflow-auto p-3 font-mono text-xs leading-relaxed" @scroll="onScroll">
      <p v-if="!lines.length" class="text-neutral-500">{{ empty ?? t('log.empty') }}</p>
      <div v-for="l in lines" :key="l.seq" class="flex gap-3 whitespace-pre-wrap break-all">
        <span class="shrink-0 select-none text-neutral-600">{{ time(l.time) }}</span>
        <span :class="l.stream === 'err' ? 'text-red-300' : l.stream === 'sys' ? 'text-sky-300' : 'text-neutral-200'">{{ l.text }}</span>
      </div>
    </div>
    <button v-if="!follow" type="button" class="border-t border-white/10 py-1 text-xs text-neutral-400 hover:text-neutral-200" @click="toBottom">
      {{ t('log.toBottom') }}
    </button>
  </div>
</template>
