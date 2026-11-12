<script setup lang="ts">
// Other offices (ADR-167): pair with a one-time code, pick which projects of
// this office the other may reach; each project then links to theirs in
// Permissions & commands → Linked projects.
interface RemoteProject { id: string, name: string, description?: string, assistant?: boolean }
interface Peer { id: string, name: string, url: string, self_url: string, shared: string[], remote: RemoteProject[], admin: boolean, as: string, created_at: string, last_used_at?: string }

definePageMeta({ admin: true })
const toast = useToast()
const { t, when } = useLang()

interface PairRequest { id: string, name: string, url: string, code: string, ip: string, created_at: string }
interface Outgoing { id: string, url: string, code: string, expires: string }
const { data, refresh } = await useFetch<{ peers: Peer[], self_name: string, requests: PairRequest[], outgoing: Outgoing[] }>('/api/peers')
const peers = computed(() => data.value?.peers ?? [])
const { data: projData } = useLiveFetch<{ projects: Project[] }>('/api/projects', { lazy: true })
const projectItems = computed(() => (projData.value?.projects ?? []).map(p => ({ label: p.name, value: p.id })))

const origin = import.meta.client ? window.location.origin : ''

// ---- invite: this office makes a code the other pastes ----
const inviteOpen = ref(false)
const invite = reactive({ name: '', url: '', shared: [] as string[] })
const code = ref('')
const inviting = ref(false)
function openInvite() {
  Object.assign(invite, { name: data.value?.self_name ?? '', url: origin, shared: [] })
  code.value = ''
  inviteOpen.value = true
}
async function makeInvite() {
  inviting.value = true
  try {
    code.value = (await $fetch<{ code: string }>('/api/peers/invite', { method: 'POST', body: invite })).code
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    inviting.value = false
  }
}
async function copyCode() {
  toast.add({ title: (await copyText(code.value)) ? t('peers.copied') : t('peers.copyFailed'), color: 'neutral' })
}

// ---- connect by address (ADR-177): the other office's admin accepts ----
const connectOpen = ref(false)
const conn = reactive({ url: '', name: '', self_url: '', shared: [] as string[] })
const sentCode = ref('')
const connecting = ref(false)
function openConnect() {
  Object.assign(conn, { url: '', name: data.value?.self_name ?? '', self_url: origin, shared: [] })
  sentCode.value = ''
  connectOpen.value = true
}
async function doConnect() {
  connecting.value = true
  try {
    sentCode.value = (await $fetch<Outgoing>('/api/peers/connect', { method: 'POST', body: conn })).code
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    connecting.value = false
  }
}
const cancelConnect = (o: Outgoing) => $fetch(`/api/peers/outgoing/${o.id}`, { method: 'DELETE' }).then(() => refresh())

