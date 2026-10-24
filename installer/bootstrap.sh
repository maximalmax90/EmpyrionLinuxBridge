#!/bin/bash
set -euo pipefail

GITHUB_REPO="${ELB_INSTALLER_REPO:-maximalmax90/EmpyrionLinuxBridge}"

TMPDIR="${ELB_INSTALLER_TMPDIR:-/tmp}"

[ "$(id -u)" -eq 0 ] || { echo "Run as root (sudo $0)." >&2; exit 1; }

if [ -n "${ELB_INSTALLER_ARCHIVE:-}" ]; then
	ARCHIVE="$ELB_INSTALLER_ARCHIVE"
	[ -f "$ARCHIVE" ] || { echo "ELB_INSTALLER_ARCHIVE points to a nonexistent file: $ARCHIVE" >&2; exit 1; }
	echo "Using the archive from ELB_INSTALLER_ARCHIVE: $ARCHIVE"
else
	ARCHIVE="$(find "$TMPDIR" -maxdepth 1 -name 'elb-installer*.tar.gz' -printf '%T@ %p\n' 2>/dev/null \
		| sort -rn | head -1 | cut -d' ' -f2-)"
	if [ -n "$ARCHIVE" ]; then
		echo "Found an archive in $TMPDIR: $ARCHIVE (download skipped)."
	else
		url="https://github.com/$GITHUB_REPO/releases/latest/download/elb-installer.tar.gz"
		ARCHIVE="$TMPDIR/elb-installer.tar.gz"
		echo "No archive found in $TMPDIR — downloading: $url"
		if command -v curl >/dev/null 2>&1; then
			curl -fL --retry 3 -o "$ARCHIVE" "$url"
		elif command -v wget >/dev/null 2>&1; then
			wget -O "$ARCHIVE" "$url"
		else
			echo "curl or wget is required to download the archive." >&2
			exit 1
		fi
	fi
fi

WORKDIR="$(mktemp -d "$TMPDIR/elb-installer.XXXXXX")"
echo "Extracting $ARCHIVE to $WORKDIR"
tar -xzf "$ARCHIVE" -C "$WORKDIR"
[ -f "$WORKDIR/install.sh" ] || { echo "install.sh not found in the archive — was it built incorrectly?" >&2; exit 1; }

exec "$WORKDIR/install.sh" "$@"
