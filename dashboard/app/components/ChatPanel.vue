<script setup lang="ts">
import type { Patch } from './PatchCard.vue'
import type { ProposedAction } from './ActionCard.vue'
import type { Attachment } from './PromptInput.vue'

interface ToolCall { name: string, summary: string, error?: boolean }
interface Message {
  id: string
  role: 'user' | 'assistant' | 'error'
  content: string
  tools: ToolCall[]
  attachments?: Attachment[]
  author: string
  // who wrote it to a bot (Discord, Telegram); none = in office
  sender?: { kind: 'discord' | 'telegram', id: string, username?: string, name?: string, private?: boolean, link?: string, account_id?: string, account?: string }
  created_at: string
  patches: Patch[]
  actions?: ProposedAction[]
  cost_usd?: number
  // the connection and model that wrote it
  provider?: string
  model?: string
  // sent by an agent from another chat (send_to_chat, a linked project's
  // report back): who, from which project and chat; a report's text folded
  relay?: { agent: string, project: string, project_id: string, conversation_id: string, title: string, report: boolean, url?: string }
  // an expert's answer to a question an agent here asked (ADR-177)
  consult?: { agent: string, project: string, office?: string, link?: string }
}
interface Queued { id: string, text: string, attachments: Attachment[], created_at: string }
interface Conversation { id: string, project_id?: string, agent_id: string, agent_name: string, title: string, updated_at: string, source?: Source, purpose?: string, external_url?: string, active_turn?: string, mode?: PermLevel, edit_mode?: 'worktree' | 'direct', effort?: string, cleaned?: string, context_tokens?: number, context_window?: number, tags?: string[], queued?: Queued[] }
interface ChatEvent { seq: number, type: 'text' | 'tool' | 'status' | 'patch' | 'done' | 'error', text?: string, tool?: ToolCall, patch?: Patch, message?: Message, next_turn_id?: string }
// the agents in a chat and the answers in progress (ADR-044)
interface Member { agent_id: string, agent_name: string, level: string, context_tokens: number, context_window: number }
interface RunningTurn { turn_id: string, agent_name: string, background: boolean }
// what waits for a person in a linked project's chat this one handed work to
interface LinkedPending { project_id: string, project: string, conversation_id: string, title: string, actions: ProposedAction[], patches: Patch[] }

// purpose "automation": the chat that builds one automation (ADR-042), made on
// first send or opened by automationId; its answers may fill the form.
// subject (skill, workflow): what the editor writes; its chat is found again
// by it (an editor's chat is not in the chat list).
// compact: no thread column (a picker instead), fills its container.
// pageContext: what the person is looking at, sent with each message.
// pane: one box of the watch screen (compact, no prefill, its own header
// buttons in the #lead/#actions slots); conversationId: the chat it opens on.
const props = defineProps<{ projectId: string, purpose?: 'automation' | 'skill' | 'workflow', automationId?: string, subject?: string, compact?: boolean, pane?: boolean, conversationId?: string, pageContext?: () => string }>()
const emit = defineEmits<{ 'automation-patch': [Record<string, unknown>], 'skill-patch': [Record<string, unknown>], 'workflow-patch': [Record<string, unknown>], 'history': [Record<string, unknown>[]], 'conversation': [string], 'current': [string], 'back': [] }>()
const single = computed(() => !!props.purpose)
// the chat page on a phone: the input stays behind a button until asked for,
// so the messages get the whole screen
const page = computed(() => !props.compact && !single.value)
const composeOpen = ref(false)
// the pickers by the send button: on a phone just their icon (as the mode
// picker), the same square as the other buttons (the agent's name is in the placeholder)
const iconOnPhone = { base: '@max-sm/chat:!w-7 @max-sm/chat:!px-0', leading: '@max-sm/chat:!ps-1.5', value: '@max-sm/chat:hidden', trailing: '@max-sm/chat:hidden' }
const toast = useToast()
const { t, when } = useLang()
const { user: me, manages, isOfficeAdmin } = useAuth()
const NuxtLink = resolveComponent('NuxtLink')
const isAdmin = computed(() => manages(props.projectId)) // its cards: the project's admin or manager, on any page (ADR-224)

const _f1 = useLiveFetch<{ agents: Agent[], default_agent_id: string }>(() => `/api/projects/${props.projectId}/chat/agents`, { lazy: true })
const { data: agentsData } = _f1
// where the chats started: the dashboard, a bot, an automation
const origin = ref<ChatFilter>('all')
// the tags picked to filter by: a chat must have every one
const tagFilter = ref<string[]>([])
const tagQuery = computed(() => tagFilter.value.map(x => `&tag=${encodeURIComponent(x)}`).join(''))
// the server pushes each chat as it changes (ADR-078): the list is loaded again only back online
const _f2 = usePushedFetch<{ conversations: Conversation[], has_more?: boolean }>(() => `/api/projects/${props.projectId}/conversations?source=${origin.value}${tagQuery.value}`, { immediate: !single.value, lazy: true })
const { data: convData, refresh: refreshConvs, pending: convsLoading } = _f2
// not awaited: the chat shows at once with its skeletons (a phone over a VPN)
// every agent of the project: the person picks who answers, by its rights
const agents = computed(() => agentsData.value?.agents ?? [])
const onAgents = computed(() => agents.value.filter(a => a.enabled !== false)) // a paused agent is not offered (its past messages keep their avatar)
const conversations = computed(() => convData.value?.conversations ?? [])
// 20 at a time (ADR-085): the next page from the last one shown
const moreConvs = computed(() => !!convData.value?.has_more)
const loadingMore = ref(false)
async function loadMoreConvs() {
  const list = convData.value?.conversations
  const last = list?.[list.length - 1]
  if (!list || !last || loadingMore.value) return
  loadingMore.value = true
  try {
    const res = await $fetch<{ conversations: Conversation[], has_more?: boolean }>(`/api/projects/${props.projectId}/conversations`, { query: { source: origin.value, before: last.updated_at, tag: tagFilter.value } })
    const known = new Set(list.map(c => c.id))
    list.push(...res.conversations.filter(c => !known.has(c.id)))
    convData.value!.has_more = !!res.has_more
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    loadingMore.value = false
  }
}

// the project's tags: picked again quickly when tagging, and the filter's choices
const _f3 = useFetch<{ tags: { tag: string, count: number }[] }>(() => `/api/projects/${props.projectId}/chat-tags`, { immediate: !single.value, lazy: true })
const { data: tagsData, refresh: refreshTags } = _f3
const projectTags = computed(() => (tagsData.value?.tags ?? []).map(x => x.tag))

const current = ref<Conversation | null>(null)
const threadsOpen = ref(false) // the chats drawer on a phone

// one queued save per conversation (tagQueue.ts): each is built from the
// previous one's confirmed result, not from a snapshot taken before it, so
// two rapid edits (e.g. removing two different tags back to back) can't lose
// one another
const tagChains: TagChains = new Map()

async function saveTags(c: Conversation, mutate: (tags: string[]) => string[]) {
  await queueTagOp(tagChains, c.id, c.tags ?? [], async (base) => {
    try {
      const tags = mutate(base)
      const res = await $fetch<{ conversation: Conversation }>(`/api/conversations/${c.id}/tags`, { method: 'PUT', body: { tags } })
      const saved = res.conversation.tags ?? []
      if (current.value?.id === c.id) current.value = { ...current.value, tags: saved }
      if (tagEdit.value?.id === c.id) tagEdit.value = { ...tagEdit.value, tags: saved }
      const list = convData.value?.conversations
      const i = list?.findIndex(x => x.id === c.id) ?? -1
      if (list && i >= 0) {
        if (hasTags(saved, tagFilter.value)) list[i] = { ...list[i]!, tags: saved }
        else list.splice(i, 1) // no longer what the filter shows
      }
      refreshTags()
      return saved
    } catch (e) {
      toast.add({ title: apiError(e), color: 'error' })
      return base
    }
  })
}
// "Gắn tag" from a chat's menu in the list
const tagEdit = ref<Conversation | null>(null)
const messages = ref<Message[]>([])
const draft = ref('')
const draftFiles = ref<Attachment[]>([])
// what was typed and not sent stays with its chat (another tab and back, a reload)
const drafts = useChatDrafts()
const draftKey = computed(() => `${props.projectId}:${current.value?.id ?? `new:${props.purpose ?? ''}`}`)
watch(draftKey, (key) => {
  const d = drafts.load(key)
  draft.value = d.text
  draftFiles.value = d.files
}, { immediate: true })
watch([draft, draftFiles], ([text, files]) => drafts.save(draftKey.value, text, files))
const editMode = ref<'worktree' | 'direct'>('worktree')
// how hard it thinks in this chat ('' = the agent's own level)
const effort = ref('')
// who answers: the chat's agent (the project's default for a new chat; the first
// message's @tag picks another, server-side); each agent's own rights decide what it may do
const pick = ref('')
const isOn = (id?: string) => !!id && onAgents.value.some(a => a.id === id)
// the chat's agent is paused: the one that is on and answered here last, else the default (as the server)
function standIn() {
  const last = [...messages.value].reverse().find(m => m.role === 'assistant' && onAgents.value.some(a => a.name === m.author))
  return onAgents.value.find(a => a.name === last?.author)?.id || onAgents.value.find(a => a.id === agentsData.value?.default_agent_id)?.id || onAgents.value[0]?.id || ''
}
watch([() => current.value?.id, () => current.value?.agent_id, agents, () => messages.value.length], (now, before) => {
  editMode.value = current.value?.edit_mode ?? 'worktree'
  if (!before || now[0] !== before[0]) effort.value = current.value?.effort ?? '' // another chat: its own level
  const own = current.value?.agent_id
  pick.value = isOn(own) ? own! : standIn()
}, { immediate: true })
const picked = computed(() => agents.value.find(a => a.id === pick.value))
// the levels the picked agent's model takes: none, no picker (Antigravity names it in the model)
const { data: provData } = useLiveFetch<{ providers: Provider[] }>('/api/providers', { lazy: true })
const pickedProvider = computed(() => {
  const ps = provData.value?.providers
  return ps?.find(x => x.id === picked.value?.provider_id) ?? ps?.find(x => x.is_default)
})
const pickedEfforts = computed(() => {
  const p = pickedProvider.value
  return effortsFor(p?.kind, picked.value?.llm_model || (picked.value && p?.tier_models[picked.value.model_tier]) || '')
})
// its connection failing (signed out, its CLI stuck): said before sending, not after a turn that never ends
const pickedBroken = computed(() => pickedProvider.value?.enabled && pickedProvider.value.status === 'error' ? pickedProvider.value : null)
const pickedLevel = computed(() => picked.value ? agentLevel(picked.value.permissions) : 'read')
const prompt = ref<{ busy: boolean, focus?: () => void } | null>(null)

