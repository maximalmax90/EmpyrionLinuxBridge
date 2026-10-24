#!/bin/bash

layout_setup() {
	local root="$BTRFS_MOUNTPOINT"
	mkdir -p "$root/EMP/Content"
	local sv
	for sv in wine EMP/elb EMP/Content/Scenarios EMP/Content/Mods EMP/Saves; do
		if [ ! -d "$root/$sv" ]; then
			btrfs subvolume create "$root/$sv" >/dev/null
		fi
	done
	mkdir -p "$root/backups"

	chown -R empyrion:empyrion "$root/EMP" "$root/wine"
	chown empyrion:empyrion "$root/backups"

	EMP_DIR="$root/EMP"
	WINE_DIR="$root/wine"
	BACKUPS_DIR="$root/backups"
}
