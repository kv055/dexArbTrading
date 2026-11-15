#!/usr/bin/env bash
# renovate-due.sh — 24h "last checked" throttle for the renovate CI jobs
# (GitHub ci-cd.yml and Gitea ci.yml; each pipeline keeps its own stamp).
#
#   renovate-due.sh check   reads $STAMP_FILE (epoch seconds of the last
#                           successful run), emits due=true|false to stdout and
#                           $GITHUB_OUTPUT, and a line to $GITHUB_STEP_SUMMARY
#   renovate-due.sh stamp   writes the current time to $STAMP_FILE
#
# Env: STAMP_FILE (default .renovate-last-run),
#      RENOVATE_MIN_INTERVAL_SECONDS (default 86400),
#      NOW (override, testing only).
set -Eeuo pipefail

stamp_file="${STAMP_FILE:-.renovate-last-run}"
interval="${RENOVATE_MIN_INTERVAL_SECONDS:-86400}"
now="${NOW:-$(date +%s)}"

fmt() { date -u -d "@$1" '+%Y-%m-%d %H:%M UTC'; }

case "${1:-}" in
  check)
    last=0
    if [ -s "$stamp_file" ]; then last=$(tr -dc '0-9' < "$stamp_file"); fi
    last=${last:-0}
    age=$(( now - last ))
    if [ "$last" -gt 0 ] && [ "$age" -lt "$interval" ]; then
      due=false
      msg="Renovate last checked $(fmt "$last") ($(( age / 3600 ))h ago); next run due after $(fmt $(( last + interval ))). Skipping."
    elif [ "$last" -gt 0 ]; then
      due=true
      msg="Renovate last checked $(fmt "$last") ($(( age / 3600 ))h ago); running."
    else
      due=true
      msg="No Renovate last-checked stamp found; running."
    fi
    echo "due=$due"
    echo "$msg"
    if [ -n "${GITHUB_OUTPUT:-}" ]; then echo "due=$due" >> "$GITHUB_OUTPUT"; fi
    if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then printf '### Renovate\n%s\n' "$msg" >> "$GITHUB_STEP_SUMMARY"; fi
    ;;
  stamp)
    echo "$now" > "$stamp_file"
    echo "Recorded Renovate last-checked stamp $(fmt "$now")"
    ;;
  *)
    echo "usage: $0 check|stamp" >&2
    exit 2
    ;;
esac
