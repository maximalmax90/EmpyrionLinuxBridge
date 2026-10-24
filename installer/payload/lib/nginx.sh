#!/bin/bash

nginx_deploy() {
	local certdir=/etc/nginx/elb
	mkdir -p "$certdir"
	if [ ! -f "$certdir/elb-ui.crt" ]; then
		openssl req -x509 -nodes -newkey rsa:2048 -days 3650 \
			-keyout "$certdir/elb-ui.key" -out "$certdir/elb-ui.crt" \
			-subj "/CN=elb-ui" >/dev/null 2>&1
		chmod 600 "$certdir/elb-ui.key"
	fi

	render_template "$ELB_ASSETS_DIR/nginx/elb-ui.conf.tmpl" /etc/nginx/sites-available/elb-ui.conf \
		"UI_PORT=$UI_PORT" "CERT=$certdir/elb-ui.crt" "KEY=$certdir/elb-ui.key"
	ln -sf /etc/nginx/sites-available/elb-ui.conf /etc/nginx/sites-enabled/elb-ui.conf
	[ -e /etc/nginx/sites-enabled/default ] && rm -f /etc/nginx/sites-enabled/default

	nginx -t
	systemctl enable --now nginx
	systemctl reload nginx
}