// filled by other tabs (e.g. "Hỏi agent" in Vận hành)
// agentId opens a new chat with that agent, conversationId opens that chat (the agent page)
const prefill = useState<{ text: string, files: Attachment[], send?: boolean, agentId?: string, conversationId?: string } | null>('chat-prefill', () => null)
let tookPrefill = false // it already opened what it was given: the first chat / the URL's does not
async function takePrefill() {
  if (!prefill.value || single.value || props.pane) return // a task's, an automation's or a watch box's own chat takes no prefill
  const p = prefill.value
  prefill.value = null
  tookPrefill = true
  if (p.conversationId) return open({ id: p.conversationId } as Conversation)
  if (p.agentId && !p.send) {
    stopStream()
    current.value = null
    messages.value = []
    return newConversation(p.agentId)
  }
  if (!p.send) {
    draft.value = p.text
    draftFiles.value = p.files
    return
  }
  stopStream()
  current.value = null
  messages.value = []
  await newConversation()
  await post(p.text, p.files)
}
onMounted(takePrefill)
watch(prefill, takePrefill)
const listEl = ref<HTMLElement | null>(null)

// live answer being streamed
const streaming = ref(false)
const sending = ref(false) // closes the window before streaming.value is set, inside follow()
const liveText = ref('')
const liveTools = ref<ToolCall[]>([])
const liveStatus = ref('')
let unfollow: (() => void) | null = null // the answer followed on the tab's stream (ADR-127)
let turnId = ''

// older messages, a page at a time, the reading place kept
const PAGE = 20
const hasOlder = ref(false)
const loadingOlder = ref(false)
async function loadOlder() {
  const c = current.value
  const first = messages.value[0]
  if (!c || !first || loadingOlder.value) return
  loadingOlder.value = true
  try {
    const el = listEl.value
    const from = el ? el.scrollHeight - el.scrollTop : 0
    const res = await $fetch<{ messages: Message[], has_more?: boolean }>(`/api/conversations/${c.id}?limit=${PAGE}&before=${first.id}`)
    messages.value = [...res.messages, ...messages.value]
    hasOlder.value = !!res.has_more
    await nextTick()
    if (el) el.scrollTop = el.scrollHeight - from
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    loadingOlder.value = false
  }
}

// what changed elsewhere, pushed with its data (ADR-078): a message into the
// open chat (a Discord message, a hand-off's answer) is put in place; a
// chat's row replaces the old one in the list (a new one: the list again)
// as Discord: at the end, what comes in scrolls into view; scrolled up to read,
// it stays put and a button takes the person down (it says when something is new)
const pinned = ref(true)
const unseen = ref(false)
function onListScroll() { // the person's scrolling (and ours, always to the end): a little up already leaves the end
  const el = listEl.value
  pinned.value = !el || el.scrollHeight - el.scrollTop - el.clientHeight < 40
  if (pinned.value) unseen.value = false
}
function toEnd() {
  pinned.value = true
  unseen.value = false
  scrollDown(true)
}
const held: Message[] = [] // an agent's answer that came while one streams: the stream's own end brings it
function place(m: Message) {
  if (messages.value.some(x => x.id === m.id)) return
  if (m.role === 'user') dropShown(m.content) // shown already, before office had it
  messages.value.push(m)
  scrollDown()
}
onLiveEvent<{ conversation_id: string, message: Message }>('message', ({ conversation_id: id, message }) => {
  if (current.value?.id !== id || loadingMsgs.value) return
  // a person's message shows at once (it often comes just after its answer
  // started streaming here); only an answer waits, not to show twice
  if (streaming.value && message.role === 'assistant') held.push(message)
  else place(message)
})
watch(streaming, (s) => { if (!s) held.splice(0).forEach(place) })
onLiveEvent<{ conversation: Conversation }>('conversation', ({ conversation: c }) => {
  if (c.project_id !== props.projectId) return
  c = { ...c, active_turn: c.active_turn || undefined } // not answering: the field is left out, so say so
  if (current.value?.id === c.id) {
    current.value = { ...current.value, ...c }
    if (c.active_turn && !streaming.value) follow(c.active_turn) // an answer started elsewhere (a bot's message here)
  }
  const list = convData.value?.conversations
  if (!list || single.value) return
  const i = list.findIndex(x => x.id === c.id)
  if (i < 0) {
    if ((origin.value === 'all' || (origin.value === 'burn' ? c.purpose === 'burn' : c.source === origin.value)) && hasTags(c.tags, tagFilter.value)) refreshConvs() // a new chat: which list it belongs in is the server's to say
    return
  }
  if (!hasTags(c.tags, tagFilter.value)) { // a tag the filter asks for was taken off
    list.splice(i, 1)
    return
  }
  if (list[i]!.updated_at === c.updated_at) { // nothing new in it (its tags): it stays where it is
    list[i] = { ...list[i]!, ...c }
    return
  }
  const [old] = list.splice(i, 1)
  list.unshift({ ...old, ...c }) // the newest on top
})
onLiveEvent<{ id: string }>('conversation.deleted', ({ id }) => {
  const list = convData.value?.conversations
  const i = list?.findIndex(x => x.id === id) ?? -1
  if (list && i >= 0) list.splice(i, 1)
})
// a diff or proposal decided, who is in the chat: still the table's notice
onLiveChange(async (tables) => {
  if (!tables.includes('*') && !tables.some(t => ['patches', 'actions', 'conversation_agents'].includes(t))) return // '*': back online
  if (!single.value && tables.includes('*')) refreshConvs()
  const c = current.value
  if (!c || streaming.value || sending.value || loadingMsgs.value) return
  try {
    const res = await $fetch<{ messages: Message[], members?: Member[], running?: RunningTurn[], conversation: Conversation, linked_pending?: LinkedPending[] }>(`/api/conversations/${c.id}?limit=${PAGE}`)
    if (current.value?.id !== c.id || streaming.value || sending.value) return
    linkedPending.value = res.linked_pending ?? [] // another chat's cards: no message of this one changes
    const known = new Set(messages.value.map(m => m.id))
    const fresh = res.messages.filter(m => !known.has(m.id))
    const changed = res.messages.some(m => { const x = messages.value.find(y => y.id === m.id); return x && JSON.stringify(x) !== JSON.stringify(m) })
    if (!fresh.length && !changed) return
    // by time: one this fetch missed (sent or pushed meanwhile) keeps its place at the end, not above the page
    messages.value = [...messages.value.filter(m => !res.messages.some(n => n.id === m.id)), ...res.messages]
      .sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at))
    applyGroup(res.members, res.running)
    if (res.conversation.active_turn && !streaming.value) follow(res.conversation.active_turn)
    if (fresh.length) scrollDown()
  } catch { /* gone, or offline: the next change tries again */ }
})

// something new at the end: in view if the person is at the end (or it is
// theirs: force), else the button says so. Once a frame, however many tokens
// came in it; at once, not smooth, so a stream stays pinned to the end.
let scrollQueued = false
async function scrollDown(force = false) {
  if (force) pinned.value = true
  else if (!pinned.value) {
    unseen.value = true
    return
  }
  if (scrollQueued) return
  scrollQueued = true
  await nextTick()
  requestAnimationFrame(() => {
    scrollQueued = false
    const el = listEl.value
    if (el && pinned.value) el.scrollTop = el.scrollHeight
  })
}
// a chat just opened shows its last message at once (no scrolling down from
// the top); it stays there while what it shows settles (markdown, images)
async function jumpToEnd() {
  pinned.value = true
  unseen.value = false
  await nextTick()
  const el = listEl.value
  if (!el) return
  const pin = () => { el.scrollTop = el.scrollHeight }
  pin()
  requestAnimationFrame(pin)
  const ro = new ResizeObserver(pin)
  for (const child of Array.from(el.children)) ro.observe(child)
  const stop = () => ro.disconnect()
  el.addEventListener('wheel', stop, { once: true, passive: true }) // the person scrolls: leave them there
  el.addEventListener('touchstart', stop, { once: true, passive: true })
  setTimeout(stop, 1500)
}

