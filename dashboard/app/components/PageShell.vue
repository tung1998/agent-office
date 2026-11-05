<script setup lang="ts">
// body: the panel body's own classes, in place of the page's (a full-height screen)
const props = defineProps<{ title: string, body?: string }>()
const { isAdmin } = useAuth()
// the FAB (FloatingChat) sits fixed over the bottom-right corner;
// reserve room so it never covers the page's last row/control
const fabVisible = useFabVisible()
</script>

<template>
  <!-- the page never scrolls sideways: wide tables scroll inside their own box;
       each block takes the full width (a centered max-w one never grows to its content) -->
  <UDashboardPanel class="office-main" :ui="{ body: props.body ?? `overflow-x-hidden min-w-0 max-sm:p-3 *:w-full ${fabVisible ? 'pb-20 max-sm:pb-20' : ''}` }">
    <template #header>
      <UDashboardNavbar :title="title" :ui="{ root: 'max-sm:px-3' }">
        <template #leading>
          <UDashboardSidebarCollapse />
        </template>
        <template v-if="$slots.subtitle" #title>
          <!-- a second line under the title (e.g. the project's git state) -->
          <div class="min-w-0 leading-tight">
            <span class="block truncate">{{ title }}</span>
            <div class="truncate text-xs font-normal text-(--ui-text-muted)">
              <slot name="subtitle" />
            </div>
          </div>
        </template>
        <!-- the header holds the title and the office's state only; a page's buttons sit atop its content -->
        <template #right>
          <HeaderStats v-if="isAdmin" />
        </template>
      </UDashboardNavbar>
    </template>
    <template #body>
      <div v-if="$slots.actions" class="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
        <slot name="actions" />
      </div>
      <slot />
    </template>
  </UDashboardPanel>
</template>
