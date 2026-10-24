#!/bin/bash

steam_install_server() {
	if [ -n "${ELB_INSTALLER_STUB_DOWNLOADS:-}" ]; then
		log_warn "ELB_INSTALLER_STUB_DOWNLOADS=1 — skipping the real steamcmd server download (~19GB, stub for fast installer test runs, see README.md)."
		return 0
	fi

	local max_attempts=5 attempt=1
	while true; do
		if runuser -u empyrion -- /usr/games/steamcmd \
			+force_install_dir "$EMP_DIR" \
			+login anonymous \
			+app_update 530870 validate \
			+quit
		then
			return 0
		fi
		if [ "$attempt" -ge "$max_attempts" ]; then
			log_err "steamcmd: app_update 530870 failed after $max_attempts attempts — giving up."
			return 1
		fi
		log_warn "steamcmd: app_update 530870 failed (attempt $attempt/$max_attempts) — retrying in 60s..."
		sleep 60
		attempt=$((attempt + 1))
	done
}

steam_configure_telnet() {
	local yaml="$EMP_DIR/dedicated.yaml"
	if [ ! -f "$yaml" ]; then
		log_warn "dedicated.yaml не найден ($yaml) — пропускаю настройку Telnet (нормально при ELB_INSTALLER_STUB_DOWNLOADS=1)."
		return 0
	fi
	local pwd
	pwd="$(openssl rand -hex 8)"
	sed -i \
		-e "s/^[[:space:]]*#\{0,1\}[[:space:]]*Tel_Enabled:.*/    Tel_Enabled: true/" \
		-e "s/^[[:space:]]*#\{0,1\}[[:space:]]*Tel_Port:.*/    Tel_Port: $GAME_TEL_PORT/" \
		-e "s/^[[:space:]]*#\{0,1\}[[:space:]]*Tel_Pwd:.*/    Tel_Pwd: $pwd/" \
		"$yaml"
	chown empyrion:empyrion "$yaml"
}
