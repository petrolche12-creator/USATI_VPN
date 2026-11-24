#!/bin/bash
# Установка сервера «Усатый VPN» v2 на Debian или Ubuntu.
# Поднимает пять транспортов: MTP/1 (UDP), MTP/2 (TCP), MTP/3 (маскировка по TCP),
# WireGuard (UDP) и Shadowsocks (TCP+UDP, свой порт на каждый ключ).
# Запускать от root. Файл mtp.py должен лежать рядом с install.sh.
#
#   bash install.sh HOST [UDP=443] [TCP=8443] [MASK=443] [WG=51820] [SS_BASE=20001]
#
# Повторный запуск безопасен: ключи и пароли сохраняются, прежние порты остаются, если свободны.
# Если порт MTP/2, MTP/3, WireGuard или Shadowsocks занят другой программой, берётся свободный,
# и в журнале написано, какая программа его занимает. Занятый UDP-порт VPN сам не меняется:
# установка остановится с понятной причиной, а прежняя служба продолжит работать.
set -euo pipefail

HOST="${1:-}"
PORT_ARG="${2:-}"
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PY=/usr/bin/python3
PICKED=""
MOVED=""

# Понятная причина остановки: программа показывает её пользователю (это stderr).
fail() {
  echo "Установка остановлена: $*" >&2
  exit 1
}

if [ -z "$HOST" ]; then
  echo "Укажи публичный IP или домен сервера"
  exit 1
fi
if [ "$(id -u)" -ne 0 ]; then
  echo "Запусти от root"
  exit 1
fi
if ! command -v apt-get >/dev/null 2>&1; then
  echo "Поддерживаются только Debian и Ubuntu"
  exit 1
fi
if [ ! -f "$SRC_DIR/mtp.py" ]; then
  echo "Не найден файл $SRC_DIR/mtp.py"
  exit 1
fi

export DEBIAN_FRONTEND=noninteractive
# На свежем сервере apt часто занят автообновлением (unattended-upgrades). Блокировку dpkg ждём
# сами (DPkg::Lock::Timeout), а сбойные шаги повторяем, а не падаем с первого раза.
APT="apt-get -o DPkg::Lock::Timeout=900"
apt_quiet() { $APT "$@" >/dev/null; }
# retry ПОПЫТОК ПАУЗА_С КОМАНДА...: повторяет команду, пока не получится или не кончатся попытки.
retry() {
  local max="$1" pause="$2" n=1
  shift 2
  until "$@"; do
    if [ "$n" -ge "$max" ]; then
      return 1
    fi
    echo "    не получилось, повтор через ${pause} с (попытка $((n + 1)) из ${max})"
    n=$((n + 1))
    sleep "$pause"
  done
}

echo "==> Ставлю пакеты: python3, cryptography, iptables, wireguard-tools, shadowsocks-libev"
retry 45 20 $APT update -qq
retry 3 20 apt_quiet install -y -qq python3 python3-cryptography iptables iproute2 ca-certificates \
  wireguard-tools shadowsocks-libev
if ! "$PY" -c "import cryptography" >/dev/null 2>&1; then
  fail "модуль cryptography не найден для $PY. Выполните: apt-get install python3-cryptography"
fi

# Если установка не дойдёт до конца, а прежняя служба работала, поднимаем её обратно.
PREV_ACTIVE=""
if systemctl is-active --quiet mtp.service 2>/dev/null; then
  PREV_ACTIVE=1
fi
INSTALLED=""
rollback() {
  if [ -n "$PREV_ACTIVE" ] && [ -z "$INSTALLED" ]; then
    systemctl start mtp.service >/dev/null 2>&1 || true
  fi
}
trap rollback EXIT

# ---- порты ----------------------------------------------------------------------------------
# Значение из config.json прошлой установки (если есть), иначе значение по умолчанию.
prev_value() {  # prev_value КЛЮЧ
  [ -f /opt/mtp/config.json ] || return 0
  "$PY" -c 'import json,sys; v = json.load(open("/opt/mtp/config.json")).get(sys.argv[1]); print("" if v is None else v)' "$1" 2>/dev/null || true
}
want_port() {  # want_port КЛЮЧ ПО_УМОЛЧАНИЮ
  local v
  v="$(prev_value "$1")"
  echo "${v:-$2}"
}

