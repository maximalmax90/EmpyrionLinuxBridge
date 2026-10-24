#!/bin/bash

updatescripts_deploy_daemon() {
	install -m 755 "$ELB_ASSETS_DIR/elb-update-daemon.sh" /usr/local/bin/elb-update-daemon
}

updatescripts_deploy_ui() {
	install -m 755 "$ELB_ASSETS_DIR/elb-update-ui.sh" /usr/local/bin/elb-update-ui
}
