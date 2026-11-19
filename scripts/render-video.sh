#!/usr/bin/env bash
# ==============================================================================
# SettleCore Launch Video Hardware-Accelerated Mastering Pipeline
# ==============================================================================
# Orchestrates:
#   1. Hardware encoder auto-detection: NVIDIA NVENC (h264_nvenc) -> Intel QSV (h264_qsv) -> CPU (libx264)
#   2. Frame-accurate 48 kHz stereo WAV synthesis (`demo-video/scripts/generate-sfx.mjs`)
#   3. Remotion v4 1920x1080 @ 60fps GPU-composited render (--concurrency=4, 4 GB VRAM safe)
#   4. Final H.264 MP4 + 48 kHz stereo AAC mastering & <5 MB animated WebP export
# ==============================================================================

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demo-video"
CAPTURES_DIR="${DEMO_DIR}/public/captures"
SOUNDTRACK_WAV="${DEMO_DIR}/public/audio/settlecore-soundtrack.wav"
OUT_DIR="${DEMO_DIR}/out"
README_WEBP="${ROOT_DIR}/docs/assets/media/settlecore-demo.webp"

MODE="all"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)
      MODE="$2"
      shift 2
      ;;
    *)
      echo "Unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

detect_h264_encoder() {
  if ! command -v ffmpeg >/dev/null 2>&1; then
    echo "Error: ffmpeg is required but not installed." >&2
    exit 1
  fi

  local encoders
  encoders="$(ffmpeg -hide_banner -encoders 2>/dev/null || true)"

  if echo "${encoders}" | grep -q "h264_nvenc" && \
     ffmpeg -hide_banner -loglevel error -f lavfi -i color=c=black:s=256x256:d=0.1 -c:v h264_nvenc -f null - >/dev/null 2>&1; then
    echo "h264_nvenc"
  elif echo "${encoders}" | grep -q "h264_qsv" && \
       ffmpeg -hide_banner -loglevel error -f lavfi -i color=c=black:s=256x256:d=0.1 -c:v h264_qsv -f null - >/dev/null 2>&1; then
    echo "h264_qsv"
  else
    echo "libx264"
  fi
}

ENCODER="$(detect_h264_encoder)"
echo "[SettleCore Video] Selected H.264 encoder: ${ENCODER}"

has_audio_stream() {
  local input_file="$1"
  local audio_streams
  audio_streams="$(ffprobe -v error -select_streams a -show_entries stream=codec_type -of csv=p=0 "${input_file}" 2>/dev/null || true)"
  [[ -n "${audio_streams}" ]]
}

encode_h264() {
  local input_file="$1"
  local output_file="$2"
  local audio_input="${3:-}"

  local -a extra_inputs=()
  local -a map_args=()
  local -a audio_args=()

  if [[ -n "${audio_input}" && -f "${audio_input}" ]]; then
    extra_inputs=(-i "${audio_input}")
    map_args=(-map 0:v:0 -map 1:a:0 -shortest)
    audio_args=(-af "loudnorm=I=-16:TP=-1.5:LRA=11" -c:a aac -b:a 192k -ar 48000 -ac 2)
  elif has_audio_stream "${input_file}"; then
    audio_args=(-af "loudnorm=I=-16:TP=-1.5:LRA=11" -c:a aac -b:a 192k -ar 48000 -ac 2)
  else
    audio_args=(-an)
  fi

  case "${ENCODER}" in
    h264_nvenc)
      ffmpeg -y -hide_banner -loglevel warning \
        -i "${input_file}" "${extra_inputs[@]}" \
        "${map_args[@]}" \
        -c:v h264_nvenc -preset p6 -tune hq -rc vbr -cq 17 -b:v 0 \
        -pix_fmt yuv420p -r 60 "${audio_args[@]}" -movflags +faststart \
        "${output_file}"
      ;;
    h264_qsv)
      ffmpeg -y -hide_banner -loglevel warning \
        -i "${input_file}" "${extra_inputs[@]}" \
        "${map_args[@]}" \
        -c:v h264_qsv -preset veryslow -global_quality 18 \
        -pix_fmt yuv420p -r 60 "${audio_args[@]}" -movflags +faststart \
        "${output_file}"
      ;;
    *)
      ffmpeg -y -hide_banner -loglevel warning \
        -i "${input_file}" "${extra_inputs[@]}" \
        "${map_args[@]}" \
        -c:v libx264 -preset slow -crf 16 \
        -pix_fmt yuv420p -r 60 "${audio_args[@]}" -movflags +faststart \
        "${output_file}"
      ;;
  esac
}

normalize_playwright_captures() {
  shopt -s nullglob
  local webm_files=("${CAPTURES_DIR}"/*.webm)
  if [[ ${#webm_files[@]} -eq 0 ]]; then
    echo "[SettleCore Video] No raw .webm captures found in ${CAPTURES_DIR}; using 4K Retina PNG captures."
    return 0
  fi

  for webm in "${webm_files[@]}"; do
    local base_mp4="${webm%.webm}.mp4"
    echo "[SettleCore Video] Normalizing capture $(basename "${webm}") -> $(basename "${base_mp4}") (60fps CFR)"
    encode_h264 "${webm}" "${base_mp4}"
  done
}

render_remotion_timeline() {
  mkdir -p "${OUT_DIR}"
  local raw_mp4="${OUT_DIR}/settlecore-launch-raw.mp4"
  local final_mp4="${OUT_DIR}/settlecore-launch.mp4"

  echo "[SettleCore Video] Synthesizing frame-accurate 48 kHz stereo WAV soundtrack..."
  node "${DEMO_DIR}/scripts/generate-sfx.mjs"

  echo "[SettleCore Video] Rendering Remotion 1920x1080 @ 60fps composition (--concurrency=4)..."
  (
    cd "${DEMO_DIR}"
    npx remotion render src/index.ts SettleCoreLaunch "${raw_mp4}" \
      --hardware-acceleration=if-possible \
      --concurrency=4
  )

  echo "[SettleCore Video] Mastering final launch MP4 + 48 kHz stereo AAC via ${ENCODER}..."
  if has_audio_stream "${raw_mp4}"; then
    encode_h264 "${raw_mp4}" "${final_mp4}"
  else
    encode_h264 "${raw_mp4}" "${final_mp4}" "${SOUNDTRACK_WAV}"
  fi
  echo "[SettleCore Video] Mastered MP4 ready: ${final_mp4}"
}

export_readme_webp() {
  local final_mp4="${OUT_DIR}/settlecore-launch.mp4"
  if [[ ! -f "${final_mp4}" ]]; then
    echo "Error: ${final_mp4} not found. Run with --mode mp4 or --mode all first." >&2
    exit 1
  fi

  mkdir -p "$(dirname "${README_WEBP}")"
  echo "[SettleCore Video] Exporting <5 MB animated WebP highlight loop to ${README_WEBP}..."
  ffmpeg -y -hide_banner -loglevel warning \
    -ss 00:00:06 -t 14 \
    -i "${final_mp4}" \
    -vf "fps=10,scale=840:-1:flags=lanczos" \
    -vcodec libwebp -lossless 0 -compression_level 6 -preset picture -q:v 40 -loop 0 \
    -an -fps_mode vfr \
    "${README_WEBP}"
}

normalize_playwright_captures

case "${MODE}" in
  mp4)
    render_remotion_timeline
    ;;
  webp)
    export_readme_webp
    ;;
  all)
    render_remotion_timeline
    export_readme_webp
    ;;
  *)
    echo "Unsupported --mode: ${MODE} (expected mp4, webp, or all)" >&2
    exit 1
    ;;
esac
