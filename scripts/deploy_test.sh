#!/bin/sh
# Exercises the remote half of deploy.sh without a server: a stand-in for ssh
# runs the script's command on this machine, in a directory that plays the
# production host, with a docker that only says what it was asked.
#
# What is under test is the refusal to drop a plugin: the running server's
# last deploy left its plugin list beside its binary, and a build without one
# of them would switch that feature off under the people using it.
#
# The version is spliced into a command the server's shell runs, and a git tag
# name can carry quotes and $(...), so a version that is not plain has to stop
# both the Makefile and the deploy before anything runs it.

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

# A tag name reaches the server's shell through dist/VERSION. A quote in it runs
# its payload there, so the deploy has to stop before ssh is reached, and no
# docker command may run for that version either.
deploy_proof=$tmp/deploy-proof
printf '%s\n' "v1'; touch '$deploy_proof'; echo '" >"$work/dist/VERSION"
if deploy "" "alpha"; then fail "a version with a quote in it was deployed"; fi
grep -q "not a plain version" "$tmp/out" || fail "the refusal does not say why"
[ -e "$deploy_proof" ] && fail "a tag name ran as a command on the server"
grep -q docker "$tmp/out" && fail "docker ran for a version that was refused"
for v in v0.9.2 v0.9.2-64-g356a68e v0.9.2-64-g356a68e-dirty v1.3.0-rc.1-14-g1a2b3c4 356a68e dev; do
  printf '%s\n' "$v" >"$work/dist/VERSION"
  deploy "" "alpha" || fail "the plain version $v was refused"
done
# dist/ARCH is spliced into the same remote command, so it is held the same way.
arch_proof=$tmp/arch-proof
good_arch=$(cat "$work/dist/ARCH")
printf '%s\n' "amd64'; touch '$arch_proof'; echo '" >"$work/dist/ARCH"
if deploy "" "alpha"; then fail "an ARCH with a quote in it was deployed"; fi
grep -q "not a platform name" "$tmp/out" || fail "the ARCH refusal does not say why"
[ -e "$arch_proof" ] && fail "dist/ARCH ran as a command on the server"
printf '%s\n' "$good_arch" >"$work/dist/ARCH"
deploy "" "alpha" || fail "the plain ARCH $good_arch was refused"
echo "deploy.sh version guard: ok"

# The Makefile is the first place a tag name reaches a shell, so it refuses the
# same things before any recipe runs.
root=$(cd "$(dirname "$0")/.." && pwd)
make_proof=$tmp/make-proof
if make -s -C "$root" version VERSION="v1; touch '$make_proof'" >"$tmp/make.out" 2>&1; then
  fail "make accepted a version that is shell syntax"
fi
[ -e "$make_proof" ] && fail "make ran a version as a command"
grep -q "is refused" "$tmp/make.out" || fail "make does not say why it refused the version"
for v in v0.9.2 v0.9.2-64-g356a68e-dirty v1.3.0-rc.1-14-g1a2b3c4 356a68e dev; do
  make -s -C "$root" version VERSION="$v" >/dev/null 2>&1 || fail "make refused the plain version $v"
done
echo "Makefile version guard: ok"

# The list and the directory of bundled packages must be able to sit side by
# side on a case-insensitive file system.
mkdir -p "$work/dist/plugins"
echo alpha >"$work/dist/PLUGIN_LIST"
[ -f "$work/dist/PLUGIN_LIST" ] && [ -d "$work/dist/plugins" ] || fail "the list and the packages directory collide"

# The names in dist/PLUGIN_LIST are spliced into the command the server's shell
# reads, inside quotes. A name with a quote in it ends them there and runs what
# follows, which is how a package file's name would reach the server. The list
# is held to plain characters before ssh is reached, so no docker command runs
# for it either.
plugin_proof=$tmp/plugin-proof
if deploy "alpha" "alpha x'\`touch '$plugin_proof'\`'"; then fail "a plugin name with a quote in it was deployed"; fi
grep -q "holds a name that is not plain" "$tmp/out" || fail "the plugin name refusal does not say why"
[ -e "$plugin_proof" ] && fail "a plugin name ran as a command on the server"
grep -q docker "$tmp/out" && fail "docker ran for a plugin list that was refused"
if deploy "alpha" "*"; then fail "a glob in the plugin list was deployed"; fi
deploy "alpha_1.2+x-y" "alpha_1.2+x-y" || fail "a plain plugin name was refused"
echo "deploy.sh plugin name guard: ok"

