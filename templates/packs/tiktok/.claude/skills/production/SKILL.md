---
name: production
description: Produce a vertical TikTok video on this machine with ffmpeg and whisper - shot list, 9:16 reframing, burned-in captions in the safe zone, loudness, cover frame, export checks. Use when cutting a video (#make-video, #repurpose), transcribing a clip, or when the creator asks for an edit.
---

# Production

## 0. Tools (check first, never install)
```sh
which ffmpeg ffprobe whisper whisper-cli
```
- Missing: stop and tell the creator what to install, e.g. `brew install ffmpeg`; captions: `pip install openai-whisper` (needs Python) or whisper.cpp (`brew install whisper-cpp`, gives `whisper-cli` + a model file). Never install, download models or sign up yourself.
- Office library skills that help, if the project has them (find_skills): `hyperframes` (HTML/CSS → video for motion graphics, text animations), `ad-video-pipeline` (ad-style cuts), `content-repurpose`. A voice-over MCP (e.g. ElevenLabs) only if the creator connected it and agreed to the cost; otherwise the creator records the voice.

## 1. Folder
`videos/<YYYY-MM-DD>-<slug>/`: `script.md`, `shotlist.md`, `raw/` (the creator's clips, never modified), `out/` (intermediate renders), `captions.srt`, `cover.jpg`, `final.mp4`, `post.md`. Assets reused across videos live in `assets/` with a licence line each in `assets/README.md`.

## 2. Spec
1080x1920 (9:16), 30 fps, H.264 (yuv420p, `-movflags +faststart`), AAC 48 kHz, about -14 LUFS integrated, true peak ≤ -1 dB. Length as the script says.

**Safe zone** (keep text and faces inside): top 150 px, bottom 400 px (caption, buttons) and right 140 px (like/comment/share) are covered by the app. Captions sit around y = 1250-1450.

## 3. Recipes
Inspect a clip:
```sh
ffprobe -v error -show_entries stream=codec_type,width,height,r_frame_rate:format=duration -of compact raw/clip.mp4
```
Horizontal → 9:16, blurred background fill:
```sh
ffmpeg -i raw/clip.mp4 -filter_complex "[0:v]scale=1080:1920:force_original_aspect_ratio=increase,crop=1080:1920,boxblur=20:2[bg];[0:v]scale=1080:-2[fg];[bg][fg]overlay=(W-w)/2:(H-h)/2,fps=30,format=yuv420p" -c:a aac -ar 48000 out/vertical.mp4
```
Center crop (subject in the middle) instead: `-vf "scale=-2:1920,crop=1080:1920,fps=30,format=yuv420p"`.

Trim (in/out from the shot list), then join clips with the same settings:
```sh
ffmpeg -ss 00:00:03.2 -to 00:00:09.8 -i out/vertical.mp4 -c:v libx264 -c:a aac out/s01.mp4
printf "file 's01.mp4'\nfile 's02.mp4'\n" > out/list.txt
ffmpeg -f concat -safe 0 -i out/list.txt -c copy out/joined.mp4
```
Captions with whisper (language of `docs/channel.md`), then burn them in:
```sh
whisper out/joined.mp4 --language vi --output_format srt --output_dir .   # or: whisper-cli -m <model> -l vi -osrt -f audio.wav
ffmpeg -i out/joined.mp4 -vf "subtitles=captions.srt:force_style='FontName=Arial,FontSize=13,Bold=1,Outline=2,Alignment=2,MarginV=120'" -c:a copy out/captioned.mp4
```
Read the SRT and fix names and terms whisper got wrong before burning.

Loudness to about -14 LUFS (two-pass is better; one pass is fine for short clips):
```sh
ffmpeg -i out/captioned.mp4 -c:v copy -af loudnorm=I=-14:TP=-1:LRA=11 -ar 48000 -c:a aac -b:a 192k -movflags +faststart final.mp4
```
Cover frame (the moment the script names, with the hook text if wanted):
```sh
ffmpeg -ss 00:00:01.5 -i final.mp4 -frames:v 1 -q:v 2 cover.jpg
```

## 4. Before saying done
- `ffprobe final.mp4`: 1080x1920, 30 fps, duration as planned, an audio stream.
- Look at 3 frames (start, middle, end): text inside the safe zone, readable, no black bars or stretched faces.
- Note in `shotlist.md` every asset used and its licence; give the commands you ran.
- Music: do not bake in a track without a licence; write in `post.md` which kind of sound to pick from TikTok's library when posting.
