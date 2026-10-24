#!/bin/bash

users_setup() {
	$WITH_DAEMON || return 0
	if id empyrion >/dev/null 2>&1; then
		log_info "User empyrion already exists, skipping."
		return
	fi
	useradd --system --create-home --home-dir /home/empyrion --shell /usr/sbin/nologin empyrion
	log_info "Created user empyrion."
}
