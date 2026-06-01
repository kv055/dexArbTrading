#!/usr/bin/env bash
# Block pushing to the protected prod branch from a dev machine.
# Git feeds pre-push one line per ref being pushed on stdin:
#   <local ref> <local sha> <remote ref> <remote sha>
# We reject the push if any remote ref is refs/heads/main (or master).
set -euo pipefail

blocked=0
while read -r _local_ref _local_sha remote_ref _remote_sha; do
  case "$remote_ref" in
    refs/heads/main | refs/heads/master)
      blocked=1
      ;;
  esac
done

if [ "$blocked" -eq 1 ]; then
  cat >&2 <<'EOF'
✖ Direct pushes to 'main' are blocked.
  'main' is the production branch. Push your branch and open a PR into 'develop';
  prod is updated by promoting develop -> main via a PR on GitHub.
  (Override: git push --no-verify … — there is no server-side backstop on this
  Free private repo, so use with care.)
EOF
  exit 1
fi

exit 0
