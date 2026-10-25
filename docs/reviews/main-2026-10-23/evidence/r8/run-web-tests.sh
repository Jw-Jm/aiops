#!/bin/sh
# Local frontend test wrapper: keeps the vitest output in the evidence log.
ROOT="/Users/mssc/Documents/Code/ops/.worktrees/main-review-fixes-20261025"
cd "$ROOT/web" || exit 1
npx vitest run > "$ROOT/docs/reviews/main-2026-10-23/evidence/r8/web-vitest.log" 2>&1
code=$?
printf '\nexit=%s\n' "$code" >> "$ROOT/docs/reviews/main-2026-10-23/evidence/r8/web-vitest.log"
exit "$code"
