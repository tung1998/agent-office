<script setup lang="ts">
// What a project offers its agents and what none of them may do: the
// commands it has (each agent picks from them in its own
// permissions), files no agent may change.
interface Policy { packs: CommandPack[], deny_paths: string[], worktree_links: string[], quick_check?: boolean, quick_checks?: string, merge_checks?: string, linked_projects?: string[], experts?: string[] | null }

const props = defineProps<{ projectId: string }>()
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()

const { data, refresh, error: loadError } = await useLiveFetch<{ policy: Policy, packs: CommandPack[], safe: string[], offices?: { value: string, label: string }[], agents?: { value: string, label: string }[], default_agent?: string }>(() => `/api/projects/${props.projectId}/policy`)
const safe = computed(() => new Set(data.value?.safe ?? []))
const { data: projData } = useLiveFetch<{ projects: Project[] }>('/api/projects', { lazy: true })
// this office's projects, then those other offices open to it (ADR-167)
type LinkItem = { label: string, value?: string, type?: 'label', icon?: string }
const projectItems = computed<LinkItem[][]>(() => {
  const own: LinkItem[] = (projData.value?.projects ?? []).filter(p => p.id !== props.projectId).map(p => ({ label: p.name, value: p.id }))
  const offices = data.value?.offices ?? []
  return offices.length ? [own, [{ type: 'label', label: t('policy.linkedOffice') }, ...offices.map(o => ({ label: o.label, value: o.value, icon: 'i-lucide-building-2' }))]] : [own]
})

const form = reactive({ packs: [] as CommandPack[], deny: [] as string[], links: '', quickCheck: true, quickChecks: '', mergeChecks: '', linked: [] as string[], experts: null as string[] | null })
// the agents projects linking here may ask (ADR-177): never chosen = its default agent
const experts = computed<string[]>({
  get: () => form.experts ?? (data.value?.default_agent ? [data.value.default_agent] : []),
  set: (v) => { form.experts = v },
})
const editedFrom = ref('') // the policy as the form was filled
const saveError = useSaveError()
const { stale, reset: resync } = useDraft(data, form, (d) => { // never over what is being edited
  editedFrom.value = (d as { version?: string }).version ?? ''
  form.packs = JSON.parse(JSON.stringify(d.policy.packs))
  form.deny = [...d.policy.deny_paths]
  form.links = (d.policy.worktree_links ?? []).join('\n')
  form.quickCheck = d.policy.quick_check ?? true
  form.quickChecks = d.policy.quick_checks ?? ''
  form.mergeChecks = d.policy.merge_checks ?? ''
  form.linked = [...(d.policy.linked_projects ?? [])]
  form.experts = d.policy.experts ? [...d.policy.experts] : null
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
      body: { version: editedFrom.value, packs: form.packs.filter(p => p.label.trim()), deny_paths: form.deny, worktree_links: form.links.split('\n').map(s => s.trim()).filter(Boolean), quick_check: form.quickCheck, quick_checks: form.quickChecks, merge_checks: form.mergeChecks, linked_projects: form.linked, experts: form.experts }
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
          <InfoTip :text="t('policy.commandsHelp')" icon-class="size-3.5 text-(--ui-text-dimmed)" />
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
        <InfoTip :text="t('policy.denySupports', { star: '*', starstar: '**/', slash: '/' })" icon-class="size-3.5 text-(--ui-text-dimmed)" />
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

    <!-- projects its agents may hand work to (send_to_chat, read_link) -->
    <UCard :ui="{ body: 'sm:p-4' }">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="flex items-center gap-1.5 text-sm font-medium">
          {{ t('policy.linked') }}
          <InfoTip :text="t('policy.linkedHelp')" icon-class="size-3.5 text-(--ui-text-dimmed)" />
        </p>
        <USelectMenu v-model="form.linked" :items="projectItems" value-key="value" multiple size="sm" :disabled="!isAdmin" :placeholder="t('policy.linkedPlaceholder')" class="w-full sm:w-72" />
      </div>
      <div class="mt-3 flex flex-wrap items-center justify-between gap-3">
        <p class="flex items-center gap-1.5 text-sm font-medium">
          {{ t('policy.experts') }}
          <InfoTip :text="t('policy.expertsHelp')" icon-class="size-3.5 text-(--ui-text-dimmed)" />
        </p>
        <USelectMenu v-model="experts" :items="data?.agents ?? []" value-key="value" multiple size="sm" :disabled="!isAdmin || !data" :placeholder="t('policy.expertsNone')" class="w-full sm:w-72" />
      </div>
    </UCard>

    <!-- each edited file checked at once (ADR-133) -->
    <UCard :ui="{ body: 'sm:p-4' }">
      <div class="flex items-center justify-between gap-3">
        <p class="flex items-center gap-1.5 text-sm font-medium">
          {{ t('policy.quickCheck') }}
          <InfoTip :text="t('policy.quickCheckHelp')" icon-class="size-3.5 text-(--ui-text-dimmed)" />
        </p>
        <USwitch v-model="form.quickCheck" size="sm" :disabled="!isAdmin" :aria-label="t('policy.quickCheck')" />
      </div>
      <UFormField v-if="form.quickCheck" class="mt-3">
        <template #label>
          <span class="flex items-center gap-1.5">{{ t('policy.quickChecks') }}
            <InfoTip :text="t('policy.quickChecksHelp')" icon-class="size-3.5 text-(--ui-text-muted)" />
          </span>
        </template>
        <UTextarea v-model="form.quickChecks" :rows="2" :maxlength="2000" autoresize :disabled="!isAdmin" class="w-full font-mono text-xs" placeholder=".ts .vue: npx eslint {file}" />
      </UFormField>
    </UCard>

    <!-- what a diff passes before office merges it on its own (ADR-222) -->
    <UCard :ui="{ body: 'sm:p-4' }">
      <UFormField>
        <template #label>
          <span class="flex items-center gap-1.5 text-sm font-medium">{{ t('policy.mergeChecks') }}
            <InfoTip :text="t('policy.mergeChecksHelp')" icon-class="size-3.5 text-(--ui-text-dimmed)" />
          </span>
        </template>
        <UTextarea v-model="form.mergeChecks" :rows="2" :maxlength="2000" autoresize :disabled="!isAdmin" class="w-full font-mono text-xs" placeholder="go build ./...&#10;dashboard/ .vue .ts: pnpm --dir dashboard typecheck" />
      </UFormField>
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
