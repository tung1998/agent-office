<script setup lang="ts">
// What a project offers its agents and what none of them may do: the
// commands it has (each agent picks from them in its own
// permissions), files no agent may change.
interface Policy { packs: CommandPack[], deny_paths: string[], worktree_links: string[] }

const props = defineProps<{ projectId: string }>()
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()

const { data, refresh, error: loadError } = await useLiveFetch<{ policy: Policy, packs: CommandPack[], safe: string[] }>(() => `/api/projects/${props.projectId}/policy`)
const safe = computed(() => new Set(data.value?.safe ?? []))

const form = reactive({ packs: [] as CommandPack[], deny: [] as string[], links: '' })
const editedFrom = ref('') // the policy as the form was filled
const saveError = useSaveError()
const { stale, reset: resync } = useDraft(data, form, (d) => { // never over what is being edited
  editedFrom.value = (d as { version?: string }).version ?? ''
  form.packs = JSON.parse(JSON.stringify(d.policy.packs))
  form.deny = [...d.policy.deny_paths]
  form.links = (d.policy.worktree_links ?? []).join('\n')
})

const newDeny = ref('')
function addDeny() {
  const v = newDeny.value.trim()
  if (v && !form.deny.includes(v)) form.deny.push(v)
  newDeny.value = ''
}
const advanced = ref(false)
const saving = ref(false)
async function save() {
  saving.value = true
  try {
    await $fetch(`/api/projects/${props.projectId}/policy`, {
      method: 'PUT',
      body: { version: editedFrom.value, packs: form.packs.filter(p => p.label.trim()), deny_paths: form.deny, worktree_links: form.links.split('\n').map(s => s.trim()).filter(Boolean) }
    })
    toast.add({ title: t('policy.saved'), color: 'success' })
    await refresh()
    resync() // saved: take what the server has now
  } catch (e) {
    saveError(e, async () => { await refresh(); resync() })
  } finally {
    saving.value = false
  }
}

