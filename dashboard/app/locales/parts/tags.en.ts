import type vi from './tags.vi'

const en: Record<keyof typeof vi, string> = {
  'chatTag.add': 'Add tag',
  'chatTag.edit': 'Tags',
  'chatTag.placeholder': 'Pick or type a new tag',
  'chatTag.remove': 'Remove tag {name}',
  'chatTag.filter': 'Filter by tag',
  'chatTag.filterHint': 'Chats with every tag picked',
  'chatTag.title': 'Chat tags',
  'handoff.menu': 'Hand over to a teammate',
  'handoff.title': 'Hand over chat',
  'handoff.to': 'Receiver',
  'handoff.note': 'Note: what is in progress, what comes next',
  'handoff.submit': 'Hand over',
  'handoff.nobody': 'Nobody else sees this project yet.',
  'handoff.done': 'Handed over to {who}'
}

export default en
