<script setup lang="ts">
// Installs a skill / agent / MCP server machine-wide or into a project.
// Source is one of: a library entry, an installed item (copy), or an MCP
// template from the catalog / registry.
export interface InstallSource {
  kind: ItemKind
  name: string
  library?: string
  from?: AutoItem
  template?: MCPTemplate
}

// fixedTarget: install only there ('__user' = machine-wide, else a project path)
const props = defineProps<{ source: InstallSource | null, fixedTarget?: string }>()
const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ done: [] }>()
const toast = useToast()
const { t } = useLang()
const { inv } = useInventory()
const { data: projData } = await useLiveFetch<{ projects: Project[] }>('/api/projects')

const target = ref('__user')
const mcpScope = ref<'project' | 'local'>('project')
const name = ref('')
const values = reactive<Record<string, string>>({})
const overwrite = ref(false)
const accept = ref(false)
const findings = ref<Finding[]>([])
const needs = ref<'' | 'exists' | 'consent' | 'unsafe'>('')
const error = ref('')
const busy = ref(false)

const template = computed<MCPTemplate | null>(() => props.source?.template ?? null)
const inputs = computed(() => template.value?.inputs ?? [])

// office projects first, then other folders Claude Code has opened
const targets = computed(() => {
  const seen = new Set<string>()
  const out: { label: string, value: string, icon: string }[] = [{ label: t('install.everywhere'), value: '__user', icon: 'i-lucide-monitor' }]
  for (const p of projData.value?.projects ?? []) {
    if (!p.path || seen.has(p.path)) continue
    seen.add(p.path)
    out.push({ label: t('install.projectOffice', { name: p.name }), value: p.path, icon: 'i-lucide-folder-git-2' })
  }
  for (const p of inv.value?.projects ?? []) {
    if (!p.exists || seen.has(p.path)) continue
    seen.add(p.path)
    out.push({ label: p.name, value: p.path, icon: 'i-lucide-folder' })
  }
  return out
})

watch(() => [open.value, props.source] as const, ([o, s]) => {
  if (!o || !s) return
  name.value = s.name
  target.value = props.fixedTarget ?? (s.from?.location.type === 'user' && targets.value[1] ? targets.value[1].value : '__user')
  mcpScope.value = 'project'
  overwrite.value = false
  accept.value = false
  findings.value = []
  needs.value = ''
  error.value = ''
  for (const k of Object.keys(values)) delete values[k]
  for (const i of s.template?.inputs ?? []) values[i.key] = i.default ?? ''
}, { immediate: true })

const where = computed(() => {
  const folder = props.source?.kind === 'skill' ? 'skills' : 'agents'
  if (target.value === '__user') {
    return props.source?.kind === 'mcp' ? t('install.whereUserMcp') : t('install.whereUserOther', { folder })
  }
  if (props.source?.kind === 'mcp') {
    return mcpScope.value === 'project' ? t('install.whereProjectShared', { target: target.value }) : t('install.whereProjectLocal')
  }
  return t('install.whereProjectOther', { target: target.value, folder })
})

async function install() {
  const s = props.source
  if (!s) return
  busy.value = true
  error.value = ''
  try {
    const scope = target.value === '__user' ? 'user' : (s.kind === 'mcp' ? mcpScope.value : 'project')
    const res = await $fetch<{ path?: string, findings: Finding[] }>('/api/automation/install', {
      method: 'POST',
      body: {
        kind: s.kind,
        name: name.value.trim(),
        target: { scope, project_path: target.value === '__user' ? '' : target.value },
        library: s.library,
        from: s.from ? refOf(s.from) : undefined,
        template: s.template,
        values: s.template ? values : undefined,
        overwrite: overwrite.value,
        accept: accept.value
      }
    })
    toast.add({ title: t('install.installed', { name: name.value }), description: res.path, color: 'success', icon: 'i-lucide-circle-check' })
    open.value = false
    emit('done')
  } catch (e) {
    const data = (e as { data?: { error?: string, code?: string, findings?: Finding[] } }).data
    needs.value = (data?.code as typeof needs.value) ?? ''
    findings.value = data?.findings ?? []
    error.value = apiError(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UModal v-model:open="open" :title="source ? t('install.title', { label: kindMeta[source.kind].label, name: source.name }) : ''" :ui="{ content: 'max-w-xl' }">
    <template #body>
      <form v-if="source" id="install-form" class="space-y-4" @submit.prevent="install">
        <UFormField v-if="!fixedTarget" :label="t('install.installTo')">
          <USelectMenu v-model="target" :items="targets" value-key="value" class="w-full" />
        </UFormField>
        <URadioGroup
          v-if="source.kind === 'mcp' && target !== '__user'"
          v-model="mcpScope"
          :items="[
            { label: t('install.scopeShared'), description: t('install.scopeSharedDesc'), value: 'project' },
            { label: t('install.scopeLocal'), description: t('install.scopeLocalDesc'), value: 'local' }
          ]"
        />
        <p class="text-xs text-(--ui-text-muted)">
          <UIcon name="i-lucide-map-pin" class="align-middle" /> {{ where }}
        </p>

        <UFormField :label="t('install.name')">
          <UInput v-model="name" class="w-full font-mono" required />
        </UFormField>

        <template v-if="inputs.length">
          <USeparator :label="t('install.inputsHeader')" />
          <UFormField
            v-for="i in inputs" :key="i.key"
            :label="i.label" :required="i.required" :help="i.hint"
            :description="i.kind === 'env' ? t('install.inputEnv') : i.kind === 'header' ? t('install.inputHeader') : t('install.inputArg')"
          >
            <UInput v-model="values[i.key]" :type="i.secret ? 'password' : 'text'" class="w-full font-mono" autocomplete="off" />
          </UFormField>
          <p v-if="inputs.some(i => i.secret) && mcpScope === 'project' && target !== '__user'" class="text-xs text-(--ui-warning)">
            <UIcon name="i-lucide-triangle-alert" class="align-middle" />
            {{ t('install.tokenWarning', { var: t('install.tokenVar') }) }}
          </p>
        </template>
        <UAlert v-if="template?.auth === 'oauth'" color="info" variant="subtle" icon="i-lucide-log-in" :title="t('install.oauthTitle')" :description="t('install.oauthDesc')" />

        <div v-if="findings.length" class="space-y-1 rounded-md border border-(--ui-border) p-3">
          <p class="text-sm font-medium">{{ t('install.safetyCheck') }}</p>
          <div v-for="(f, i) in findings" :key="i" class="flex gap-2 text-sm">
            <UIcon :name="f.severity === 'refuse' ? 'i-lucide-octagon-x' : 'i-lucide-triangle-alert'" :class="f.severity === 'refuse' ? 'text-(--ui-error)' : 'text-(--ui-warning)'" class="mt-0.5 shrink-0" />
            <div class="min-w-0">
              <p>{{ f.message }} · {{ t('tools.lineNum', { n: f.line }) }}</p>
              <code class="block truncate text-xs text-(--ui-text-muted)">{{ f.excerpt }}</code>
            </div>
          </div>
        </div>

        <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />
        <UCheckbox v-if="needs === 'exists'" v-model="overwrite" :label="t('install.overwriteLabel')" />
        <UCheckbox v-if="needs === 'consent'" v-model="accept" :label="t('install.consentLabel')" />
      </form>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="open = false" />
        <UButton type="submit" form="install-form" icon="i-lucide-download" :label="t('install.submit')" :loading="busy" :disabled="needs === 'unsafe'" />
      </div>
    </template>
  </UModal>
</template>
