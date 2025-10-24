#!/usr/bin/env bash
set -euo pipefail

# i18n guard (warning-only): scans for deprecated/forbidden patterns.
# Always exits 0. Prints warnings to stdout.

ROOT_DIR="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"

if ! command -v rg >/dev/null 2>&1; then
  echo "[i18n] WARN: ripgrep (rg) not found; skipping scan."
  exit 0
fi

echo "[i18n] Scanning repo for deprecated patterns (warning-only)…"

patterns=(
  "!chess"
  "BOT_PREFIX=!chess"
  "Unknown command. Try 'help'."
)

exclude_globs=(
  "--glob=!**/.git/**"
  "--glob=!**/node_modules/**"
  "--glob=!**/vendor/**"
  "--glob=!**/bin/**"
  "--glob=!**/.cache/**"
  "--glob=!**/scripts/check-i18n.sh"
)

warn_count=0
for pat in "${patterns[@]}"; do
  if rg -n -S "${pat}" "${exclude_globs[@]}" "${ROOT_DIR}" >/tmp/i18n_hits.$$ 2>/dev/null; then
    echo "[i18n] WARN: Found pattern: ${pat}"
    cat /tmp/i18n_hits.$$
    warn_count=$((warn_count+1))
  fi
done

rm -f /tmp/i18n_hits.$$ || true

if [[ ${warn_count} -eq 0 ]]; then
  echo "[i18n] OK: No deprecated patterns detected."
else
  echo "[i18n] Completed with ${warn_count} warning group(s). (non-blocking)"
fi

exit 0
