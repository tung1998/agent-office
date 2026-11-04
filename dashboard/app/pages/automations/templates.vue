<script setup lang="ts">
const { t } = useLang()
const toast = useToast()
const { isAdmin } = useAuth()
const { data } = await useLiveFetch<{ projects: Project[] }>('/api/projects', { lazy: true })
const projects = computed(() => data.value?.projects ?? [])
const { data: savedData, refresh: refreshSaved } = useLiveFetch<{ items: SavedAutomationTemplate[] }>('/api/automation/templates', { lazy: true })
const savedTemplates = computed(() => (savedData.value?.items ?? []).map(automationTemplateFromLibrary))
const templates = computed(() => [...automationTemplates, ...savedTemplates.value])
const projectId = ref('')
const selectedTemplate = ref('')
const projectModalOpen = ref(false)
const search = ref('')
const category = ref('all')
const categories = computed(() => ['all', ...new Set(templates.value.map(x => x.category))])
const visibleTemplates = computed(() => templates.value.filter(x => {
  const q = search.value.trim().toLowerCase()
  return (category.value === 'all' || x.category === category.value) && (!q || `${x.name} ${x.description} ${x.category}`.toLowerCase().includes(q))
}).sort((a, b) => Number(featuredTemplateKeys.has(b.key)) - Number(featuredTemplateKeys.has(a.key)) || a.name.localeCompare(b.name)))
const projectItems = computed(() => projects.value.map(p => ({ label: p.name, value: p.id })))
const templateModalOpen = ref(false)
const templateSaving = ref(false)
const editingTemplate = ref<SavedAutomationTemplate | null>(null)
const templateForm = reactive({ name: '', description: '', category: '', prompt: '', source: 'schedule' as 'schedule' | 'webhook' })
function clearTemplateForm() {
  templateForm.name = ''
  templateForm.description = ''
  templateForm.category = ''
  templateForm.prompt = ''
  templateForm.source = 'schedule'
}
function addTemplate() {
  editingTemplate.value = null
  clearTemplateForm()
  tplCheck.reset()
  templateModalOpen.value = true
}
function editTemplate(template: AutomationTemplate) {
  const saved = savedData.value?.items.find(item => item.key === template.key)
  if (!saved) return
  editingTemplate.value = saved
  Object.assign(templateForm, saved)
  tplCheck.reset()
  templateModalOpen.value = true
}
function templateKey(name: string) {
  const slug = name.normalize('NFKD').replace(/[\u0300-\u036f]/g, '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 53) || 'automation'
  const base = `custom-${slug}`
  if (editingTemplate.value) return editingTemplate.value.key
  let key = base
  let suffix = 2
  while (templates.value.some(template => template.key === key)) key = `${base.slice(0, 62 - String(suffix).length)}-${suffix++}`
  return key
}
// the buttons stay pressable: what is missing is marked and focused
const tplCheck = useFormCheck(() => {
  const need = t('common.required')
  return { name: !templateForm.name.trim() && need, category: !templateForm.category.trim() && need,
    description: !templateForm.description.trim() && need, prompt: !templateForm.prompt.trim() && need }
})
const pickCheck = useFormCheck(() => ({ project: !projectId.value && t('common.pickOne') }))
async function saveTemplate() {
  if (templateSaving.value || !tplCheck.check()) return
  templateSaving.value = true
  try {
    const key = templateKey(templateForm.name)
    await $fetch(`/api/automation/templates/${encodeURIComponent(key)}`, {
      method: 'PUT',
      body: { ...templateForm }
    })
    await refreshSaved()
    templateModalOpen.value = false
    toast.add({ title: t(editingTemplate.value ? 'auto.templateUpdated' : 'auto.templateAdded'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    templateSaving.value = false
  }
}
async function deleteTemplate(template: AutomationTemplate) {
  if (!confirm(t('auto.templateDeleteConfirm', { name: template.name }))) return
  try {
    await $fetch(`/api/automation/templates/${encodeURIComponent(template.key)}`, { method: 'DELETE' })
    await refreshSaved()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
function chooseTemplate(key: string) {
  selectedTemplate.value = key
  projectId.value = ''
  pickCheck.reset()
  projectModalOpen.value = true
}
async function startTemplate() {
  if (!pickCheck.check() || !selectedTemplate.value) return
  await navigateTo(`/projects/${projectId.value}/automations/new?template=${encodeURIComponent(selectedTemplate.value)}`)
}
</script>

<template>
  <PageShell :title="t('nav.automationTemplates')">
    <div class="space-y-4">
      <div class="flex flex-wrap items-end gap-3 rounded-xl border border-(--ui-border) p-4">
        <div class="me-auto min-w-60">
          <p class="text-sm text-(--ui-text-muted)">{{ t('auto.templateHint') }}</p>
        </div>
        <UInput v-model="search" icon="i-lucide-search" :placeholder="t('auto.templateSearch')" class="w-52" />
        <USelect v-model="category" :items="categories.map(x => ({ label: x === 'all' ? t('auto.templateAllCategories') : x, value: x }))" class="w-40" />
        <UButton v-if="isAdmin" class="sm:ms-auto" size="sm" icon="i-lucide-plus" :label="t('auto.templateAdd')" @click="addTemplate" />
      </div>

      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <div v-for="item in visibleTemplates" :key="item.key" class="flex flex-col rounded-xl border border-(--ui-border) p-4">
          <div class="flex items-start gap-3">
            <UIcon :name="item.icon" class="mt-0.5 size-5 shrink-0 text-primary" />
            <div class="min-w-0">
              <p class="flex items-center gap-1.5 font-medium">
                <UIcon v-if="featuredTemplateKeys.has(item.key)" name="i-lucide-star" class="size-3.5 text-(--ui-warning)" :title="t('auto.featuredTemplate')" />
                {{ item.name }}
              </p>
              <p class="mt-1 text-xs text-(--ui-text-muted)">{{ item.category }}</p>
            </div>
          </div>
          <p class="mt-3 flex-1 text-sm text-(--ui-text-muted)">{{ item.description }}</p>
          <div class="mt-4 flex flex-wrap items-center gap-2">
            <UButton size="sm" icon="i-lucide-plus" :label="t('auto.useTemplate')" @click="chooseTemplate(item.key)" />
            <template v-if="isAdmin && item.custom">
              <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-pencil" :label="t('auto.templateEdit')" @click="editTemplate(item)" />
              <UButton size="sm" color="error" variant="ghost" icon="i-lucide-trash-2" :aria-label="t('auto.delete')" :title="t('auto.delete')" @click="deleteTemplate(item)" />
            </template>
          </div>
        </div>
      </div>
      <p v-if="!visibleTemplates.length" class="rounded-xl border border-dashed border-(--ui-border) p-8 text-center text-sm text-(--ui-text-muted)">{{ t('auto.templateNoResults') }}</p>
    </div>
  </PageShell>
  <UModal v-model:open="projectModalOpen" :title="t('auto.chooseProjectForTemplateTitle')" :description="t('auto.chooseProjectForTemplateDesc')">
    <template #body>
      <UFormField :label="t('auto.projectPick')" required data-field="project" :error="pickCheck.errors.value.project">
        <USelectMenu v-model="projectId" :items="projectItems" value-key="value" :placeholder="t('auto.projectPick')" :aria-label="t('auto.projectPick')" class="w-full" />
      </UFormField>
      <p v-if="!projects.length" class="mt-3 text-sm text-(--ui-text-muted)">{{ t('auto.chooseProjectEmpty') }}</p>
    </template>
    <template #footer>
      <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="projectModalOpen = false" />
      <UButton color="primary" :label="t('auto.startFromTemplate')" @click="startTemplate" />
    </template>
  </UModal>
  <UModal v-model:open="templateModalOpen" :title="t(editingTemplate ? 'auto.templateEditTitle' : 'auto.templateAddTitle')" :ui="{ content: 'max-w-2xl' }">
    <template #body>
      <div class="grid gap-4 sm:grid-cols-2">
        <UFormField :label="t('auto.name')" required data-field="name" :error="tplCheck.errors.value.name">
          <UInput v-model="templateForm.name" class="w-full" />
        </UFormField>
        <UFormField :label="t('auto.templateCategory')" required data-field="category" :error="tplCheck.errors.value.category">
          <UInput v-model="templateForm.category" class="w-full" />
        </UFormField>
        <UFormField :label="t('auto.templateDescription')" required data-field="description" :error="tplCheck.errors.value.description" class="sm:col-span-2">
          <UInput v-model="templateForm.description" class="w-full" />
        </UFormField>
        <UFormField :label="t('auto.source')" required class="sm:col-span-2">
          <USelect v-model="templateForm.source" :items="[{ label: t('auto.sourceSchedule'), value: 'schedule' }, { label: t('auto.sourceWebhook'), value: 'webhook' }]" class="w-full" />
        </UFormField>
        <UFormField :label="t('auto.prompt')" required data-field="prompt" :error="tplCheck.errors.value.prompt" class="sm:col-span-2">
          <UTextarea v-model="templateForm.prompt" :rows="12" autoresize class="w-full" :placeholder="t('auto.promptPlaceholder')" />
        </UFormField>
      </div>
    </template>
    <template #footer>
      <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="templateModalOpen = false" />
      <UButton color="primary" icon="i-lucide-save" :label="t('auto.save')" :loading="templateSaving" @click="saveTemplate" />
    </template>
  </UModal>
</template>
