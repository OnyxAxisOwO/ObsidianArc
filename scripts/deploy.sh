#!/bin/sh
# Ships what `make release` put in dist/ to the Arc server and replaces the
# running container with one built from it. `make deploy` runs this.
#
# One SSH connection carries both the files and the commands, so a server
# that asks for a password asks once. The image is built there from the
# two-line Dockerfile.release, which takes seconds; the database container is
# never touched — `up --no-build server` recreates the server alone.
#
# Arc only. Chat is a separate system on the same kind of host (a systemd
# unit, its own port); nothing here goes near it.

set -eu

host=${DEPLOY_HOST:?set DEPLOY_HOST, e.g. make deploy DEPLOY_HOST=root@203.0.113.7}
dir=${DEPLOY_DIR:-/data/obsidian-arc}
# The transport, overridable so the remote half can be exercised without a
# server. Anything that takes a host and then a command will do.
ssh_command=${DEPLOY_SSH:-ssh}

if [ ! -f dist/obsidian-arc ] || [ ! -f dist/Dockerfile ]; then
  echo "dist/ is empty; run make release first" >&2
  exit 1
fi
# A plain version is what git describe prints for a release, a commit past one
# or a dirty tree: letters, digits, dot, plus and minus, starting with a letter
# or digit. Anything else is refused, because the version is spliced into a
# command the server's shell runs. LC_ALL is pinned inside the check, since a
# range such as A-Z means different things in different locales.
version_is_plain() (
  export LC_ALL=C
  case "$1" in
    ''|[!A-Za-z0-9]*|*[!A-Za-z0-9.+-]*) exit 1 ;;
  esac
)
arch=$(cat dist/ARCH)
version=$(cat dist/VERSION)
# dist/VERSION is whatever make wrote from a git tag name, and a tag name may
# carry quotes and $(...). Checked before anything else reads it.
if ! version_is_plain "$version"; then
  echo "dist/VERSION is refused: \"$version\" is not a plain version; rebuild with make release" >&2
  exit 1
fi
# dist/ARCH reaches the same remote command line as the version, so it is held
# to a platform name: lower-case letters and digits, as amd64 and arm64 are.
case "$arch" in
  ''|*[!a-z0-9]*)
    echo "dist/ARCH is refused: \"$arch\" is not a platform name; rebuild with make release" >&2
    exit 1 ;;
esac
# What this build carries, and whether the caller has accepted losing some of
# what the running server has.
plugins=$(cat dist/PLUGIN_LIST 2>/dev/null || true)
# The names are spliced into the remote command below, inside quotes the
# server's shell reads, so each is held to the characters a plain name holds.
# A quote in one would end those quotes and run what followed. Globbing is off
# for the loop so that a '*' is judged as written, not as the files it names.
plugin_list_is_plain() (
  export LC_ALL=C
  set -f
  for p in $1; do
    case "$p" in
      *[!A-Za-z0-9._+-]*) exit 1 ;;
    esac
  done
)
if ! plugin_list_is_plain "$plugins"; then
  echo "dist/PLUGIN_LIST is refused: \"$plugins\" holds a name that is not plain; rebuild with make release" >&2
  exit 1
fi
drop=${DROP_PLUGINS:-}

# macOS tar would otherwise add AppleDouble files and extended attributes,
# which GNU tar on the server warns about and extracts as clutter.
COPYFILE_DISABLE=1 tar -czf - -C dist . | $ssh_command "$host" "set -e
  # A binary for the wrong processor starts as 'exec format error' and a
  # container in a restart loop. Checked before anything is replaced.
  case \$(uname -m) in
    x86_64) have=amd64 ;;
    aarch64|arm64) have=arm64 ;;
    *) have=\$(uname -m) ;;
  esac
  if [ \"\$have\" != '$arch' ]; then
    # Read the upload to the end first, or the sending tar dies of a broken
    # pipe and its 'Write error' reads like the cause instead of this line.
    cat >/dev/null
    echo \"this server is \$have but the build is $arch; run: make deploy ARCH=\$have\" >&2
    exit 1
  fi
  cd '$dir'
  # The last deploy left its plugin list beside its binary. A plugin the
  # running server carries and this build does not would be switched off
  # under the people using it — a registration field gone, a sign-up check
  # no longer standing in front of the form — with nothing on the way
  # saying so. The person deploying has to have decided that.
  # The list is not called PLUGINS: dist/plugins holds the bundled packages,
  # and on a case-insensitive file system (a Mac's, where this is built) the
  # two are the same name. A server last deployed before the rename still has
  # the old file, which is read once more.
  previous=
  if [ -f dist/PLUGIN_LIST ]; then previous=\$(cat dist/PLUGIN_LIST)
  elif [ -f dist/PLUGINS ]; then previous=\$(cat dist/PLUGINS); fi
  if [ -n \"\$previous\" ] && [ -z '$drop' ]; then
    lost=
    for p in \$previous; do
      case ' $plugins ' in
        *\" \$p \"*) ;;
        *) lost=\"\$lost \$p\" ;;
      esac
    done
    if [ -n \"\$lost\" ]; then
      cat >/dev/null
      echo \"the running server carries\$lost, which this build does not; build with it, or uninstall it in the backoffice and run again with DROP_PLUGINS=1\" >&2
      exit 1
    fi
  fi
  # dist/ because both the server's .gitignore and .dockerignore already
  # leave it out: a checkout there stays clean, and a later source build
  # does not drag the binary into its context.
  rm -rf dist
  mkdir dist
  tar -xzf - -C dist
  # Tagged with the version as well as latest, so the build before this one
  # is still there to go back to: docker tag obsidian-arc:<version> obsidian-arc:latest
  docker build -q -t 'obsidian-arc:$version' -t obsidian-arc:latest dist
  docker compose up -d --no-build server
  docker compose ps server"

echo "deployed $version to $host:$dir"
