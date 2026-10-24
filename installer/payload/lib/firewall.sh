#!/bin/bash

firewall_required_ports() {
	local ports=()
	if $WITH_DAEMON; then
		local i gp
		for i in 0 1 2 3; do
			gp=$((GAME_SRV_PORT + i))
			[ "$gp" = "$GAME_TEL_PORT" ] && continue
			ports+=("$gp/tcp" "$gp/udp")
		done
		[ "$INSTALL_MODE" != "all" ] && ports+=("$DAEMON_HTTP_PORT/tcp")
	fi
	if $WITH_UI && ! $NGINX_ENABLE; then
		ports+=("$UI_PORT/tcp")
	fi
	$NGINX_ENABLE && ports+=("80/tcp" "443/tcp")
	printf '%s\n' "${ports[@]}"
}

firewall_setup() {
	local ports=()
	local p
	while IFS= read -r p; do
		[ -n "$p" ] && ports+=("$p")
	done < <(firewall_required_ports)

	if [ ${#ports[@]} -eq 0 ]; then
		log_info "No ports need to be opened for the selected install options — skipping firewall setup."
		return
	fi

	if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
		log_info "An active ufw was detected — adding rules to it (not touching your existing ones)."
		for p in "${ports[@]}"; do
			ufw allow "${p%/*}/${p#*/}" >/dev/null
		done
		return
	fi

	if command -v firewall-cmd >/dev/null 2>&1 && systemctl is-active --quiet firewalld; then
		log_info "An active firewalld was detected — adding rules to it (not touching your existing ones)."
		for p in "${ports[@]}"; do
			firewall-cmd --permanent --add-port="${p%/*}/${p#*/}" >/dev/null
		done
		firewall-cmd --reload >/dev/null
		return
	fi

	if command -v nft >/dev/null 2>&1 && [ -n "$(nft list ruleset 2>/dev/null)" ]; then
		log_warn "An existing nftables ruleset was detected — its table/chain structure varies too much to safely modify automatically. Open these ports manually: ${ports[*]}"
		return
	fi

	if command -v iptables >/dev/null 2>&1 && iptables -S 2>/dev/null | grep -qv '^-P'; then
		log_info "Existing custom iptables rules were detected — inserting ACCEPT rules for the required ports, without touching your existing rules/policy."
		for p in "${ports[@]}"; do
			iptables -I INPUT -p "${p#*/}" --dport "${p%/*}" -j ACCEPT
		done
		if command -v netfilter-persistent >/dev/null 2>&1; then
			netfilter-persistent save
		else
			log_warn "netfilter-persistent is not installed — the new rules may not survive a reboot unless you persist them yourself."
		fi
		return
	fi

	log_info "No active firewall found — installing iptables-persistent."
	DEBIAN_FRONTEND=noninteractive apt-get install -y iptables iptables-persistent

	iptables -F INPUT
	iptables -A INPUT -i lo -j ACCEPT
	iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
	iptables -A INPUT -p tcp --dport 22 -j ACCEPT
	for p in "${ports[@]}"; do
		iptables -A INPUT -p "${p#*/}" --dport "${p%/*}" -j ACCEPT
	done
	iptables -P INPUT DROP
	iptables -P FORWARD DROP

	install -d /etc/iptables
	iptables-save >/etc/iptables/rules.v4
	netfilter-persistent save
}
