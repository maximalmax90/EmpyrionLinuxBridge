#!/bin/bash
STATE_FILE="/etc/elb/installer-state.sh"

installer_save_state() {
	mkdir -p "$(dirname "$STATE_FILE")"
	local var
	{
		for var in ELB_INSTALLER_STAGE INSTALL_MODE WITH_DAEMON WITH_UI \
			BTRFS_MOUNTPOINT BTRFS_FORMAT_DEVICE \
			DAEMON_HTTP_PORT GAME_SRV_PORT GAME_TEL_PORT UI_PORT NGINX_ENABLE \
			DAEMON_URL DAEMON_TOKEN DAEMON_CERT_FP; do
			printf '%s=%q\n' "$var" "${!var}"
		done
	} >"$STATE_FILE"
	chmod 600 "$STATE_FILE"
}

installer_load_state() {
	[ -f "$STATE_FILE" ] || die "No saved install state found at $STATE_FILE — cannot resume. Run without --resume to start a fresh install."
	source "$STATE_FILE"
	log_info "Resuming a previous install (stage $ELB_INSTALLER_STAGE already done)."
}
