#!/bin/bash

WINEHQ_KEYRING=/etc/apt/keyrings/winehq-archive.key
WINEHQ_KEY_URL=https://dl.winehq.org/wine-builds/winehq.key
WINEHQ_SOURCES=/etc/apt/sources.list.d/winehq-bookworm.sources
WINEHQ_SOURCES_URL=https://dl.winehq.org/wine-builds/debian/dists/bookworm/winehq-bookworm.sources

packages_ensure_components() {
	local f=/etc/apt/sources.list
	[ -f "$f" ] || return 0
	local codename
	codename="$(. /etc/os-release && echo "$VERSION_CODENAME")"
	[ -n "$codename" ] || return 0
	grep -Eq "^deb[^#]*\\b${codename}\\b main[^#]*contrib" "$f" && return 0
	sed -i -E "s/^(deb(-src)? [^ ]+ ${codename}(-security|-updates)? main)([^#]*)\$/\\1 contrib non-free non-free-firmware/" "$f"
}

packages_setup_wine_repo() {
	dpkg --add-architecture i386
	mkdir -p /etc/apt/keyrings
	[ -f "$WINEHQ_KEYRING" ] || wget -qO "$WINEHQ_KEYRING" "$WINEHQ_KEY_URL"
	[ -f "$WINEHQ_SOURCES" ] || wget -qO "$WINEHQ_SOURCES" "$WINEHQ_SOURCES_URL"
}

packages_preseed_steam_eula() {
	echo "steam steam/question select I AGREE" | debconf-set-selections
	echo "steam steam/license note" | debconf-set-selections
}

packages_install() {
	packages_ensure_components
	$WITH_DAEMON && packages_setup_wine_repo
	$WITH_DAEMON && packages_preseed_steam_eula

	local pkgs=(ca-certificates wget openssl)
	if $WITH_DAEMON; then
		pkgs+=(winehq-stable xvfb steamcmd btrfs-progs zstd python3-venv auditd)
	fi
	if $NGINX_ENABLE; then
		pkgs+=(nginx)
	fi

	apt-get update
	DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${pkgs[@]}"

	if $WITH_DAEMON; then
		mkdir -p /opt/elb
		python3 -m venv /opt/elb/iconextract-venv
		/opt/elb/iconextract-venv/bin/pip install --quiet --upgrade pip
		/opt/elb/iconextract-venv/bin/pip install --quiet UnityPy Pillow

		systemctl enable --now auditd
	fi
}
