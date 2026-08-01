#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
  echo "usage: $0 <tag> <source-sha256> <output>" >&2
  exit 2
fi

release_tag=$1
source_sha256=$2
formula_output=$3
release_version=${release_tag#v}

if ! printf '%s\n' "$release_tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo "tag must use vMAJOR.MINOR.PATCH form" >&2
	exit 2
fi

case "$source_sha256" in
	*[!0-9a-f]*|"")
		echo "source SHA-256 must be 64 lowercase hexadecimal characters" >&2
		exit 2
		;;
esac

if [ "${#source_sha256}" -ne 64 ]; then
	echo "source SHA-256 must be 64 lowercase hexadecimal characters" >&2
	exit 2
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
template_path="$script_dir/../packaging/homebrew/vlc-media-watcher.rb.tmpl"

sed \
  -e "s/__TAG__/$release_tag/g" \
  -e "s/__VERSION__/$release_version/g" \
  -e "s/__SHA256__/$source_sha256/g" \
  "$template_path" >"$formula_output"
