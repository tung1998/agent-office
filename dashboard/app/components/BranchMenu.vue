<script setup lang="ts">
// The project folder's branches (the Files tab): which one is checked out,
// switch to another, make a new one, delete a merged one. git refuses what
// would lose work; its message is shown as it is.
interface Branch { name: string, current: boolean, upstream?: string, track?: string, date: string, subject: string }

const props = defineProps<{ projectId: string }>()
const emit = defineEmits<{ changed: [] }>()
const toast = useToast()
const { t, dateLocale } = useLang()
const base = computed(() => `/api/projects/${props.projectId}/git`)

const open = ref(false)
const branches = ref<Branch[] | null>(null)
const busy = ref('')
const current = computed(() => branches.value?.find(b => b.current)?.name ?? '')

async function load() {
  try {
    branches.value = (await $fetch<{ branches: Branch[] }>(`${base.value}/branches`)).branches
  } catch (e) {
    branches.value = []
    toast.add({ title: apiError(e), color: 'error' })
  }
}
onMounted(load)
watch(open, (o) => { if (o) load() })
watch(() => props.projectId, load)

async function run(key: string, fn: () => Promise<unknown>, done: string) {
  busy.value = key
  try {
    await fn()
    toast.add({ title: done, color: 'success' })
    await load()
    emit('changed')
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
function switchTo(b: Branch) {
  if (b.current || !confirm(t('branch.switchConfirm', { name: b.name }))) return
  run('s' + b.name, () => $fetch(`${base.value}/switch`, { method: 'POST', body: { name: b.name } }), t('branch.switched', { name: b.name }))
}
function remove(b: Branch) {
  if (!confirm(t('branch.deleteConfirm', { name: b.name }))) return
  run('d' + b.name, () => $fetch(`${base.value}/branches/delete`, { method: 'POST', body: { name: b.name } }), t('branch.deleted', { name: b.name }))
}
function create() {
  const name = prompt(t('branch.newPrompt'))?.trim()
  if (!name) return
  run('new', () => $fetch(`${base.value}/branches`, { method: 'POST', body: { name } }), t('branch.created', { name }))
}
const when = (d: string) => d ? new Date(d).toLocaleDateString(dateLocale.value, { day: '2-digit', month: '2-digit' }) : ''
defineExpose({ load })
</script>

<template>
  <UPopover v-model:open="open" :content="{ align: 'start' }">
    <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-git-branch" trailing-icon="i-lucide-chevron-down" :loading="!!busy" class="min-w-0 max-w-40" :title="t('branch.title')">
      <span class="truncate font-mono">{{ current || '…' }}</span>
    </UButton>
    <template #content>
      <div class="w-80 max-w-[90vw]">
        <div class="max-h-80 overflow-y-auto py-1">
          <LoadingRows v-if="!branches" :n="3" :icon="false" />
          <div v-for="b in branches ?? []" :key="b.name" class="group flex items-center gap-2 px-2 py-1 hover:bg-(--ui-bg-elevated)">
            <button type="button" class="flex min-w-0 flex-1 items-center gap-2 text-left" :disabled="b.current" :title="b.current ? '' : t('branch.switch')" @click="switchTo(b)">
              <UIcon :name="b.current ? 'i-lucide-check' : 'i-lucide-git-branch'" class="size-3.5 shrink-0" :class="b.current ? 'text-(--ui-success)' : 'text-(--ui-text-muted)'" />
              <span class="min-w-0 flex-1">
                <span class="block truncate font-mono text-xs" :class="b.current && 'font-semibold'">{{ b.name }} <span v-if="b.track" class="text-(--ui-text-muted)">{{ b.track }}</span></span>
                <span class="block truncate text-[11px] text-(--ui-text-muted)">{{ when(b.date) }} · {{ b.subject }}</span>
              </span>
            </button>
            <UButton
              v-if="!b.current" size="xs" color="neutral" variant="ghost" icon="i-lucide-trash-2" class="opacity-0 group-hover:opacity-100 max-sm:opacity-100"
              :aria-label="t('branch.delete')" :title="t('branch.delete')" :loading="busy === 'd' + b.name" @click="remove(b)"
            />
          </div>
        </div>
        <div class="border-t border-(--ui-border) p-1">
          <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-plus" :label="t('branch.new')" block class="justify-start" :loading="busy === 'new'" @click="create" />
        </div>
      </div>
    </template>
  </UPopover>
</template>