# Занят ли порт: кто-то слушает его по TCP или UDP.
port_busy() {  # port_busy tcp|udp ПОРТ
  local out
  if [ "$1" = tcp ]; then
    out="$(ss -H -ltn "sport = :$2" 2>/dev/null || true)"
  else
    out="$(ss -H -lun "sport = :$2" 2>/dev/null || true)"
  fi
  [ -n "$out" ]
}

# Название программы, которая занимает порт (для журнала).
port_owner() {  # port_owner tcp|udp ПОРТ
  local out
  if [ "$1" = tcp ]; then
    out="$(ss -H -ltnp "sport = :$2" 2>/dev/null || true)"
  else
    out="$(ss -H -lunp "sport = :$2" 2>/dev/null || true)"
  fi
  printf '%s\n' "$out" | sed -n 's/.*users:(("\([^"]*\)".*/\1/p' | head -n 1
}

# Занят ли хотя бы один порт диапазона ОТ..ОТ+199 (TCP или UDP).
range_busy() {  # range_busy ОТ
  local from="$1" to
  to=$((from + 199))
  ss -H -lntu 2>/dev/null | awk -v a="$from" -v b="$to" '
    { n = split($5, parts, ":"); p = parts[n] + 0; if (p >= a && p <= b) found = 1 }
    END { exit (found ? 0 : 1) }'
}

# Берёт желаемый порт, а если он занят, первый свободный из запасных. Результат в PICKED.
pick_port() {  # pick_port tcp|udp ЖЕЛАЕМЫЙ [ЗАПАСНЫЕ...]
  local proto="$1" want="$2" p owner
  shift 2
  for p in "$want" "$@"; do
    if ! port_busy "$proto" "$p"; then
      PICKED="$p"
      if [ "$p" != "$want" ]; then
        MOVED=1
        echo "    ${proto^^} $want занят, буду использовать ${proto^^} $p"
      fi
      return 0
    fi
    owner="$(port_owner "$proto" "$p")"
    echo "    ${proto^^} $p занят программой «${owner:-неизвестно}»"
  done
  return 1
}

# То же для диапазона Shadowsocks: первый свободный блок из 200 портов. Результат в PICKED.
pick_range() {  # pick_range ЖЕЛАЕМЫЙ [ЗАПАСНЫЕ...]
  local want="$1" b
  shift
  for b in "$want" "$@"; do
    if ! range_busy "$b"; then
      PICKED="$b"
      if [ "$b" != "$want" ]; then
        MOVED=1
        echo "    диапазон Shadowsocks $want-$((want + 199)) занят, буду использовать $b-$((b + 199))"
      fi
      return 0
    fi
    echo "    диапазон Shadowsocks $b-$((b + 199)) занят"
  done
  return 1
}

echo "==> Проверяю порты"
# Служба прошлой установки держит свои порты: останавливаем её, чтобы они не считались занятыми.
systemctl stop mtp.service >/dev/null 2>&1 || true
# WireGuard-интерфейс от прошлого запуска держит UDP-порт и без службы: убираем, сервер создаст его заново.
ip link del uwg0 >/dev/null 2>&1 || true

# UDP-порт VPN задаёт программа (поле «порт VPN»). Если его не передали, берём прежний или 443.
PORT="${PORT_ARG:-$(want_port port 443)}"
TCP_PORT="${3:-$(want_port tcp_port 8443)}"
MASK_PORT="${4:-$(want_port mask_port 443)}"
WG_PORT="${5:-$(want_port wg_port 51820)}"
SS_BASE="${6:-$(want_port ss_base 20001)}"
for p in "$PORT" "$TCP_PORT" "$MASK_PORT" "$WG_PORT" "$SS_BASE"; do
  if ! [[ "$p" =~ ^[0-9]+$ ]] || [ "$p" -lt 1 ] || [ "$p" -gt 65000 ]; then
    fail "неверный порт: $p"
  fi
