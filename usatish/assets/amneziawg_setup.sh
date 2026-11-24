#!/bin/bash
# AmneziaWG-сервер на Debian/Ubuntu без Docker: userspace amneziawg-go и amneziawg-tools.
# Запускать от root (программа делает это через sudo по SSH).
# Результат: интерфейс awg0 с обфускацией AmneziaWG и конфиг клиента в /etc/amnezia/amneziawg/awg0.conf.
set -euo pipefail

HOST="${1:-}"
[ -n "$HOST" ] || { echo "Укажи публичный IP или домен сервера" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || { echo "Запусти от root" >&2; exit 1; }
command -v apt-get >/dev/null 2>&1 || { echo "Поддерживаются только Debian и Ubuntu" >&2; exit 1; }

DIR=/etc/amnezia/amneziawg
PORT=51820
SUBNET=10.66.66
AWG_GO_VERSION=v0.2.13
AWG_TOOLS_VERSION=v1.0.20251013

export DEBIAN_FRONTEND=noninteractive
APT="apt-get -o DPkg::Lock::Timeout=900"

echo "==> Ставлю пакеты: curl, tar, iptables"
$APT update -qq
$APT install -y -qq curl tar iptables iproute2 ca-certificates >/dev/null

if ! command -v amneziawg-go >/dev/null 2>&1; then
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) echo "Архитектура $(uname -m) не поддерживается" >&2; exit 1 ;;
  esac
  echo "==> Скачиваю amneziawg-go $AWG_GO_VERSION ($arch)"
  tmp="$(mktemp -d)"
  url="https://github.com/amnezia-vpn/amneziawg-go/releases/download/${AWG_GO_VERSION}/amneziawg-go-linux-${arch}"
  if curl -fsSL -o "$tmp/amneziawg-go" "$url"; then
    install -m 755 "$tmp/amneziawg-go" /usr/local/bin/amneziawg-go
  else
    echo "    готового файла нет — собираю из исходников (нужен интернет в GitHub)"
    $APT install -y -qq golang-go >/dev/null
    curl -fsSL -o "$tmp/awg.tar.gz" "https://codeload.github.com/amnezia-vpn/amneziawg-go/tar.gz/refs/tags/${AWG_GO_VERSION}"
    tar xzf "$tmp/awg.tar.gz" -C "$tmp"
    ( cd "$tmp"/amneziawg-go-* && CGO_ENABLED=0 go build -trimpath -o /usr/local/bin/amneziawg-go . )
  fi
  rm -rf "$tmp"
fi

if ! command -v awg >/dev/null 2>&1; then
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) arch="" ;;
  esac
  echo "==> Скачиваю amneziawg-tools $AWG_TOOLS_VERSION ($arch)"
  tmp="$(mktemp -d)"
  url="https://github.com/amnezia-vpn/amneziawg-tools/releases/download/${AWG_TOOLS_VERSION}/amneziawg-tools-linux-${arch}.tar.gz"
  if curl -fsSL -o "$tmp/tools.tgz" "$url"; then
    tar xzf "$tmp/tools.tgz" -C "$tmp"
    bin="$(find "$tmp" -type f -name awg -perm -u+x | head -n1)"
    [ -n "$bin" ] && install -m 755 "$bin" /usr/local/bin/awg
    for tool in awg-quick; do
      tbin="$(find "$tmp" -type f -name "$tool" | head -n1)"
      [ -n "$tbin" ] && install -m 755 "$tbin" "/usr/local/bin/$tool"
    done
  else
    echo "    готовых tools нет — собираю из исходников"
    $APT install -y -qq golang-go make gcc libc6-dev >/dev/null
    curl -fsSL -o "$tmp/tools.tar.gz" "https://codeload.github.com/amnezia-vpn/amneziawg-tools/tar.gz/refs/tags/${AWG_TOOLS_VERSION}"
    tar xzf "$tmp/tools.tar.gz" -C "$tmp"
    ( cd "$tmp"/amneziawg-tools-*/src && make -j"$(nproc)" >/dev/null && make install PREFIX=/usr/local >/dev/null )
  fi
  rm -rf "$tmp"
