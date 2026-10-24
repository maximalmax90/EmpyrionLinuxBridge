#!/bin/bash

pkg_installed() { dpkg -s "$1" >/dev/null 2>&1; }

preflight_check() {
	local findings=()

	if $WITH_DAEMON; then
		port_in_use "$DAEMON_HTTP_PORT" && findings+=("port $DAEMON_HTTP_PORT (daemon API) is already in use")
		unit_exists "empyrion.service" && findings+=("systemd unit empyrion.service already exists")
		unit_exists "xvfb.service" && findings+=("systemd unit xvfb.service already exists")
		unit_exists "elb-daemon.service" && findings+=("systemd unit elb-daemon.service already exists")
		id empyrion >/dev/null 2>&1 && findings+=("user empyrion already exists")
		[ -e /usr/local/bin/elb-daemon ] && findings+=("/usr/local/bin/elb-daemon already exists")
		[ -e /etc/elb/daemon.json ] && findings+=("/etc/elb/daemon.json already exists")
		pkg_installed winehq-stable && findings+=("package winehq-stable is already installed")
		pkg_installed steamcmd && findings+=("package steamcmd is already installed")
	fi

	if $WITH_UI; then
		port_in_use "$UI_PORT" && findings+=("port $UI_PORT (web panel) is already in use")
		unit_exists "elb-ui.service" && findings+=("systemd unit elb-ui.service already exists")
		[ -e /usr/local/bin/elb-ui ] && findings+=("/usr/local/bin/elb-ui already exists")
		[ -e /etc/elb/ui.json ] && findings+=("/etc/elb/ui.json already exists")
	fi

	if $NGINX_ENABLE; then
		pkg_installed nginx && findings+=("nginx is already installed")
		[ -e /etc/nginx/sites-enabled/elb-ui.conf ] && findings+=("/etc/nginx/sites-enabled/elb-ui.conf already exists")
	fi

	if [ ${#findings[@]} -eq 0 ]; then
		log_info "No conflicts found."
		return
	fi

	log_warn "Possible conflicts found:"
	local f
	for f in "${findings[@]}"; do
		echo "  - $f"
	done
	confirm "Continue anyway?" || die "Cancelled by user."
}