done

if port_busy udp "$PORT"; then
  fail "UDP-порт $PORT для VPN уже занят программой «$(port_owner udp "$PORT")». Выберите в программе другой порт VPN и установите снова."
fi
pick_port tcp "$TCP_PORT" 8444 8448 || fail "нет свободного TCP-порта для MTP/2"
TCP_PORT="$PICKED"
pick_port tcp "$MASK_PORT" 2053 2083 2087 2096 9443 10443 || fail "нет свободного TCP-порта для MTP/3"
MASK_PORT="$PICKED"
pick_port udp "$WG_PORT" 51821 51822 || fail "нет свободного UDP-порта для WireGuard"
WG_PORT="$PICKED"
pick_range "$SS_BASE" 21001 22001 30001 || fail "нет свободного диапазона портов для Shadowsocks"
SS_BASE="$PICKED"
echo "    порты: VPN UDP $PORT, MTP/2 TCP $TCP_PORT, MTP/3 TCP $MASK_PORT, WireGuard UDP $WG_PORT, Shadowsocks $SS_BASE-$((SS_BASE + 199))"

echo "==> Отключаю стандартную службу shadowsocks-libev (серверы поднимает mtp.py)"
systemctl disable --now shadowsocks-libev.service >/dev/null 2>&1 || true

echo "==> Копирую сервер в /opt/mtp"
install -d -m 755 /opt/mtp
install -m 755 "$SRC_DIR/mtp.py" /opt/mtp/mtp.py
"$PY" /opt/mtp/mtp.py init --host "$HOST" --port "$PORT" --tcp-port "$TCP_PORT" \
  --mask-port "$MASK_PORT" --wg-port "$WG_PORT" --ss-base "$SS_BASE"

echo "==> Включаю пересылку пакетов"
echo "net.ipv4.ip_forward = 1" > /etc/sysctl.d/99-usatyi-vpn.conf
sysctl -q -w net.ipv4.ip_forward=1 || true

echo "==> Настраиваю службу systemd: mtp"
cat > /etc/systemd/system/mtp.service <<EOF
[Unit]
Description=Usatyi VPN (MTP/1, MTP/2, MTP/3, WireGuard, Shadowsocks)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$PY /opt/mtp/mtp.py serve
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
  echo "==> Открываю порты в ufw"
  ufw allow "$PORT/udp" >/dev/null
  ufw allow "$TCP_PORT/tcp" >/dev/null
  ufw allow "$MASK_PORT/tcp" >/dev/null
  ufw allow "$WG_PORT/udp" >/dev/null
  ufw allow "$SS_BASE:$((SS_BASE + 199))/tcp" >/dev/null
  ufw allow "$SS_BASE:$((SS_BASE + 199))/udp" >/dev/null
fi

systemctl daemon-reload
systemctl enable mtp.service >/dev/null 2>&1
systemctl restart mtp.service
sleep 3

if systemctl is-active --quiet mtp.service; then
  INSTALLED=1
  echo "==> Готово: сервер работает"
  if [ -n "$MOVED" ]; then
    echo "    Часть портов была занята другими программами, поэтому выбраны запасные."
    echo "    Ключи, выданные до этой установки, для перенесённых портов могут не работать:"
    echo "    возьмите новые строки ключей в программе (вкладка «Создать», список ключей)."
  fi
  echo "    Откройте в фаерволе хостинга (панель провайдера):"
  echo "      UDP $PORT (MTP/1), TCP $TCP_PORT (MTP/2), TCP $MASK_PORT (MTP/3),"
  echo "      UDP $WG_PORT (WireGuard), TCP и UDP $SS_BASE-$((SS_BASE + 199)) (Shadowsocks)"
else
  echo "==> Служба не запустилась. Последние строки журнала:"
  journalctl -u mtp.service -n 40 --no-pager || true
  fail "служба mtp не запустилась, причина в журнале выше"
fi
