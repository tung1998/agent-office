import { primaryVariables, onAccent, COLOR_THEMES } from '../app/utils/themes.ts'
import assert from 'node:assert/strict'
import { COLOR_GROUPS, readComponentColors, componentColorsCss } from '../app/utils/component-colors.ts'
import { existsSync } from 'node:fs'
import { MEDIA_BACKGROUNDS, validMediaSource, readMediaBackground, mediaFileKind, bounded } from '../app/utils/backgrounds.ts'

for (const value of ['javascript:alert(1)', 'data:image/svg+xml,test', 'file:///etc/passwd', '//example.com/x', 'https://user:pass@example.com/image.jpg', 'blob:expired', '', null]) {
  assert.equal(validMediaSource(value), false, String(value))
}
for (const value of ['https://example.com/a.mp4?token=temporary', 'http://localhost/a.jpg', '/backgrounds/j2-team.webp', 'local:01234567-89ab-cdef-0123-456789abcdef']) assert.equal(validMediaSource(value), true)
assert.equal(readMediaBackground({ kind: 'script', src: 'https://example.com/a' }), undefined)
assert.equal(readMediaBackground({ kind: 'image', src: 'javascript:alert(1)' }), undefined)
assert.deepEqual(readMediaBackground({ kind: 'video', src: '/backgrounds/fj2-dg.mp4', poster: 'data:text/html,test' }), { kind: 'video', src: '/backgrounds/fj2-dg.mp4', name: '' })
assert.equal(mediaFileKind({ type: 'video/mp4', size: 100 * 1024 * 1024 }), 'video')
assert.equal(mediaFileKind({ type: 'video/mp4', size: 100 * 1024 * 1024 + 1 }), undefined)
assert.equal(mediaFileKind({ type: 'text/html', size: 100 }), undefined)
assert.equal(mediaFileKind({ type: 'image/svg+xml', size: 100 }), undefined)
assert.equal(mediaFileKind({ type: 'image/gif', size: 100 }), 'image')
assert.equal(mediaFileKind({ type: 'image/bmp', size: 0 }), undefined)
assert.equal(bounded(Infinity, 0.45, 0, 1), 0.45)
assert.equal(bounded(NaN, 0.45, 0, 1), 0.45)
assert.equal(bounded(-5, 0.45, 0, 1), 0)
assert.equal(bounded(100, 0, 0, 30), 30)
assert.equal(MEDIA_BACKGROUNDS.length, 15)
assert.equal(new Set(MEDIA_BACKGROUNDS.map(b => b.src)).size, 15)
for (const b of MEDIA_BACKGROUNDS) {
  assert.ok(validMediaSource(b.src))
  assert.ok(existsSync(new URL(`../public${b.src}`, import.meta.url)), b.src)
  if (b.poster) assert.ok(existsSync(new URL(`../public${b.poster}`, import.meta.url)), b.poster)
}
console.log('backgrounds: URL safety, file limits, bounds and bundled assets passed')

// Component overrides reject unknown groups/CSS and disappear completely on reset.
assert.deepEqual(readComponentColors({ accent: 'red;display:none', border: '#123456', surfaces: { sidebar: { bg: '#abcdef', text: 'var(--bad)' }, unknown: { bg: '#123456' } } }), { border: '#123456', surfaces: { sidebar: { bg: '#abcdef' } } })
assert.equal(componentColorsCss({}), '')
assert.equal(componentColorsCss({ surfaces: { sidebar: {} } }), '')
assert.equal(componentColorsCss({ surfaces: { card: { bg: 'url(evil)' } } }), '')
for (const group of COLOR_GROUPS) {
  const css = componentColorsCss({ surfaces: { [group]: { bg: '#102030', text: '#f1f2f3' } } })
  assert.ok(css.includes(`.office-${group}{--office-backdrop-surface:#102030;--ui-bg:#102030`))
  assert.ok(css.includes(`.office-${group}{--ui-text:#f1f2f3`))
  assert.ok(css.includes('--office-base-text:var(--ui-text)'), 'nested groups inherit theme rather than the parent override')
  assert.equal(css.includes(`html[data-component-colors][data-wallpaper] .office-${group}{`), !['input', 'popup'].includes(group))
}
assert.ok(componentColorsCss({ link: '#123456' }).includes('a:not([data-slot]):not([role="button"])'))
console.log('component colors: validation, reset, isolated groups and wallpaper transparency passed')

assert.equal(primaryVariables('#ffffff')['--ui-text-inverted'], '#18181b')
assert.equal(primaryVariables('#112233')['--ui-text-inverted'], '#ffffff')
assert.equal(primaryVariables('#123456')['--ui-color-primary-500'], '#123456')

for (const theme of COLOR_THEMES) {
  const accent = '#000000'
  assert.ok(componentColorsCss({ accent }, theme).includes('--ui-text-inverted:' + onAccent({ ...theme, accent })))
}
