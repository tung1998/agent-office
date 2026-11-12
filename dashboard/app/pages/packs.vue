<script setup lang="ts">
// Office's library of project packs (ADR-199): bundles named @agentoffice/…,
// a root pack (a whole organisation) and the unit packs it opens later; each
// links to its source and makes a project from the Add project form.
const { t } = useLang()
const { isAdmin } = useAuth()
interface ProjectPack { ref: string, key: string, title: string, description: string, root?: boolean, bundle: string, source?: string, skills?: string[] }
interface PackBundle { name: string, title: string, description: string }
const { data, error, refresh } = useLiveFetch<{ bundles: PackBundle[], packs: ProjectPack[] }>('/api/project-packs', { lazy: true })
const bundles = computed(() => (data.value?.bundles ?? []).map(b => ({ ...b, packs: (data.value?.packs ?? []).filter(p => p.bundle === b.name) })))

function use(p: ProjectPack) {
  navigateTo({ path: '/projects', query: { template: p.ref } })
}
</script>

<template>
  <PageShell :title="t('packs.title')">
    <div class="space-y-6">
      <p class="text-sm text-(--ui-text-muted)">{{ t('packs.desc') }}</p>
      <LoadingRows v-if="!data && !error" :n="3" />
      <UAlert v-else-if="error" color="error" variant="subtle" :description="apiError(error)">
        <template #actions><UButton size="xs" color="neutral" variant="outline" :label="t('common.refresh')" @click="refresh()" /></template>
      </UAlert>
      <section v-for="b in bundles" :key="b.name" class="space-y-3">
        <div>
          <h2 class="flex flex-wrap items-baseline gap-2 font-semibold">
            {{ b.title }} <code class="text-xs font-normal text-(--ui-text-muted)">{{ b.name }}</code>
          </h2>
          <p class="text-sm text-(--ui-text-muted)">{{ b.description }}</p>
        </div>
        <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <div v-for="p in b.packs" :key="p.ref" class="flex flex-col rounded-xl border p-4" :class="p.root ? 'border-(--ui-primary)/50' : 'border-(--ui-border)'">
            <div class="flex items-start gap-3">
              <UIcon :name="p.root ? 'i-lucide-building-2' : 'i-lucide-users-round'" class="mt-0.5 size-5 shrink-0 text-primary" />
              <div class="min-w-0">
                <p class="font-medium">{{ p.title }}</p>
                <code class="block truncate text-xs text-(--ui-text-muted)" :title="p.ref">{{ p.ref }}</code>
              </div>
            </div>
            <p class="mt-3 flex-1 text-sm text-(--ui-text-muted)">{{ p.description }}</p>
            <div class="mt-4 flex flex-wrap items-center gap-2">
              <UButton v-if="isAdmin" size="sm" icon="i-lucide-plus" :label="t(p.root ? 'packs.useRoot' : 'packs.use')" @click="use(p)" />
              <UButton v-if="p.source" size="sm" color="neutral" variant="ghost" icon="i-lucide-github" :label="t('packs.source')" :to="p.source" target="_blank" external />
            </div>
          </div>
        </div>
      </section>
    </div>
  </PageShell>
</template>
