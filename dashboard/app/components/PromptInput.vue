<script setup lang="ts">
// Prompt box shared by Chat and Việc: "/" opens the project's skills and commands, "#" its workflows, "@"
// the agents to tag (Chat, ADR-044), files
// can be attached (button, drag & drop, paste) and are uploaded right away.
export interface Attachment { id: string, name: string, kind: 'image' | 'pdf' | 'text', mime: string, size: number }
export interface Skill { name: string, description: string, source: 'project' | 'user' | 'plugin' | 'workflow' | 'office', kind?: 'skill' | 'command', tool?: string }

const props = withDefaults(defineProps<{
  projectId: string
  placeholder?: string
  rows?: number
  maxrows?: number
  submitOnEnter?: boolean
  mentions?: { name: string, label: string, icon?: string }[] // agents "@" can tag
  commands?: Skill[] // office's own commands "/" offers first (/btw, /goal: ADR-273)
}>(), { placeholder: '', rows: 1, maxrows: 8, submitOnEnter: true, mentions: () => [], commands: () => [] })
const text = defineModel<string>({ default: '' })
const files = defineModel<Attachment[]>('attachments', { default: () => [] })
const emit = defineEmits<{ submit: [] }>()
const toast = useToast()
const { t } = useLang()

// ---- skills ----
// in the background: the box shows at once, the skill list fills in
const { data: skillData } = useLiveFetch<{ skills: Skill[] }>(() => `/api/projects/${props.projectId}/skills`, { lazy: true })
// the project's workflows on, run by "#key" (ADR-109; "/key" still runs one)
const { data: wfData } = useLiveFetch<{ workflows: ProjectWorkflow[] }>(() => `/api/projects/${props.projectId}/workflows`, { lazy: true })
const workflows = computed<Skill[]>(() => (wfData.value?.workflows ?? []).filter(w => w.enabled && !w.error && w.callable !== 'sub')
  .map(w => ({ name: w.key, description: w.description || w.name, source: 'workflow' as const })))
