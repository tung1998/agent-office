---
name: assets
description: Write a campaign's assets per channel - social posts, ad copy, emails, landing pages, image and video briefs - in the brand's voice, with hook variants, one call to action and a publish checklist the owner follows. Use in #make-assets and whenever an asset is written or fixed.
---

# Assets

## Every asset
- One idea, one call to action, the hook first (the first line, the first 3 seconds, the subject line).
- In the brand's voice (`docs/brand.md`): its words, the words to avoid, how formal.
- Claims only from `docs/brand.md` or the owner; prices and conditions exact; unsure: `[cần xác nhận]`.
- 2-3 variants of the hook or headline when the asset will run long or with budget, so it can be tested.
- File: `campaigns/<YYYY-MM>-<slug>/<channel>-<n>.md`; an `assets` record per asset.

## Per channel (adapt, do not paste the same text)
| Channel | Shape |
|---|---|
| Facebook / Instagram post | hook line, 3-6 short lines, CTA, 3-5 hashtags (IG); image or carousel brief |
| Ads (Meta, Google, TikTok) | the platform's text limits; primary text, headline, description; the landing page matches the ad's promise |
| Zalo OA / message | short, personal, one link; only to people who opted in |
| Email | subject (≤ 50 chars) + preview text, one main link, plain layout that reads on a phone; unsubscribe kept |
| Landing page | headline = the ad's promise, proof, objections, one form or button; above the fold readable on a phone |
| Video | a script in seconds (hook 0-3s), on-screen text, the shots; or hand it to a video team |

## Images and video
A brief (subject, size, text on it, style) or a file you made. Stock or AI images: licence noted in the file. No person's likeness, celebrity or another brand's logo without the right to use it.

## Publish checklist (end of each asset file)
- [ ] Link with UTM tags: `?utm_source=<channel>&utm_medium=<post|ad|email>&utm_campaign=<slug>`
- [ ] Image/video file and its licence
- [ ] When to post, which audience or list
- [ ] After posting: send back the link and the date (for `assets.url`, `published_on`)
