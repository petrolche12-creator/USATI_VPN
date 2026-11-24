#!/bin/bash
# Удаление сервера «Усатый VPN» (MTP) с Debian или Ubuntu. Запускать от root.
# Останавливает службу, убирает правила файрвола, адреса и маршруты шлюза, каталог /opt/mtp.
set -euo pipefail

[ "$(id -u)" -eq 0 ] || { echo "Запусти от root"; exit 1; }

echo "==> Останавливаю службу mtp"
systemctl disable --now mtp.service >/dev/null 2>&1 || true

echo "==> Убираю правила iptables"
for chain_spec in \
  "nat POSTROUTING -s 10.8.0.0/24 -o $(ip -4 route show default | sed -n 's/.*dev \([^ ]*\).*/\1/p' | head -n1) -j MASQUERADE" \
  "nat POSTROUTING -s 172.28.0.0/16 -d 127.0.0.0/8 -j MASQUERADE"; do
  set -- $chain_spec
  table="$1"; chain="$2"; shift 2
  iptables -t "$table" -D "$chain" "$@" >/dev/null 2>&1 || true
done
iptables -D FORWARD -i mtp0 -j ACCEPT >/dev/null 2>&1 || true
iptables -D FORWARD -o mtp0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT >/dev/null 2>&1 || true
iptables -D FORWARD -i mtp0 -d 127.0.0.0/8 -j ACCEPT >/dev/null 2>&1 || true
iptables -D FORWARD -o mtp0 -j ACCEPT >/dev/null 2>&1 || true

echo "==> Убираю интерфейсы и адреса шлюза"
ip link del mtp0 >/dev/null 2>&1 || true
ip link del uwg0 >/dev/null 2>&1 || true
ip addr del 172.28.0.1/32 dev lo >/dev/null 2>&1 || true
ip addr del 172.28.0.53/32 dev lo >/dev/null 2>&1 || true
ip route del 172.28.0.53 dev lo >/dev/null 2>&1 || true

echo "==> Удаляю файлы"
rm -rf /opt/mtp
rm -f /etc/systemd/system/mtp.service /etc/sysctl.d/99-usatyi-vpn.conf /etc/dnsmasq.d/usatyi-tunnel.conf
systemctl daemon-reload

echo "==> Готово: сервер MTP удалён. AmneziaWG (если стоит) не тронут."
