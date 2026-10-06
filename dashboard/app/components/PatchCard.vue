<script setup lang="ts">
export interface Patch {
  id: string
  diff: string
  files: string[]
  status: 'pending' | 'applied' | 'rejected' | 'failed'
  detail: string
  decided_by: string
  origin?: '' | 'worktree'
  // a big diff comes cut short (truncated); size/add/del are of the whole
  truncated?: boolean
  size?: number
  add?: number
  del?: number
}

const props = defineProps<{ patch: Patch }>()
const emit = defineEmits<{ updated: [Patch] }>()
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()
const busy = ref<'' | 'approve' | 'reject' | 'skip'>('')
const open = ref(props.patch.status === 'pending' || props.patch.status === 'failed')

// the whole diff, once asked for ("Xem thêm"); until then what came with the chat
const full = ref<string | null>(null)
const loadingFull = ref(false)
const diffText = computed(() => full.value ?? props.patch.diff)
const cut = computed(() => !!props.patch.truncated && full.value === null)
const sizeLabel = (n = 0) => n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`
async function loadFull() {
  loadingFull.value = true
  try {
    full.value = (await $fetch<{ patch: Patch }>(`/api/patches/${props.patch.id}`)).patch.diff
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    loadingFull.value = false
  }
}
const stats = computed(() => {
  if (props.patch.add !== undefined && props.patch.del !== undefined) return { add: props.patch.add, del: props.patch.del }
  const lines = props.patch.diff.split('\n')
  return {
    add: lines.filter(l => l.startsWith('+') && !l.startsWith('+++')).length,
    del: lines.filter(l => l.startsWith('-') && !l.startsWith('---')).length
  }
})

const statusMeta = computed<Record<Patch['status'], { label: string, color: 'warning' | 'success' | 'neutral' | 'error', icon: string }>>(() => ({
  pending: { label: t('patch.pending'), color: 'warning', icon: 'i-lucide-clock' },
  applied: { label: t('patch.applied'), color: 'success', icon: 'i-lucide-circle-check' },
  rejected: { label: t('patch.rejected'), color: 'neutral', icon: 'i-lucide-circle-x' },
  failed: { label: t('patch.failed'), color: 'error', icon: 'i-lucide-triangle-alert' }
}))

// skip: rejected, and its agent is not run again about it
async function decide(approve: boolean, skip = false) {
  busy.value = approve ? 'approve' : skip ? 'skip' : 'reject'
  try {
    const res = await $fetch<{ patch: Patch }>(`/api/patches/${props.patch.id}/${approve ? 'approve' : 'reject'}`, { method: 'POST', body: skip ? { skip: true } : {} })
    emit('updated', res.patch)
    if (res.patch.status === 'applied') toast.add({ title: t('patch.appliedToast'), description: res.patch.files.join(', '), color: 'success' })
    if (res.patch.status === 'failed') toast.add({ title: t('patch.failedToast'), description: res.patch.detail, color: 'error' })
    open.value = res.patch.status === 'failed'
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
</script>

<template>
  <div class="overflow-hidden rounded-lg border border-(--ui-border)">
    <div class="flex flex-wrap items-center gap-2 bg-(--ui-bg-muted) px-3 py-2 text-sm">
      <button type="button" class="flex min-w-0 flex-1 items-center gap-2 text-left" @click="open = !open">
        <UIcon :name="open ? 'i-lucide-chevron-down' : 'i-lucide-chevron-right'" class="size-4 shrink-0" />
        <UIcon name="i-lucide-file-diff" class="size-4 shrink-0 text-primary" />
        <span class="truncate font-mono text-xs">{{ patch.files.join(', ') }}</span>
        <span class="shrink-0 text-xs"><span class="text-(--ui-success)">+{{ stats.add }}</span> <span class="text-(--ui-error)">−{{ stats.del }}</span></span>
      </button>
      <UBadge :label="statusMeta[patch.status].label" :color="statusMeta[patch.status].color" :icon="statusMeta[patch.status].icon" variant="subtle" size="sm" />
      <UBadge v-if="patch.origin === 'worktree'" color="neutral" variant="outline" size="sm" icon="i-lucide-git-branch" :label="t('patch.worktree')" :title="t('patch.worktreeHint')" />
      <UBadge v-if="patch.decided_by?.startsWith('auto:')" color="warning" variant="outline" size="sm" icon="i-lucide-zap" :label="t('patch.auto')" :title="patch.decided_by.slice(5)" />
      <template v-if="patch.status === 'pending' && isAdmin">
        <UTooltip :text="t('action.skipInfo')">
          <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-skip-forward" :label="t('action.skip')" :loading="busy === 'skip'" :disabled="!!busy" @click="decide(false, true)" />
        </UTooltip>
        <UButton size="xs" color="neutral" variant="ghost" :label="t('patch.reject')" :loading="busy === 'reject'" :disabled="!!busy" @click="decide(false)" />
        <UButton size="xs" icon="i-lucide-check" :label="patch.origin === 'worktree' ? t('patch.merge') : t('patch.approveApply')" :loading="busy === 'approve'" :disabled="!!busy" @click="decide(true)" />
      </template>
    </div>
    <p v-if="patch.detail && patch.status !== 'applied'" class="border-t border-(--ui-border) px-3 py-1.5 text-xs text-(--ui-error)">{{ patch.detail }}</p>
    <DiffView v-if="open" :diff="diffText" class="max-h-96 border-t border-(--ui-border)" />
    <div v-if="open && cut" class="flex items-center gap-2 border-t border-(--ui-border) px-3 py-1.5 text-xs text-(--ui-text-muted)">
      <span class="flex-1">{{ t('patch.cut', { size: sizeLabel(patch.size) }) }}</span>
      <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-chevrons-down" :label="t('patch.loadFull')" :loading="loadingFull" @click="loadFull" />
    </div>
  </div>
</template>
