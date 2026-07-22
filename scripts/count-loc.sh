#!/usr/bin/env bash
set -euo pipefail

# Script counting the lines of code in the parent repository and its submodules, excluding binary files.

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

grand_total=0

count_loc() {
  local dir="$1"
  local label="$2"
  local total=0

  if [ ! -d "$dir/.git" ] && [ ! -f "$dir/.git" ]; then
    echo "  $label: (not initialized)"
    return
  fi

  # Count non-blank, non-binary lines tracked by git
  while IFS= read -r file; do
    [ -f "$dir/$file" ] || continue
    # Skip binary files
    if file "$dir/$file" | grep -qE 'binary|image|executable|archive|compressed|PDF'; then
      continue
    fi
    count=$(grep -c '' "$dir/$file" 2>/dev/null || true)
    total=$((total + count))
  done < <(git -C "$dir" ls-files 2>/dev/null)

  printf "  %-40s %6d lines\n" "$label" "$total"
  grand_total=$((grand_total + total))
}

echo ""
echo "Lines of code per repo"
echo "────────────────────────────────────────────────────"

# Parent repo (top-level tracked files only, excluding submodule paths)
SUBMODULE_PATHS=$(git -C "$ROOT" submodule--helper list 2>/dev/null | awk '{print $4}' || git -C "$ROOT" config --file .gitmodules --get-regexp path | awk '{print $2}')

parent_total=0
while IFS= read -r file; do
  # Skip files that belong to a submodule
  skip=false
  for sm in $SUBMODULE_PATHS; do
    if [[ "$file" == "$sm"/* || "$file" == "$sm" ]]; then
      skip=true
      break
    fi
  done
  $skip && continue

  [ -f "$ROOT/$file" ] || continue
  if file "$ROOT/$file" | grep -qE 'binary|image|executable|archive|compressed|PDF'; then
    continue
  fi
  count=$(grep -c '' "$ROOT/$file" 2>/dev/null || true)
  parent_total=$((parent_total + count))
done < <(git -C "$ROOT" ls-files 2>/dev/null)

printf "  %-40s %6d lines\n" "dexArbTrading (root)" "$parent_total"
grand_total=$((grand_total + parent_total))

# Submodules
while IFS= read -r sm_path; do
  [ -n "$sm_path" ] || continue
  count_loc "$ROOT/$sm_path" "$sm_path"
done <<< "$SUBMODULE_PATHS"

echo "────────────────────────────────────────────────────"
printf "  %-40s %6d lines\n" "TOTAL" "$grand_total"
echo ""
