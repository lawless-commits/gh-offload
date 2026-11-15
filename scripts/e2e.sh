#!/usr/bin/env bash
# End-to-end test: offload a command from this checkout to a real GitHub-hosted
# runner and check everything that comes back.
#
#   GH_TOKEN=... scripts/e2e.sh [linux|linux-arm|macos|windows]
#
# Needs a built ./gh-offload (or GH_OFFLOAD_BIN), a token that may push
# branches and run Actions here, and offload.yml on the default branch.
set -euo pipefail

target="${1:-linux}"
bin="${GH_OFFLOAD_BIN:-./gh-offload}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp" out e2e-untracked.txt .env' EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

echo "== refuses to upload a file that looks like a secret"
printf 'TOKEN=not-really\n' > .env
set +e
"$bin" --yes -- true 2> "$tmp/secret.err"
code=$?
set -e
rm -f .env
[ "$code" -eq 125 ] || fail "secret check exit code $code, want 125"
grep -q "look like secrets" "$tmp/secret.err" || fail "secret check message: $(cat "$tmp/secret.err")"

echo "== offloads to $target and brings back output, exit code and files"
marker="e2e-$target-$RANDOM$RANDOM"
printf '%s\n' "$marker" > e2e-untracked.txt # untracked, so it proves the snapshot carries local files
rm -rf out
set +e
# shellcheck disable=SC2016 # $RUNNER_OS is for the runner to expand
"$bin" --os "$target" --yes --json --timeout 15 \
  --get 'out/*.txt' --get 'not-there.bin' \
  -- 'cat e2e-untracked.txt; mkdir -p out; printf data > out/result.txt; echo "hello from $RUNNER_OS"; exit 3' \
  > "$tmp/result.json"
code=$?
set -e
cat "$tmp/result.json"

[ "$code" -eq 3 ] || fail "exit code $code, want 3"
jq -e '.exit_code == 3' "$tmp/result.json" > /dev/null || fail "JSON exit_code"
jq -e '.conclusion == "failure"' "$tmp/result.json" > /dev/null || fail "JSON conclusion"
jq -r .output "$tmp/result.json" | grep -q "$marker" || fail "untracked file did not reach the runner"
jq -r .output "$tmp/result.json" | grep -q "hello from" || fail "command output missing"
jq -e '.files == ["out/result.txt"]' "$tmp/result.json" > /dev/null || fail "JSON files"
jq -e '[.notes[] | select(contains("no match: not-there.bin"))] | length == 1' "$tmp/result.json" > /dev/null || fail "missing --get note"
[ "$(cat out/result.txt)" = data ] || fail "out/result.txt not brought back"

id="$(jq -r .id "$tmp/result.json")"
[ -z "$(git ls-remote origin "refs/heads/offload/$id")" ] || fail "snapshot branch offload/$id was not deleted"
[ -z "$(git status --porcelain --untracked-files=no)" ] || fail "tracked files changed: $(git status --porcelain)"

echo "PASS ($target)"
