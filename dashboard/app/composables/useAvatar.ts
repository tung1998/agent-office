// Agent avatars: a color and an icon, or a small image. With none set, one is
// picked from the agent's id (random-looking, but the same every time).
export interface AvatarSpec { color?: string, icon?: string, image?: string }

// same names the API accepts (internal/api/avatar.go); literal classes so
// Tailwind keeps them
export const avatarColors: Record<string, string> = {
  sky: 'bg-sky-600', blue: 'bg-blue-600', indigo: 'bg-indigo-600', violet: 'bg-violet-600', purple: 'bg-purple-600', fuchsia: 'bg-fuchsia-600',
  pink: 'bg-pink-600', rose: 'bg-rose-600', red: 'bg-red-600', orange: 'bg-orange-600', amber: 'bg-amber-600', yellow: 'bg-yellow-600',
  lime: 'bg-lime-600', green: 'bg-green-600', emerald: 'bg-emerald-600', teal: 'bg-teal-600', cyan: 'bg-cyan-600', slate: 'bg-slate-600'
}
export const avatarIcons = [
  'i-lucide-bot', 'i-lucide-sparkles', 'i-lucide-rocket', 'i-lucide-brain', 'i-lucide-code', 'i-lucide-wrench', 'i-lucide-shield',
  'i-lucide-bug', 'i-lucide-search', 'i-lucide-flask-conical', 'i-lucide-terminal', 'i-lucide-database', 'i-lucide-cloud', 'i-lucide-cpu',
  'i-lucide-zap', 'i-lucide-star', 'i-lucide-crown', 'i-lucide-briefcase', 'i-lucide-palette', 'i-lucide-pen-tool', 'i-lucide-book-open',
  'i-lucide-compass', 'i-lucide-leaf', 'i-lucide-flame', 'i-lucide-gem', 'i-lucide-ghost', 'i-lucide-cat', 'i-lucide-dog', 'i-lucide-bird',
  'i-lucide-fish', 'i-lucide-rabbit', 'i-lucide-turtle', 'i-lucide-snail', 'i-lucide-squirrel', 'i-lucide-rat'
]

function hash(s: string) {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619)
  return h >>> 0
}

// avatarOf fills what the agent did not choose from its id (or name).
export function avatarOf(a: { id?: string, name?: string, avatar?: AvatarSpec } | undefined): Required<Pick<AvatarSpec, 'color' | 'icon'>> & { image?: string } {
  const seed = hash(a?.id || a?.name || '?')
  const colors = Object.keys(avatarColors)
  const av = a?.avatar ?? {}
  return {
    color: av.color && avatarColors[av.color] ? av.color : colors[seed % colors.length]!,
    icon: av.icon || avatarIcons[(seed >>> 8) % avatarIcons.length]!,
    image: av.image || undefined
  }
}

// resizeImage makes an uploaded picture a small square (the API keeps up to 200KB).
export function resizeImage(file: File, size = 128): Promise<string> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    const url = URL.createObjectURL(file)
    img.onload = () => {
      const c = document.createElement('canvas')
      c.width = c.height = size
      const s = Math.min(img.width, img.height)
      c.getContext('2d')!.drawImage(img, (img.width - s) / 2, (img.height - s) / 2, s, s, 0, 0, size, size)
      URL.revokeObjectURL(url)
      resolve(c.toDataURL('image/webp', 0.85))
    }
    img.onerror = () => { URL.revokeObjectURL(url); reject(new Error('image')) }
    img.src = url
  })
}
