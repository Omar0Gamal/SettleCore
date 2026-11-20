# SettleCore Launch Video Studio (`demo-video/`)

Programmatic Apple / Linear-style launch video studio built with **Playwright** (`2560x1440` live high-DPI UI capture) and **Remotion v4** (`1920x1080` @ 60fps composition) paired with automatic **FFmpeg hardware-accelerated encoding** (`h264_nvenc` -> `h264_qsv` -> `libx264`).

## Pipeline Architecture

1. **Supersampled Capture (`2560x1440` QHD -> `1920x1080` FHD)**
   - `capture/record-demo-flows.ts` drives the live Next.js 16.4 app connected to local Anvil, the C++17 CLOB gateway, and the external Market-Maker bot at `2560x1440`.
   - Compositing `2560x1440` footage onto a `1920x1080` timeline allows up to `1.333x` true 1:1 pixel zoom-ins (and sharp `1.75x` crops) on the Order Book, Telemetry Strip, and Chaos Control Panel without upscaling blur.
2. **VRAM-Safe GPU Chromium Compositing**
   - `remotion.config.ts` caps render concurrency at `--concurrency=4`, uses `png` frame buffers for sharp typography, and selects `--gl=angle` on Windows/macOS (`--gl=egl` on Linux) so 1440p textures fit comfortably inside a 4 GB VRAM GPU (such as an RTX 3050 Ti).
3. **Hardware-Accelerated FFmpeg Mastering (`scripts/render-video.sh`)**
   - Auto-detects hardware encoders in priority order:
     1. **NVIDIA NVENC (`h264_nvenc`)** — `-preset p6 -tune hq -rc vbr -cq 17`
     2. **Intel Quick Sync (`h264_qsv`)** — `-preset veryslow -global_quality 18`
     3. **CPU Fallback (`libx264`)** — `-preset slow -crf 16`
   - Outputs the mastered 60fps H.264 MP4 (`demo-video/out/settlecore-launch.mp4`) and `docs/assets/media/settlecore-launch.mp4`.

## Commands

```bash
# 1. Record live 3840x2160 @ 60fps Next.js trading & chaos flows (requires `make up`)
make demo-capture

# 2. Preview scenes interactively in Remotion Studio
cd demo-video && npm run studio

# 3. Render 1920x1080 @ 60fps MP4 with hardware acceleration
make demo-render
```
