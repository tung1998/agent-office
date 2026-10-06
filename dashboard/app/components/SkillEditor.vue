<script setup lang="ts">
// Writing a skill (.claude/skills/<name>/SKILL.md): the editor on one side,
// its own chat on the other; the chat's ```skill blocks fill the editor and
// nothing is written until Save. name "" = a new skill of the project.
const props = defineProps<{ projectId: string, projectPath: string, name?: string, scope?: 'project' | 'user' }>()
const toast = useToast()
const saveError = useSaveError()
const { t } = useLang()
const editing = computed(() => !!props.name)
const scope = computed(() => props.scope ?? 'project')

const form = reactive({ name: props.name ?? '', description: '', body: '' })
const parsed = ref<SkillMd | null>(null) // the saved SKILL.md: its other frontmatter kept as it is
const otherFiles = ref<Record<string, string>>({}) // the skill's other files, kept
const loaded = ref(!editing.value)
const skillMd = () => writeSkillMd({ name: form.name, description: form.description, body: form.body, entries: parsed.value?.entries })

let edited: ReturnType<typeof refOf> | undefined
let editedFrom: string | undefined
onMounted(async () => {
  if (!editing.value) return
  try {
    const inv = await $fetch<{ items: AutoItem[] }>('/api/automation/scan')
    const item = inv.items.find(i => i.kind === 'skill' && i.name === props.name && i.location.type === scope.value &&
      (scope.value === 'user' || i.location.project_path === props.projectPath))
    if (!item) throw new Error(t('skill.notFound'))
    edited = refOf(item)
    const it = await $fetch<LibraryItem & { version?: string }>('/api/automation/content', { method: 'POST', body: edited })
    editedFrom = it.version // the files as they were read: a save over someone else's change is refused (409)
    const files = { ...(it.files ?? {}) }
    const p = parseSkillMd(files['SKILL.md'] ?? '')
    delete files['SKILL.md']
    otherFiles.value = files
    parsed.value = p
    form.description = p.description || item.description
    form.body = p.body
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    loaded.value = true
  }
})

// the chat next to it fills the editor
const highlight = ref<string[]>([])
function applyPatch(p: Record<string, unknown>) {
  const changed: string[] = []
  for (const k of ['name', 'description', 'body'] as const) {
    const v = p[k]
    if (typeof v !== 'string' || (k === 'name' && editing.value)) continue // a saved skill keeps its name (its folder)
    form[k] = k === 'name' ? v.toLowerCase().replace(/[^a-z0-9._-]+/g, '-').slice(0, 64) : v
    changed.push(k)
  }
  if (!changed.length) return
  highlight.value = changed
  setTimeout(() => { highlight.value = [] }, 4000)
  toast.add({ title: t('auto.filled', { n: changed.length }), color: 'info' })
}
// back on its chat (?c=): a new skill takes up the chat's drafts again, in
// order; a saved skill keeps what its file says
function replay(blocks: Record<string, unknown>[]) {
  if (editing.value || !blocks.length) return
  applyPatch(Object.assign({}, ...blocks))
}
// a draft handed over from a project chat
onMounted(() => {
  if (editing.value) return
  try {
    const raw = sessionStorage.getItem('office.skillDraft')
    sessionStorage.removeItem('office.skillDraft')
    if (raw) applyPatch(JSON.parse(raw))
  } catch { /* nothing handed over */ }
})
const pageContext = () => JSON.stringify({ page: editing.value ? 'skill.edit' : 'skill.new', scope: scope.value, draft: { ...form }, other_files: Object.keys(otherFiles.value) })
const hl = (k: string) => highlight.value.includes(k) ? 'rounded-lg ring-2 ring-primary/60 ring-offset-2 ring-offset-(--ui-bg) transition' : ''