fi

echo "==> Создаю ключи и конфиг AmneziaWG"
mkdir -p "$DIR"
chmod 700 "$DIR"
if [ ! -f "$DIR/server.key" ]; then
  awg genkey | tee "$DIR/server.key" | awg pubkey > "$DIR/server.pub"
  awg genkey | tee "$DIR/client.key" | awg pubkey > "$DIR/client.pub"
  chmod 600 "$DIR"/*.key
fi
# Обфускация: фиксированные параметры сервера (клиент обязан совпадать с ними)
if [ ! -f "$DIR/params.env" ]; then
  cat > "$DIR/params.env" <<EOF
JC=$(shuf -i 3-7 -n1)
JMIN=$(shuf -i 10-40 -n1)
JMAX=$(shuf -i 200-600 -n1)
S1=$(shuf -i 20-80 -n1)
S2=$(shuf -i 20-80 -n1)
H1=$(shuf -i 100000-200000 -n1)
H2=$(shuf -i 300000-400000 -n1)
H3=$(shuf -i 500000-600000 -n1)
H4=$(shuf -i 700000-800000 -n1)
EOF
  chmod 600 "$DIR/params.env"
fi
# shellcheck disable=SC1091
. "$DIR/params.env"

cat > "$DIR/awg0.conf" <<EOF
[Interface]
Address = ${SUBNET}.1/24
ListenPort = $PORT
PrivateKey = $(cat "$DIR/server.key")
Jc = $JC
Jmin = $JMIN
Jmax = $JMAX
S1 = $S1
S2 = $S2
H1 = $H1
H2 = $H2
H3 = $H3
H4 = $H4

[Peer]
PublicKey = $(cat "$DIR/client.pub")
AllowedIPs = ${SUBNET}.2/32
EOF
chmod 600 "$DIR/awg0.conf"

echo "==> Поднимаю интерфейс awg0"
ip link del awg0 >/dev/null 2>&1 || true
nohup amneziawg-go awg0 >/var/log/amneziawg-go.log 2>&1 &
for _ in $(seq 1 50); do
  ip link show awg0 >/dev/null 2>&1 && break
  sleep 0.1
done
awg setconf awg0 "$DIR/awg0.conf"
ip addr replace "${SUBNET}.1/24" dev awg0
ip link set awg0 mtu 1420 up

echo "==> Включаю NAT для AmneziaWG"
WAN="$(ip -4 route show default | sed -n 's/.*dev \([^ ]*\).*/\1/p' | head -n1)"
iptables -t nat -C POSTROUTING -s "${SUBNET}.0/24" -o "$WAN" -j MASQUERADE 2>/dev/null \
  || iptables -t nat -A POSTROUTING -s "${SUBNET}.0/24" -o "$WAN" -j MASQUERADE
echo "net.ipv4.ip_forward = 1" > /etc/sysctl.d/99-usatyi-awg.conf
sysctl -q -w net.ipv4.ip_forward=1 || true

echo "==> Служба amneziawg на старте системы"
cat > /etc/systemd/system/usatyi-awg.service <<EOF
[Unit]
Description=AmneziaWG (usatyi vpn)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/amneziawg-go -f awg0
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable usatyi-awg.service >/dev/null 2>&1 || true

echo "==> Конфиг клиента для программы (сохранён и на сервере в $DIR/client.conf)"
cat > "$DIR/client.conf" <<EOF
[Interface]
PrivateKey = $(cat "$DIR/client.key")
Address = ${SUBNET}.2/32
DNS = 1.1.1.1
MTU = 1420
Jc = $JC
Jmin = $JMIN
Jmax = $JMAX
S1 = $S1
S2 = $S2
H1 = $H1
H2 = $H2
H3 = $H3
H4 = $H4

[Peer]
PublicKey = $(cat "$DIR/server.pub")
Endpoint = ${HOST}:${PORT}
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = 25
EOF
chmod 644 "$DIR/client.conf"
echo "==> Готово: AmneziaWG на UDP $PORT"
