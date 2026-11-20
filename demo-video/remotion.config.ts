import { Config } from "@remotion/cli/config";

/**
 * SettleCore Remotion v4 Studio Configuration
 *
 * - Image Format: 'png' preserves lossless crisp sub-pixel typography and 1px border hairlines
 *   when zooming into 2560x1440 Playwright captures on a 1920x1080 timeline.
 * - Concurrency: Capped at 4 workers so multiple 2560x1440 textures stay within a 4 GB VRAM
 *   budget (e.g., laptop RTX 3050 Ti) without CUDA/D3D11 out-of-memory evictions.
 * - OpenGL Renderer: 'angle' uses Direct3D11/Metal hardware compositing on Windows/macOS.
 *   On native Linux or WSL2 with NVIDIA EGL drivers, override via `--gl=egl` or set
 *   `process.platform === 'linux' ? 'egl' : 'angle'`.
 */
Config.setVideoImageFormat("png");
Config.setOverwriteOutput(true);
Config.setConcurrency(4);
Config.setChromiumOpenGlRenderer(process.platform === "linux" ? "egl" : "angle");
