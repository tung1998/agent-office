<script setup lang="ts">
import type { Permissions } from '~/composables/useOffice'
// An agent's permissions: pick a package (a preset), then switch single
// capabilities on or off and narrow the commands it may run by itself.
// ADR-074: admin-only full access and extra directories.
const perms = defineModel<Permissions>({ required: true })
const props = defineProps<{ projectId?: string, disabled?: boolean }>()
const { t } = useLang()
const { isAdmin } = useAuth()

const level = computed(() => agentLevel(perms.value))
const caps = computed(() => agentCaps(perms.value))
const custom = computed(() => !!perms.value.caps)
const base = computed(() => perms.value.level ?? level.value)

function pickPreset(l: PermLevel) {
  perms.value.level = l
  perms.value.caps = null
  perms.value.read_only = l === 'read'
}

// a pick that equals a package's preset goes back to being that package
function setCaps(next: string[]) {
  const same = permLevels.find(p => {
    const pre = presetCaps(p.level)
    return pre.length === next.length && pre.every(c => next.includes(c))
  })
  if (same) return pickPreset(same.level)
  perms.value.caps = permCaps.map(c => c.id).filter(id => next.includes(id))
  perms.value.read_only = agentLevel(perms.value) === 'read'
}
function toggle(id: string, on: boolean) {
  let next = caps.value.filter(c => c !== id)
  if (on) next.push(id)
  if (on && id !== 'propose' && !next.includes('propose')) next.push('propose') // acting alone implies proposing
  if (!on && id === 'propose') next = [] // nothing is done alone without proposing
  setCaps(next)
}

// ---- commands it may run, from the project's catalog (default: the safe ones) ----
const { data: pol } = useLiveFetch<{ packs: CommandPack[], safe: string[] }>(
  () => `/api/projects/${props.projectId}/policy`, { lazy: true, immediate: !!props.projectId })
const safe = computed(() => pol.value?.safe ?? [])
const tree = computed(() => {
  const seen = new Set<string>()
  const groups: { id: string, label: string, icon?: string, commands: string[] }[] = []
  for (const p of pol.value?.packs ?? []) {
    const cmds = p.commands.filter(c => !seen.has(c))
    cmds.forEach(c => seen.add(c))
    if (cmds.length) groups.push({ ...p, commands: cmds })
  }
  return groups
})
const byDefault = computed(() => perms.value.commands == null)
const picked = computed(() => perms.value.commands ?? safe.value)
function setDefault(on: boolean) { perms.value.commands = on ? null : [...safe.value] }
function toggleCmd(c: string, on: boolean) {
  const next = picked.value.filter(x => x !== c)
  if (on) next.push(c)
  perms.value.commands = next
}
function packState(cmds: string[]): boolean | 'indeterminate' {
  const n = cmds.filter(c => picked.value.includes(c)).length
  return n === 0 ? false : n === cmds.length ? true : 'indeterminate'
}
function togglePack(cmds: string[]) {
  const on = packState(cmds) !== true
  const next = picked.value.filter(c => !cmds.includes(c))
  perms.value.commands = on ? [...next, ...cmds] : next
}

// ---- processes and containers it may run/restart (default: check jobs, no containers) ----
interface Proc { id: string, name: string, kind: 'service' | 'job' }
const { data: procData } = useLiveFetch<{ processes: Proc[] }>(() => `/api/projects/${props.projectId}/processes`, { lazy: true, immediate: !!props.projectId })
const { data: composeData } = useLiveFetch<{ services: { name: string }[] }>(() => `/api/projects/${props.projectId}/compose`, { lazy: true, immediate: !!props.projectId })
const procs = computed(() => procData.value?.processes ?? [])
const services = computed(() => composeData.value?.services ?? [])
const pickedProcs = computed(() => perms.value.processes ?? procs.value.filter(p => p.kind === 'job').map(p => p.id))
const pickedContainers = computed(() => perms.value.containers ?? [])
function toggleIn(key: 'processes' | 'containers', current: string[], v: string, on: boolean) {
  const next = current.filter(x => x !== v)
  if (on) next.push(v)
  perms.value[key] = next
}
</script>

