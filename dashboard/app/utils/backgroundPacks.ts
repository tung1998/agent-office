// Background images that ship with the dashboard (public/backgrounds), in
// packs: the theme settings show a pack as one tile, opened to pick from it.
export interface PackImage { id: string, name: string, src: string }
export interface BackgroundPack { id: string, name: string, images: PackImage[] }

const J2 = ['team', 'hand-drawn', 'neon', 'storming', 'colorful-energy', 'oil-painting', 'classic', 'lava', 'ascii', 'crystal-ice', 'broken', 'eagle-transformer', 'float', 'space']
const title = (id: string) => id === 'team' ? 'J2teaM' : id.split('-').map(s => s[0]!.toUpperCase() + s.slice(1)).join(' ')

export const BACKGROUND_PACKS: BackgroundPack[] = [
  { id: 'j2team', name: 'J2TEAM', images: J2.map(id => ({ id: `j2-${id}`, name: title(id), src: `/backgrounds/j2-${id}.webp` })) }
]

// a background src the dashboard ships (never a URL from elsewhere)
export const isPackSrc = (src: unknown): src is string => typeof src === 'string' && /^\/backgrounds\/[a-z0-9-]+\.webp$/.test(src)
