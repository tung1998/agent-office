<script setup lang="ts">
import type { DropdownMenuItem } from '@nuxt/ui'

// The chats of a project, newest first: the chat's side column on a wide
// screen, a drawer on a phone.
interface Thread { id: string, agent_id: string, agent_name: string, title: string, updated_at: string, active_turn?: string, source?: Source, purpose?: string, tags?: string[] }
// tags: the project's, to filter by (a chat must have every one picked)
defineProps<{ loading?: boolean, conversations: Thread[], agents: { id: string, name: string }[], currentId?: string, unread?: Set<string>, hasMore?: boolean, loadingMore?: boolean, menu: (c: Thread) => DropdownMenuItem[][], tags?: string[] }>()
const origin = defineModel<ChatFilter>('origin', { default: 'all' })
const tagFilter = defineModel<string[]>('tagFilter', { default: () => [] })
// the filters stay folded under one icon; open at once when one is set
const filtered = computed(() => origin.value !== 'all' || tagFilter.value.length > 0)
const filtersOpen = ref(filtered.value)
const emit = defineEmits<{ open: [Thread], new: [], more: [] }>()
const { t, dateLocale } = useLang()
// a skill's or template's editor chat, else where it started (web, a bot, an automation)
const kindIcon = (c: Thread) => c.purpose === 'burn' ? 'i-lucide-flame' : c.purpose === 'skill' ? 'i-lucide-sparkles' : c.purpose === 'template' ? 'i-lucide-network' : sourceIcon[c.source ?? 'web']
const kindTitle = (c: Thread) => c.purpose === 'burn' ? t('burn.title') : c.purpose === 'skill' ? t('chat.skillChat') : c.purpose === 'template' ? t('chat.templateChat') : t(`source.${c.source ?? 'web'}`)
const when = (d: string) => new Date(d).toLocaleString(dateLocale.value, { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit' })
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <div class="space-y-1.5 border-b border-(--ui-border) p-2">
      <div class="flex items-center gap-1">
        <UTooltip :text="t('chat.newThread')">
          <UButton icon="i-lucide-plus" size="sm" color="neutral" variant="ghost" :aria-label="t('chat.newThread')" @click="emit('new')" />
        </UTooltip>
        <span class="flex-1" />
        <UTooltip :text="t('chat.filters')">
          <UButton
            icon="i-lucide-filter" size="sm" :color="filtered ? 'primary' : 'neutral'" :variant="filtersOpen || filtered ? 'soft' : 'ghost'"
            :aria-label="t('chat.filters')" :aria-expanded="filtersOpen" @click="filtersOpen = !filtersOpen"
          />
        </UTooltip>
      </div>
      <template v-if="filtersOpen">
        <SourceFilter v-model="origin" burn />
        <USelectMenu
          v-if="tags?.length || tagFilter.length" v-model="tagFilter" :items="tags ?? []" multiple size="xs" icon="i-lucide-tag"
          :placeholder="t('chatTag.filter')" :title="t('chatTag.filterHint')" class="w-full"
        />
      </template>
    </div>
    <div class="flex-1 overflow-y-auto p-1">
      <LoadingRows v-if="loading && !conversations.length" :n="6" />
      <p v-else-if="!conversations.length" class="p-3 text-xs text-(--ui-text-muted)">{{ t('chat.none') }}</p>
      <div
        v-for="c in conversations" :key="c.id"
        class="group flex cursor-pointer items-start gap-1 rounded-md px-2 py-1.5 text-sm"
        :class="currentId === c.id ? 'bg-(--ui-bg-accented)' : 'hover:bg-(--ui-bg-muted)'"
        @click="emit('open', c)"
      >
        <!-- where it came from, big; who answers, small underneath -->
        <span class="mt-0.5 grid size-6 shrink-0 place-items-center rounded-md bg-(--ui-bg-elevated) text-(--ui-text-muted)" :title="kindTitle(c)">
          <UIcon :name="kindIcon(c)" class="size-3.5" />
        </span>
        <div class="min-w-0 flex-1">
          <p class="flex items-center gap-1.5" :class="unread?.has(c.id) && 'font-semibold'">
            <span v-if="unread?.has(c.id)" class="size-2 shrink-0 rounded-full bg-primary" :title="t('chat.unread')" />
            <span class="truncate">{{ c.title || t('chat.newThreadTitle') }}</span>
          </p>
          <p class="flex items-center gap-1 truncate text-xs text-(--ui-text-muted)">
            <AgentAvatar :agent="agents.find(a => a.id === c.agent_id) ?? { id: c.agent_id, name: c.agent_name }" size="2xs" class="shrink-0" />
            <span class="truncate">{{ c.agent_name }} · {{ when(c.updated_at) }}</span>
          </p>
          <div v-if="c.tags?.length" class="mt-0.5 flex flex-wrap gap-0.5">
            <ChatTags :tags="c.tags" size="xs" />
          </div>
        </div>
        <UIcon v-if="c.active_turn" name="i-lucide-loader-circle" class="mt-1 size-3.5 animate-spin text-(--ui-text-muted)" />
        <UDropdownMenu :items="menu(c)" :content="{ align: 'end' }">
          <!-- visible on a phone (no hover there) -->
          <button type="button" class="-me-1 rounded px-0.5 text-(--ui-text-dimmed) hover:text-(--ui-text) md:invisible md:group-hover:visible data-[state=open]:visible" :aria-label="t('chat.more')" @click.stop>
            <UIcon name="i-lucide-ellipsis" class="size-4" />
          </button>
        </UDropdownMenu>
      </div>
      <UButton
        v-if="hasMore" block size="xs" color="neutral" variant="ghost" class="mt-1" icon="i-lucide-chevrons-down"
        :label="t('chat.moreThreads')" :loading="loadingMore" @click="emit('more')"
      />
    </div>
  </div>
</template>
