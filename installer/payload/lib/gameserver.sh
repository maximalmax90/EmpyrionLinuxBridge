#!/bin/bash

gameserver_deploy() {
	render_template "$ELB_ASSETS_DIR/empyrion.env.tmpl" "$BTRFS_MOUNTPOINT/empyrion.env" \
		"WINE_DIR=$WINE_DIR"
	render_template "$ELB_ASSETS_DIR/server.sh.tmpl" "$BTRFS_MOUNTPOINT/server.sh" \
		"MOUNTPOINT=$BTRFS_MOUNTPOINT" "EMP_DIR=$EMP_DIR"
	chmod 755 "$BTRFS_MOUNTPOINT/server.sh"
	chown empyrion:empyrion "$BTRFS_MOUNTPOINT/server.sh" "$BTRFS_MOUNTPOINT/empyrion.env"

	install -m 644 "$ELB_ASSETS_DIR/systemd/xvfb.service" /etc/systemd/system/xvfb.service
	render_template "$ELB_ASSETS_DIR/systemd/empyrion.service.tmpl" /etc/systemd/system/empyrion.service \
		"MOUNTPOINT=$BTRFS_MOUNTPOINT"

	systemctl daemon-reload
	systemctl enable --now xvfb.service
	systemctl enable empyrion.service
}
