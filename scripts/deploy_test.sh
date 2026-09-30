#!/bin/sh
# Exercises the remote half of deploy.sh without a server: a stand-in for ssh
# runs the script's command on this machine, in a directory that plays the
# production host, with a docker that only says what it was asked.
#
# What is under test is the refusal to drop a plugin: the running server's
# last deploy left its plugin list beside its binary, and a build without one
# of them would switch that feature off under the people using it.

set -eu

script=$(cd "$(dirname "$0")" && pwd)/deploy.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
work=$tmp/work
server=$tmp/server
mkdir -p "$work/dist" "$server/dist" "$tmp/bin"

echo binary >"$work/dist/obsidian-arc"
echo recipe >"$work/dist/Dockerfile"
echo v1 >"$work/dist/VERSION"
case $(uname -m) in
  x86_64) echo amd64 >"$work/dist/ARCH" ;;
  *) echo arm64 >"$work/dist/ARCH" ;;
esac
printf '#!/bin/sh\nshift\nexec sh -c "$1"\n' >"$tmp/bin/ssh"
printf '#!/bin/sh\necho "docker $*"\n' >"$tmp/bin/docker"
chmod +x "$tmp/bin/ssh" "$tmp/bin/docker"

# deploy <what the server has> <what the build carries> [DROP_PLUGINS] [the
# file the server keeps its list in]
deploy() {
  rm -f "$server/dist/PLUGINS" "$server/dist/PLUGIN_LIST"
  if [ -n "$1" ]; then echo "$1" >"$server/dist/${4:-PLUGIN_LIST}"; fi
  echo "$2" >"$work/dist/PLUGIN_LIST"
  (cd "$work" && PATH="$tmp/bin:$PATH" DEPLOY_SSH="$tmp/bin/ssh" DEPLOY_HOST=host \
    DEPLOY_DIR="$server" DROP_PLUGINS="${3:-}" sh "$script") >"$tmp/out" 2>&1
}

fail() { echo "FAIL: $1" >&2; cat "$tmp/out" >&2; exit 1; }

if deploy "alpha beta" "alpha"; then fail "a build without beta replaced a server that runs it"; fi
grep -q "carries beta" "$tmp/out" || fail "the refusal does not name the plugin"
grep -q "docker" "$tmp/out" && fail "the container was touched before the refusal"

deploy "alpha beta" "alpha" 1 || fail "DROP_PLUGINS=1 did not let the plugin go"
deploy "alpha beta" "beta gamma alpha" || fail "a build carrying everything, and more, was refused"
deploy "" "alpha" || fail "a first deploy, with no list on the server, was refused"
deploy " " "alpha" || fail "a server whose list is empty was refused"

# A server last deployed before the list was renamed still has the old file,
# and a build that drops what it names is refused all the same.
if deploy "alpha beta" "alpha" "" PLUGINS; then fail "the old list file was not read"; fi
grep -q "carries beta" "$tmp/out" || fail "the refusal from the old list file does not name the plugin"
deploy "alpha beta" "alpha beta" "" PLUGINS || fail "a build carrying everything the old list names was refused"

# The list and the directory of bundled packages must be able to sit side by
# side on a case-insensitive file system.
mkdir -p "$work/dist/plugins"
echo alpha >"$work/dist/PLUGIN_LIST"
[ -f "$work/dist/PLUGIN_LIST" ] && [ -d "$work/dist/plugins" ] || fail "the list and the packages directory collide"

echo "deploy.sh plugin guard: ok"