<template>
  <div class="space-y-3">
    <!-- ADR-074: full access (admin-only) -->
    <div v-if="isAdmin" class="rounded-lg border border-(--ui-border) p-3">
      <label class="flex cursor-pointer items-start gap-2.5">
        <USwitch size="sm" class="mt-0.5" :model-value="perms.full_access ?? false" @update:model-value="(v: boolean) => perms.full_access = v" />
        <span class="min-w-0 flex-1">
          <span class="text-sm font-medium">{{ t('org.agent.fullAccess') }}</span>
          <span class="block text-xs text-(--ui-text-muted)">{{ t('org.agent.fullAccessHint') }}</span>
          <span v-if="perms.full_access && perms.full_access_by" class="block text-xs text-(--ui-text-muted) italic">{{ t('org.agent.fullAccessBy', { who: perms.full_access_by }) }}</span>
        </span>
      </label>
    </div>
    <div v-else-if="perms.full_access" class="rounded-lg border border-(--ui-border) bg-(--ui-bg-elevated)/50 p-3 opacity-60">
      <p class="flex items-center gap-1.5 text-xs font-medium text-(--ui-text-muted)">
        <UIcon name="i-lucide-lock" class="size-4" />{{ t('org.agent.permissionsLocked') }}
      </p>
    </div>

    <!-- packages -->
    <div class="flex flex-wrap gap-1 rounded-lg bg-(--ui-bg-elevated) p-1">
      <button
        v-for="p in permLevels" :key="p.level" type="button" :disabled="disabled"
        class="flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs font-medium transition"
        :class="!custom && level === p.level ? 'bg-(--ui-bg) text-(--ui-text-highlighted) shadow-sm' : 'text-(--ui-text-muted) hover:text-(--ui-text)'"
        @click="pickPreset(p.level)"
      >
        <UIcon :name="p.icon" class="size-3.5" :class="!custom && level === p.level ? 'text-primary' : ''" />{{ p.label }}
      </button>
      <span v-if="custom" class="flex items-center gap-1.5 rounded-md bg-(--ui-bg) px-2.5 py-1.5 text-xs font-medium text-(--ui-text-highlighted) shadow-sm">
        <UIcon name="i-lucide-sliders-horizontal" class="size-3.5 text-primary" />{{ t('perm.editor.custom') }}
      </span>
    </div>
    <p class="text-xs text-(--ui-text-muted)">
      <template v-if="custom">
        {{ t('perm.editor.customFrom', { label: permOf(base).label }) }}
        <button v-if="!disabled" type="button" class="text-primary underline-offset-2 hover:underline" @click="pickPreset(base)">{{ t('perm.editor.backToPreset', { label: permOf(base).label }) }}</button>
      </template>
      <template v-else>{{ t('perm.editor.presetHint', { description: permOf(level).description }) }}</template>
    </p>

    <!-- single capabilities, by group -->
    <div class="grid gap-2 sm:grid-cols-2">
      <div v-for="g in permGroups" :key="g.id" class="rounded-lg border border-(--ui-border)">
        <p class="flex items-center gap-1.5 border-b border-(--ui-border) px-3 py-1.5 text-xs font-medium text-(--ui-text-muted)">
          <UIcon :name="g.icon" class="size-3.5" />{{ g.label }}
        </p>
        <div class="divide-y divide-(--ui-border)">
          <label
            v-for="c in permCaps.filter(x => x.group === g.id)" :key="c.id"
            class="flex items-start gap-2.5 px-3 py-2" :class="disabled ? '' : 'cursor-pointer hover:bg-(--ui-bg-elevated)/50'"
          >
            <USwitch size="sm" class="mt-0.5" :model-value="caps.includes(c.id)" :disabled="disabled" @update:model-value="(v: boolean) => toggle(c.id, v)" />
            <span class="min-w-0 flex-1">
              <span class="text-sm">{{ c.label }}</span>
              <span class="block text-xs text-(--ui-text-muted)">{{ c.description }}</span>
            </span>
            <UBadge size="sm" color="neutral" variant="outline" :label="permOf(c.min).label" class="shrink-0" :title="t('perm.editor.inPackage', { label: permOf(c.min).label })" />
          </label>
          <div v-if="g.id === 'git'" class="flex items-start gap-2.5 px-3 py-2 text-(--ui-text-muted)">
            <UIcon name="i-lucide-lock" class="mt-0.5 size-4 shrink-0" />
            <span class="text-xs">{{ t('perm.editor.gitPushNote') }}</span>
          </div>
          <!-- commands it may run: safe ones on its own from read -->
          <div v-if="g.id === 'commands'" class="space-y-2 px-3 py-2">
            <p v-if="!projectId" class="text-xs text-(--ui-text-muted)">{{ t('perm.editor.commandsNoModel') }}</p>
            <template v-else-if="tree.length">
              <label class="flex cursor-pointer items-center gap-2 text-xs">
                <UCheckbox :model-value="byDefault" :disabled="disabled" @update:model-value="(v: boolean | 'indeterminate') => setDefault(v === true)" />
                <UIcon name="i-lucide-shield-check" class="size-3.5 text-(--ui-success)" />{{ t('perm.editor.safeDefault', { n: safe.length }) }}
              </label>
              <div v-if="!byDefault" class="space-y-1.5">
                <details v-for="p in tree" :key="p.id" class="rounded-md bg-(--ui-bg-elevated)/60 px-2 py-1">
                  <summary class="flex cursor-pointer list-none items-center gap-2 text-xs font-medium">
                    <UCheckbox :model-value="packState(p.commands)" :disabled="disabled" @click.stop @update:model-value="togglePack(p.commands)" />
                    <UIcon v-if="p.icon" :name="p.icon" class="size-3.5" />{{ p.label }}
                    <span class="ms-auto font-normal text-(--ui-text-muted)">{{ p.commands.filter(c => picked.includes(c)).length }}/{{ p.commands.length }}</span>
                  </summary>
                  <label v-for="c in p.commands" :key="c" class="flex cursor-pointer items-center gap-2 py-0.5 ps-6 text-xs">
                    <UCheckbox :model-value="picked.includes(c)" :disabled="disabled" @update:model-value="(v: boolean | 'indeterminate') => toggleCmd(c, v === true)" />
                    <code class="truncate">{{ c }}</code>
                    <UIcon v-if="safe.includes(c)" name="i-lucide-shield-check" class="size-3 shrink-0 text-(--ui-success)" />
                  </label>
                </details>
              </div>
            </template>
          </div>
          <!-- processes and containers it may run/restart -->
          <div v-if="g.id === 'ops' && projectId && (caps.includes('ops.process') && procs.length || caps.includes('ops.container') && services.length)" class="flex flex-wrap gap-x-3 gap-y-1.5 px-3 py-2">
            <template v-if="caps.includes('ops.process')">
              <label v-for="p in procs" :key="p.id" class="flex cursor-pointer items-center gap-1.5 text-xs">
                <UCheckbox :model-value="pickedProcs.includes(p.id)" :disabled="disabled" @update:model-value="(v: boolean | 'indeterminate') => toggleIn('processes', pickedProcs, p.id, v === true)" />
                <UIcon :name="p.kind === 'job' ? 'i-lucide-flask-conical' : 'i-lucide-play'" class="size-3.5 text-(--ui-text-muted)" />{{ p.name }}
              </label>
            </template>
            <template v-if="caps.includes('ops.container')">
              <label v-for="sv in services" :key="sv.name" class="flex cursor-pointer items-center gap-1.5 text-xs">
                <UCheckbox :model-value="pickedContainers.includes(sv.name)" :disabled="disabled" @update:model-value="(v: boolean | 'indeterminate') => toggleIn('containers', pickedContainers, sv.name, v === true)" />
                <UIcon name="i-lucide-container" class="size-3.5 text-(--ui-text-muted)" />{{ sv.name }}
              </label>
            </template>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
