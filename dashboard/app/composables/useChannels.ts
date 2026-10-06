// Telegram / Discord bots of a project (ADR-048): the connection only. What a
// message does is an automation whose source is the channel (ADR-049).
export interface Channel {
  id: string, kind: 'telegram' | 'discord', name: string, has_token: boolean, enabled: boolean,
  allow: string[], refusal: string, approvers: string[], approval: 'ask' | 'direct' | 'admin', header: string, reply_mode?: '' | 'steps', defaults?: CommandSetup | null, bot_name: string, last_error: string, last_message_at: string | null,
  state?: BotState
  version?: string // what an edit is made from (409 when changed since)
}

// BotState: how the bot is doing now (connecting while Discord/Telegram answers).
export type BotState = 'connecting' | 'running' | ''
type BotLike = { enabled?: boolean, bot_name?: string, last_error?: string, state?: BotState }
// botStatus: what the dot and its words say; connecting wins over an old error
export function botStatus(b: BotLike, t: (k: 'channels.connecting' | 'auto.botRunning' | 'auto.off') => string) {
  if (b.enabled === false) return { tone: 'off' as const, text: t('auto.off') }
  if (b.state === 'connecting') return { tone: 'wait' as const, text: t('channels.connecting') }
  if (b.last_error) return { tone: 'error' as const, text: b.last_error }
  if (b.state === 'running' || b.bot_name) return { tone: 'ok' as const, text: t('auto.botRunning') }
  return { tone: 'wait' as const, text: t('channels.connecting') }
}
export const botDot = { off: 'bg-(--ui-text-dimmed)', wait: 'bg-(--ui-warning) animate-pulse', error: 'bg-(--ui-error)', ok: 'bg-(--ui-success)' }

// useFollowBot refreshes while a bot connects (every 3s, 2 minutes at most):
// its state turns green or red without a reload
export function useFollowBot(connecting: () => boolean, refresh: () => unknown) {
  let timer: ReturnType<typeof setInterval> | undefined
  let until = 0
  const stop = () => { clearInterval(timer); timer = undefined }
  watch(connecting, (on) => {
    if (!on) return stop()
    if (timer) return
    until = Date.now() + 120_000
    timer = setInterval(() => { if (Date.now() > until) stop(); else refresh() }, 3000)
  }, { immediate: true })
  onBeforeUnmount(stop)
}