const skills = computed<Skill[]>(() => [...props.commands, ...(skillData.value?.skills ?? []).filter(s => !props.commands.some(c => c.name === s.name))])
const menuIndex = ref(0)
const menuClosed = ref(false)
// open while typing the first word after "/" (skills) or "#" (workflows)
const call = computed(() => {
  const m = /^([/#])([^\s]*)$/.exec(text.value)
  return m ? { sign: m[1] as '/' | '#', q: m[2]!.toLowerCase() } : null
})
const query = computed(() => call.value ? `${call.value.sign}${call.value.q}` : null)
const matches = computed(() => {
  if (!call.value) return []
  const q = call.value.q
  return (call.value.sign === '#' ? workflows.value : skills.value)
    .filter(s => s.name.toLowerCase().includes(q) || s.description.toLowerCase().includes(q))
    .sort((a, b) => Number(!a.name.toLowerCase().startsWith(q)) - Number(!b.name.toLowerCase().startsWith(q)))
    .slice(0, 8)
})
// "@" right before the caret: the agents whose name has what was typed after it
const caret = ref(0)
function syncCaret() { caret.value = box.value?.textareaRef?.selectionStart ?? text.value.length }
const mentionQuery = computed(() => {
  if (!props.mentions.length) return null
  const m = /(?:^|\s)@([^\s@]*)$/u.exec(text.value.slice(0, caret.value))
  return m ? m[1]!.toLowerCase() : null
})
const mentionMatches = computed(() => {
  const q = mentionQuery.value
  if (q === null) return []
  return props.mentions.filter(a => a.name.toLowerCase().includes(q)).slice(0, 8)
})
const menuKind = computed<'mention' | 'skill' | 'workflow'>(() => mentionMatches.value.length ? 'mention' : call.value?.sign === '#' ? 'workflow' : 'skill')
const menuCount = computed(() => menuKind.value === 'mention' ? mentionMatches.value.length : matches.value.length)
const menuOpen = computed(() => !menuClosed.value && menuCount.value > 0)
watch([query, mentionQuery], () => { menuIndex.value = 0; menuClosed.value = false })
const activeSkill = computed(() => {
  const m = /^([/#])(\S+)\s/.exec(text.value)
  if (!m) return undefined
  // "/key" of a workflow still runs it (a workflow wins over a skill of its name)
  return workflows.value.find(s => s.name === m[2]) ?? (m[1] === '/' ? skills.value.find(s => s.name === m[2]) : undefined)
})
const sourceLabel = computed(() => ({ project: t('prompt.sourceProject'), user: t('prompt.sourceUser'), plugin: t('prompt.sourcePlugin'), workflow: t('wf.slashBadge'), office: t('prompt.sourceOffice') }))
// whose folder a skill is in, when not Claude Code's: "Codex · máy"
const toolName: Record<string, string> = { codex: 'Codex', gemini: 'Gemini', antigravity: 'Antigravity' }
const badge = (s: Skill) => [toolName[s.tool ?? ''], sourceLabel.value[s.source]].filter(Boolean).join(' · ')
const skillIcon = (s: Skill) => s.source === 'workflow' ? 'i-lucide-workflow' : s.source === 'office' ? (s.name === 'goal' ? 'i-lucide-target' : 'i-lucide-message-circle-question') : s.kind === 'command' ? 'i-lucide-square-terminal' : 'i-lucide-sparkles'

// The menu is teleported to <body> with fixed position so no scrolling or
// overflow-hidden parent can clip it; it opens upward unless there is no room.
const root = ref<HTMLElement | null>(null)
const menuStyle = ref<Record<string, string>>({})
const MENU_H = 300
function place() {
  const r = root.value?.getBoundingClientRect()
  if (!r) return
  const width = `${Math.min(r.width, 520)}px`
  const left = `${r.left}px`
  menuStyle.value = r.top > MENU_H + 8 || r.top > window.innerHeight - r.bottom
    ? { left, width, bottom: `${window.innerHeight - r.top + 4}px`, maxHeight: `${Math.min(MENU_H, r.top - 8)}px` }
    : { left, width, top: `${r.bottom + 4}px`, maxHeight: `${Math.min(MENU_H, window.innerHeight - r.bottom - 8)}px` }
}
watch(menuOpen, (open) => {
  if (open) {
    place()
    window.addEventListener('scroll', place, true)
    window.addEventListener('resize', place)
  } else {
    window.removeEventListener('scroll', place, true)
    window.removeEventListener('resize', place)
  }
})
onBeforeUnmount(() => {
  window.removeEventListener('scroll', place, true)
  window.removeEventListener('resize', place)
})
watch(menuCount, () => { if (menuOpen.value) nextTick(place) })

const box = ref<{ textareaRef?: HTMLTextAreaElement } | null>(null)
function pick(s: Skill) {
  text.value = `${s.source === 'workflow' ? '#' : '/'}${s.name} `
  nextTick(() => box.value?.textareaRef?.focus())
}
function pickMention(name: string) {
  const before = text.value.slice(0, caret.value).replace(/@([^\s@]*)$/u, `@${name} `)
  text.value = before + text.value.slice(caret.value)
  nextTick(() => {
    const el = box.value?.textareaRef
    el?.focus()
    el?.setSelectionRange(before.length, before.length)
    syncCaret()
  })
}
function pickCurrent() {
  if (menuKind.value === 'mention') pickMention(mentionMatches.value[menuIndex.value]!.name)
  else pick(matches.value[menuIndex.value]!)
}

function onKey(e: KeyboardEvent) {
  if (menuOpen.value) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      const n = menuCount.value
      menuIndex.value = (menuIndex.value + (e.key === 'ArrowDown' ? 1 : n - 1)) % n
      return
    }
    if ((e.key === 'Enter' || e.key === 'Tab') && !e.isComposing) {
      e.preventDefault()
      pickCurrent()
      return
    }
    if (e.key === 'Escape') {
      menuClosed.value = true
      return
    }
  }
  if (props.submitOnEnter && e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    if (!uploading.value) emit('submit')
  }
}

// ---- attachments ----
const uploading = ref(0)
const dragging = ref(false)
const input = ref<HTMLInputElement | null>(null)
const MAX = 10 * 1024 * 1024

function toBase64(f: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result).split(',')[1] ?? '')
    r.onerror = () => reject(r.error)
    r.readAsDataURL(f)
  })
}

