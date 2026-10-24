#!/bin/bash

btrfs_ask() {
	echo "The daemon/game server needs a btrfs partition (instant CoW snapshots, see CLAUDE.md)."
	echo "  1) Already have a mounted btrfs partition"
	echo "  2) Have a free device to format as btrfs"
	local choice
	ask choice "Choice (1/2)" "1"
	case "$choice" in
	1) btrfs_ask_existing ;;
	2) btrfs_ask_device ;;
	*) die "Invalid choice: $choice" ;;
	esac
}

btrfs_ask_existing() {
	local path fstype mounts
	mounts="$(findmnt -t btrfs -o TARGET,SOURCE,LABEL,SIZE,AVAIL --list 2>/dev/null || true)"
	[ -n "$mounts" ] || die "No mounted btrfs filesystems found. Re-run and choose option 2 to format a device instead, or mount an existing btrfs filesystem manually first."
	log_info "Mounted btrfs filesystems:"
	echo "$mounts"
	echo
	ask path "Path to the mounted btrfs partition" "$BTRFS_MOUNTPOINT"
	fstype="$(findmnt -no FSTYPE --target "$path" 2>/dev/null || true)"
	[ "$fstype" = "btrfs" ] || die "$path is not mounted as btrfs (found: '${fstype:-nothing}'). btrfs is required."
	BTRFS_MOUNTPOINT="$path"
	BTRFS_FORMAT_DEVICE=""
	log_info "OK: $path — btrfs."
}

btrfs_min_device_bytes() { echo $((10 * 1024 * 1024 * 1024)); }

btrfs_ask_device() {
	local dev mp candidates
	candidates="$(lsblk -e7 -p -l -n -b -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINT 2>/dev/null | awk -v min="$(btrfs_min_device_bytes)" '
		$5 != "" { next }
		$4 == "swap" { next }
		$2 ~ /^[0-9]+$/ && $2+0 < min { next }
		{ print $1 }
	' || true)"
	[ -n "$candidates" ] || die "No suitable device/partition found (everything is either already mounted, swap, or under 10GB). Nothing to format — free up or unmount a device first."
	log_info "Block devices and partitions (unmounted, non-swap, 10GB+):"
	lsblk -e7 -p -l -d -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINT $candidates || true
	echo
	ask dev "Device/partition to format (e.g. /dev/sdb1)" ""
	[ -b "$dev" ] || die "$dev is not a block device. btrfs is required."
	if findmnt -no TARGET --source "$dev" >/dev/null 2>&1; then
		die "$dev is already mounted — cannot format a mounted device."
	fi
	ask mp "Where to mount it after formatting" "$BTRFS_MOUNTPOINT"
	BTRFS_FORMAT_DEVICE="$dev"
	BTRFS_MOUNTPOINT="$mp"
}

btrfs_ensure_mounted() {
	local fstype
	fstype="$(findmnt -no FSTYPE --target "$BTRFS_MOUNTPOINT" 2>/dev/null || true)"
	[ "$fstype" = "btrfs" ] || die "$BTRFS_MOUNTPOINT is no longer mounted as btrfs — installation aborted."
}

btrfs_format_and_mount() {
	mkfs.btrfs -f "$BTRFS_FORMAT_DEVICE"
	mkdir -p "$BTRFS_MOUNTPOINT"
	local uuid
	uuid="$(blkid -s UUID -o value "$BTRFS_FORMAT_DEVICE" 2>/dev/null || true)"
	[ -n "$uuid" ] || die "Could not determine the UUID of $BTRFS_FORMAT_DEVICE after formatting."
	if ! grep -q "UUID=$uuid" /etc/fstab 2>/dev/null; then
		echo "UUID=$uuid $BTRFS_MOUNTPOINT btrfs defaults 0 0" >>/etc/fstab
	fi
	mount "$BTRFS_MOUNTPOINT"
	btrfs_ensure_mounted
	log_info "$BTRFS_FORMAT_DEVICE mounted at $BTRFS_MOUNTPOINT."
}