# The Makefile holds the same things before any recipe runs. The package recipe
# copies the paths with an unquoted loop, and the names are echoed into
# dist/PLUGIN_LIST, so a backquote in either one runs a command on the build
# machine. PROOF comes from the environment because a package path is cut at its
# last slash to get the name, so a path written into the payload would be lost.
make_plugin_proof=$tmp/make-plugin-proof
if PROOF=$make_plugin_proof make -s -C "$root" version PLUGINS='alpha`:>$$PROOF`' >"$tmp/make.out" 2>&1; then
  fail "make accepted a plugin name that runs a command"
fi
[ -e "$make_plugin_proof" ] && fail "make ran a plugin name as a command"
grep -q "is refused" "$tmp/make.out" || fail "make does not say why it refused the plugin name"
make_package_proof=$tmp/make-package-proof
if PROOF=$make_package_proof make -s -C "$root" version PACKAGES='pkgs/x`:>$$PROOF`-1.0.arcx' >"$tmp/make.out" 2>&1; then
  fail "make accepted a package path that runs a command"
fi
[ -e "$make_package_proof" ] && fail "make ran a package path as a command"
grep -q "is refused" "$tmp/make.out" || fail "make does not say why it refused the package path"
make -s -C "$root" version PLUGINS="alpha_1.2+x-y" PACKAGES="../pkgs/alpha-1.0.arcx" >/dev/null 2>&1 \
  || fail "make refused a plain plugin name and package path"
echo "Makefile plugin name guard: ok"

# The check must see each value as typed. Expanded first, a $(shell ...) in a value
# runs while the makefile is parsed, and the check judges only what it printed; and
# a newline, which the check must refuse, reaches the echo recipes as a second
# shell command. Every payload must be refused before a recipe runs, so none
# creates its file. They run from a directory of their own, not the tree, in case
# one gets through; the newline payloads hold no slash, which the check refuses
# for another reason.
probe=$tmp/make-probe
mkdir -p "$probe"
nl='
'
# make_refuses <the file a payload would create> <make arguments...>
make_refuses() {
  proof=$1
  shift
  rm -f "$probe/$proof"
  if (cd "$probe" && make -f "$root/Makefile" "$@") >"$tmp/out" 2>&1; then
    fail "make accepted $*"
  fi
  [ -e "$probe/$proof" ] && fail "make ran a value as a command: $*"
  grep -q "is refused" "$tmp/out" || fail "make does not say why it refused $*"
}
make_refuses shell-proof version 'PLUGINS=$(shell touch shell-proof)'
make_refuses shell-proof version 'PACKAGES=pkgs/$(shell touch shell-proof)-1.0.arcx'
make_refuses shell-proof version 'VERSION=v1$(shell touch shell-proof)'
make_refuses newline-proof version "PLUGINS=alpha${nl}touch newline-proof"
make_refuses newline-proof version "PACKAGES=alpha-1.0.arcx${nl}touch newline-proof"
make_refuses newline-proof package "PLUGINS=alpha${nl}touch newline-proof"
rm -f "$probe/env-proof"
if (cd "$probe" && VERSION='v1$(shell touch env-proof)' make -f "$root/Makefile" version) >"$tmp/out" 2>&1; then
  fail "make accepted a VERSION from the environment that runs a command"
fi
[ -e "$probe/env-proof" ] && fail "make ran a VERSION from the environment as a command"
(cd "$probe" && make -s -f "$root/Makefile" version PLUGINS=alpha PACKAGES=pkgs/alpha-1.0.arcx) >"$tmp/out" 2>&1 \
  || fail "make refused a plain plugin name and package path"
grep -q "plugins: alpha alpha" "$tmp/out" || fail "the plain plugin list is not printed"
echo "Makefile value guard: ok"

echo "deploy.sh plugin guard: ok"
