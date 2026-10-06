// Where a chat or a task came from: the dashboard, a bot, an automation.
export type Source = 'web' | 'discord' | 'telegram' | 'auto'
export const sources: Source[] = ['web', 'discord', 'telegram', 'auto']
// a chat list also filters Burn chats (purpose, not a source)
export type ChatFilter = 'all' | Source | 'burn'
export const sourceIcon: Record<Source, string> = {
  web: 'i-lucide-monitor', discord: 'i-lucide-gamepad-2', telegram: 'i-lucide-send', auto: 'i-lucide-alarm-clock'
}
