// A tag's colour comes from its name: the same tag looks the same everywhere,
// with no colour to pick ("Bug" and "bug" are one tag).
const tagColors = ['primary', 'info', 'success', 'warning', 'error', 'neutral'] as const
export type TagColor = typeof tagColors[number]

export function tagColor(tag: string): TagColor {
  let h = 0
  for (const ch of tag.toLowerCase()) h = (h * 31 + ch.charCodeAt(0)) >>> 0
  return tagColors[h % tagColors.length]!
}

// sameTag compares the way the server does (without case)
export const sameTag = (a: string, b: string) => a.toLowerCase() === b.toLowerCase()

// hasTags: c has every tag of want
export const hasTags = (have: string[] | undefined, want: string[]) => want.every(w => (have ?? []).some(h => sameTag(h, w)))
