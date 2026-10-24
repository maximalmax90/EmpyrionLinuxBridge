#!/bin/bash

daemon_deploy() {
	install -m 755 "$ELB_BIN_DIR/elb-daemon" /usr/local/bin/elb-daemon

	local http_addr
	if [ "$INSTALL_MODE" = "all" ]; then
		http_addr="127.0.0.1:$DAEMON_HTTP_PORT"
	else
		http_addr=":$DAEMON_HTTP_PORT"
	fi

	render_template "$ELB_ASSETS_DIR/systemd/elb-daemon.service.tmpl" /etc/systemd/system/elb-daemon.service \
		"HTTP_ADDR=$http_addr" \
		"DEDICATED_YAML=$EMP_DIR/dedicated.yaml" \
		"DATA_ROOT=$EMP_DIR" \
		"BACKUPS_DIR=$BACKUPS_DIR" \
		"STEAMCMD_BIN=/usr/games/steamcmd"

	systemctl daemon-reload
	systemctl enable --now elb-daemon.service

	log_info "Waiting for the daemon's first start (generating token/certificate)..."
	local i
	for i in $(seq 1 30); do
		[ -f /etc/elb/daemon.json ] && break
		sleep 1
	done
	[ -f /etc/elb/daemon.json ] || die "elb-daemon did not create /etc/elb/daemon.json within 30s — check: journalctl -u elb-daemon"

	DAEMON_TOKEN="$(python3 -c 'import json; print(json.load(open("/etc/elb/daemon.json"))["tokens"][0])' 2>/dev/null || true)"
	[ -n "$DAEMON_TOKEN" ] || die "Could not read the token from /etc/elb/daemon.json"
	DAEMON_CERT_FP="$(openssl x509 -in /etc/elb/tls.crt -noout -fingerprint -sha256 2>/dev/null | sed 's/^.*=//; s/://g' | tr 'A-F' 'a-f')"

	local ext_ip
	ext_ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if ($i=="src") print $(i+1)}')"
	[ -n "$ext_ip" ] || ext_ip="127.0.0.1"
	if [ "$INSTALL_MODE" = "all" ]; then
		DAEMON_URL="https://127.0.0.1:$DAEMON_HTTP_PORT"
	else
		DAEMON_URL="https://$ext_ip:$DAEMON_HTTP_PORT"
	fi
}