// Save: written through the same install as the library (its safety check
// asks before anything risky); an edit replaces the old one (kept in the trash)
const saving = ref(false)
const findings = ref<Finding[]>([])
async function save(accept = false) {
  if (!form.name.trim() || !form.description.trim()) return toast.add({ title: t('skill.needNameDesc'), color: 'error' })
  saving.value = true
  try {
    await $fetch('/api/automation/install', {
      method: 'POST',
      body: {
        kind: 'skill', name: form.name.trim(), overwrite: editing.value, accept,
        target: { scope: scope.value, project_path: scope.value === 'user' ? '' : props.projectPath },
        files: { ...otherFiles.value, 'SKILL.md': skillMd() },
        edited: editing.value ? edited : undefined, version: editing.value ? editedFrom : undefined
      }
    })
    toast.add({ title: t('skill.saved'), color: 'success' })
    await navigateTo({ path: `/projects/${props.projectId}`, query: { tab: 'skill' } })
  } catch (e) {
    const data = (e as { data?: { code?: string, findings?: Finding[] } }).data
    if (data?.code === 'consent' && data.findings) {
      findings.value = data.findings
      if (confirm(t('skill.consent', { n: data.findings.length }))) return save(true)
    } else {
      if (data?.findings) findings.value = data.findings
      saveError(e, () => reloadNuxtApp())
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="grid gap-4 lg:h-[calc(100vh-9rem)] lg:min-h-[36rem] lg:grid-cols-2">
    <div class="flex flex-col lg:min-h-0">
      <div class="space-y-4 lg:min-h-0 lg:flex-1 lg:overflow-y-auto lg:pe-1">
        <div v-if="!loaded" class="p-4 text-sm text-(--ui-text-muted)">{{ t('common.loading') }}</div>
        <template v-else>
          <UFormField :label="t('skill.name')" :help="editing ? t('skill.nameFixed') : t('skill.nameHelp')" required :class="hl('name')">
            <UInput
              :model-value="form.name" class="w-full font-mono" placeholder="tra-don" :disabled="editing"
              @update:model-value="(v: string | number) => { form.name = String(v).toLowerCase().replace(/[^a-z0-9._-]+/g, '-').slice(0, 64) }"
            >
              <template #leading><span class="font-mono text-xs text-(--ui-text-muted)">/</span></template>
            </UInput>
          </UFormField>
          <UFormField :label="t('skill.description')" :help="t('skill.descriptionHelp')" required :class="hl('description')">
            <UTextarea v-model="form.description" :rows="2" autoresize class="w-full" :placeholder="t('skill.descriptionPlaceholder')" />
          </UFormField>
          <UFormField :label="t('skill.body')" :help="t('skill.bodyHelp')" :class="hl('body')">
            <UTextarea v-model="form.body" :rows="18" autoresize class="w-full font-mono text-xs" :placeholder="t('skill.bodyPlaceholder')" />
          </UFormField>
          <p v-if="Object.keys(otherFiles).length" class="text-xs text-(--ui-text-muted)">
            {{ t('skill.otherFiles', { files: Object.keys(otherFiles).join(', ') }) }}
          </p>
          <UAlert v-if="findings.length" color="warning" variant="subtle" icon="i-lucide-shield-alert" :title="t('skill.findings')">
            <template #description>
              <ul class="list-disc ps-4 text-xs">
                <li v-for="(f, i) in findings" :key="i">{{ f.message }}<span v-if="f.line"> ({{ t('skill.line', { n: f.line }) }})</span></li>
              </ul>
            </template>
          </UAlert>
        </template>
      </div>
      <div class="flex justify-end gap-2 border-t border-(--ui-border) pt-3">
        <UButton color="neutral" variant="ghost" :label="t('org.form.close')" :to="{ path: `/projects/${projectId}`, query: { tab: 'skill' } }" />
        <UButton icon="i-lucide-save" :loading="saving" :label="t('auto.save')" @click="save()" />
      </div>
    </div>
    <div class="h-[32rem] lg:h-auto lg:min-h-0">
      <ChatPanel :project-id="projectId" purpose="skill" :page-context="pageContext" @skill-patch="applyPatch" @history="replay" />
    </div>
  </div>
</template>