// requests other offices sent: accepted once the codes match
const reqShared = reactive<Record<string, string[]>>({})
const reqBusy = ref('')
async function answerRequest(r: PairRequest, accept: boolean) {
  if (!accept && !confirm(t('peers.rejectConfirm', { name: r.name }))) return
  reqBusy.value = r.id
  try {
    if (accept) await $fetch(`/api/peers/requests/${r.id}/accept`, { method: 'POST', body: { name: data.value?.self_name ?? '', shared: reqShared[r.id] ?? [] } })
    else await $fetch(`/api/peers/requests/${r.id}`, { method: 'DELETE' })
    if (accept) toast.add({ title: t('peers.joined', { name: r.name }), color: 'success' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    reqBusy.value = ''
  }
}

// ---- join: paste the other's code ----
const joinOpen = ref(false)
const join = reactive({ code: '', name: '', url: '' })
const joining = ref(false)
function openJoin() {
  Object.assign(join, { code: '', name: data.value?.self_name ?? '', url: origin })
  joinOpen.value = true
}
async function doJoin() {
  joining.value = true
  try {
    const p = await $fetch<Peer>('/api/peers/join', { method: 'POST', body: join })
    toast.add({ title: t('peers.joined', { name: p.name }), color: 'success' })
    joinOpen.value = false
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    joining.value = false
  }
}

// ---- each paired office ----
const shared = reactive<Record<string, string[]>>({})
watch(peers, list => list.forEach((p) => { shared[p.id] = [...p.shared] }), { immediate: true })
const busy = ref('')
async function act(p: Peer, fn: () => Promise<unknown>, done?: string) {
  busy.value = p.id
  try {
    await fn()
    if (done) toast.add({ title: done, color: 'success' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
const saveShared = (p: Peer) => act(p, () => $fetch(`/api/peers/${p.id}/shared`, { method: 'PUT', body: { shared: shared[p.id] } }), t('common.saved'))
// Administrator: the other office runs this office's assistant with the machine
function setAdmin(p: Peer, on: boolean) {
  if (on && !confirm(t('peers.adminConfirm', { name: p.name }))) return
  act(p, () => $fetch(`/api/peers/${p.id}/admin`, { method: 'PUT', body: { admin: on } }), t('common.saved'))
}
const refreshPeer = (p: Peer) => act(p, () => $fetch(`/api/peers/${p.id}/refresh`, { method: 'POST' }))
function removePeer(p: Peer) {
  if (confirm(t('peers.removeConfirm', { name: p.name }))) act(p, () => $fetch(`/api/peers/${p.id}`, { method: 'DELETE' }))
}
const changed = (p: Peer) => JSON.stringify(shared[p.id] ?? []) !== JSON.stringify(p.shared)
</script>

<template>
  <PageShell :title="t('peers.title')">
    <template #actions>
      <InfoTip :text="t('peers.help')" icon-class="size-4 text-(--ui-text-dimmed)" />
      <UButton color="neutral" variant="outline" icon="i-lucide-log-in" :label="t('peers.join')" @click="openJoin" />
      <UButton color="neutral" variant="outline" icon="i-lucide-ticket" :label="t('peers.invite')" @click="openInvite" />
      <UButton icon="i-lucide-plug" :label="t('peers.connect')" @click="openConnect" />
    </template>

    <div class="max-w-4xl space-y-3">
      <UCard v-for="r in data?.requests ?? []" :key="r.id" class="border-(--ui-warning)/50" :ui="{ body: 'space-y-3 sm:p-4' }">
        <div class="flex flex-wrap items-center gap-2">
          <UIcon name="i-lucide-plug" class="size-4 text-(--ui-warning)" />
          <span class="font-medium">{{ t('peers.requestFrom', { name: r.name }) }}</span>
          <span class="truncate text-xs text-(--ui-text-muted)">{{ r.url }} · IP {{ r.ip }} · {{ when(r.created_at) }}</span>
        </div>
        <p class="flex flex-wrap items-center gap-2 text-sm">
          {{ t('peers.matchCode') }}
          <code class="rounded bg-(--ui-bg-elevated) px-2 py-0.5 text-base font-semibold tracking-widest">{{ r.code }}</code>
          <InfoTip :text="t('peers.matchHelp')" icon-class="size-3.5 text-(--ui-text-dimmed)" />
        </p>
        <div class="flex flex-wrap items-center gap-2">
          <span class="text-sm">{{ t('peers.shared') }}</span>
          <USelectMenu v-model="reqShared[r.id]" :items="projectItems" value-key="value" multiple size="sm" :placeholder="t('peers.none')" class="w-full sm:w-72" />
          <div class="ms-auto flex gap-2">
            <UButton size="sm" color="neutral" variant="ghost" :label="t('peers.reject')" :loading="reqBusy === r.id" @click="answerRequest(r, false)" />
            <UButton size="sm" icon="i-lucide-check" :label="t('peers.accept')" :loading="reqBusy === r.id" @click="answerRequest(r, true)" />
          </div>
        </div>
      </UCard>
      <div v-for="o in data?.outgoing ?? []" :key="o.id" class="flex flex-wrap items-center gap-2 rounded-lg border border-dashed border-(--ui-border) px-3 py-2 text-sm">
        <UIcon name="i-lucide-hourglass" class="size-4 text-(--ui-text-muted)" />
        <span>{{ t('peers.waiting', { url: o.url }) }}</span>
        <code class="rounded bg-(--ui-bg-elevated) px-2 py-0.5 font-semibold tracking-widest">{{ o.code }}</code>
        <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-x" class="ms-auto" :title="t('common.cancel')" @click="cancelConnect(o)" />
      </div>
      <div
        v-if="!peers.length && !data?.requests?.length && !data?.outgoing?.length"
        class="flex flex-col items-center justify-center gap-1.5 rounded-lg border border-dashed border-(--ui-border) px-4 py-10 text-center"
      >
        <UIcon name="i-lucide-building-2" class="size-5 text-(--ui-text-dimmed)" />
        <p class="text-sm text-(--ui-text-dimmed)">{{ t('peers.empty') }}</p>
      </div>
      <UCard v-for="p in peers" :key="p.id" :ui="{ body: 'space-y-3 sm:p-4' }">
        <div class="flex flex-wrap items-center gap-2">
          <UIcon name="i-lucide-building-2" class="size-4 text-primary" />
          <span class="font-medium">{{ p.name }}</span>
          <a :href="p.url" target="_blank" rel="noopener" class="truncate text-xs text-(--ui-text-muted) hover:underline">{{ p.url }}</a>
          <span class="ms-auto text-xs text-(--ui-text-dimmed)">{{ p.last_used_at ? t('peers.lastUsed', { when: when(p.last_used_at) }) : t('peers.neverUsed') }}</span>
          <UButton size="xs" color="error" variant="ghost" icon="i-lucide-unlink" :title="t('peers.remove')" :loading="busy === p.id" @click="removePeer(p)" />
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <span class="flex items-center gap-1.5 text-sm">{{ t('peers.shared') }}
            <InfoTip :text="t('peers.sharedHelp', { as: p.as })" icon-class="size-3.5 text-(--ui-text-dimmed)" />
          </span>
          <USelectMenu v-model="shared[p.id]" :items="projectItems" value-key="value" multiple size="sm" :placeholder="t('peers.none')" class="w-full sm:w-72" />
          <UButton v-if="changed(p)" size="xs" icon="i-lucide-save" :label="t('common.save')" :loading="busy === p.id" @click="saveShared(p)" />
        </div>
        <div class="flex items-center gap-2">
          <USwitch :model-value="p.admin" :disabled="busy === p.id" color="error" @update:model-value="setAdmin(p, $event)" />
          <span class="flex items-center gap-1.5 text-sm">{{ t('peers.admin') }}
            <InfoTip :text="t('peers.adminHelp', { as: p.as })" icon-class="size-3.5 text-(--ui-text-dimmed)" />
          </span>
        </div>
        <div class="flex flex-wrap items-center gap-1.5">
          <span class="flex items-center gap-1.5 text-sm">{{ t('peers.remote') }}
            <InfoTip :text="t('peers.remoteHelp')" icon-class="size-3.5 text-(--ui-text-dimmed)" />
          </span>
          <UBadge v-for="r in p.remote" :key="r.id" :color="r.assistant ? 'error' : 'neutral'" variant="subtle" :icon="r.assistant ? 'i-lucide-shield-alert' : undefined" :title="r.assistant ? t('peers.remoteAdmin') : r.description">
            {{ r.name }}
          </UBadge>
          <span v-if="!p.remote.length" class="text-xs text-(--ui-text-dimmed)">{{ t('peers.none') }}</span>
          <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-refresh-cw" :title="t('common.refresh')" :loading="busy === p.id" @click="refreshPeer(p)" />
        </div>
      </UCard>
    </div>

    <UModal v-model:open="inviteOpen" :title="t('peers.invite')">
      <template #body>
        <form v-if="!code" id="peer-invite" class="space-y-4" @submit.prevent="makeInvite">
          <UFormField :label="t('peers.selfName')" required>
            <UInput v-model="invite.name" class="w-full" />
          </UFormField>
          <UFormField required>
            <template #label>
              <span class="flex items-center gap-1.5">{{ t('peers.selfUrl') }}<InfoTip :text="t('peers.selfUrlHelp')" icon-class="size-3.5 text-(--ui-text-muted)" /></span>
            </template>
            <UInput v-model="invite.url" class="w-full font-mono" placeholder="https://office.example.com" />
          </UFormField>
          <UFormField :label="t('peers.shared')">
            <USelectMenu v-model="invite.shared" :items="projectItems" value-key="value" multiple :placeholder="t('peers.none')" class="w-full" />
          </UFormField>
        </form>
        <div v-else class="space-y-2">
          <p class="text-sm text-(--ui-text-muted)">{{ t('peers.codeHelp') }}</p>
          <div class="flex items-center gap-2">
            <code class="min-w-0 flex-1 truncate rounded bg-(--ui-bg-elevated) px-2 py-1 text-xs">{{ code }}</code>
            <UButton size="sm" icon="i-lucide-copy" :label="t('common.copy')" @click="copyCode" />
          </div>
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t(code ? 'peers.close' : 'common.cancel')" @click="inviteOpen = false; refresh()" />
          <UButton v-if="!code" type="submit" form="peer-invite" :loading="inviting" :label="t('peers.makeCode')" />
        </div>
      </template>
    </UModal>

    <UModal v-model:open="connectOpen" :title="t('peers.connect')">
      <template #body>
        <form v-if="!sentCode" id="peer-connect" class="space-y-4" @submit.prevent="doConnect">
          <UFormField :label="t('peers.theirUrl')" :description="t('peers.theirUrlHelp')" required>
            <UInput v-model="conn.url" class="w-full font-mono" placeholder="http://100.64.0.2:8080" />
          </UFormField>
          <UFormField :label="t('peers.selfName')" required>
            <UInput v-model="conn.name" class="w-full" />
          </UFormField>
          <UFormField required>
            <template #label>
              <span class="flex items-center gap-1.5">{{ t('peers.selfUrl') }}<InfoTip :text="t('peers.selfUrlHelp')" icon-class="size-3.5 text-(--ui-text-muted)" /></span>
            </template>
            <UInput v-model="conn.self_url" class="w-full font-mono" />
          </UFormField>
          <UFormField :label="t('peers.shared')">
            <USelectMenu v-model="conn.shared" :items="projectItems" value-key="value" multiple :placeholder="t('peers.none')" class="w-full" />
          </UFormField>
        </form>
        <div v-else class="space-y-2">
          <p class="text-sm text-(--ui-text-muted)">{{ t('peers.sentHelp') }}</p>
          <code class="block rounded bg-(--ui-bg-elevated) px-3 py-2 text-center text-2xl font-semibold tracking-widest">{{ sentCode }}</code>
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t(sentCode ? 'peers.close' : 'common.cancel')" @click="connectOpen = false" />
          <UButton v-if="!sentCode" type="submit" form="peer-connect" :loading="connecting" :label="t('peers.sendRequest')" />
        </div>
      </template>
    </UModal>

    <UModal v-model:open="joinOpen" :title="t('peers.join')">
      <template #body>
        <form id="peer-join" class="space-y-4" @submit.prevent="doJoin">
          <UFormField :label="t('peers.code')" required>
            <UInput v-model="join.code" class="w-full font-mono" placeholder="ofi_…" />
          </UFormField>
          <UFormField :label="t('peers.selfName')" required>
            <UInput v-model="join.name" class="w-full" />
          </UFormField>
          <UFormField required>
            <template #label>
              <span class="flex items-center gap-1.5">{{ t('peers.selfUrl') }}<InfoTip :text="t('peers.selfUrlHelp')" icon-class="size-3.5 text-(--ui-text-muted)" /></span>
            </template>
            <UInput v-model="join.url" class="w-full font-mono" />
          </UFormField>
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="joinOpen = false" />
          <UButton type="submit" form="peer-join" :loading="joining" :label="t('peers.pair')" />
        </div>
      </template>
    </UModal>
  </PageShell>
</template>
