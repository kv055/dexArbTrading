#!/usr/bin/env bash
# Block creating a merge commit while on the protected prod branch.
# Promotion to main must happen via a reviewed PR on GitHub (develop -> main),
# never a local `git merge` on a dev machine.
set -euo pipefail

branch=$(git rev-parse --abbrev-ref HEAD)

case "$branch" in
  main | master)
    cat >&2 <<EOF
✖ Local merges into '$branch' are blocked.
  '$branch' is the production branch — it should only change via a reviewed PR on
  GitHub (develop -> main). Do your work on a feature branch and open a PR into
  'develop'.
  (Override: git merge --no-verify … — there is no server-side backstop on this
  Free private repo, so use with care.)
EOF
    exit 1
    ;;
esac

exit 0
