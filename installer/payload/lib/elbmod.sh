#!/bin/bash

elbmod_deploy() {
	local src="$ELB_BIN_DIR/ElbMod"
	[ -f "$src/ElbMod.dll" ] || die "$src/ElbMod.dll not found in the archive — was it built without the mod? See build-archive.sh."
	local dest="$EMP_DIR/Content/Mods/ElbMod"
	mkdir -p "$dest"
	cp -r "$src"/. "$dest/"
	chown -R empyrion:empyrion "$dest"
}
