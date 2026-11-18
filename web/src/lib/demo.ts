/**
 * Deterministic Demo Controller (`?demo=1` or `NEXT_PUBLIC_DEMO_MODE=1`)
 *
 * Provides a seeded mulberry32 PRNG, deterministic virtual clock,
 * frame-stepping hook (`window.__demo.seek(frame)`), and normalized
 * `[0..1]` bounding-box telemetry (`window.__demo.getRects()`) for
 * automated Playwright video capture and reproducible static demos.
 */

export interface NormalizedRect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface DemoBridge {
  seek: (frame: number) => { frame: number };
  getRects: () => Record<string, NormalizedRect>;
  getFrame: () => number;
  isActive: () => boolean;
}

declare global {
  interface Window {
    __demo?: DemoBridge;
  }
}

export const DEMO_INITIAL_EPOCH_MS = Date.parse("2026-11-20T01:30:00.000Z");
export const DEMO_FPS = 60;

/**
 * Deterministic 32-bit PRNG (Mulberry32).
 */
export function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function isDemoMode(): boolean {
  if (process.env.NEXT_PUBLIC_DEMO_MODE === "1") return true;
  if (typeof window === "undefined") return false;
  try {
    return new URLSearchParams(window.location.search).get("demo") === "1";
  } catch {
    return false;
  }
}

export function isFrameSteppedDemo(): boolean {
  if (typeof window === "undefined") return false;
  try {
    return new URLSearchParams(window.location.search).get("demo") === "1";
  } catch {
    return false;
  }
}

let currentFrame = 0;
const seekListeners = new Set<(frame: number) => void>();

export function getDemoFrame(): number {
  return currentFrame;
}

export function getDemoNowMs(): number {
  if (isDemoMode()) {
    return DEMO_INITIAL_EPOCH_MS + Math.round((currentFrame * 1000) / DEMO_FPS);
  }
  return Date.now();
}

export function subscribeDemoSeek(fn: (frame: number) => void): () => void {
  seekListeners.add(fn);
  return () => seekListeners.delete(fn);
}

export function seekDemoFrame(frame: number): { frame: number } {
  const safeFrame = Number.isFinite(frame) ? Math.max(0, Math.floor(frame)) : 0;
  currentFrame = safeFrame;
  seekListeners.forEach((fn) => fn(safeFrame));
  return { frame: safeFrame };
}

export function collectNormalizedRects(): Record<string, NormalizedRect> {
  if (typeof document === "undefined" || typeof window === "undefined") return {};
  const vw = Math.max(1, window.innerWidth);
  const vh = Math.max(1, window.innerHeight);
  const rects: Record<string, NormalizedRect> = {};

  const nodes = document.querySelectorAll<HTMLElement>("[data-testid]");
  nodes.forEach((el) => {
    const id = el.getAttribute("data-testid");
    if (!id) return;
    const r = el.getBoundingClientRect();
    if (r.width <= 0 || r.height <= 0) return;
    if (!rects[id]) {
      rects[id] = {
        x: Number((r.left / vw).toFixed(4)),
        y: Number((r.top / vh).toFixed(4)),
        w: Number((r.width / vw).toFixed(4)),
        h: Number((r.height / vh).toFixed(4)),
      };
    }
  });

  return rects;
}

export function installDemoBridge(): void {
  if (typeof window === "undefined") return;
  window.__demo = {
    seek: (frame: number) => seekDemoFrame(frame),
    getRects: () => collectNormalizedRects(),
    getFrame: () => currentFrame,
    isActive: () => isDemoMode(),
  };
}
