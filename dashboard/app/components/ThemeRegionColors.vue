<script setup lang="ts">
// Settings › Theme › own colors › Advanced: a background and text color for
// one region of the page, and the links' color. Each is automatic (the
// theme's) until set, and goes back to automatic on its own (utils/regionColors).
import { REGIONS, hasRegionColors, readRegionColors, type Region } from '~/utils/regionColors'
import { isHex } from '~/utils/themes'

const props = defineProps<{ base: { bg: string, text: string, accent: string } }>()
const { t } = useLang()
const toast = useToast()
const { saved, setRegions } = useTheme()
const regions = computed(() => readRegionColors(saved.value.regions))
const any = computed(() => hasRegionColors(regions.value))

async function put(next: ReturnType<typeof readRegionColors>) {
  try { await setRegions(readRegionColors(next)) } catch (e) { toast.add({ title: apiError(e), color: 'error' }) }
}
function setColor(r: Region, k: 'bg' | 'text', v?: string) {
  const next = { ...regions.value, [r]: { ...regions.value[r] } }
  if (v && isHex(v)) next[r]![k] = v
  else delete next[r]![k]
  put(next)
}
function setLink(v?: string) {
  const next = { ...regions.value }
  if (v && isHex(v)) next.link = v
  else delete next.link
  put(next)
}
</script>

<template>
  <details class="rounded-lg border border-(--ui-border) p-3">
    <summary class="flex cursor-pointer items-center gap-1 text-xs font-semibold text-(--ui-text-muted)">
      {{ t('theme.regions.title') }}
      <UTooltip :text="t('theme.regions.hint')"><UIcon name="i-lucide-info" class="size-3.5" /></UTooltip>
    </summary>
    <div class="mt-3 space-y-2">
      <div v-for="r in REGIONS" :key="r" class="grid grid-cols-[5rem_1fr_1fr] items-center gap-2 text-xs">
        <span class="truncate text-(--ui-text-muted)">{{ t(`theme.regions.${r}`) }}</span>
        <span v-for="k in (['bg', 'text'] as const)" :key="k" class="flex min-w-0 items-center gap-1.5 rounded-md border border-(--ui-border) p-1">
          <input type="color" :value="regions[r]?.[k] ?? props.base[k]" :aria-label="`${t(`theme.regions.${r}`)} · ${t(`theme.custom.${k}`)}`" class="size-7 shrink-0 cursor-pointer rounded border-0 bg-transparent p-0" @change="setColor(r, k, ($event.target as HTMLInputElement).value)">
          <code v-if="regions[r]?.[k]" class="min-w-0 truncate text-(--ui-text)">{{ regions[r]![k] }}</code>
          <span v-else class="min-w-0 truncate italic">{{ t(`theme.custom.${k}`) }} · {{ t('theme.custom.auto') }}</span>
          <button v-if="regions[r]?.[k]" type="button" class="ms-auto shrink-0 rounded p-0.5 hover:bg-(--ui-bg-elevated)" :aria-label="t('theme.custom.reset')" :title="t('theme.custom.reset')" @click.prevent="setColor(r, k)">
            <UIcon name="i-lucide-rotate-ccw" class="size-3.5" />
          </button>
        </span>
      </div>
      <div class="grid grid-cols-[5rem_1fr_1fr] items-center gap-2 text-xs">
        <span class="truncate text-(--ui-text-muted)">{{ t('theme.regions.link') }}</span>
        <span class="flex min-w-0 items-center gap-1.5 rounded-md border border-(--ui-border) p-1">
          <input type="color" :value="regions.link ?? props.base.accent" :aria-label="t('theme.regions.link')" class="size-7 shrink-0 cursor-pointer rounded border-0 bg-transparent p-0" @change="setLink(($event.target as HTMLInputElement).value)">
          <code v-if="regions.link" class="min-w-0 truncate text-(--ui-text)">{{ regions.link }}</code>
          <span v-else class="min-w-0 truncate italic">{{ t('theme.custom.auto') }}</span>
          <button v-if="regions.link" type="button" class="ms-auto shrink-0 rounded p-0.5 hover:bg-(--ui-bg-elevated)" :aria-label="t('theme.custom.reset')" :title="t('theme.custom.reset')" @click.prevent="setLink()">
            <UIcon name="i-lucide-rotate-ccw" class="size-3.5" />
          </button>
        </span>
      </div>
      <UButton v-if="any" size="xs" color="neutral" variant="ghost" icon="i-lucide-rotate-ccw" :label="t('theme.regions.resetAll')" @click="put({})" />
    </div>
  </details>
</template>
