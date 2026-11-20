import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

/**
 * SettleCore Studio Sound Designer (48 kHz 16-bit Stereo PCM WAV)
 *
 * Synthesizes `demo-video/public/audio/settlecore-soundtrack.wav` (50.0s, 3000 frames @ 60fps)
 * aligned to a 96 BPM beat grid (37.5 frames/beat) and synced frame-for-frame
 * to the SettleCoreLaunch Remotion timeline:
 *   1. Subtle -26 dBFS ambient institutional synth pad (F#2 / C#3 / G#3) with beat-synced swells
 *   2. Soft filtered air-band whooshes on intro reveal, camera glides & workspace switches
 *   3. Crisp 12ms pitched mechanical switch clicks (1.8 kHz -> 420 Hz + micro-noise) at every click
 *   4. Crystalline dual-tone harmonic fill chimes (C5 -> G5) on LIMIT BUY & MARKET BUY fills
 *   5. Deep tactile sub-bass warning pulse (140 Hz + 440 Hz -> 330 Hz) on 3-Block Reorg trigger
 *   6. Resolving major-ninth harmonic chord (C4 - E4 - G4 - B4 - D5) on Merkle Proof verification
 */

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const OUT_WAV = path.resolve(__dirname, "../public/audio/settlecore-soundtrack.wav");

const SAMPLE_RATE = 48000;
const FPS = 60;
const DURATION_SEC = 50.0;
const TOTAL_SAMPLES = Math.round(SAMPLE_RATE * DURATION_SEC);
const TWO_PI = Math.PI * 2;
const BPM = 96;
const BEAT_SEC = 60 / BPM; // 0.625s = 37.5 frames

const left = new Float64Array(TOTAL_SAMPLES);
const right = new Float64Array(TOTAL_SAMPLES);

function frameToSample(frame) {
  return Math.round((frame / FPS) * SAMPLE_RATE);
}

