#!/usr/bin/env bash
# Runs doiq for action.yml and exposes its results as step outputs.
set -uo pipefail

out_dir="${RUNNER_TEMP:-/tmp}/doiq"
mkdir -p "$out_dir"
json="$out_dir/report.json"
md="$out_dir/comment.md"

args=(-json-out "$json" -markdown-out "$md" -quiet)
[ -n "${INPUT_DOCKERFILE:-}" ] && args+=(-dockerfile "$INPUT_DOCKERFILE")
[ "${INPUT_MEASURE:-false}" = "true" ] && args+=(-measure)
[ "${INPUT_SCAN_VULNS:-false}" = "true" ] && args+=(-scan-vulns)
if [ -n "${INPUT_FAIL_OVER:-}" ] && [ "${INPUT_FAIL_OVER}" != "0" ]; then
  args+=(-fail-over "$INPUT_FAIL_OVER")
fi
while IFS= read -r line; do
  line="${line#"${line%%[![:space:]]*}"}"
  [ -n "$line" ] && args+=(-build-arg "$line")
done <<< "${INPUT_BUILD_ARGS:-}"

set +e
"$DOIQ_BIN" "${args[@]}" "${INPUT_PATH:-.}"
code=$?
set -e

{
  echo "exit-code=$code"
  echo "report=$json"
} >> "$GITHUB_OUTPUT"

if [ -s "$md" ]; then
  echo "markdown=$md" >> "$GITHUB_OUTPUT"
  cat "$md" >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
fi

if [ -s "$json" ]; then
  # Measured numbers win over estimates when both exist.
  read -r saving before after < <(jq -r '
    if (.measured != null and .measured.beforeMB > 0) then
      [((.measured.beforeMB - .measured.afterMB) / .measured.beforeMB * 100 | floor),
       (.measured.beforeMB | floor), (.measured.afterMB | ceil)]
    else
      [(.estimate.savedPercent | floor), .estimate.before.totalMB, .estimate.after.totalMB]
    end | map(tostring) | join(" ")' "$json")
  {
    echo "saving-percent=$saving"
    echo "before-mb=$before"
    echo "after-mb=$after"
  } >> "$GITHUB_OUTPUT"
fi
exit 0
