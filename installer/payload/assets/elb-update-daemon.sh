#!/bin/bash
set -euo pipefail

C_RED=$'\033[31m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_RESET=$'\033[0m'
log_info() { echo "${C_GREEN}[*]${C_RESET} $*"; }
log_warn() { echo "${C_YELLOW}[!]${C_RESET} $*" >&2; }
die()      { echo "${C_RED}[x]${C_RESET} $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "Запустите от root (sudo $0)."

BIN_NAME="elb-daemon"
SERVICE_NAME="elb-daemon.service"
INSTALL_PATH="/usr/local/bin/$BIN_NAME"
[ -f "$INSTALL_PATH" ] || die "$INSTALL_PATH не найден — демон не установлен на этой машине."

GITHUB_REPO="${ELB_INSTALLER_REPO:-maximalmax90/EmpyrionLinuxBridge}"

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

url="https://github.com/$GITHUB_REPO/releases/latest/download/elb-installer.tar.gz"
archive="$WORKDIR/elb-installer.tar.gz"
log_info "Скачиваю: $url"
if command -v curl >/dev/null 2>&1; then
	curl -fL --retry 3 -o "$archive" "$url"
elif command -v wget >/dev/null 2>&1; then
	wget -qO "$archive" "$url"
else
	die "Нужен curl или wget."
fi

extract="$WORKDIR/extract"
mkdir -p "$extract"
tar -xzf "$archive" -C "$extract"
new_bin="$extract/bin/$BIN_NAME"
[ -f "$new_bin" ] || die "$BIN_NAME не найден в скачанном архиве — собран ли он в этот релиз?"
chmod 755 "$new_bin"

current_version="$("$INSTALL_PATH" --version 2>/dev/null || echo "?")"
new_version="$("$new_bin" --version 2>/dev/null || echo "?")"
echo "Текущая версия: $current_version"
echo "Новая версия:   $new_version"

reply=""
if [ "$current_version" = "$new_version" ] && [ "$current_version" != "?" ]; then
	log_info "Версия не изменилась — обычно обновлять нечего."
	read -r -p "Всё равно заменить бинарник и перезапустить $SERVICE_NAME? [y/N]: " reply </dev/tty
else
	read -r -p "Обновить $BIN_NAME и перезапустить $SERVICE_NAME? [y/N]: " reply </dev/tty
fi
[[ "$reply" =~ ^[Yy]([Ee][Ss])?$ ]] || { echo "Отменено."; exit 0; }

backup="$INSTALL_PATH.bak"
cp "$INSTALL_PATH" "$backup"
log_info "Предыдущий бинарник сохранён: $backup"

install -m 755 "$new_bin" "$INSTALL_PATH"
systemctl restart "$SERVICE_NAME"
sleep 2
if systemctl is-active --quiet "$SERVICE_NAME"; then
	log_info "$SERVICE_NAME обновлён и перезапущен успешно ($current_version → $new_version)."
else
	log_warn "$SERVICE_NAME не поднялся после обновления!"
	log_warn "Предыдущий бинарник сохранён в $backup — вернуть вручную:"
	log_warn "  install -m 755 $backup $INSTALL_PATH && systemctl restart $SERVICE_NAME"
	die "Проверьте: journalctl -u $SERVICE_NAME -n 50"
fi
