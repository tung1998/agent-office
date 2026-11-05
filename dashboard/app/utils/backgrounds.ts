export interface MediaBackground { kind: 'image' | 'video', src: string, name: string, poster?: string }

const imageNames = ['hand-drawn', 'neon', 'storming', 'colorful-energy', 'oil-painting', 'classic', 'lava', 'ascii', 'crystal-ice', 'broken', 'eagle-transformer', 'float', 'space', 'j2team']
export const MEDIA_BACKGROUNDS: MediaBackground[] = [
  { kind: 'video', src: '/backgrounds/fj2-dg.mp4', name: 'FJ2 - DG', poster: '/backgrounds/fj2-dg.webp' },
  ...imageNames.map(id => ({ kind: 'image' as const, src: `/backgrounds/j2-${id === 'j2team' ? 'team' : id}.webp`, name: id === 'j2team' ? 'J2teaM' : `J2 ${id.split('-').map(s => s[0]!.toUpperCase() + s.slice(1)).join(' ')}` }))
]

// Only media loaded by the browser; never passed to the server as a fetch URL.
export function validMediaSource(src: unknown): src is string {
  if (typeof src !== 'string' || src.length > 2048) return false
  if (/^local:[a-f0-9-]{36}$/.test(src) || /^\/backgrounds\/[a-z0-9-]+\.(webp|mp4)$/.test(src)) return true
  try {
    const url = new URL(src)
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password
  } catch { return false }
}

export function readMediaBackground(v: unknown): MediaBackground | undefined {
  if (!v || typeof v !== 'object') return
  const b = v as MediaBackground
  if (!['image', 'video'].includes(b.kind) || !validMediaSource(b.src)) return
  return { kind: b.kind, src: b.src, name: typeof b.name === 'string' ? b.name.slice(0, 120) : '', ...(validMediaSource(b.poster) ? { poster: b.poster } : {}) }
}

export const mediaFileKind = (file: Pick<File, 'type' | 'size'>): MediaBackground['kind'] | undefined => {
  if (file.size <= 0 || file.size > 100 * 1024 * 1024) return
  if (file.type === 'video/mp4') return 'video'
  if (['image/jpeg', 'image/png', 'image/webp', 'image/gif', 'image/bmp', 'image/avif'].includes(file.type)) return 'image'
}

export function bounded(value: unknown, fallback: number, min: number, max: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? Math.max(min, Math.min(max, value)) : fallback
}
