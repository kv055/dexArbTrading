#!/usr/bin/env bash
# Block creating direct commits on the protected production branch.
set -euo pipefail

branch=$(git rev-parse --abbrev-ref HEAD)

case "$branch" in
  main | master)
    cat >&2 <<EOF
✖ Direct commits to '$branch' are blocked.
  '$branch' is the production branch — make changes on a feature branch and
  open a reviewed PR into 'develop'.
  (Override: git commit --no-verify … — use with care.)
EOF
    exit 1
    ;;
esac

exit 0
