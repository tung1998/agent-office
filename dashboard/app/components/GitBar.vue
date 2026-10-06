<script setup lang="ts">
// The project's git state under the page title: branch, how it stands against
// its remote, changed files; admins can commit, push and fetch from here.
import type { ProposedAction } from '~/components/ActionCard.vue'

interface GitStatus { branch: string, upstream?: string, ahead: number, behind: number, changes: { path: string, status: string }[] }

const props = defineProps<{ projectId: string }>()
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()
const Dropdown = resolveComponent('UDropdownMenu')

const repo = ref(false)
const st = ref<GitStatus | null>(null)
const busy = ref('')

function set(res: { repo: boolean, status?: GitStatus }) {
  repo.value = res.repo
  st.value = res.status ?? null
}
async function load() {
  try {
    set(await $fetch(`/api/projects/${props.projectId}/git`))
  } catch {
    repo.value = false
  }
}
// fetch: ask the remote what changed (read-only for the working tree)
async function fetchRemote(quiet = false) {
  busy.value = 'fetch'
  try {
    set(await $fetch(`/api/projects/${props.projectId}/git/fetch`, { method: 'POST', body: {} }))
  } catch (e) {
    if (!quiet) toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
async function push() {
  busy.value = 'push'
  try {
    const res = await $fetch<{ action: ProposedAction }>(`/api/projects/${props.projectId}/git/push`, { method: 'POST', body: {} })
    toast.add({ title: t('git.pushed'), description: res.action.detail, color: 'success' })
    await load()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}

const commit = reactive({ open: false, message: '', picked: [] as string[], busy: false })
function openCommit() {
  commit.message = ''
  commit.picked = (st.value?.changes ?? []).map(c => c.path)
  commit.open = true
}
async function doCommit() {
  commit.busy = true
  try {
    const res = await $fetch<{ action: ProposedAction }>(`/api/projects/${props.projectId}/git/commit`, { method: 'POST', body: { message: commit.message, files: commit.picked } })
    commit.open = false
    toast.add({ title: t('git.committed'), description: res.action.detail, color: 'success' })
    await load()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    commit.busy = false
  }
}
function toggle(path: string, on: boolean) {
  commit.picked = on ? [...commit.picked, path] : commit.picked.filter(p => p !== path)
}

// status is local and cheap: follow it; ask the remote once when opening
let timer: ReturnType<typeof setInterval> | undefined
onMounted(async () => {
  await load()
  if (repo.value && st.value?.upstream) fetchRemote(true)
  timer = setInterval(load, 30000)
  window.addEventListener('focus', load)
})
onBeforeUnmount(() => {
  clearInterval(timer)
  window.removeEventListener('focus', load)
})
watch(() => props.projectId, load)

const changes = computed(() => st.value?.changes.length ?? 0)
// what changed, file by file, and the branches: the Files tab
const viewChanges = () => navigateTo({ query: { tab: 'files', view: 'changes' } })
const menu = computed(() => [[
  { label: t('git.viewChanges'), icon: 'i-lucide-file-diff', disabled: !changes.value, onSelect: viewChanges },
  { label: t('git.commit'), icon: 'i-lucide-git-commit-horizontal', disabled: !changes.value, onSelect: openCommit },
  { label: st.value?.upstream ? t('git.push', { n: st.value.ahead }) : t('git.pushNew'), icon: 'i-lucide-upload', disabled: !!st.value?.upstream && !st.value.ahead, onSelect: push }
], [
  { label: t('git.fetch'), icon: 'i-lucide-refresh-cw', onSelect: () => fetchRemote() }
]])
</script>

<template>
  <div v-if="repo && st" class="flex min-w-0 items-center gap-2">
  <component :is="isAdmin ? Dropdown : 'div'" :items="menu" :content="{ align: 'start' }">
    <button type="button" class="flex min-w-0 items-center gap-1.5 hover:text-(--ui-text)" :disabled="!isAdmin" :title="st.upstream || t('git.noUpstream')">
      <UIcon :name="busy ? 'i-lucide-loader-circle' : 'i-lucide-git-branch'" class="size-3 shrink-0" :class="busy && 'animate-spin'" />
      <span class="truncate font-mono">{{ st.branch || 'HEAD' }}</span>
      <template v-if="!st.upstream">
        <span class="shrink-0">· {{ t('git.noUpstream') }}</span>
      </template>
      <template v-else-if="!st.ahead && !st.behind">
        <UIcon name="i-lucide-check" class="size-3 shrink-0 text-(--ui-success)" />
        <span class="hidden shrink-0 sm:inline">{{ t('git.synced') }}</span>
      </template>
      <template v-else>
        <span v-if="st.ahead" class="shrink-0 tabular-nums" :title="t('git.aheadTitle', { n: st.ahead })">↑{{ st.ahead }}</span>
        <span v-if="st.behind" class="shrink-0 tabular-nums text-(--ui-warning)" :title="t('git.behindTitle', { n: st.behind })">↓{{ st.behind }}</span>
      </template>
      <span v-if="changes" class="shrink-0">· {{ t('git.changes', { n: changes }) }}</span>
      <UIcon v-if="isAdmin" name="i-lucide-chevron-down" class="size-3 shrink-0" />
    </button>
  </component>
  <button
    v-if="changes && isAdmin" type="button" class="shrink-0 text-primary underline-offset-2 hover:underline"
    @click="viewChanges"
  >
    {{ t('git.viewChanges') }}
  </button>
  </div>

  <UModal v-model:open="commit.open" :title="t('git.commitTitle', { branch: st?.branch ?? '' })">
    <template #body>
      <div class="space-y-3">
        <UTextarea v-model="commit.message" :rows="3" autoresize class="w-full" :placeholder="t('git.messagePlaceholder')" />
        <p class="text-xs text-(--ui-text-muted)">{{ t('git.files', { picked: commit.picked.length, total: changes }) }}</p>
        <div class="max-h-64 space-y-1 overflow-y-auto">
          <label v-for="c in st?.changes ?? []" :key="c.path" class="flex items-center gap-2 text-xs">
            <UCheckbox :model-value="commit.picked.includes(c.path)" @update:model-value="(v: boolean | 'indeterminate') => toggle(c.path, v === true)" />
            <span class="w-5 shrink-0 font-mono text-(--ui-text-muted)">{{ c.status }}</span>
            <span class="truncate font-mono">{{ c.path }}</span>
          </label>
        </div>
      </div>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="commit.open = false" />
        <UButton icon="i-lucide-git-commit-horizontal" :label="t('git.commit')" :loading="commit.busy" :disabled="!commit.message.trim() || !commit.picked.length" @click="doCommit" />
      </div>
    </template>
  </UModal>
</template>
