<script setup lang="ts">
export interface Patch {
  id: string
  diff: string
  files: string[]
  status: 'pending' | 'queued' | 'applied' | 'rejected' | 'failed'
  deciding?: string // who is deciding it now (it says pending until done)
  detail: string
  decided_by: string
  origin?: '' | 'worktree'
  // a big diff comes cut short (truncated); size/add/del are of the whole
  truncated?: boolean
  size?: number
  add?: number
  del?: number
  // its number among the chat's proposals: they are approved in that order
  seq?: number
}

// stale: from an older answer of its agent
const props = defineProps<{ patch: Patch, projectId?: string, stale?: boolean }>()
const emit = defineEmits<{ updated: [Patch] }>()
const toast = useToast()
const auth = useAuth()
const isAdmin = computed(() => props.projectId ? auth.manages(props.projectId) : auth.isAdmin.value) // on any page (ADR-224)
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
  queued: { label: t('card.queued'), color: 'neutral', icon: 'i-lucide-list-ordered' },
  applied: { label: t('patch.applied'), color: 'success', icon: 'i-lucide-circle-check' },
  rejected: { label: t('patch.rejected'), color: 'neutral', icon: 'i-lucide-circle-x' },
  failed: { label: t('patch.failed'), color: 'error', icon: 'i-lucide-triangle-alert' }
}))

// a diff this big is rarely what was meant (a whole branch copied in): it warns, and asks once more
const BIG_FILES = 100
const BIG_SIZE = 1 << 20
const big = computed(() => props.patch.files.length > BIG_FILES || (props.patch.size ?? props.patch.diff.length) > BIG_SIZE)
const num = computed(() => props.patch.seq)

// a queued diff is applied once those before it are decided: watched until then
useQueuedWatch(() => props.patch.status === 'queued', async () => {
  const res = await $fetch<{ patch: Patch }>(`/api/patches/${props.patch.id}?brief=1`)
  if (res.patch.status !== 'queued') emit('updated', res.patch)
})

// skip: rejected, and its agent is not run again about it
async function decide(approve: boolean, skip = false) {
  if (approve && big.value && !confirm(t('patch.bigConfirm', { n: props.patch.files.length, size: sizeLabel(props.patch.size ?? props.patch.diff.length) }))) return
  if (approve && props.stale && !confirm(t('card.staleConfirm'))) return
  busy.value = approve ? 'approve' : skip ? 'skip' : 'reject'
  try {
    const res = await $fetch<{ patch: Patch, queued_after?: number }>(`/api/patches/${props.patch.id}/${approve ? 'approve' : 'reject'}`, { method: 'POST', body: skip ? { skip: true } : {} })
    emit('updated', res.patch)
    if (res.patch.status === 'queued') toast.add({ title: t('card.queuedToast', { n: res.queued_after ?? '' }), color: 'info' })
    if (res.patch.status === 'applied') toast.add({ title: t('patch.appliedToast'), description: res.patch.files.join(', '), color: 'success' })
    if (res.patch.status === 'failed') toast.add({ title: t('patch.failedToast'), description: res.patch.detail, color: 'error' })
    open.value = res.patch.status === 'failed'
  } catch (e) {
    const p = (e as { data?: { patch?: Patch } }).data?.patch
    if (p) emit('updated', { ...props.patch, ...p, diff: props.patch.diff })
    const d = alreadyDecided(e)
    toast.add(d ? { title: alreadyDecidedText(d, t), color: 'info' } : { title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
</script>

<template>
  <div class="overflow-hidden rounded-lg border border-(--ui-border)">
    <div class="flex flex-wrap items-center gap-2 bg-(--ui-bg-muted) px-3 py-2 text-sm">
      <button type="button" class="flex min-w-0 flex-1 basis-48 items-center gap-2 text-left" @click="open = !open">
        <UIcon :name="open ? 'i-lucide-chevron-down' : 'i-lucide-chevron-right'" class="size-4 shrink-0" />
        <span v-if="num" class="shrink-0 font-mono text-xs text-(--ui-text-muted)">#{{ num }}</span>
        <UIcon name="i-lucide-file-diff" class="size-4 shrink-0 text-primary" />
        <span class="truncate font-mono text-xs">{{ patch.files.join(', ') }}</span>
        <span class="shrink-0 text-xs"><span class="text-(--ui-success)">+{{ stats.add }}</span> <span class="text-(--ui-error)">−{{ stats.del }}</span></span>
      </button>
      <UBadge v-if="patch.status === 'pending' && patch.deciding" color="info" variant="subtle" size="sm" icon="i-lucide-loader-circle" :ui="{ leadingIcon: 'animate-spin' }" :label="t('card.deciding')" :title="patch.deciding" />
      <UBadge v-else :label="statusMeta[patch.status].label" :color="statusMeta[patch.status].color" :icon="statusMeta[patch.status].icon" variant="subtle" size="sm" />
      <UBadge v-if="stale && patch.status === 'pending'" color="warning" variant="outline" size="sm" icon="i-lucide-history" :label="t('card.stale')" :title="t('card.staleHint')" />
      <UBadge v-if="patch.origin === 'worktree'" color="neutral" variant="outline" size="sm" icon="i-lucide-git-branch" :label="t('patch.worktree')" :title="t('patch.worktreeHint')" />
      <UBadge v-if="patch.decided_by?.startsWith('auto:')" color="warning" variant="outline" size="sm" icon="i-lucide-zap" :label="t('patch.auto')" :title="patch.decided_by.slice(5)" />
      <UButton v-if="patch.status === 'queued' && isAdmin" size="xs" color="neutral" variant="ghost" :label="t('card.unqueue')" :loading="busy === 'reject'" :disabled="!!busy" @click="decide(false)" />
      <template v-if="patch.status === 'pending' && !patch.deciding && isAdmin">
        <UTooltip :text="t('action.skipInfo')">
          <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-skip-forward" :label="t('action.skip')" :loading="busy === 'skip'" :disabled="!!busy" @click="decide(false, true)" />
        </UTooltip>
        <UButton size="xs" color="neutral" variant="ghost" :label="t('patch.reject')" :loading="busy === 'reject'" :disabled="!!busy" @click="decide(false)" />
        <UButton size="xs" icon="i-lucide-check" :label="patch.origin === 'worktree' ? t('patch.merge') : t('patch.approveApply')" :loading="busy === 'approve'" :disabled="!!busy" @click="decide(true)" />
      </template>
    </div>
    <p v-if="big && patch.status === 'pending'" class="flex items-center gap-1.5 border-t border-(--ui-border) bg-(--ui-error)/10 px-3 py-1.5 text-xs font-medium text-(--ui-error)">
      <UIcon name="i-lucide-triangle-alert" class="size-4 shrink-0" />{{ t('patch.big', { n: patch.files.length, size: sizeLabel(patch.size ?? patch.diff.length) }) }}
    </p>
    <p v-if="patch.detail && patch.status !== 'applied'" class="border-t border-(--ui-border) px-3 py-1.5 text-xs text-(--ui-error)">{{ patch.detail }}</p>
    <DiffView v-if="open" :diff="diffText" class="max-h-96 border-t border-(--ui-border)" />
    <div v-if="open && cut" class="flex items-center gap-2 border-t border-(--ui-border) px-3 py-1.5 text-xs text-(--ui-text-muted)">
      <span class="flex-1">{{ t('patch.cut', { size: sizeLabel(patch.size) }) }}</span>
      <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-chevrons-down" :label="t('patch.loadFull')" :loading="loadingFull" @click="loadFull" />
    </div>
  </div>
</template>