async function upload(list: FileList | File[]) {
  for (const f of Array.from(list)) {
    if (files.value.length >= 10) {
      toast.add({ title: t('prompt.maxFiles'), color: 'warning' })
      break
    }
    if (f.size > MAX) {
      toast.add({ title: t('prompt.tooLarge', { name: f.name }), color: 'error' })
      continue
    }
    uploading.value++
    try {
      const a = await $fetch<Attachment>(`/api/projects/${props.projectId}/attachments`, {
        method: 'POST', body: { name: f.name || 'pasted.png', data: await toBase64(f) }
      })
      files.value = [...files.value, a]
    } catch (e) {
      toast.add({ title: `${f.name}: ${apiError(e)}`, color: 'error' })
    } finally {
      uploading.value--
    }
  }
}
function onPick(e: Event) {
  const t = e.target as HTMLInputElement
  if (t.files) upload(t.files)
  t.value = ''
}
function onDrop(e: DragEvent) {
  dragging.value = false
  if (e.dataTransfer?.files.length) upload(e.dataTransfer.files)
}
function onPaste(e: ClipboardEvent) {
  const list = Array.from(e.clipboardData?.files ?? [])
  if (list.length) {
    e.preventDefault()
    upload(list)
  }
}
function remove(a: Attachment) {
  files.value = files.value.filter(f => f.id !== a.id)
}
const size = (n: number) => n >= 1048576 ? `${(n / 1048576).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`
const icon = { image: 'i-lucide-image', pdf: 'i-lucide-file-text', text: 'i-lucide-file-code' }

function startSkill() { text.value = '/'; box.value?.textareaRef?.focus() }
function startWorkflow() { text.value = '#'; box.value?.textareaRef?.focus() }
const extras = computed(() => [
  { label: t('prompt.attach'), icon: 'i-lucide-paperclip', onSelect: () => input.value?.click() },
  { label: t('prompt.pickSkill'), icon: 'i-lucide-slash', disabled: !skills.value.length, onSelect: startSkill },
  ...(workflows.value.length ? [{ label: t('prompt.pickWorkflow'), icon: 'i-lucide-workflow', onSelect: startWorkflow }] : []),
])
defineExpose({ busy: computed(() => uploading.value > 0), focus: () => box.value?.textareaRef?.focus() })
</script>

