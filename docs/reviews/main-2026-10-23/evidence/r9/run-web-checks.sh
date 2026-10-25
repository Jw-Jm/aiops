#!/bin/sh
# Local frontend checks: types, vitest and the node contract tests.
ROOT="/Users/mssc/Documents/Code/ops/.worktrees/main-review-fixes-20261025"
LOG="$ROOT/docs/reviews/main-2026-10-23/evidence/r9"
cd "$ROOT/web" || exit 1
npx tsc --noEmit > "$LOG/web-tsc.log" 2>&1
tsc=$?
npx vitest run > "$LOG/web-vitest.log" 2>&1
vitest=$?
node --experimental-strip-types --test ./test/*.test.mjs > "$LOG/web-contract.log" 2>&1
contract=$?
printf 'tsc=%s vitest=%s contract=%s\n' "$tsc" "$vitest" "$contract" > "$LOG/web-summary.txt"
exit 0
