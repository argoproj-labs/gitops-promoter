#!/usr/bin/env bash
# Encode a recording to docs/assets/demo.gif. See .agents/skills/record-ui-demo-gif/SKILL.md.
# Usage: make-gif.sh [recording-dir] [output.gif]
# Env: GIF_WIDTH (1100), GIF_FPS (10), GIF_TIMELAPSE_FPS (8), GIF_COLORS (128), GIF_LOSSY (40)
set -euo pipefail

REC="${1:-/tmp/promoter-ui-demo/recording}"
OUT="${2:-$(git rev-parse --show-toplevel)/docs/assets/demo.gif}"
WIDTH="${GIF_WIDTH:-1100}"
FPS="${GIF_FPS:-10}"
TIMELAPSE_FPS="${GIF_TIMELAPSE_FPS:-8}"
COLORS="${GIF_COLORS:-128}"
LOSSY="${GIF_LOSSY:-40}"

LIST="${REC}/frames.txt"
MARKS="${REC}/marks.json"
[ -f "${LIST}" ] || { echo "missing ${LIST}" >&2; exit 1; }
[ -f "${MARKS}" ] || { echo "missing ${MARKS}" >&2; exit 1; }
command -v ffmpeg >/dev/null || { echo "ffmpeg is required" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

# One trim+setpts chain per segment, then concat. Sped-up segments are sampled
# at a lower frame rate: nearly every frame changes in a time-lapse, and those
# full-frame updates are what makes a GIF large.
FILTER="$(jq -r --argjson fps "${FPS}" --argjson tlfps "${TIMELAPSE_FPS}" '
  [.segments[] | select(.end > .start and .maxDuration > 0)] as $s
  | ($s | to_entries | map(
      .key as $i | .value as $seg
      | (($seg.end - $seg.start) / $seg.maxDuration | if . < 1 then 1 else . end) as $speed
      | (if $speed > 1 then $tlfps else $fps end) as $segfps
      | "[0:v]trim=start=\($seg.start):end=\($seg.end),setpts=(PTS-STARTPTS)/\($speed),fps=\($segfps)[s\($i)];"
    ) | join(""))
    + ($s | to_entries | map("[s\(.key)]") | join(""))
    + "concat=n=\($s | length):v=1:a=0[cat]"
' "${MARKS}")"

echo ">> segments:" >&2
jq -r '.segments[] | select(.end > .start and .maxDuration > 0)
  | "   \(.name): \((.end - .start) * 10 | round / 10)s -> \((if (.end - .start) < .maxDuration then (.end - .start) else .maxDuration end) * 10 | round / 10)s"' "${MARKS}" >&2

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

ffmpeg -v error -y -f concat -safe 0 -i "${LIST}" \
  -filter_complex "${FILTER};[cat]fps=${FPS},scale=${WIDTH}:-2:flags=lanczos,split[a][b];[a]palettegen=max_colors=${COLORS}:stats_mode=diff[p];[b][p]paletteuse=dither=none:diff_mode=rectangle[out]" \
  -map '[out]' -loop 0 "${TMP}/demo.gif"

if command -v gifsicle >/dev/null; then
  gifsicle -O3 --lossy="${LOSSY}" "${TMP}/demo.gif" -o "${OUT}"
else
  echo ">> gifsicle not found; skipping lossy optimisation (brew install gifsicle)" >&2
  cp "${TMP}/demo.gif" "${OUT}"
fi

echo ">> wrote ${OUT}" >&2
ffprobe -v error -show_entries stream=width,height,nb_frames,r_frame_rate:format=duration -of default=nw=1 "${OUT}" >&2
ls -la "${OUT}" >&2
