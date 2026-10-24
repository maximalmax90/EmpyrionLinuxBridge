#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib/common.sh"
source "$SCRIPT_DIR/lib/preflight.sh"
source "$SCRIPT_DIR/lib/btrfs.sh"
source "$SCRIPT_DIR/lib/packages.sh"
source "$SCRIPT_DIR/lib/users.sh"
source "$SCRIPT_DIR/lib/layout.sh"
source "$SCRIPT_DIR/lib/wine.sh"
source "$SCRIPT_DIR/lib/steam.sh"
source "$SCRIPT_DIR/lib/gameserver.sh"
source "$SCRIPT_DIR/lib/elbmod.sh"
source "$SCRIPT_DIR/lib/daemon.sh"
source "$SCRIPT_DIR/lib/ui.sh"
source "$SCRIPT_DIR/lib/nginx.sh"
source "$SCRIPT_DIR/lib/firewall.sh"
source "$SCRIPT_DIR/lib/updatescripts.sh"
source "$SCRIPT_DIR/lib/summary.sh"
source "$SCRIPT_DIR/lib/state.sh"

ARG_DAEMON_URL=""
ARG_DAEMON_TOKEN=""
ARG_DAEMON_CERT_FP=""
RESUME=false
while [ $# -gt 0 ]; do
	case "$1" in
	--daemon-url) ARG_DAEMON_URL="$2"; shift 2 ;;
	--daemon-token) ARG_DAEMON_TOKEN="$2"; shift 2 ;;
	--daemon-cert-fingerprint) ARG_DAEMON_CERT_FP="$2"; shift 2 ;;
	--resume) RESUME=true; shift ;;
	-h | --help)
		echo "Usage: $0 [--daemon-url URL --daemon-token TOKEN --daemon-cert-fingerprint SHA256] [--resume]"
		exit 0
		;;
	*) die "Unknown argument: $1" ;;
	esac
done

require_root
check_os
sync_time

echo "${C_BOLD}ELB installer (Debian 12/13)${C_RESET}"
if $RESUME; then
	echo "Resuming a previous install."
else
	echo "4 stages (prepare/gameserver/panel/finish) — a mid-way failure can be retried with --resume instead of starting over."
fi
echo

if ! $RESUME; then

echo "What do you want to install?"
echo "  1) Daemon + game server"
echo "  2) Web panel (UI) only"
echo "  3) Everything (daemon + game server + panel)"
MODE_CHOICE=""
ask MODE_CHOICE "Choice (1/2/3)" "3"
case "$MODE_CHOICE" in
1) INSTALL_MODE="daemon" ;;
2) INSTALL_MODE="ui" ;;
3) INSTALL_MODE="all" ;;
*) die "Invalid choice: $MODE_CHOICE" ;;
esac
WITH_DAEMON=false; WITH_UI=false
[ "$INSTALL_MODE" = "daemon" ] || [ "$INSTALL_MODE" = "all" ] && WITH_DAEMON=true
[ "$INSTALL_MODE" = "ui" ] || [ "$INSTALL_MODE" = "all" ] && WITH_UI=true

BTRFS_MOUNTPOINT="/server"
BTRFS_FORMAT_DEVICE=""
DAEMON_HTTP_PORT="8090"
GAME_SRV_PORT="30000"
GAME_TEL_PORT="30004"
UI_PORT="8081"
NGINX_ENABLE=false
DAEMON_URL="$ARG_DAEMON_URL"
DAEMON_TOKEN="$ARG_DAEMON_TOKEN"
DAEMON_CERT_FP="$ARG_DAEMON_CERT_FP"

if $WITH_DAEMON; then
	log_step "btrfs partition"
	btrfs_ask
	log_step "Game server/daemon ports"
	ask DAEMON_HTTP_PORT "Daemon API port" "$DAEMON_HTTP_PORT"
	ask GAME_SRV_PORT "Game server port (Srv_Port)" "$GAME_SRV_PORT"
	ask GAME_TEL_PORT "Telnet port (Tel_Port)" "$GAME_TEL_PORT"
fi

if $WITH_UI; then
	log_step "Web panel"
	if [ "$INSTALL_MODE" = "ui" ]; then
		if [ -z "$DAEMON_URL" ] || [ -z "$DAEMON_TOKEN" ]; then
			echo "The panel is installed separately from the daemon — need the details of an already deployed daemon"
			echo "(printed at the end of the daemon installer: URL, token, certificate sha256 fingerprint)."
			ask DAEMON_URL "Daemon URL (https://host:port)" "$DAEMON_URL"
			ask DAEMON_TOKEN "Daemon token" "$DAEMON_TOKEN"
			ask DAEMON_CERT_FP "Daemon certificate SHA-256 fingerprint (empty = skip verification, LAN only)" "$DAEMON_CERT_FP"
		fi
	fi
	ask UI_PORT "Web panel port" "$UI_PORT"
	if [ "$INSTALL_MODE" = "ui" ]; then
		log_warn "The panel has no TLS of its own — traffic to the admin's browser is plaintext."
		confirm "Install nginx as a reverse proxy with a self-signed cert in front of the panel?" && NGINX_ENABLE=true
	else
		confirm "Install nginx as a reverse proxy with a self-signed cert in front of the panel? (recommended if the panel faces the internet)" && NGINX_ENABLE=true
	fi
fi

log_step "Checking for conflicts"
preflight_check

summary_confirm

ELB_INSTALLER_STAGE=0
installer_save_state

else
	installer_load_state
fi

if $WITH_DAEMON; then
	EMP_DIR="$BTRFS_MOUNTPOINT/EMP"
	WINE_DIR="$BTRFS_MOUNTPOINT/wine"
	BACKUPS_DIR="$BTRFS_MOUNTPOINT/backups"
fi


if [ "$ELB_INSTALLER_STAGE" -lt 1 ]; then
	log_step "Installing packages"
	packages_install

	log_step "System user"
	users_setup

	if $WITH_DAEMON; then
		if [ -n "$BTRFS_FORMAT_DEVICE" ]; then
			log_step "Formatting $BTRFS_FORMAT_DEVICE as btrfs"
			btrfs_format_and_mount
		else
			log_step "Mounting the existing btrfs partition"
			btrfs_ensure_mounted
		fi
		log_step "Directory/subvolume layout"
		layout_setup
	fi

	ELB_INSTALLER_STAGE=1
	installer_save_state
fi

if [ "$ELB_INSTALLER_STAGE" -lt 2 ]; then
	if $WITH_DAEMON; then
		log_step "Deploying the game server (server.sh, systemd units, starting Xvfb)"
		gameserver_deploy
		log_step "Initializing wine"
		wine_init
		log_step "Installing the game server via steamcmd"
		steam_install_server
		log_step "Configuring Telnet in dedicated.yaml"
		steam_configure_telnet
		log_step "Installing ElbMod"
		elbmod_deploy
		log_step "Installing the daemon"
		daemon_deploy
		updatescripts_deploy_daemon
	fi

	ELB_INSTALLER_STAGE=2
	installer_save_state
fi

if [ "$ELB_INSTALLER_STAGE" -lt 3 ]; then
	if $WITH_UI; then
		log_step "Installing the web panel"
		ui_deploy
		updatescripts_deploy_ui
		if $NGINX_ENABLE; then
			log_step "Configuring nginx"
			nginx_deploy
		fi
	fi

	ELB_INSTALLER_STAGE=3
	installer_save_state
fi

if [ "$ELB_INSTALLER_STAGE" -lt 4 ]; then
	log_step "Firewall"
	firewall_setup

	ELB_INSTALLER_STAGE=4
	installer_save_state
fi

summary_final