// Deterministic PRNG (Mulberry32) so soundtrack output is 100% reproducible
function createRng(seed = 0x53455454) {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function addStereoSample(idx, valL, valR) {
  if (idx < 0 || idx >= TOTAL_SAMPLES) return;
  left[idx] += valL;
  right[idx] += valR;
}

function panGains(pan = 0) {
  const clamped = Math.max(-1, Math.min(1, pan));
  const angle = ((clamped + 1) * 0.25) * Math.PI;
  return [Math.cos(angle), Math.sin(angle)];
}

/**
 * 1. Subtle -26 dBFS ambient institutional synth pad (F#2 / C#3 / G#3)
 * with smooth 96 BPM beat-grid pulse & transition filter swells.
 */
function renderAmbientPad() {
  const swellFrames = [115, 375, 1350, 1800, 2175, 2700];
  const swellSamples = swellFrames.map(frameToSample);

  const f1 = 92.5; // F#2
  const f2 = 138.59; // C#3
  const f3 = 207.65; // G#3
  const f4 = 185.0; // F#3 shimmer

  let lpL = 0;
  let lpR = 0;

  const padGain = 0.048;

  for (let i = 0; i < TOTAL_SAMPLES; i++) {
    const t = i / SAMPLE_RATE;

    const fadeIn = Math.min(1, t / 0.9);
    const fadeOut = Math.min(1, Math.max(0, (DURATION_SEC - t) / 1.8));
    const masterEnv = Math.sin(fadeIn * Math.PI * 0.5) * Math.sin(fadeOut * Math.PI * 0.5);

    // Subtle 96 BPM rhythmic breathing
    const beatPhase = (t % BEAT_SEC) / BEAT_SEC;
    const beatPulse = 0.88 + 0.12 * Math.exp(-beatPhase * 4.5);

    let swell = 0;
    for (const center of swellSamples) {
      const dt = (i - center) / SAMPLE_RATE;
      if (dt > -0.5 && dt < 1.5) {
        const g = Math.exp(-((dt - 0.15) * (dt - 0.15)) / 0.35);
        swell = Math.max(swell, g);
      }
    }

    const cutoffHz = 260 + swell * 320 + Math.sin(TWO_PI * 0.08 * t) * 35;
    const rc = 1 / (TWO_PI * cutoffHz);
    const dtSample = 1 / SAMPLE_RATE;
    const alpha = dtSample / (rc + dtSample);

    const oscL =
      0.44 * Math.sin(TWO_PI * (f1 - 0.18) * t) +
      0.11 * Math.sin(TWO_PI * (f1 * 2 - 0.25) * t) +
      0.30 * Math.sin(TWO_PI * (f2 + 0.22) * t) +
      0.08 * Math.sin(TWO_PI * (f2 * 2) * t) +
      0.20 * Math.sin(TWO_PI * (f3 - 0.31) * t) +
      0.06 * Math.sin(TWO_PI * (f4 + 0.15) * t);

    const oscR =
      0.44 * Math.sin(TWO_PI * (f1 + 0.18) * t) +
      0.11 * Math.sin(TWO_PI * (f1 * 2 + 0.25) * t) +
      0.30 * Math.sin(TWO_PI * (f2 - 0.22) * t) +
      0.08 * Math.sin(TWO_PI * (f2 * 2) * t) +
      0.20 * Math.sin(TWO_PI * (f3 + 0.31) * t) +
      0.06 * Math.sin(TWO_PI * (f4 - 0.15) * t);

    lpL += alpha * (oscL - lpL);
    lpR += alpha * (oscR - lpR);

    addStereoSample(i, lpL * padGain * masterEnv * beatPulse, lpR * padGain * masterEnv * beatPulse);
  }
}

/**
 * 2. Soft filtered air-band whoosh for intro blur resolve, camera glides & workspace switches
 */
function addAirWhoosh(startFrame, durationSec = 0.42, gain = 0.085, panStart = -0.2, panEnd = 0.2, rng) {
  const startIdx = frameToSample(startFrame);
  const len = Math.round(durationSec * SAMPLE_RATE);

  let lp = 0;
  let hp = 0;
  let prevNoise = 0;

  for (let n = 0; n < len; n++) {
    const p = n / len;
    const env = Math.sin(Math.PI * p) ** 1.65;
    const noise = rng() * 2 - 1;

    const centerHz = 380 + 1270 * Math.sin(Math.PI * p);
    const lpAlpha = Math.min(0.85, (TWO_PI * centerHz) / SAMPLE_RATE);
    lp += lpAlpha * (noise - lp);

    const hpAlpha = 0.96;
    hp = hpAlpha * (hp + lp - prevNoise);
    prevNoise = lp;

    const sampleVal = hp * env * gain;
    const pan = panStart + (panEnd - panStart) * p;
    const [gL, gR] = panGains(pan);
    addStereoSample(startIdx + n, sampleVal * gL, sampleVal * gR);
  }
}

/**
 * 3. Crisp 12ms pitched mechanical switch click (1.8 kHz -> 420 Hz + micro-noise click)
 */
function addMechanicalClick(clickFrame, pan = 0, gain = 0.32, rng) {
  const startIdx = frameToSample(clickFrame);
  const len = Math.round(0.012 * SAMPLE_RATE);
  let phase = 0;

  const [gL, gR] = panGains(pan);
  for (let n = 0; n < len; n++) {
    const p = n / len;
    const env = Math.exp(-p * 6.2) * (n < 16 ? n / 16 : 1);

    const freq = 420 + (1800 - 420) * Math.exp(-p * 5.0);
    phase += (TWO_PI * freq) / SAMPLE_RATE;
    const body = Math.sin(phase) * 0.72 + Math.sin(phase * 2.15) * 0.18;

    const snapEnv = n < 168 ? Math.exp(-n / 42) : 0;
    const microNoise = (rng() * 2 - 1) * 0.45 * snapEnv;

    const out = (body * env + microNoise) * gain;
    addStereoSample(startIdx + n, out * gL, out * gR);
  }
}

/**
 * 4. Crystalline dual-tone harmonic fill chime (C5 -> G5, 523.25 Hz -> 783.99 Hz)
 */
function addOrderFillChime(startFrame, gain = 0.24) {
  const notes = [
    { delayMs: 0, freq: 523.25, pan: -0.18, dur: 0.48 },
    { delayMs: 68, freq: 783.99, pan: 0.22, dur: 0.62 },
  ];

  for (const note of notes) {
    const startIdx = frameToSample(startFrame) + Math.round((note.delayMs / 1000) * SAMPLE_RATE);
    const len = Math.round(note.dur * SAMPLE_RATE);
    const [gL, gR] = panGains(note.pan);

    for (let n = 0; n < len; n++) {
      const t = n / SAMPLE_RATE;
      const attack = Math.min(1, n / (0.004 * SAMPLE_RATE));
      const decay = Math.exp(-t * 6.2);
      const env = attack * decay;

      const sig =
        0.78 * Math.sin(TWO_PI * note.freq * t) +
        0.16 * Math.sin(TWO_PI * note.freq * 2 * t) * Math.exp(-t * 10) +
        0.06 * Math.sin(TWO_PI * note.freq * 3 * t) * Math.exp(-t * 14);

      const out = sig * env * gain;
      addStereoSample(startIdx + n, out * gL, out * gR);
    }
  }
}

/**
 * 5. Deep tactile sub-bass warning pulse (140 Hz + 440 Hz -> 330 Hz)
 */
function addChaosReorgPulse(startFrame, gain = 0.34) {
  const startIdx = frameToSample(startFrame);
  const len = Math.round(0.58 * SAMPLE_RATE);
  let subPhase = 0;
  let warnPhase = 0;

  for (let n = 0; n < len; n++) {
    const t = n / SAMPLE_RATE;
    const attack = Math.min(1, n / (0.006 * SAMPLE_RATE));

    const subFreq = 68 + (140 - 68) * Math.exp(-t * 9.5);
    subPhase += (TWO_PI * subFreq) / SAMPLE_RATE;
    const subEnv = attack * Math.exp(-t * 5.2);
    const subSig = Math.sin(subPhase) * subEnv * 0.85;

    const warnFreq = t < 0.15 ? 440 : 330;
    warnPhase += (TWO_PI * warnFreq) / SAMPLE_RATE;
    const warnEnv = attack * Math.exp(-t * 4.8);
    const warnSig = (Math.sin(warnPhase) * 0.38 + Math.sin(warnPhase * 2) * 0.08) * warnEnv;

    const out = (subSig + warnSig) * gain;
    addStereoSample(startIdx + n, out, out);
  }
}

/**
 * 6. Resolving major-ninth harmonic chord (C4 - E4 - G4 - B4 - D5)
 */
function addMerkleVerifiedChord(startFrame, gain = 0.16) {
  const chord = [
    { freq: 261.63, delayMs: 0, pan: -0.3 },
    { freq: 329.63, delayMs: 22, pan: -0.15 },
    { freq: 392.0, delayMs: 44, pan: 0.0 },
    { freq: 493.88, delayMs: 66, pan: 0.18 },
    { freq: 587.33, delayMs: 88, pan: 0.32 },
  ];

  for (const voice of chord) {
    const startIdx = frameToSample(startFrame) + Math.round((voice.delayMs / 1000) * SAMPLE_RATE);
    const len = Math.round(1.55 * SAMPLE_RATE);
    const [gL, gR] = panGains(voice.pan);

    for (let n = 0; n < len; n++) {
      const t = n / SAMPLE_RATE;
      const attack = Math.min(1, n / (0.018 * SAMPLE_RATE));
      const decay = Math.exp(-t * 2.35);
      const env = Math.sin(attack * Math.PI * 0.5) * decay;

      const sig =
        0.82 * Math.sin(TWO_PI * voice.freq * t) +
        0.14 * Math.sin(TWO_PI * voice.freq * 2 * t) * Math.exp(-t * 4.5) +
        0.04 * Math.sin(TWO_PI * voice.freq * 3 * t) * Math.exp(-t * 7.0);

      const out = sig * env * gain;
      addStereoSample(startIdx + n, out * gL, out * gR);
    }
  }
}

function writeStereoWav(filePath, bufL, bufR) {
  fs.mkdirSync(path.dirname(filePath), { recursive: true });

  const numChannels = 2;
  const bitsPerSample = 16;
  const byteRate = (SAMPLE_RATE * numChannels * bitsPerSample) / 8;
  const blockAlign = (numChannels * bitsPerSample) / 8;
  const dataSize = TOTAL_SAMPLES * blockAlign;
  const buffer = Buffer.alloc(44 + dataSize);

  buffer.write("RIFF", 0);
  buffer.writeUInt32LE(36 + dataSize, 4);
  buffer.write("WAVE", 8);
  buffer.write("fmt ", 12);
  buffer.writeUInt32LE(16, 16);
  buffer.writeUInt16LE(1, 20);
  buffer.writeUInt16LE(numChannels, 22);
  buffer.writeUInt32LE(SAMPLE_RATE, 24);
  buffer.writeUInt32LE(byteRate, 28);
  buffer.writeUInt16LE(blockAlign, 32);
  buffer.writeUInt16LE(bitsPerSample, 34);
  buffer.write("data", 36);
  buffer.writeUInt32LE(dataSize, 40);

  let offset = 44;
  for (let i = 0; i < TOTAL_SAMPLES; i++) {
    const sL = Math.tanh(bufL[i]);
    const sR = Math.tanh(bufR[i]);
    const i16L = Math.max(-32767, Math.min(32767, Math.round(sL * 32000)));
    const i16R = Math.max(-32767, Math.min(32767, Math.round(sR * 32000)));
    buffer.writeInt16LE(i16L, offset);
    buffer.writeInt16LE(i16R, offset + 2);
    offset += 4;
  }

  fs.writeFileSync(filePath, buffer);
}

function main() {
  const rng = createRng(0x53455454);

  // 1. Ambient institutional synth bed with 96 BPM pulse
  renderAmbientPad();

  // 2. Filtered air-band whooshes on intro reveal, glides & scene cuts
  addAirWhoosh(115, 0.48, 0.085, -0.2, 0.2, rng); // Intro blur resolve -> live desk
  addAirWhoosh(435, 0.42, 0.065, -0.15, 0.25, rng); // Glide onto Order Ticket
  addAirWhoosh(935, 0.40, 0.055, 0.2, -0.1, rng); // Pan across Chart & Book
  addAirWhoosh(1110, 0.42, 0.065, -0.1, 0.2, rng); // Open 2-Phase Order Lifecycle Drawer
  addAirWhoosh(1390, 0.40, 0.075, -0.25, 0.15, rng); // Switch to Chaos & Systems Lab
  addAirWhoosh(1800, 0.40, 0.075, -0.15, 0.25, rng); // Switch to Proof of Reserves
  addAirWhoosh(2175, 0.48, 0.080, -0.3, 0.3, rng); // Enter BenchmarksScene
  addAirWhoosh(2700, 0.48, 0.070, 0.25, -0.25, rng); // Enter OutroScene

  // 3. Frame-exact 12ms mechanical switch clicks matching every cursor clickAtFrame
  const clickCues = [
    { frame: 505, pan: 0.32 }, // OrderExecutionScene: Price input (local 130)
    { frame: 570, pan: 0.34 }, // Qty input (local 195)
    { frame: 675, pan: 0.36 }, // Submit LIMIT BUY (local 300)
    { frame: 780, pan: 0.34 }, // Market tab (local 405)
    { frame: 855, pan: 0.36 }, // Submit MARKET BUY (local 480)
    { frame: 1110, pan: 0.32 }, // Inspect 2-Phase Order Lifecycle (local 735)
    { frame: 1390, pan: -0.24 }, // Chaos & Systems Lab tab (local 40)
    { frame: 1550, pan: -0.28 }, // Trigger 3-Block Reorg (local 200)
    { frame: 1800, pan: -0.12 }, // Proof of Reserves tab (local 450)
    { frame: 1950, pan: 0.0 }, // Verify Merkle Proof (local 600)
  ];
  for (const cue of clickCues) {
    addMechanicalClick(cue.frame, cue.pan, 0.32, rng);
  }

  // 4. Crystalline fill chimes at exact LIMIT BUY & MARKET BUY fill frames
  addOrderFillChime(677, 0.23);
  addOrderFillChime(857, 0.24);

  // 5. Deep tactile sub-bass warning pulse at 3-Block Reorg execution frame
  addChaosReorgPulse(1552, 0.32);

  // 6. Resolving major-ninth harmonic chord on Merkle Proof verification
  addMerkleVerifiedChord(1952, 0.16);

  writeStereoWav(OUT_WAV, left, right);
  const sizeBytes = fs.statSync(OUT_WAV).size;
  console.log(
    `[SettleCore Audio] Wrote ${OUT_WAV} (${SAMPLE_RATE} Hz, 16-bit stereo PCM, ${DURATION_SEC.toFixed(1)}s, ${(sizeBytes / (1024 * 1024)).toFixed(2)} MB)`
  );
}

main();
