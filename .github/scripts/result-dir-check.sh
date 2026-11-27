#!/usr/bin/env bash
# Checks how the CLI treats an existing -result folder, end to end.
# Usage: result-dir-check.sh NEW_CLI OLD_CLI   (run from the repository root)
#   NEW_CLI  the CLI built from this commit
#   OLD_CLI  a previous release (2.0.0), used to make a result folder the
#            way older versions did
# Builds from the custom-font test fixtures, so no network is needed.
set -euo pipefail
new=$1
old=$2
fx=internal/custom/testdata
fonts=(-font "$fx/fixture-light.ttf" -font "$fx/fixture-regular.ttf" -font "$fx/fixture-medium.ttf" -font "$fx/fixture-bold.ttf")
expected="msyh.ttc msyhbd.ttc msyhl.ttc segoeui.ttf simsun.ttc simsunb.ttf"

# Relative paths, so Git Bash on Windows hands the CLI ordinary paths.
work=ci-result-check
rm -rf "$work"
mkdir -p "$work"
trap 'rm -rf "$work"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
listing() { (cd "$1" && find . -mindepth 1 | sed 's|^\./||' | LC_ALL=C sort | tr '\n' ' ' | sed 's/ $//'); }
fingerprint() { (cd "$1" && find . -type f | LC_ALL=C sort | xargs sha256sum; find . -mindepth 1 -type d | LC_ALL=C sort); }
build() { local cli=$1 dir=$2; shift 2; "$cli" custom "${fonts[@]}" -result "$dir" "$@" >"$work/log.txt" 2>&1; }
expect_six() {
  local got
  got=$(listing "$1")
  [ "$got" = "$expected" ] || fail "$1 holds [$got], want [$expected]"
}
expect_refused() {
  local dir=$1 stray=$2 before after
  before=$(fingerprint "$dir")
  if build "$new" "$dir"; then
    cat "$work/log.txt"
    fail "built into $dir although it holds $stray"
  fi
  grep -q "$stray" "$work/log.txt" || { cat "$work/log.txt"; fail "error does not name $stray"; }
  after=$(fingerprint "$dir")
  [ "$before" = "$after" ] || fail "$dir was changed although the build was refused"
}

echo "1. a folder that does not exist yet is created with exactly the six fonts"
build "$new" "$work/new/nested" || { cat "$work/log.txt"; fail "build into a new folder"; }
expect_six "$work/new/nested"

echo "2. the same folder can be built into again, and every font is rewritten"
for f in $expected; do printf stale >"$work/new/nested/$f"; done
build "$new" "$work/new/nested" || { cat "$work/log.txt"; fail "second build into the same folder"; }
expect_six "$work/new/nested"
for f in $expected; do
  ! printf stale | cmp -s - "$work/new/nested/$f" || fail "$f was not rewritten"
done

echo "3. a result folder made by the previous release is accepted"
build "$old" "$work/old" || { cat "$work/log.txt"; fail "previous release could not build"; }
expect_six "$work/old"
build "$new" "$work/old" || { cat "$work/log.txt"; fail "build into the previous release's folder"; }
expect_six "$work/old"

echo "4. a stray file stops the build and nothing is touched"
printf 'user data' >"$work/old/a.txt"
expect_refused "$work/old" a.txt
rm "$work/old/a.txt"

echo "5. a subfolder stops the build and nothing is touched"
mkdir "$work/old/photos"
printf 'user data' >"$work/old/photos/b.txt"
expect_refused "$work/old" photos

echo "all -result checks passed"
