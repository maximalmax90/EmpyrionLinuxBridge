#!/bin/bash

summary_confirm() {
	log_step "Final plan"
	echo "Install mode: $INSTALL_MODE"
	if $WITH_DAEMON; then
		if [ -n "$BTRFS_FORMAT_DEVICE" ]; then
			echo "  btrfs: FORMAT $BTRFS_FORMAT_DEVICE -> $BTRFS_MOUNTPOINT"
		else
			echo "  btrfs: use existing partition $BTRFS_MOUNTPOINT"
		fi
		echo "  daemon API port: $DAEMON_HTTP_PORT"
		echo "  game port: $GAME_SRV_PORT, telnet: $GAME_TEL_PORT"
	fi
	if $WITH_UI; then
		echo "  panel port: $UI_PORT"
		[ "$INSTALL_MODE" = "ui" ] && echo "  daemon: $DAEMON_URL"
		echo "  nginx: $($NGINX_ENABLE && echo yes || echo no)"
	fi
	echo

	if [ -n "$BTRFS_FORMAT_DEVICE" ]; then
		log_warn "$BTRFS_FORMAT_DEVICE WILL BE COMPLETELY FORMATTED. All data on it will be destroyed permanently."
		confirm_exact "To confirm, type the exact device name ($BTRFS_FORMAT_DEVICE)" "$BTRFS_FORMAT_DEVICE" \
			|| die "Name did not match — cancelled."
	fi

	confirm "Start the installation with these parameters?" || die "Cancelled by user."
}

summary_final() {
	log_step "Installation complete"
	if $WITH_DAEMON; then
		echo "Daemon:"
		echo "  URL:         $DAEMON_URL"
		echo "  Token:       $DAEMON_TOKEN"
		echo "  Fingerprint: $DAEMON_CERT_FP"
		echo "  (save these — needed to install the panel on another machine;"
		echo "   if the URL shows the wrong address for reaching this machine"
		echo "   from outside — replace it manually)"
		echo "  Check: systemctl status elb-daemon xvfb"
		echo "  Game server is NOT running — start it from the panel (or: systemctl start empyrion)"
	fi
	if $WITH_UI; then
		echo "Panel:"
		local ip ips_found=false
		while IFS= read -r ip; do
			[ -n "$ip" ] || continue
			ips_found=true
			if $NGINX_ENABLE; then
				echo "  https://$ip/  (self-signed certificate)"
			else
				echo "  http://$ip:$UI_PORT/"
			fi
		done < <(host_ips)
		if ! $ips_found; then
			if $NGINX_ENABLE; then
				echo "  https://<this-host>/  (self-signed certificate; no non-loopback IPv4 address detected)"
			else
				echo "  http://<this-host>:$UI_PORT/  (no non-loopback IPv4 address detected)"
			fi
		fi
		echo "  First login: admin/admin — you'll be asked to change it immediately."
		echo "  Check: systemctl status elb-ui"
	fi
}
