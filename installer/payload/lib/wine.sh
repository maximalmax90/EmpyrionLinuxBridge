#!/bin/bash

wine_mono_required_version() {
	local full_ver="$1" mv
	mv="$(wget -qO- "https://gitlab.winehq.org/wine/wine/-/raw/wine-${full_ver}/dlls/appwiz.cpl/addons.c" 2>/dev/null \
		| grep -m1 -oP '^#define\s+MONO_VERSION\s+"\K[^"]+')"
	if [ -z "$mv" ]; then
		local short_ver="${full_ver%.*}"
		mv="$(wget -qO- "https://gitlab.winehq.org/wine/wine/-/raw/wine-${short_ver}/dlls/appwiz.cpl/addons.c" 2>/dev/null \
			| grep -m1 -oP '^#define\s+MONO_VERSION\s+"\K[^"]+')"
	fi
	echo "$mv"
}

wine_init() {
	runuser -u empyrion -- env DISPLAY=:0.0 WINEARCH=win64 WINEPREFIX="$WINE_DIR" WINEDEBUG=-all \
		WINEDLLOVERRIDES="mscoree,mshtml=" \
		/opt/wine-stable/bin/wineboot --init || true
	sleep 10

	local wine_ver mono_ver mono_msi
	wine_ver="$(/opt/wine-stable/bin/wine --version | sed -n 's/^wine-//p')"
	[ -n "$wine_ver" ] || die "Could not determine the installed wine version (wine --version)."

	mono_ver="$(wine_mono_required_version "$wine_ver")"
	[ -n "$mono_ver" ] || die "Could not determine the required wine-mono version for wine $wine_ver (dlls/appwiz.cpl/addons.c unreachable)."

	log_info "wine $wine_ver -> minimum required wine-mono: $mono_ver"
	mono_msi="/tmp/wine-mono-${mono_ver}-x86.msi"
	wget -qO "$mono_msi" "https://dl.winehq.org/wine/wine-mono/${mono_ver}/wine-mono-${mono_ver}-x86.msi"
	runuser -u empyrion -- env DISPLAY=:0.0 WINEARCH=win64 WINEPREFIX="$WINE_DIR" WINEDEBUG=-all \
		/opt/wine-stable/bin/wine msiexec /i "$mono_msi" /qn
	rm -f "$mono_msi"
}
