<script setup lang="ts">
// What happened inside a workflow run (its own chat), to read: the
// coordinator's and the roles' answers, their tools, what they proposed.
// No person writes there; proposals can still be decided.
import type { Patch } from '~/components/PatchCard.vue'
import type { ProposedAction } from '~/components/ActionCard.vue'

interface Message {
  id: string
  role: 'user' | 'assistant' | 'error'
  content: string
  tools: { name: string, summary: string, error?: boolean }[]
  author: string
  created_at: string
  patches: Patch[]
  actions?: ProposedAction[]
  cost_usd?: number
}

const props = defineProps<{ projectId: string, conversationId: string }>()
const { t, dateLocale } = useLang()
const { data, error } = useLiveFetch<{ messages: Message[], running: { turn_id: string, agent_name: string }[] }>(() => `/api/conversations/${props.conversationId}`, { lazy: true })
const { data: agentsData } = useLiveFetch<{ agents: Agent[] }>(() => `/api/projects/${props.projectId}/chat/agents`, { lazy: true })
const agentOf = (name: string) => agentsData.value?.agents.find(a => a.name === name) ?? { name }
const when = (d: string) => new Date(d).toLocaleTimeString(dateLocale.value, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
</script>

<template>
  <div class="space-y-4">
    <UAlert v-if="error" color="error" variant="subtle" :title="apiError(error)" />
    <LoadingRows v-else-if="!data" />
    <template v-else>
      <template v-for="m in data.messages" :key="m.id">
        <div v-if="m.role === 'user'" class="rounded-lg bg-(--ui-bg-elevated) px-3 py-2 text-sm">
          <p class="mb-1 flex items-center gap-1.5 text-xs text-(--ui-text-muted)">
            <UIcon name="i-lucide-corner-down-right" class="size-3.5" />{{ t('wf.runPage.toCoordinator') }} · {{ when(m.created_at) }}
          </p>
          <p class="whitespace-pre-wrap">{{ m.content }}</p>
        </div>
        <div v-else-if="m.role === 'error'" class="flex items-start gap-2 text-sm text-(--ui-error)">
          <UIcon name="i-lucide-circle-alert" class="mt-0.5 size-4 shrink-0" />
          <span class="whitespace-pre-wrap">{{ m.content }}</span>
        </div>
        <div v-else class="min-w-0 space-y-2">
          <div class="flex items-center gap-2 text-xs text-(--ui-text-muted)">
            <AgentAvatar :agent="agentOf(m.author)" size="xs" />
            <span class="font-medium">{{ m.author }}</span>
            <span>{{ when(m.created_at) }}</span>
            <span v-if="m.cost_usd">· ${{ m.cost_usd.toFixed(3) }}</span>
          </div>
          <details v-if="m.tools.length" class="text-xs text-(--ui-text-muted)">
            <summary class="cursor-pointer">{{ t('chat.toolsUsed', { n: m.tools.length }) }}</summary>
            <ul class="mt-1 space-y-0.5 ps-4">
              <li v-for="(x, i) in m.tools" :key="i" :class="x.error ? 'text-(--ui-error)' : ''">{{ x.summary }}</li>
            </ul>
          </details>
          <!-- eslint-disable-next-line vue/no-v-html -->
          <div class="markdown min-w-0 text-sm" v-html="renderMarkdown(m.content)" />
          <PatchCard v-for="p in m.patches" :key="p.id" :patch="p" :project-id="projectId" @updated="(np: Patch) => m.patches = m.patches.map(x => x.id === np.id ? np : x)" />
          <ActionCard
            v-for="a in m.actions ?? []" :key="a.id" :action="a" :project-id="projectId"
            @updated="(na: ProposedAction) => m.actions = (m.actions ?? []).map(x => x.id === na.id ? na : x)"
          />
        </div>
      </template>
      <p v-for="r in data.running" :key="r.turn_id" class="flex items-center gap-2 text-xs text-(--ui-text-muted)">
        <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin text-primary" />{{ t('wf.runPage.working', { name: r.agent_name }) }}
      </p>
      <p v-if="!data.messages.length" class="text-sm text-(--ui-text-muted)">{{ t('wf.runPage.empty') }}</p>
    </template>
  </div>
</template>