// ---- the project's commands: detected packs + its own packs ----
const packs = computed<CommandPack[]>(() => [...(data.value?.packs ?? []).filter(p => !p.custom), ...form.packs.map(p => ({ ...p, custom: true }))])
const total = computed(() => new Set(packs.value.flatMap(p => p.commands)).size)
const newCmd = reactive<Record<string, string>>({})
function addCmd(p: CommandPack) {
  const pack = form.packs.find(x => x.id === p.id)
  const c = (newCmd[p.id] ?? '').trim().replace(/\s+/g, ' ')
  if (!pack || !c) return
  if (/[;&|<>$`\\]/.test(c)) {
    toast.add({ title: t('policy.shellWarning'), color: 'warning' })
    return
  }
  if (!pack.commands.includes(c)) pack.commands.push(c)
  newCmd[p.id] = ''
}
function removeCmd(p: CommandPack, c: string) {
  if (!confirm(t('policy.removeCmdConfirm', { cmd: c }))) return
  const pack = form.packs.find(x => x.id === p.id)
  if (pack) pack.commands = pack.commands.filter(x => x !== c)
}
function addPack() {
  form.packs.push({ id: `custom-new-${Date.now()}`, label: t('policy.newPackLabel', { n: form.packs.length + 1 }), commands: [] })
}
function removePack(p: CommandPack) {
  if (!confirm(t('policy.removePackConfirm', { label: p.label }))) return
  form.packs = form.packs.filter(x => x.id !== p.id)
}
</script>

<template>
  <div class="max-w-4xl space-y-3">
    <StaleNotice :show="stale" @reload="resync" />
    <UAlert v-if="loadError" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="t('common.loadError')" :actions="[{ label: t('common.refresh'), onClick: () => refresh() }]" />
    <!-- save stays in reach while scrolling -->
    <div v-if="isAdmin" class="sticky top-0 z-10 flex justify-end bg-(--ui-bg)/80 py-1 backdrop-blur">
      <UButton icon="i-lucide-save" size="sm" :label="t('policy.save')" :loading="saving" @click="save" />
    </div>
    <!-- the commands agents can be given -->
    <UCard :ui="{ body: 'space-y-3 sm:p-4' }">
      <div class="flex items-center gap-2">
        <p class="flex items-center gap-1.5 text-sm font-medium">
          {{ t('policy.commandsTitle') }}
          <UTooltip :text="t('policy.commandsHelp')"><UIcon name="i-lucide-info" class="size-3.5 text-(--ui-text-dimmed)" /></UTooltip>
        </p>
        <UBadge color="neutral" variant="subtle" size="sm" :label="t('policy.commandsCount', { n: total })" />
        <span class="flex items-center gap-1 text-xs text-(--ui-text-muted)">
          <UIcon name="i-lucide-shield-check" class="size-3.5 text-(--ui-success)" />{{ t('policy.safeLegend') }}
        </span>
        <UButton v-if="isAdmin" size="xs" color="neutral" variant="ghost" icon="i-lucide-plus" :label="t('policy.addPack')" class="ms-auto" @click="addPack" />
      </div>
      <div class="grid gap-2 sm:grid-cols-2">
        <details v-for="p in packs" :key="p.id" class="group rounded-lg border border-(--ui-border)" :open="p.custom">
          <summary class="flex cursor-pointer list-none items-center gap-2 px-3 py-2">
            <UIcon :name="p.icon || 'i-lucide-package'" class="size-4 text-primary" />
            <UInput v-if="p.custom && isAdmin" v-model="form.packs.find(x => x.id === p.id)!.label" size="xs" variant="none" class="min-w-0 flex-1 font-medium" @click.stop />
            <span v-else class="min-w-0 flex-1 truncate text-sm font-medium">{{ p.label }}</span>
            <span class="text-xs tabular-nums text-(--ui-text-muted)">{{ p.commands.length }}</span>
            <UIcon name="i-lucide-chevron-down" class="size-4 text-(--ui-text-dimmed) transition group-open:rotate-180" />
          </summary>
          <div class="space-y-0.5 border-t border-(--ui-border) px-3 py-2">
            <div v-for="c in p.commands" :key="c" class="group/cmd flex items-center gap-2 py-0.5 text-xs">
              <UIcon :name="safe.has(c) ? 'i-lucide-shield-check' : 'i-lucide-terminal'" class="size-3.5 shrink-0" :class="safe.has(c) ? 'text-(--ui-success)' : 'text-(--ui-text-dimmed)'" />
              <code class="min-w-0 flex-1 truncate">{{ c }}</code>
              <button v-if="p.custom && isAdmin" type="button" class="invisible text-(--ui-text-dimmed) group-hover/cmd:visible" :title="t('policy.removeCmd')" @click="removeCmd(p, c)">
                <UIcon name="i-lucide-x" class="size-3.5" />
              </button>
            </div>
            <form v-if="p.custom && isAdmin" class="flex items-center gap-1 pt-1" @submit.prevent="addCmd(p)">
              <UInput v-model="newCmd[p.id]" size="xs" :placeholder="t('policy.newCmdPlaceholder')" class="flex-1 font-mono" />
              <UButton type="submit" size="xs" color="neutral" variant="outline" icon="i-lucide-plus" />
              <UButton size="xs" color="error" variant="ghost" icon="i-lucide-trash-2" :title="t('policy.removePack')" @click="removePack(p)" />
            </form>
          </div>
        </details>
      </div>
    </UCard>

    <!-- files no agent may change -->
    <UCard :ui="{ body: 'space-y-3 sm:p-4' }">
      <p class="flex items-center gap-1.5 text-sm font-medium">
        {{ t('policy.denyTitle') }}
        <UTooltip :text="t('policy.denySupports', { star: '*', starstar: '**/', slash: '/' })"><UIcon name="i-lucide-info" class="size-3.5 text-(--ui-text-dimmed)" /></UTooltip>
      </p>
      <div class="flex flex-wrap items-center gap-1.5">
        <UBadge v-for="d in form.deny" :key="d" color="neutral" variant="subtle" class="font-mono">
          {{ d }}
          <button v-if="isAdmin" type="button" class="ms-0.5 text-(--ui-text-dimmed) hover:text-(--ui-text)" :aria-label="t('policy.removeCmd')" @click="form.deny = form.deny.filter(x => x !== d)">
            <UIcon name="i-lucide-x" class="size-3" />
          </button>
        </UBadge>
        <form v-if="isAdmin" class="flex items-center gap-1" @submit.prevent="addDeny">
          <UInput v-model="newDeny" size="xs" placeholder="migrations/" class="w-36 font-mono" />
          <UButton type="submit" size="xs" color="neutral" variant="ghost" icon="i-lucide-plus" />
        </form>
      </div>
    </UCard>

    <div>
      <button type="button" class="flex items-center gap-1 text-sm text-(--ui-text-muted) hover:text-(--ui-text)" @click="advanced = !advanced">
        <UIcon :name="advanced ? 'i-lucide-chevron-down' : 'i-lucide-chevron-right'" class="size-4" />{{ t('policy.advanced') }}
      </button>
      <UCard v-if="advanced" class="mt-2" :ui="{ body: 'space-y-4 sm:p-4' }">
        <UFormField :label="t('policy.worktreeLinks')" :description="t('policy.worktreeLinksDesc')">
          <UTextarea v-model="form.links" :rows="2" :disabled="!isAdmin" class="w-full font-mono" placeholder="apps/web/.cache" />
        </UFormField>
      </UCard>
    </div>
  </div>
</template>
