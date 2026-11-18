/**
 * Zero-dependency Web Audio API procedural sound engine for SettleCore.
 * Safe no-op during SSR, headless runs, or when AudioContext is unavailable.
 */

let sfxEnabled = true;
let audioCtx: AudioContext | null = null;

export function isSfxEnabled(): boolean {
  return sfxEnabled;
}

export function setSfxEnabled(enabled: boolean): void {
  sfxEnabled = enabled;
}

function getAudioContext(): AudioContext | null {
  if (!sfxEnabled || typeof window === "undefined") return null;

  const win = window as Window & {
    webkitAudioContext?: typeof AudioContext;
  };
  const Ctor = window.AudioContext ?? win.webkitAudioContext;
  if (!Ctor) return null;

  try {
    if (!audioCtx) {
      audioCtx = new Ctor();
    }
    if (audioCtx.state === "suspended") {
      void audioCtx.resume().catch(() => undefined);
    }
    return audioCtx.state === "running" ? audioCtx : audioCtx;
  } catch {
    return null;
  }
}

function scheduleTone(
  ctx: AudioContext,
  options: {
    type: OscillatorType;
    startFreq: number;
    endFreq?: number;
    startOffset: number;
    duration: number;
    peakGain: number;
  }
): void {
  const now = ctx.currentTime + options.startOffset;
  const osc = ctx.createOscillator();
  const gain = ctx.createGain();

  osc.type = options.type;
  osc.frequency.setValueAtTime(options.startFreq, now);
  if (options.endFreq && options.endFreq !== options.startFreq) {
    osc.frequency.exponentialRampToValueAtTime(
      Math.max(20, options.endFreq),
      now + options.duration
    );
  }

  gain.gain.setValueAtTime(0.0001, now);
  gain.gain.linearRampToValueAtTime(options.peakGain, now + Math.min(0.008, options.duration * 0.2));
  gain.gain.exponentialRampToValueAtTime(0.0001, now + options.duration);

  osc.connect(gain);
  gain.connect(ctx.destination);

  osc.start(now);
  osc.stop(now + options.duration + 0.01);
}

/**
 * 12ms pitched mechanical switch transient (1.8 kHz -> 420 Hz)
 * for tab switches, order book price clicks, and slider nodes.
 */
export function playClickSfx(): void {
  const ctx = getAudioContext();
  if (!ctx) return;
  try {
    scheduleTone(ctx, {
      type: "triangle",
      startFreq: 1800,
      endFreq: 420,
      startOffset: 0,
      duration: 0.018,
      peakGain: 0.055,
    });
  } catch {
    // No-op if browser blocks autoplay
  }
}

/**
 * Crystalline dual-tone harmonic fill chime (C5 -> G5, 523 Hz -> 784 Hz)
 * when a Limit or Market order executes in the C++17 CLOB.
 */
export function playOrderFillSfx(): void {
  const ctx = getAudioContext();
  if (!ctx) return;
  try {
    scheduleTone(ctx, {
      type: "sine",
      startFreq: 523.25,
      startOffset: 0,
      duration: 0.14,
      peakGain: 0.075,
    });
    scheduleTone(ctx, {
      type: "sine",
      startFreq: 783.99,
      startOffset: 0.055,
      duration: 0.22,
      peakGain: 0.085,
    });
    scheduleTone(ctx, {
      type: "triangle",
      startFreq: 1567.98,
      startOffset: 0.055,
      duration: 0.16,
      peakGain: 0.025,
    });
  } catch {
    // No-op
  }
}

/**
 * Tactile low-end warning pulse (140 Hz + 440 Hz -> 330 Hz)
 * when a Chaos Lab fault-injection drill fires.
 */
export function playChaosAlertSfx(): void {
  const ctx = getAudioContext();
  if (!ctx) return;
  try {
    scheduleTone(ctx, {
      type: "sawtooth",
      startFreq: 140,
      endFreq: 92,
      startOffset: 0,
      duration: 0.24,
      peakGain: 0.055,
    });
    scheduleTone(ctx, {
      type: "square",
      startFreq: 440,
      endFreq: 330,
      startOffset: 0.02,
      duration: 0.18,
      peakGain: 0.035,
    });
  } catch {
    // No-op
  }
}

/**
 * Resolving major-ninth chord (C4 - E4 - G4 - B4 - D5)
 * when Proof of Reserves Merkle inclusion verifies on-chain solvency.
 */
export function playVerifiedSfx(): void {
  const ctx = getAudioContext();
  if (!ctx) return;
  try {
    const notes = [261.63, 329.63, 392.0, 493.88, 587.33];
    notes.forEach((freq, idx) => {
      scheduleTone(ctx, {
        type: "sine",
        startFreq: freq,
        startOffset: idx * 0.04,
        duration: 0.42 - idx * 0.02,
        peakGain: 0.048,
      });
    });
  } catch {
    // No-op
  }
}
