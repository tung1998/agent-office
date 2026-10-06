<script setup lang="ts">
// Writing a skill of the project (or of the machine, ?scope=user), with AI.
const route = useRoute()
const { t } = useLang()
const projectId = computed(() => route.params.id as string)
const name = computed(() => typeof route.query.name === 'string' ? route.query.name : '')
const scope = computed(() => route.query.scope === 'user' ? 'user' : 'project')
const { data } = await useLiveFetch<{ project: { path: string } }>(() => `/api/projects/${projectId.value}`)
</script>

<template>
  <PageShell :title="name ? `/${name}` : t('skill.new')">
    <UButton :to="{ path: `/projects/${projectId}`, query: { tab: 'skill' } }" icon="i-lucide-arrow-left" size="xs" color="neutral" variant="ghost" class="-ms-2 mb-2" :label="t('project.sectionSkill')" />
    <SkillEditor v-if="data" :key="name" :project-id="projectId" :project-path="data.project.path" :name="name" :scope="scope" />
  </PageShell>
</template>
