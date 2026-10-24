#!/bin/bash

ELB_INSTALLER_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ELB_ASSETS_DIR="$ELB_INSTALLER_ROOT/assets"
ELB_BIN_DIR="$ELB_INSTALLER_ROOT/bin"

C_RED=$'\033[31m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_BOLD=$'\033[1m'; C_RESET=$'\033[0m'

log_info()  { echo "${C_GREEN}[*]${C_RESET} $*"; }
log_warn()  { echo "${C_YELLOW}[!]${C_RESET} $*" >&2; }
log_err()   { echo "${C_RED}[x]${C_RESET} $*" >&2; }
log_step()  { echo; echo "${C_BOLD}==> $*${C_RESET}"; }

die() { log_err "$*"; exit 1; }

ask() {
	local __var="$1" __prompt="$2" __default="${3-}" __reply
	if [ -n "$__default" ]; then
		read -r -p "$__prompt [$__default]: " __reply </dev/tty
	else
		read -r -p "$__prompt: " __reply </dev/tty
	fi
	[ -z "$__reply" ] && __reply="$__default"
	printf -v "$__var" '%s' "$__reply"
}

ask_secret() {
	local __var="$1" __prompt="$2" __reply
	read -r -s -p "$__prompt: " __reply </dev/tty
	echo
	printf -v "$__var" '%s' "$__reply"
}

confirm() {
	local __prompt="$1" __reply
	read -r -p "$__prompt [y/N]: " __reply </dev/tty
	[[ "$__reply" =~ ^[Yy]([Ee][Ss])?$ ]]
}

confirm_exact() {
	local __prompt="$1" __expect="$2" __reply
	read -r -p "$__prompt: " __reply </dev/tty
	[ "$__reply" = "$__expect" ]
}

require_root() {
	[ "$(id -u)" -eq 0 ] || die "This installer must run as root (sudo $0)."
}

check_os() {
	[ -r /etc/os-release ] || die "/etc/os-release not found — cannot detect the distribution."
	. /etc/os-release
	if [ "$ID" != "debian" ] || { [[ "$VERSION_ID" != 12* ]] && [[ "$VERSION_ID" != 13* ]]; }; then
		log_warn "This installer targets Debian 12/13. Detected: $PRETTY_NAME."
		confirm "Continue anyway, at your own risk?" || die "Cancelled by user."
	fi
}

sync_time() {
	timedatectl set-ntp true 2>/dev/null \
		|| log_warn "Could not enable NTP (timedatectl set-ntp true) — check system time manually."
}

render_template() {
	local src="$1" dst="$2"; shift 2
	local sed_args=()
	local pair key val
	for pair in "$@"; do
		key="${pair%%=*}"
		val="${pair#*=}"
		val="${val//\\/\\\\}"
		val="${val//&/\\&}"
		val="${val//|/\\|}"
		sed_args+=(-e "s|__${key}__|${val}|g")
	done
	mkdir -p "$(dirname "$dst")"
	sed "${sed_args[@]}" "$src" >"$dst"
}

port_in_use() {
	local port="$1"
	ss -Htln "sport = :$port" 2>/dev/null | grep -q . || \
		ss -Htun "sport = :$port" 2>/dev/null | grep -q .
}

unit_exists() {
	systemctl list-unit-files "$1" --no-legend 2>/dev/null | grep -q .
}

host_ips() {
	ip -4 -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1
}
