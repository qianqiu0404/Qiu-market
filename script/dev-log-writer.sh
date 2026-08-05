#!/usr/bin/env bash
set -euo pipefail

target="${1:?log target is required}"
max_bytes="${S78_LOG_MAX_BYTES:-20971520}"

case "$max_bytes" in
  ''|*[!0-9]*)
    echo "S78_LOG_MAX_BYTES must be a positive integer" >&2
    exit 2
    ;;
esac
if [ "$max_bytes" -le 0 ]; then
  echo "S78_LOG_MAX_BYTES must be greater than zero" >&2
  exit 2
fi

rotate_log() {
  local file="$1"
  [ ! -f "$file.5" ] || rm -f "$file.5"
  [ ! -f "$file.4" ] || mv "$file.4" "$file.5"
  [ ! -f "$file.3" ] || mv "$file.3" "$file.4"
  [ ! -f "$file.2" ] || mv "$file.2" "$file.3"
  [ ! -f "$file.1" ] || mv "$file.1" "$file.2"
  [ ! -f "$file" ] || mv "$file" "$file.1"
}

current_size=0
if [ -f "$target" ]; then
  current_size="$(wc -c < "$target" | tr -dc '0-9')"
  current_size="${current_size:-0}"
fi
if [ "$current_size" -ge "$max_bytes" ]; then
  rotate_log "$target"
  current_size=0
fi

while IFS= read -r line || [ -n "$line" ]; do
  line_size=$((${#line} + 1))
  if [ "$current_size" -gt 0 ] &&
    [ $((current_size + line_size)) -gt "$max_bytes" ]; then
    rotate_log "$target"
    current_size=0
  fi
  printf '%s\n' "$line" >> "$target"
  printf '%s\n' "$line"
  current_size=$((current_size + line_size))
done