// after an answer: the conversation's context and the connection's usage changed
const { refresh: refreshLimits } = useLimits()
async function afterTurn() {
  refreshLimits(true)
  const id = current.value?.id
  if (!id) return
  try {
    const res = await $fetch<{ conversation: Conversation, members?: Member[], running?: RunningTurn[], linked_pending?: LinkedPending[] }>(`/api/conversations/${id}`)
    if (current.value?.id !== id) return
    linkedPending.value = res.linked_pending ?? []
    current.value = { ...current.value, context_tokens: res.conversation.context_tokens, context_window: res.conversation.context_window }
    applyGroup(res.members, res.running)
  } catch { /* the next open shows it */ }
}

// agents working in the background on a hand-off (like subagents): their
// answers land in the thread when done; the one who asked then reports back
const members = ref<Member[]>([])
const running = ref<RunningTurn[]>([])
// a report back's text without its first line (who and where, shown above it)
const reportBody = (text: string) => text.split('\n\n').slice(1).join('\n\n').trim() || text
const linkedPending = ref<LinkedPending[]>([])
const background = computed(() => running.value.filter(r => r.background))
const bgFollows = new Map<string, () => void>()
function applyGroup(m?: Member[], r?: RunningTurn[]) {
  members.value = m ?? []
  running.value = r ?? []
  for (const b of background.value) {
    if (bgFollows.has(b.turn_id)) continue
    bgFollows.set(b.turn_id, followTurn<ChatEvent>(b.turn_id, (ev) => {
      if (ev.type !== 'done' && ev.type !== 'error') return
      if (ev.message && !messages.value.some(x => x.id === ev.message!.id)) messages.value.push(ev.message)
      bgFollows.delete(b.turn_id)
      scrollDown()
      afterTurn()
    }, () => {
      bgFollows.delete(b.turn_id)
      afterTurn()
    }))
  }
  // the one who asked reports back: follow it when nothing else is streaming
  const fg = running.value.find(x => !x.background)
  if (fg && !streaming.value) follow(fg.turn_id)
}
function stopBackground() {
  bgFollows.forEach(stop => stop())
  bgFollows.clear()
}
async function cancelTurn(id: string) {
  await $fetch(`/api/chat/turns/${id}/cancel`, { method: 'POST', body: {} }).catch(() => {})
}
// who wrote a message to a bot; an older one only kept "discord:<name>" as its author
function senderOf(m: Message): Message['sender'] {
  if (m.sender) return m.sender
  const [kind, name] = [m.author.split(':', 1)[0], m.author.slice(m.author.indexOf(':') + 1)]
  return (kind === 'discord' || kind === 'telegram') && name ? { kind, id: name, username: name } : undefined
}
// the person's message with the tags of agents marked
function tagged(text: string) {
  const names = agents.value.map(a => a.name).sort((a, b) => b.length - a.length).map(n => n.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
  if (!names.length) return [{ text, tag: false }]
  // split with a capture group: odd parts are the tags
  // not in the middle of a word (an email) nor inside code
  if (text.includes('`')) return [{ text, tag: false }]
  return text.split(new RegExp(`((?<![\\p{L}\\p{N}_])@(?:${names.join('|')}))`, 'iu')).map((part, i) => ({ text: part, tag: i % 2 === 1 })).filter(p => p.text)
}

// the open chat (and a message) live in the URL, so a link points at them;
// only the project's Chat page (not the corner chat, a task's or a builder's)
const route = useRoute()
const router = useRouter()
const ownsUrl = computed(() => !single.value && !props.compact)
const copy = useCopy()
const chatLink = (id: string, messageId?: string) => `${location.origin}/projects/${props.projectId}?tab=chat&c=${id}${messageId ? `&m=${messageId}` : ''}`
// unread: an agent answered since the person last looked. The open chat is
// seen (also an answer that comes while it shows), unless they marked it unread.
const unread = useUnread()
const keptUnread = ref('')
watch([() => current.value?.id, unread.ids], ([id]) => {
  if (!id || id === keptUnread.value || !unread.ids.value.has(id) || document.visibilityState !== 'visible') return
  void unread.mark(id, true).catch(() => {})
})
watch(() => current.value?.id, (id) => { if (id !== keptUnread.value) keptUnread.value = '' })
function markRead(c: Conversation, seen: boolean) {
  keptUnread.value = seen ? '' : c.id
  void unread.mark(c.id, seen).catch(e => toast.add({ title: apiError(e), color: 'error' }))
}
function threadMenu(c: Conversation) {
  const isUnread = unread.ids.value.has(c.id)
  const items: { label: string, icon: string, onSelect: () => unknown }[] = [
    { label: t('chat.copyLink'), icon: 'i-lucide-link', onSelect: () => copy(chatLink(c.id)) },
    { label: t('chat.copyId'), icon: 'i-lucide-hash', onSelect: () => copy(c.id) },
    isUnread
      ? { label: t('chat.markSeen'), icon: 'i-lucide-mail-open', onSelect: () => markRead(c, true) }
      : { label: t('chat.markUnread'), icon: 'i-lucide-mail', onSelect: () => markRead(c, false) }
  ]
  // a Discord/Telegram chat: where it is there (its thread, once it has one)
  items.push({ label: t('chatTag.edit'), icon: 'i-lucide-tag', onSelect: () => { tagEdit.value = c } })
  if (c.external_url) items.unshift({ label: c.source === 'telegram' ? t('chat.openInTelegram') : t('chat.openInDiscord'), icon: 'i-lucide-external-link', onSelect: () => { window.open(c.external_url, '_blank', 'noopener') } })
  return [items, [{ label: t('chat.delete'), icon: 'i-lucide-trash-2', color: 'error' as const, onSelect: () => remove(c) }]]
}
// an agent's avatar opens onto its settings page
function agentMenu(a?: { id?: string, name?: string }) {
  if (!a?.id) return []
  return [[{ label: t('chat.agentSettings'), icon: 'i-lucide-settings', to: `/projects/${props.projectId}/agents/${a.id}` }]]
}
function messageMenu(m: Message) {
  const items = [{ label: t('chat.copyText'), icon: 'i-lucide-copy', onSelect: () => copy(m.content) }]
  if (current.value) items.push({ label: t('chat.copyMessageLink'), icon: 'i-lucide-link', onSelect: () => copy(chatLink(current.value!.id, m.id)) })
  return [items]
}
const marked = ref('') // the message a link points at
async function showMessage(id: string) {
  await nextTick()
  const el = document.getElementById(`m-${id}`)
  if (!el) return
  el.scrollIntoView({ block: 'center' })
  marked.value = id
  setTimeout(() => { if (marked.value === id) marked.value = '' }, 2500)
}

const loadingMsgs = ref(false) // a chat's messages on their way: its skeleton shows
async function open(c: Conversation, messageId?: string) {
  stopStream()
  if (current.value?.id !== c.id) messages.value = [] // another chat: not the last one's messages meanwhile
  current.value = c
  if (ownsUrl.value && route.query.c !== c.id) router.replace({ query: { ...route.query, c: c.id, m: undefined } })
  stopBackground()
  // the last page (a phone over a VPN); a link to one message loads it all to find it
  loadingMsgs.value = true
  let res: { conversation: Conversation, messages: Message[], has_more?: boolean, members?: Member[], running?: RunningTurn[], linked_pending?: LinkedPending[] }
  try {
    res = await $fetch(`/api/conversations/${c.id}${messageId ? '' : `?limit=${PAGE}`}`)
  } finally {
    loadingMsgs.value = false
  }
  if (current.value?.id !== c.id) return // đã chuyển sang chat khác trong lúc chờ: bỏ kết quả cũ
  messages.value = res.messages
  hasOlder.value = !!res.has_more
  current.value = res.conversation
  linkedPending.value = res.linked_pending ?? []
  applyGroup(res.members, res.running)
  if (res.conversation.active_turn && !streaming.value) follow(res.conversation.active_turn)
  if (messageId) showMessage(messageId)
  else jumpToEnd()
}

// clicks in a row wait for the one on its way (office brings back an empty chat, not a new one)
let making: Promise<void> | null = null
function newConversation(agentId = '') {
  making ??= makeConversation(agentId).finally(() => { making = null })
  return making
}
async function makeConversation(agentId: string) {
  try {
    const res = await $fetch<{ conversation: Conversation }>(`/api/projects/${props.projectId}/conversations`, { method: 'POST', body: { agent_id: agentId, purpose: props.purpose ?? '', subject: props.subject ?? '' } })
    if (props.purpose) emit('conversation', res.conversation.id)
    else await refreshConvs()
    // the skill editor's chat is in the URL: back, reload or a link reopens it
    if (props.purpose === 'skill' || props.purpose === 'workflow') router.replace({ query: { ...route.query, c: res.conversation.id } })
    await open(res.conversation)
    // a new chat is for writing: the box opens (phone) and takes the cursor
    composeOpen.value = true
    await nextTick()
    prompt.value?.focus?.()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

// written while an answer is on its way (as the CLI): office keeps it (not
// this page: leaving loses nothing), shown greyed under the answer and sent
// as the next message once the chat is free (several: as one)
// shown at once, before office answers (it only syncs them): "tmp-" ids
let shownSeq = 0
const shownQueued = ref<(Queued & { convId: string })[]>([])
const queuedHere = computed(() => {
  const kept = current.value?.queued ?? []
  return [...kept, ...shownQueued.value.filter(q => q.convId === current.value?.id && !kept.some(k => k.text === q.text))]
})
function dropShown(text: string) {
  messages.value = messages.value.filter(x => !(x.id.startsWith('tmp-') && x.content === text))
}
function setQueued(id: string, list: Queued[]) {
  if (current.value?.id === id) current.value = { ...current.value, queued: list }
}
async function unqueue(q: Queued) {
  const id = current.value?.id
  if (!id) return
  setQueued(id, queuedHere.value.filter(x => x.id !== q.id))
  await $fetch(`/api/conversations/${id}/queued`, { method: 'DELETE', query: { id: q.id } }).catch(e => toast.add({ title: apiError(e), color: 'error' }))
}
// Stop: what waited goes back into the box, not sent after all
async function unqueueAll() {
  const id = current.value?.id
  const here = queuedHere.value
  if (!id || !here.length) return
  setQueued(id, [])
  draft.value = [...here.map(q => q.text), draft.value].filter(Boolean).join('\n\n')
  draftFiles.value = [...here.flatMap(q => q.attachments), ...draftFiles.value]
  await $fetch(`/api/conversations/${id}/queued`, { method: 'DELETE' }).catch(() => {})
}

// leaving the page: what is in the box is kept as this chat's draft
onBeforeUnmount(() => {
  drafts.save(draftKey.value, draft.value, draftFiles.value)
})

function send() {
  const text = draft.value.trim()
  const files = draftFiles.value
  if ((!text && !files.length) || prompt.value?.busy) return
  if ((streaming.value || sending.value) && !current.value) return // the new chat is still being made: the box keeps it
  draft.value = ''
  draftFiles.value = []
  return post(text, files) // answering now: office keeps it for next
}

// post sends one message; it fails back into the box (with what is there now)
async function post(text: string, files: Attachment[]) {
  text = text.trim()
  if (!text && !files.length) return
  if (!current.value) await newConversation()
  if (!current.value) return restore(text, files)
  // shown at once: waiting under the answer when it answers, else as sent
  const shown = { id: `tmp-${++shownSeq}`, text, attachments: files, created_at: new Date().toISOString() }
  if (streaming.value || sending.value) shownQueued.value.push({ ...shown, convId: current.value.id })
  else messages.value.push({ id: shown.id, role: 'user', content: text, attachments: files, author: '', created_at: shown.created_at, tools: [], patches: [] })
  scrollDown(true) // theirs: in view
  sending.value = true
  try {
    // mode operate: the agent's own rights are the limit (members are capped server-side)
    const id = current.value.id
    const res = await $fetch<{ turn_id: string, message: Message, notice?: Message, queued?: Queued }>(`/api/conversations/${id}/messages`, { method: 'POST', body: { text, attachments: files.map(a => a.id), mode: 'operate', edit_mode: editMode.value, effort: effort.value, context: props.pageContext?.() ?? '' } })
    if (current.value?.id !== id) return // switched chats while this was in flight: the reply belongs to the old one
    if (res.queued) { // it answers: sent once it is free (the live row may have it already)
      if (!queuedHere.value.some(q => q.id === res.queued!.id)) setQueued(id, [...queuedHere.value, res.queued])
      scrollDown(true) // theirs: in view
      return
    }
    const first = !messages.value.some(m => m.id !== res.message.id && !m.id.startsWith('tmp-'))
    // the server's push of this message may have come first (ADR-078): once only
    if (!messages.value.some(m => m.id === res.message.id)) messages.value.push(res.message)
    if (first) refreshConvs() // the server titles a conversation from its first message
    // only paused agents were called: their notice, nothing streams
    if (res.notice && !messages.value.some(m => m.id === res.notice!.id)) messages.value.push(res.notice)
    if (res.turn_id) follow(res.turn_id)
    scrollDown(true) // the person's own message: in view
  } catch (e) {
    restore(text, files)
    const d = (e as { data?: { code?: string, error?: string } }).data
    if (d?.code === 'budget') {
      toast.add({ title: t('chat.budgetHit'), description: d.error, color: 'warning', actions: [{ label: t('chat.seeCosts'), onClick: () => { navigateTo(`/projects/${props.projectId}?tab=info`) } }] })
    } else {
      toast.add({ title: apiError(e), color: 'error' })
    }
  } finally {
    shownQueued.value = shownQueued.value.filter(q => q.id !== shown.id)
    messages.value = messages.value.filter(m => m.id !== shown.id)
    sending.value = false
  }
}
function restore(text: string, files: Attachment[]) {
  draft.value = [text, draft.value].filter(Boolean).join('\n\n')
  draftFiles.value = [...files, ...draftFiles.value]
}

function follow(id: string) {
  stopStream()
  turnId = id
  streaming.value = true
  liveText.value = ''
  liveTools.value = []
  liveStatus.value = ''
  let afterTool = false // text after a tool call is a new paragraph, not glued to the last one
  unfollow = followTurn<ChatEvent>(id, (ev) => {
    switch (ev.type) {
      case 'status': liveStatus.value = ev.text ?? ''; break
      case 'text':
        if (afterTool && liveText.value && ev.text) liveText.value += '\n\n'
        afterTool = false
        liveText.value += ev.text ?? ''; scrollDown(); break
      case 'tool': if (ev.tool) liveTools.value.push(ev.tool); afterTool = true; scrollDown(); break
      case 'done':
      case 'error':
        if (ev.message) {
          if (!messages.value.some(x => x.id === ev.message!.id)) messages.value.push(ev.message) // the live refresh may have it already
          if (props.purpose === 'automation' && ev.type === 'done') fencedBlocks(ev.message.content, 'automation').forEach(p => emit('automation-patch', p))
          if (props.purpose === 'skill' && ev.type === 'done') fencedBlocks(ev.message.content, 'skill').forEach(p => emit('skill-patch', p))
          if (props.purpose === 'workflow' && ev.type === 'done') fencedBlocks(ev.message.content, 'workflow').forEach(p => emit('workflow-patch', p))
        }
        if (ev.next_turn_id) { // the next agent tagged answers now
          follow(ev.next_turn_id)
          scrollDown()
          break
        }
        finishStream()
        // what waited was sent as this ended: its answer streams now
        if (current.value?.active_turn && current.value.active_turn !== id) follow(current.value.active_turn)
        afterTurn()
        if (!single.value) refreshConvs()
        scrollDown()
        break
    }
  }, () => {
    // the turn is gone (finished before we followed it): reload the thread
    finishStream()
    if (current.value) open(current.value)
  })
}

function finishStream() {
  unfollow?.()
  unfollow = null
  streaming.value = false
  liveText.value = ''
  liveTools.value = []
}
function stopStream() {
  unfollow?.()
  unfollow = null
  streaming.value = false
}
// "Gửi ngay" on a queued message (as the CLI): the answer being written stops,
// what waits goes at once (the queue's watch sends it when the stream ends)
async function sendNow() {
  if (streaming.value && turnId) await cancelTurn(turnId)
}
async function cancel() {
  await unqueueAll()
  // Stop: the answer and every agent working in the background in this chat
  if (current.value && !single.value) await $fetch(`/api/conversations/${current.value.id}/stop`, { method: 'POST', body: {} }).catch(() => {})
  else if (turnId) await $fetch(`/api/chat/turns/${turnId}/cancel`, { method: 'POST', body: {} }).catch(() => {})
}

async function remove(c: Conversation) {
  if (!confirm(t('chat.deleteConfirm'))) return
  await $fetch(`/api/conversations/${c.id}`, { method: 'DELETE' })
  if (current.value?.id === c.id) {
    current.value = null
    messages.value = []
    if (ownsUrl.value) router.replace({ query: { ...route.query, c: undefined, m: undefined } })
  }
  refreshConvs()
}

// a proposal waiting from an older answer of its agent is "cũ": the agent
// may have changed course since
const staleCards = computed(() => {
  const last: Record<string, string> = {}
  for (const m of messages.value) if (m.role === 'assistant') last[m.author] = m.id
  const stale: Record<string, boolean> = {}
  for (const m of messages.value) {
    for (const c of [...(m.actions ?? []), ...m.patches]) stale[c.id] = last[m.author] !== m.id
  }
  return stale
})

// "Duyệt tất cả": the chat's waiting cards, by their number. Approved last
// first, all but the first are queued at once and run in order after it.
// A very big diff is left to be approved on its own (PatchCard asks).
const BIG_DIFF_FILES = 100
const BIG_DIFF_SIZE = 1 << 20
const bigDiff = (p: Patch) => p.files.length > BIG_DIFF_FILES || (p.size ?? p.diff.length) > BIG_DIFF_SIZE
const waitingCards = computed(() => messages.value.flatMap(m => [
  ...(m.actions ?? []).filter(a => a.status === 'pending').map(a => ({ m, kind: 'action' as const, id: a.id, seq: a.seq ?? 0, big: false })),
  ...m.patches.filter(p => p.status === 'pending').map(p => ({ m, kind: 'patch' as const, id: p.id, seq: p.seq ?? 0, big: bigDiff(p) }))
]).sort((a, b) => a.seq - b.seq))
const approvingAll = ref(false)
async function approveAll() {
  const cards = waitingCards.value.filter(c => !c.big)
  const left = waitingCards.value.length - cards.length
  if (!cards.length || !confirm(t('chat.approveAllConfirm', { n: cards.length }))) return
  approvingAll.value = true
  try {
    for (const c of [...cards].reverse()) {
      if (c.kind === 'action') {
        const res = await $fetch<{ action: ProposedAction }>(`/api/actions/${c.id}/approve`, { method: 'POST', body: {} })
        c.m.actions = (c.m.actions ?? []).map(x => x.id === c.id ? { ...res.action, seq: x.seq } : x)
      } else {
        const res = await $fetch<{ patch: Patch }>(`/api/patches/${c.id}/approve`, { method: 'POST', body: {} })
        onPatchUpdated(c.m, res.patch)
      }
    }
    toast.add({ title: t('chat.approveAllDone', { n: cards.length }), description: left ? t('chat.approveAllBig', { n: left }) : undefined, color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    approvingAll.value = false
  }
}

function onPatchUpdated(msg: Message, p: Patch) {
  const i = msg.patches.findIndex(x => x.id === p.id)
  if (i >= 0) msg.patches[i] = { ...p, seq: msg.patches[i]!.seq }
}


// ```automation {…}``` (or ```skill```) blocks of an answer: form changes (bad JSON is skipped)
function fencedBlocks(text: string, lang: string): Record<string, unknown>[] {
  const out: Record<string, unknown>[] = []
  // the closing fence starts a line: JSON has no raw newline, while a skill's body may hold ``` of its own
  for (const m of text.matchAll(new RegExp('```' + lang + '[ \\t]*\\n([\\s\\S]*?)\\n[ \\t]*```', 'g'))) {
    if (lang === 'workflow') { // a workflow is its whole file, not JSON
      out.push({ source: m[1]! })
      continue
    }
    try {
      const v = JSON.parse(m[1]!)
      if (v && typeof v === 'object' && !Array.isArray(v)) out.push(v)
    } catch { /* not JSON: ignore */ }
  }
  return out
}

// a code block's Apply (useMarkdown): the draft fills the editor next to the chat again
function onMarkdownClick(e: MouseEvent) {
  const btn = (e.target as HTMLElement).closest<HTMLElement>('[data-md-act="apply"]')
  const lang = btn?.dataset.lang
  const code = btn?.closest('.md-code')?.querySelector('code')?.textContent
  if (!lang || code == null || lang !== props.purpose) return
  const blocks = fencedBlocks('```' + lang + '\n' + code.replace(/\n$/, '') + '\n```', lang)
  if (!blocks.length) return toast.add({ title: t('chat.applyBad'), color: 'error' })
  for (const b of blocks) emit(`${lang}-patch` as 'workflow-patch', b)
}

// a skill an agent drafted in the chat (Claude Code may not write .claude/): the
// editor opens with it, a person reviews and saves
const skillDrafts = (text: string) => text.includes('```skill') ? fencedBlocks(text, 'skill').filter(d => typeof d.name === 'string' && typeof d.body === 'string') : []
function openSkillDraft(d: Record<string, unknown>) {
  try { sessionStorage.setItem('office.skillDraft', JSON.stringify(d)) } catch { /* private mode: the editor opens empty */ }
  navigateTo(`/projects/${props.projectId}/skills/edit`)
}

// a workflow editor's chat, followed from the chat list: the library's lives
// in the office assistant's project, a project's own in that project
async function openWorkflowEditor(id: string) {
  const asst = await $fetch<{ project_id: string }>('/api/assistant').catch(() => null)
  await navigateTo(asst?.project_id === props.projectId ? `/workflows/edit?c=${id}` : `/projects/${props.projectId}/workflows/edit?c=${id}`)
}

// the workflow runs of the open chat (ADR-098): a card each, in the thread
// where it started, kept up to date as the run goes
const runs = ref<WorkflowRun[]>([])
async function loadRuns() {
  const id = current.value?.id
  if (!id) { runs.value = []; return }
  try {
    const res = await $fetch<{ runs: WorkflowRun[] }>(`/api/projects/${props.projectId}/workflow-runs`, { query: { conversation: id } })
    if (current.value?.id === id) runs.value = res.runs
  } catch { /* workflows off, or offline: no cards */ }
}
watch(() => current.value?.id, (id, before) => {
  if (id !== before) runs.value = []
  void loadRuns()
})
useLive(['workflow_runs'], loadRuns)
function onRunUpdated(r: WorkflowRun) {
  runs.value = runs.value.map(x => x.id === r.id ? r : x)
}
// each card goes just before the first message after it started; one older
// than the messages loaded waits for "older"
const runsAt = computed(() => {
  const before = new Map<string, WorkflowRun[]>()
  const after: WorkflowRun[] = []
  const msgs = messages.value
  const first = msgs[0] ? Date.parse(msgs[0].created_at) : 0
  for (const r of [...runs.value].sort((a, b) => Date.parse(a.started_at) - Date.parse(b.started_at))) {
    const at = Date.parse(r.started_at)
    if (hasOlder.value && at < first) continue
    const m = msgs.find(x => Date.parse(x.created_at) > at)
    if (m) before.set(m.id, [...(before.get(m.id) ?? []), r])
    else after.push(r)
  }
  return { before, after }
})

async function openAutomation() {
  if (!props.automationId) return
  try {
    const res = await $fetch<{ conversation: Conversation }>(`/api/automations/${props.automationId}/conversation`, { method: 'POST', body: {} })
    emit('conversation', res.conversation.id)
    await open(res.conversation)
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const threadItems = computed(() => conversations.value.map(c => ({ label: c.title || t('chat.untitled'), value: c.id })))
// the corner chat's switcher: every chat, the current one checked
const threadMenuItems = computed(() => [threadItems.value.length
  ? threadItems.value.map(i => ({ label: i.label, icon: i.value === current.value?.id ? 'i-lucide-check' : undefined, onSelect: () => { threadPick.value = i.value } }))
  : [{ label: t('chat.none'), disabled: true }]])
const threadPick = computed({
  get: () => current.value?.id,
  set: (id?: string) => { const c = conversations.value.find(x => x.id === id); if (c) open(c) }
})

// an editor (skill, workflow) opened on its chat again: that chat, and the drafts it gave
async function openEditorChat(id: string) {
  try {
    await open({ id } as Conversation)
    emit('conversation', id)
    emit('history', messages.value.filter(m => m.role === 'assistant').flatMap(m => fencedBlocks(m.content, props.purpose!)))
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

// a saved skill or workflow: the chat it was written with, if any
async function openSubjectChat() {
  if (!props.subject) return
  try {
    const res = await $fetch<{ conversation: Conversation | null }>(`/api/projects/${props.projectId}/editor-chat`, { query: { purpose: props.purpose, subject: props.subject } })
    if (res.conversation && !current.value) await openEditorChat(res.conversation.id)
  } catch { /* none: a new one on first send */ }
}
// an editor's chat cleared: gone, the next message starts a new one
async function clearEditorChat() {
  const c = current.value
  if (!c || !confirm(t('chat.clearEditorConfirm'))) return
  try {
    if (streaming.value) await cancel()
    await $fetch(`/api/conversations/${c.id}`, { method: 'DELETE' })
  } catch (e) {
    return toast.add({ title: apiError(e), color: 'error' })
  }
  current.value = null
  messages.value = []
  emit('conversation', '')
  if (route.query.c === c.id) router.replace({ query: { ...route.query, c: undefined } })
  if (props.purpose === 'automation') openAutomation() // a saved automation: a new chat tied to it
}

onMounted(() => {
  if (props.purpose === 'automation') openAutomation()
  else if (props.purpose === 'skill' || props.purpose === 'workflow') {
    if (typeof route.query.c === 'string') openEditorChat(route.query.c)
    else openSubjectChat()
  }
  else if (props.conversationId) open({ id: props.conversationId } as Conversation).catch(() => { openFirst.value = true }) // gone: the latest one
  else if (ownsUrl.value && typeof route.query.draft === 'string' && !tookPrefill) {
    // handed over by the office assistant: a new chat with the message ready to send
    draft.value = route.query.draft
    router.replace({ query: { ...route.query, draft: undefined, c: undefined } })
  } else if (ownsUrl.value && typeof route.query.c === 'string' && !tookPrefill) open({ id: route.query.c } as Conversation, typeof route.query.m === 'string' ? route.query.m : undefined)
  else if (!tookPrefill) openFirst.value = true
})
// the latest chat opens once the list is there
const openFirst = ref(false)
watch([openFirst, convData], () => {
  if (!openFirst.value || !convData.value) return
  openFirst.value = false
  if (!current.value && conversations.value[0] && !tookPrefill) open(conversations.value[0])
})
// the chat shown, for a box that keeps it (the watch screen)
watch(() => current.value?.id, (id) => { if (id) emit('current', id) })
onBeforeUnmount(() => {
  stopStream()
  stopBackground()
})
</script>

<template>
  <div
    :data-project="projectId"
    class="@container/chat flex overflow-hidden rounded-lg border border-(--ui-border)"
    :class="compact || purpose ? 'h-full min-h-0' : 'min-h-[24rem] flex-1 @max-sm/chat:-m-3 @max-sm/chat:rounded-none @max-sm/chat:border-0'"
  >
    <!-- threads -->
    <aside v-if="!single && !compact" class="hidden w-60 shrink-0 flex-col border-e border-(--ui-border) @md/chat:flex">
      <ThreadList :loading="convsLoading" v-model:origin="origin" v-model:tag-filter="tagFilter" :tags="projectTags" :conversations="conversations" :agents="agents" :current-id="current?.id" :unread="unread.ids.value" :has-more="moreConvs" :loading-more="loadingMore" :menu="threadMenu" @open="open" @new="newConversation()" @more="loadMoreConvs" />
    </aside>
    <!-- a phone: the chats in a drawer -->
    <USlideover v-if="!single && !compact" v-model:open="threadsOpen" side="left" :title="t('chat.threads')" :ui="{ content: 'max-w-xs', body: 'p-0 sm:p-0 flex flex-col' }">
      <template #body>
        <ThreadList
          :loading="convsLoading" v-model:origin="origin" v-model:tag-filter="tagFilter" :tags="projectTags" :conversations="conversations" :agents="agents" :current-id="current?.id" :unread="unread.ids.value" :has-more="moreConvs" :loading-more="loadingMore" :menu="threadMenu" @more="loadMoreConvs"
          @open="(c) => { threadsOpen = false; open(c) }" @new="threadsOpen = false; newConversation()"
        />
      </template>
    </USlideover>

    <!-- thread -->
    <section class="relative flex min-w-0 flex-1 flex-col">
      <!-- a phone: which chat this is (tap: the drawer of chats), a new one -->
      <div v-if="!single && !compact" class="flex items-center gap-1 border-b border-(--ui-border) p-2 @md/chat:hidden">
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-panel-left" :aria-label="t('chat.threads')" @click="threadsOpen = true" />
        <button type="button" class="flex min-w-0 flex-1 items-center gap-1 rounded-md px-1 py-1 text-left text-sm font-medium hover:bg-(--ui-bg-elevated)" :aria-label="t('chat.threads')" @click="threadsOpen = true">
          <span class="truncate">{{ current?.title || t('chat.newThreadTitle') }}</span>
          <UIcon name="i-lucide-chevron-down" class="size-4 shrink-0 text-(--ui-text-muted)" />
        </button>
        <UButton v-if="isAdmin && waitingCards.length" size="sm" color="primary" variant="soft" icon="i-lucide-check-check" :label="t('chat.approveAll', { n: waitingCards.length })" :loading="approvingAll" :disabled="approvingAll" @click="approveAll" />
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-square-pen" :aria-label="t('chat.newThread')" @click="newConversation()" />
      </div>
      <!-- compact (the corner chat): back, this chat's title (click to switch), a new one -->
      <div v-if="compact && !single" class="flex items-center gap-1 border-b border-(--ui-border) p-2 @max-[420px]/chat:gap-0.5 @max-[420px]/chat:p-1">
        <slot v-if="pane" name="lead" />
        <UButton v-else size="sm" color="neutral" variant="ghost" icon="i-lucide-arrow-left" :aria-label="t('common.close')" @click="emit('back')" />
        <UDropdownMenu :items="threadMenuItems" :content="{ align: 'start' }" :ui="{ content: 'max-h-80 w-72' }" class="min-w-0 flex-1">
          <button type="button" class="flex min-w-0 flex-1 items-center gap-1 rounded-md px-2 py-1 text-left hover:bg-(--ui-bg-elevated) @max-[420px]/chat:px-1 @max-[420px]/chat:text-xs">
            <span class="truncate font-semibold">{{ current?.title || t('chat.newThreadTitle') }}</span>
            <UIcon name="i-lucide-chevron-down" class="size-4 shrink-0 text-(--ui-text-muted)" />
          </button>
        </UDropdownMenu>
        <UButton v-if="isAdmin && waitingCards.length" size="sm" color="primary" variant="soft" icon="i-lucide-check-check" :label="t('chat.approveAll', { n: waitingCards.length })" :loading="approvingAll" :disabled="approvingAll" @click="approveAll" />
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-plus" :aria-label="t('chat.newThread')" @click="newConversation()" />
        <slot v-if="pane" name="actions" />
      </div>
      <!-- this chat's tags: chips (✕ to take one off) and "Thêm tag" -->
      <div v-if="!single && current" class="flex flex-wrap items-center gap-1 border-b border-(--ui-border) px-3 py-1 @max-[420px]/chat:px-2 @max-[420px]/chat:py-0.5">
        <ChatTags :tags="current.tags ?? []" removable @remove="(x) => saveTags(current!, (tags) => tags.filter(y => y !== x))" />
        <UPopover :content="{ align: 'start' }">
          <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-tag" :label="current.tags?.length ? undefined : t('chatTag.add')" :aria-label="t('chatTag.add')" :title="t('chatTag.add')" />
          <template #content>
            <div class="w-64 p-2">
              <TagPicker :model-value="current.tags ?? []" :suggestions="projectTags" @update:model-value="(v) => { const c = current!; const before = c.tags ?? []; saveTags(c, (base) => applyTagDiff(base, before, v)) }" />
            </div>
          </template>
        </UPopover>
      </div>
      <div v-if="!single && (members.length > 1 || background.length)" class="flex flex-wrap items-center gap-2 border-b border-(--ui-border) px-3 py-1.5 text-xs @max-[420px]/chat:gap-1 @max-[420px]/chat:px-2 @max-[420px]/chat:py-1">
        <span class="text-(--ui-text-muted)">{{ t('chat.members') }}</span>
        <div class="flex -space-x-1.5">
          <span
            v-for="m in members" :key="m.agent_id" class="rounded-full ring-2 ring-(--ui-bg)"
            :title="`${m.agent_name} · ${permOf(m.level).label}${m.context_window ? ` · ${Math.round(m.context_tokens / m.context_window * 100)}% context` : ''}`"
          ><UDropdownMenu :items="agentMenu({ id: m.agent_id })" :content="{ align: 'start' }">
            <button type="button" class="block rounded-full" :aria-label="m.agent_name"><AgentAvatar :agent="agents.find(a => a.id === m.agent_id) ?? { id: m.agent_id, name: m.agent_name }" size="xs" /></button>
          </UDropdownMenu></span>
        </div>
        <span v-for="b in background" :key="b.turn_id" class="flex items-center gap-1 rounded-full bg-(--ui-bg-elevated) py-0.5 ps-2 pe-1 text-(--ui-text-muted)">
          <UIcon name="i-lucide-loader-circle" class="size-3 animate-spin" />{{ t('chat.working', { name: b.agent_name }) }}
          <button type="button" class="rounded-full px-1 hover:text-(--ui-error)" :aria-label="t('chat.stop')" :title="t('chat.stop')" @click="cancelTurn(b.turn_id)">
            <UIcon name="i-lucide-square" class="size-3" />
          </button>
        </span>
      </div>
      <div class="relative flex min-h-0 flex-1 flex-col">
      <div ref="listEl" class="min-w-0 space-y-3" :class="['flex-1 overflow-y-auto overflow-x-hidden p-3 @max-md/chat:space-y-2 @max-md/chat:p-2 @max-[420px]/chat:space-y-1.5 @max-[420px]/chat:p-1.5', page && '@max-sm/chat:pb-32' /* room for the floating input, open or not: nothing jumps */]" @scroll.passive="onListScroll">
        <!-- a skill editor's chat, followed from the chat list: back to its editor -->
        <div v-if="!single && current?.purpose === 'skill'" class="flex items-center gap-2 rounded-lg border border-(--ui-border) px-3 py-2 text-sm">
          <UIcon name="i-lucide-sparkles" class="size-4 shrink-0 text-(--ui-primary)" />
          <span class="min-w-0 flex-1 truncate text-(--ui-text-muted)">{{ t('chat.skillChat') }}</span>
          <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-pencil" :label="t('chat.backToSkillEditor')" :to="`/projects/${projectId}/skills/edit?c=${current.id}`" />
        </div>
        <div v-else-if="!single && current?.purpose === 'workflow'" class="flex items-center gap-2 rounded-lg border border-(--ui-border) px-3 py-2 text-sm">
          <UIcon name="i-lucide-workflow" class="size-4 shrink-0 text-(--ui-primary)" />
          <span class="min-w-0 flex-1 truncate text-(--ui-text-muted)">{{ t('chat.workflowChat') }}</span>
          <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-pencil" :label="t('chat.backToWorkflowEditor')" @click="openWorkflowEditor(current.id)" />
        </div>
        <!-- an editor's chat: cleared to start over -->
        <div v-if="single && current && messages.length" class="flex justify-end">
          <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-eraser" :label="t('chat.clearEditorChat')" @click="clearEditorChat" />
        </div>
        <div v-if="hasOlder" class="text-center">
          <UButton size="xs" color="neutral" variant="soft" icon="i-lucide-arrow-up" :loading="loadingOlder" :label="t('chat.older')" @click="loadOlder" />
        </div>
        <!-- the messages on their way -->
        <div v-if="loadingMsgs && !messages.length" class="space-y-5" aria-busy="true">
          <div v-for="i in 3" :key="i" class="space-y-4">
            <div class="flex justify-end"><USkeleton class="h-9 rounded-2xl" :style="{ width: `${30 + i * 10}%` }" /></div>
            <div class="space-y-2">
              <div class="flex items-center gap-2"><USkeleton class="size-5 rounded-full" /><USkeleton class="h-3 w-24" /></div>
              <USkeleton class="h-3.5 w-11/12" /><USkeleton class="h-3.5 w-4/5" /><USkeleton class="h-3.5 w-2/3" />
            </div>
          </div>
        </div>
        <div v-else-if="!messages.length && !streaming" class="flex h-full flex-col items-center justify-center gap-2 text-center text-(--ui-text-muted)">
          <UIcon name="i-lucide-messages-square" class="size-8" />
          <template v-if="purpose === 'automation'">
            <p class="text-sm">{{ t('chat.askAboutAutomation') }}</p>
            <p class="text-xs">{{ t('chat.automationHint') }}</p>
          </template>
          <template v-else-if="purpose === 'workflow'">
            <p class="text-sm">{{ t('wf.askAI') }}</p>
            <p class="text-xs">{{ t('wf.askAIHint') }}</p>
          </template>
          <template v-else-if="purpose === 'skill'">
            <p class="text-sm">{{ t('skill.askAI') }}</p>
            <p class="text-xs">{{ t('skill.askAIHint') }}</p>
          </template>
          <template v-else>
            <p class="text-sm">{{ t('chat.askAboutProject') }}</p>
            <!-- what the hint promises follows the picked agent's permission -->
            <p class="text-xs">{{ t(permRank(pickedLevel) >= permRank('edit') ? 'chat.editHint' : 'chat.readOnlyHint') }}</p>
          </template>
        </div>

        <template v-for="m in messages" :key="m.id">
          <WorkflowRunCard v-for="r in runsAt.before.get(m.id) ?? []" :key="r.id" :run="r" @updated="onRunUpdated" />
          <div v-if="m.role === 'user'" :id="`m-${m.id}`" class="group/msg flex flex-col items-end gap-1.5 rounded-lg transition" :class="marked === m.id && 'ring-2 ring-primary/60 ring-offset-4 ring-offset-(--ui-bg)'">
            <div v-if="senderOf(m)" class="flex max-w-[80%] flex-wrap items-center justify-end gap-x-1.5 text-xs text-(--ui-text-muted)">
              <UIcon :name="sourceIcon[senderOf(m)!.kind]" class="size-3.5 shrink-0" :title="t(`source.${senderOf(m)!.kind}`)" />
              <template v-if="senderOf(m)!.account">
                <UIcon name="i-lucide-user-check" class="size-3.5 shrink-0" :title="t('chat.senderAccount')" />
                <span class="font-medium text-(--ui-text)">{{ senderOf(m)!.account }}</span>
                <span v-if="senderOf(m)!.account_id === me?.id">({{ t('chat.senderYou') }})</span>
                <span>· {{ senderOf(m)!.name || senderOf(m)!.username }}</span>
              </template>
              <template v-else>
                <span class="font-medium text-(--ui-text)">{{ senderOf(m)!.name || senderOf(m)!.username || senderOf(m)!.id }}</span>
                <span v-if="senderOf(m)!.name && senderOf(m)!.username">@{{ senderOf(m)!.username }}</span>
              </template>
              <span v-if="m.sender">· {{ senderOf(m)!.private ? t('chat.senderDm') : t(`chat.senderIn.${senderOf(m)!.kind}`) }}</span>
              <span>· {{ when(m.created_at) }}</span>
              <a v-if="senderOf(m)!.link" :href="senderOf(m)!.link" target="_blank" rel="noopener" class="hover:text-(--ui-text)" :aria-label="t('chat.senderOpen')" :title="t('chat.senderOpen')"><UIcon name="i-lucide-external-link" class="size-3.5" /></a>
            </div>
            <!-- an agent sent it from another chat: who, which project, which chat -->
            <div v-if="m.relay" class="flex max-w-[80%] flex-wrap items-center justify-end gap-x-1.5 text-xs text-(--ui-text-muted)">
              <UIcon :name="m.relay.report ? 'i-lucide-corner-down-left' : 'i-lucide-bot'" class="size-3.5 shrink-0 text-primary" />
              <span>{{ t(m.relay.report ? 'chat.relayReport' : 'chat.relaySent') }}</span>
              <span class="font-medium text-(--ui-text)">{{ m.relay.agent }}</span>
              <span v-if="m.relay.project">· {{ m.relay.project }}</span>
              <span>· {{ t('chat.relayFrom') }}</span>
              <a v-if="m.relay.url" :href="m.relay.url" target="_blank" rel="noopener" class="max-w-60 truncate text-primary hover:underline" :title="m.relay.title">
                {{ m.relay.title || t('chat.relayUntitled') }}
              </a>
              <NuxtLink v-else-if="m.relay.project_id" :to="`/projects/${m.relay.project_id}?tab=chat&c=${m.relay.conversation_id}`" class="max-w-60 truncate text-primary hover:underline" :title="m.relay.title">
                {{ m.relay.title || t('chat.relayUntitled') }}
              </NuxtLink>
              <span v-else class="max-w-60 truncate">{{ m.relay.title || t('chat.relayUntitled') }}</span>
              <span>· {{ when(m.created_at) }}</span>
            </div>
            <AttachmentList :items="m.attachments ?? []" align="end" />
            <div class="flex max-w-[80%] items-start gap-1">
              <UDropdownMenu :items="messageMenu(m)" :content="{ align: 'end' }">
                <button type="button" class="invisible mt-1.5 rounded px-0.5 text-(--ui-text-dimmed) hover:text-(--ui-text) group-hover/msg:visible data-[state=open]:visible" :aria-label="t('chat.more')">
                  <UIcon name="i-lucide-ellipsis" class="size-4" />
                </button>
              </UDropdownMenu>
              <!-- a linked project's report back: its text folded (who and where above) -->
              <div v-if="m.relay?.report" class="min-w-0 rounded-2xl rounded-br-sm border border-(--ui-border) bg-(--ui-bg-elevated)/60 px-3.5 py-2 text-sm">
                <details class="group/rep">
                  <summary class="flex cursor-pointer list-none items-center gap-1 text-xs font-medium hover:text-primary">
                    <UIcon name="i-lucide-chevron-right" class="size-3.5 transition group-open/rep:rotate-90" />{{ t('chat.reportShow') }}
                  </summary>
                  <!-- eslint-disable-next-line vue/no-v-html -->
                  <div class="markdown mt-1.5 min-w-0" @click="onMarkdownClick" v-html="renderMarkdown(reportBody(m.content))" />
                </details>
              </div>
              <div v-else-if="m.content" class="min-w-0 whitespace-pre-wrap rounded-2xl rounded-br-sm bg-(--ui-primary) px-3.5 py-2 text-sm text-white @max-[420px]/chat:px-2 @max-[420px]/chat:py-1 @max-[420px]/chat:text-xs"><template v-for="(p, i) in tagged(m.content)" :key="i"><span v-if="p.tag" class="rounded bg-white/20 px-0.5 font-medium">{{ p.text }}</span><template v-else>{{ p.text }}</template></template></div>
            </div>
          </div>
          <div v-else-if="m.role === 'error'" class="flex items-start gap-2 text-sm text-(--ui-error)">
            <UIcon name="i-lucide-circle-alert" class="mt-0.5 size-4 shrink-0" />
            <span>{{ m.content }}</span>
          </div>
          <div v-else :id="`m-${m.id}`" class="group/msg min-w-0 space-y-2 rounded-lg transition" :class="marked === m.id && 'ring-2 ring-primary/60 ring-offset-4 ring-offset-(--ui-bg)'">
            <div class="flex items-center gap-2 text-xs text-(--ui-text-muted)">
              <UDropdownMenu v-if="agents.some(a => a.name === m.author)" :items="agentMenu(agents.find(a => a.name === m.author))" :content="{ align: 'start' }">
                <button type="button" class="block rounded-full" :aria-label="m.author"><AgentAvatar :agent="agents.find(a => a.name === m.author)!" size="xs" /></button>
              </UDropdownMenu>
              <AgentAvatar v-else :agent="{ name: m.author }" size="xs" />
              <span class="font-medium">{{ m.author }}</span>
              <UBadge v-if="m.consult" color="info" variant="subtle" size="sm" icon="i-lucide-graduation-cap" :title="m.consult.office ? t('chat.expertFromOffice', { office: m.consult.office }) : undefined">
                <NuxtLink v-if="m.consult.link" :to="m.consult.link" class="hover:underline">{{ t('chat.expert') }}</NuxtLink>
                <template v-else>{{ t('chat.expert') }}</template>
              </UBadge>
              <span>{{ when(m.created_at) }}</span>
              <span v-if="m.cost_usd">· ${{ m.cost_usd.toFixed(3) }}</span>
              <UDropdownMenu :items="messageMenu(m)" :content="{ align: 'start' }">
                <button type="button" class="invisible rounded px-0.5 text-(--ui-text-dimmed) hover:text-(--ui-text) group-hover/msg:visible data-[state=open]:visible" :aria-label="t('chat.more')">
                  <UIcon name="i-lucide-ellipsis" class="size-4" />
                </button>
              </UDropdownMenu>
            </div>
            <div v-if="m.provider || m.model" class="-mt-1.5 truncate ps-8 text-[11px] text-(--ui-text-dimmed)">{{ [m.provider, m.model].filter(Boolean).join(' · ') }}</div>
            <details v-if="m.tools.length" class="text-xs text-(--ui-text-muted)">
              <summary class="cursor-pointer">{{ t('chat.toolsUsed', { n: m.tools.length }) }}</summary>
              <ul class="mt-1 space-y-0.5 ps-4">
                <li v-for="(t, i) in m.tools" :key="i" :class="t.error ? 'text-(--ui-error)' : ''">{{ t.summary }}</li>
              </ul>
            </details>
            <!-- eslint-disable-next-line vue/no-v-html -->
            <div class="markdown min-w-0 text-sm @max-[420px]/chat:text-xs" :class="purpose && `md-apply-${purpose}`" @click="onMarkdownClick" v-html="renderMarkdown(m.content)" />
            <UButton
              v-for="(d, i) in (purpose || current?.purpose === 'skill' || current?.purpose === 'workflow' ? [] : skillDrafts(m.content))" :key="`sk${i}`"
              icon="i-lucide-sparkles" size="sm" color="neutral" variant="outline" :label="t('chat.openSkillEditor', { name: String(d.name) })"
              @click="openSkillDraft(d)"
            />
            <ActionCard
              v-for="a in (m.actions ?? []).filter(x => !x.after_diff)" :key="a.id" :action="a" :project-id="projectId" :stale="staleCards[a.id]"
              @updated="(na: ProposedAction) => m.actions = (m.actions ?? []).map(x => x.id === na.id ? { ...na, seq: x.seq } : x)"
            />
            <PatchCard v-for="p in m.patches" :key="p.id" :patch="p" :project-id="projectId" :stale="staleCards[p.id]" @updated="(np: Patch) => onPatchUpdated(m, np)" />
            <!-- a commit asked with the edits comes after their diff (ADR-189) -->
            <ActionCard
              v-for="a in (m.actions ?? []).filter(x => x.after_diff)" :key="a.id" :action="a" :project-id="projectId" :stale="staleCards[a.id]"
              @updated="(na: ProposedAction) => m.actions = (m.actions ?? []).map(x => x.id === na.id ? { ...na, seq: x.seq } : x)"
            />
          </div>
        </template>
        <WorkflowRunCard v-for="r in runsAt.after" :key="r.id" :run="r" @updated="onRunUpdated" />
        <!-- a linked project's chat this one handed work to waits for a person: decided here -->
        <div v-for="lp in linkedPending" :key="lp.conversation_id" class="space-y-2 rounded-lg border border-(--ui-warning)/40 bg-(--ui-warning)/5 p-3">
          <p class="flex items-center gap-1.5 text-xs text-(--ui-text-muted)">
            <UIcon name="i-lucide-waypoints" class="size-3.5 text-(--ui-warning)" />
            {{ t('chat.linkedPending', { project: lp.project }) }}
            <NuxtLink :to="`/projects/${lp.project_id}?tab=chat&c=${lp.conversation_id}`" class="ms-auto truncate text-primary hover:underline">{{ lp.title || t('chat.linkedOpen') }}</NuxtLink>
          </p>
          <ActionCard
            v-for="a in lp.actions" :key="a.id" :action="a" :project-id="lp.project_id"
            @updated="(na: ProposedAction) => lp.actions = lp.actions.map(x => x.id === na.id ? na : x)"
          />
          <PatchCard v-for="p in lp.patches" :key="p.id" :patch="p" :project-id="lp.project_id" @updated="(np: Patch) => lp.patches = lp.patches.map(x => x.id === np.id ? np : x)" />
        </div>

        <div v-if="streaming && !runs.some(r => r.status === 'running' && r.caller_conversation_id === current?.id)" class="space-y-2">
          <div class="flex items-center gap-2 text-xs text-(--ui-text-muted)">
            <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin text-primary" />
            <span>{{ liveStatus || t('chat.replying') }}</span>
          </div>
          <ul v-if="liveTools.length" class="space-y-0.5 ps-6 text-xs text-(--ui-text-muted)">
            <li v-for="(t, i) in liveTools" :key="i" class="line-clamp-2 break-all" :title="t.summary">{{ t.summary }}</li>
          </ul>
          <!-- eslint-disable-next-line vue/no-v-html -->
          <div v-if="liveText" class="markdown text-sm" v-html="renderMarkdown(liveText)" />
        </div>

        <!-- written while it answers: sent next, greyed until then -->
        <div v-for="q in queuedHere" :key="q.id" class="flex flex-col items-end gap-1 opacity-50">
          <div v-if="q.text" class="min-w-0 max-w-[85%] whitespace-pre-wrap rounded-2xl rounded-br-sm bg-(--ui-bg-accented) px-3.5 py-2 text-sm">{{ q.text }}</div>
          <div class="flex items-center gap-1 text-xs text-(--ui-text-muted)">
            <UIcon name="i-lucide-clock" class="size-3.5" />
            <span>{{ q.attachments.length ? `${t('chat.queued')} · ${q.attachments.map(f => f.name).join(', ')}` : t('chat.queued') }}</span>
            <button v-if="streaming" type="button" class="flex items-center gap-0.5 rounded px-1 py-0.5 text-(--ui-text) hover:text-primary" :title="t('chat.sendNowHint')" @click="sendNow">
              <UIcon name="i-lucide-zap" class="size-3.5" />{{ t('chat.sendNow') }}
            </button>
            <button v-if="!q.id.startsWith('tmp-')" type="button" class="rounded p-0.5 hover:text-(--ui-error)" :aria-label="t('chat.unqueue')" :title="t('chat.unqueue')" @click="unqueue(q)">
              <UIcon name="i-lucide-x" class="size-3.5" />
            </button>
          </div>
        </div>
      </div>
      <!-- scrolled up to read: down to the newest (as Discord), saying when something came -->
      <Transition enter-from-class="opacity-0 translate-y-2" leave-to-class="opacity-0 translate-y-2" enter-active-class="transition" leave-active-class="transition">
        <UButton
          v-if="!pinned && messages.length" size="sm" :color="unseen ? 'primary' : 'neutral'" :variant="unseen ? 'solid' : 'outline'" icon="i-lucide-arrow-down"
          :label="unseen ? t('chat.newMessages') : undefined" :aria-label="t('chat.toLatest')" :title="t('chat.toLatest')"
          :class="['absolute bottom-3 left-1/2 z-10 -translate-x-1/2 rounded-full shadow-lg', !unseen && 'bg-(--ui-bg)', page && '@max-sm/chat:bottom-20']"
          @click="toEnd"
        />
      </Transition>
      </div>

      <p v-if="current?.cleaned" class="flex items-center gap-2 border-t border-(--ui-border) p-3 text-sm text-(--ui-text-muted) @max-md/chat:p-2">
        <UIcon name="i-lucide-archive" class="size-4 shrink-0" />{{ t('chat.cleaned') }}
      </p>
      <form
        v-else
        :class="['border-t border-(--ui-border) p-2 @max-md/chat:p-1.5', page && (composeOpen
          ? '@max-sm/chat:absolute @max-sm/chat:inset-x-2 @max-sm/chat:bottom-2 @max-sm/chat:z-10 @max-sm/chat:border-0 @max-sm/chat:p-0 @max-sm/chat:[&>*]:shadow-lg'
          : '@max-sm/chat:hidden')]"
        @submit.prevent="send"
      >
        <component :is="isOfficeAdmin ? NuxtLink : 'p'" v-if="pickedBroken" :to="isOfficeAdmin ? '/providers' : undefined" class="mb-1.5 flex items-center gap-1.5 px-1 text-xs text-(--ui-error) hover:underline" :title="pickedBroken.status_detail">
          <UIcon name="i-lucide-plug-zap" class="size-3.5 shrink-0" />
          <span class="truncate">{{ t('chat.providerBroken', { name: pickedBroken.name, detail: pickedBroken.status_detail }) }}</span>
        </component>
        <PromptInput
          ref="prompt" v-model="draft" v-model:attachments="draftFiles" :project-id="projectId"
          :placeholder="picked ? t('chat.placeholderWithAgent', { agent: picked.name }) : t('chat.placeholderNoAgent')"
          :mentions="single ? [] : onAgents.map(a => ({ name: a.name, label: permOf(agentLevel(a.permissions)).label, icon: permOf(agentLevel(a.permissions)).icon }))"
          @submit="send"
        >
          <template #actions>
            <ContextMeter :tokens="current?.context_tokens" :window="current?.context_window" />
            <EffortSelect v-if="pickedEfforts.length" v-model="effort" :options="pickedEfforts" :fallback="picked?.effort ?? ''" size="sm" variant="ghost" class="min-w-0 max-w-44 shrink" :ui="iconOnPhone" :title="t('chat.effort')" :aria-label="t('chat.effort')" />
            <EditModePicker v-if="permRank(pickedLevel) >= permRank('propose')" v-model="editMode" class="min-w-0 shrink" />
            <UButton v-if="streaming" size="sm" icon="i-lucide-square" color="neutral" variant="outline" :label="t('chat.stop')" @click="cancel" />
            <!-- while it answers: queued, sent next -->
            <UButton size="sm" type="submit" icon="i-lucide-send" class="shrink-0" :loading="sending && !streaming" :disabled="!draft.trim() && !draftFiles.length" :title="streaming || sending ? t('chat.queued') : undefined" />
            <UButton v-if="page" size="sm" color="neutral" variant="ghost" icon="i-lucide-x" :class="'shrink-0 @sm/chat:hidden'" :aria-label="t('common.close')" @click="composeOpen = false" />
          </template>
        </PromptInput>
      </form>
      <UButton
        v-if="page && !composeOpen && !current?.cleaned" class="absolute bottom-3 end-3 z-10 rounded-full shadow-lg @sm/chat:hidden" size="lg" icon="i-lucide-message-circle"
        :aria-label="t('chat.write')" @click="composeOpen = true; nextTick(() => prompt?.focus?.())"
      />
    </section>
    <!-- "Gắn tag" from a chat's menu in the list -->
    <UModal :open="!!tagEdit" :title="t('chatTag.title')" :description="tagEdit?.title || t('chat.newThreadTitle')" @update:open="(o) => { if (!o) tagEdit = null }">
      <template #body>
        <TagPicker v-if="tagEdit" :model-value="tagEdit.tags ?? []" :suggestions="projectTags" @update:model-value="(v) => { const c = tagEdit!; const before = c.tags ?? []; saveTags(c, (base) => applyTagDiff(base, before, v)) }" />
      </template>
    </UModal>
  </div>
</template>