<template>
  <div
    ref="root"
    class="@container relative rounded-lg border bg-(--ui-bg) transition @max-[420px]:rounded-md"
    :class="dragging ? 'border-(--ui-primary) ring-2 ring-(--ui-primary)/20' : 'border-(--ui-border)'"
    @dragover.prevent="dragging = true" @dragleave.self="dragging = false" @drop.prevent="onDrop"
  >
    <!-- skill menu -->
    <Teleport to="body">
      <div
        v-if="menuOpen" role="listbox" :style="menuStyle"
        class="fixed z-50 overflow-auto rounded-lg border border-(--ui-border) bg-(--ui-bg) p-1 shadow-lg"
      >
      <template v-if="menuKind === 'mention'">
        <p class="px-2 py-1 text-xs text-(--ui-text-muted)">{{ t('prompt.mentionHint') }}</p>
        <button
          v-for="(a, i) in mentionMatches" :key="a.name" type="button" role="option" :aria-selected="i === menuIndex"
          class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left"
          :class="i === menuIndex ? 'bg-(--ui-bg-elevated)' : 'hover:bg-(--ui-bg-elevated)'"
          @mousedown.prevent="pickMention(a.name)" @mouseenter="menuIndex = i"
        >
          <UIcon :name="a.icon || 'i-lucide-bot'" class="size-4 shrink-0 text-primary" />
          <span class="min-w-0 flex-1 truncate text-sm">@{{ a.name }}</span>
          <UBadge color="neutral" variant="subtle" size="sm" :label="a.label" />
        </button>
      </template>
      <template v-else>
      <p class="px-2 py-1 text-xs text-(--ui-text-muted)">{{ menuKind === 'workflow' ? t('prompt.workflowHint') : t('prompt.skillHint') }}</p>
      <button
        v-for="(s, i) in matches" :key="s.name" type="button" role="option" :aria-selected="i === menuIndex"
        class="flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left"
        :class="i === menuIndex ? 'bg-(--ui-bg-elevated)' : 'hover:bg-(--ui-bg-elevated)'"
        @mousedown.prevent="pick(s)" @mouseenter="menuIndex = i"
      >
        <UIcon :name="skillIcon(s)" class="mt-0.5 size-4 shrink-0 text-primary" />
        <span class="min-w-0 flex-1">
          <span class="font-mono text-sm">{{ s.source === 'workflow' ? '#' : '/' }}{{ s.name }}</span>
          <span class="line-clamp-1 text-xs text-(--ui-text-muted)">{{ s.description }}</span>
        </span>
        <UBadge color="neutral" variant="subtle" size="sm" :label="badge(s)" />
      </button>
      </template>
      </div>
    </Teleport>

    <!-- attached files -->
    <div v-if="files.length || uploading" class="flex flex-wrap gap-2 px-3 pt-3">
      <div v-for="a in files" :key="a.id" class="group relative flex items-center gap-2 rounded-md border border-(--ui-border) bg-(--ui-bg-elevated) py-1 ps-1 pe-2 text-xs">
        <img v-if="a.kind === 'image'" :src="`/api/attachments/${a.id}`" :alt="a.name" class="size-8 rounded object-cover">
        <UIcon v-else :name="icon[a.kind]" class="size-5 text-(--ui-text-muted)" />
        <span class="max-w-40 truncate">{{ a.name }}</span>
        <span class="text-(--ui-text-dimmed)">{{ size(a.size) }}</span>
        <button type="button" class="text-(--ui-text-muted) hover:text-(--ui-error)" :aria-label="t('chat.removeAttachment', { name: a.name })" @click="remove(a)">
          <UIcon name="i-lucide-x" class="size-3.5" />
        </button>
      </div>
      <div v-if="uploading" class="flex items-center gap-1 text-xs text-(--ui-text-muted)">
        <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> {{ t('prompt.uploading') }}
      </div>
    </div>

    <!-- a phone: two lines tall at least, so the placeholder never scrolls inside it -->
    <UTextarea
      ref="box" v-model="text" :rows="rows" autoresize :maxrows="maxrows" variant="none" class="w-full @max-sm:[&_textarea]:!text-sm @max-sm:[&_textarea]:min-h-13 @max-[420px]:[&_textarea]:!text-xs @max-[420px]:[&_textarea]:min-h-10"
      :placeholder="placeholder" @keydown="onKey" @paste="onPaste" @keyup="syncCaret" @click="syncCaret" @input="syncCaret"
    />

    <!-- a phone: a size smaller (icons, labels), tighter, all the same height -->
    <div class="flex items-center gap-2 px-2 pb-2 @max-sm:gap-0.5 @max-sm:[&_.iconify]:!size-4 @max-sm:[&_button]:!text-xs @max-sm:[&_button]:!py-1 @max-[420px]:gap-0 @max-[420px]:px-1 @max-[420px]:pb-1 @max-[420px]:[&_.iconify]:!size-3.5 @max-[420px]:[&_button]:!py-0.5">
      <!-- a phone: attach, skills and workflows behind one ⋯, the row keeps room for the actions -->
      <UDropdownMenu :items="extras" :content="{ side: 'top', align: 'start' }" class="@sm:hidden">
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-ellipsis" class="@sm:hidden" :aria-label="t('common.actions')" />
      </UDropdownMenu>
      <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-paperclip" class="@max-sm:hidden" :aria-label="t('prompt.attach')" @click="input?.click()" />
      <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-slash" class="@max-sm:hidden" :aria-label="t('prompt.pickSkill')" :title="t('prompt.pickSkill')" :disabled="!skills.length" @click="startSkill" />
      <UButton v-if="workflows.length" size="sm" color="neutral" variant="ghost" icon="i-lucide-workflow" class="@max-sm:hidden" :aria-label="t('prompt.pickWorkflow')" :title="t('prompt.pickWorkflow')" @click="startWorkflow" />
      <UBadge v-if="activeSkill" color="primary" variant="subtle" size="sm" :icon="skillIcon(activeSkill)" :label="activeSkill.name" />
      <!-- only when the box itself is wide: narrow panels (the corner chat) keep one tidy row;
           it takes only the room the actions leave (basis 0), so it is cut before they are -->
      <span class="hidden min-w-0 flex-1 basis-0 truncate text-xs text-(--ui-text-dimmed) @2xl:inline" :title="t('prompt.dragHint')">{{ t('prompt.dragHint') }}</span>
      <!-- the actions shrink (their labels cut) before the row overflows -->
      <div class="ms-auto flex min-w-0 items-center justify-end gap-2 @max-sm:gap-0.5">
        <slot name="actions" />
      </div>
    </div>
    <input ref="input" type="file" multiple class="hidden" accept="image/png,image/jpeg,image/gif,image/webp,application/pdf,text/*,.md,.csv,.json,.log,.yaml,.yml,.ts,.js,.vue,.go,.py,.sql,.php" @change="onPick">
  </div>
</template>
