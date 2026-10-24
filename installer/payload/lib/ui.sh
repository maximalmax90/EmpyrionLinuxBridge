#!/bin/bash

ui_deploy() {
	install -m 755 "$ELB_BIN_DIR/elb-ui" /usr/local/bin/elb-ui

	if [ "$INSTALL_MODE" = "ui" ] && [ -z "$DAEMON_CERT_FP" ]; then
		log_warn "daemon_cert_fingerprint is not set — TLS certificate verification for the daemon is disabled (InsecureSkipVerify)."
		log_warn "Acceptable for LAN/testing; fill it in for a production setup over the network (see CLAUDE.md)."
	fi

	mkdir -p /var/lib/elb-ui
	ui_write_config

	install -m 644 "$ELB_ASSETS_DIR/systemd/elb-ui.service" /etc/systemd/system/elb-ui.service

	systemctl daemon-reload
	systemctl enable --now elb-ui.service
}

ui_write_config() {
	local esc_url esc_token esc_fp
	esc_url=$(printf '%s' "$DAEMON_URL" | sed 's/\\/\\\\/g; s/"/\\"/g')
	esc_token=$(printf '%s' "$DAEMON_TOKEN" | sed 's/\\/\\\\/g; s/"/\\"/g')
	esc_fp=$(printf '%s' "$DAEMON_CERT_FP" | sed 's/\\/\\\\/g; s/"/\\"/g')
	mkdir -p /etc/elb
	local trust_proxy=false
	$NGINX_ENABLE && trust_proxy=true
	cat >/etc/elb/ui.json <<JSON
{
  "listen_addr": ":$UI_PORT",
  "daemon_url": "$esc_url",
  "daemon_token": "$esc_token",
  "daemon_cert_fingerprint": "$esc_fp",
  "db_file": "/var/lib/elb-ui/ui.db",
  "poll_interval_seconds": 3,
  "trust_proxy_headers": $trust_proxy
}
JSON
	chmod 600 /etc/elb/ui.json
}
