#!/bin/bash
# Живая проверка второй ступени цепочки (SSH → MTP/2) без внешнего сервера.
#   сервер (netns srv): mtp.py, тестовый SSH-шлюз, заглушки DNS 172.28.0.53 и HTTP 172.28.0.1;
#   клиент (netns cli): Linux-сборка тестов, полный путь программы.
# Запуск от root:  sudo tests/live_netns.sh
set -euo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin:$PATH
cd "$(dirname "$0")/.."

SRC=usatish
BIN=/tmp/usatyi-live.test
SRV_IP=10.200.0.1
CLI_IP=10.200.0.2
GW_IP=172.28.0.1
DNS_IP=172.28.0.53
MTP_PORT=18443
SSH_PORT=2222
SSH_USER=usatyi
SSH_PASS=live-test-pass
DIR=/tmp/usatyi-live

cleanup() {
  set +e
  pkill -f "$DIR/mtp/mtp.py serve" 2>/dev/null
  ip netns del srv 2>/dev/null
  ip netns del cli 2>/dev/null
  rm -rf "$DIR"
}
trap cleanup EXIT

echo "== собираю тестовый бинарник"
( cd $SRC && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o $BIN . )

echo "== сетевые пространства"
cleanup
ip netns add srv
ip netns add cli
ip link add vsrv type veth peer name vcli
ip link set vsrv netns srv
ip link set vcli netns cli
ip -n srv addr add $SRV_IP/24 dev vsrv
ip -n cli addr add $CLI_IP/24 dev vcli
ip -n srv link set lo up
ip -n cli link set lo up
ip -n srv link set vsrv up
ip -n cli link set vcli up
ip netns exec srv /usr/sbin/sysctl -qw net.ipv4.ip_forward=1

echo "== шлюз $GW_IP и DNS $DNS_IP на петле сервера (как install.sh)"
ip netns exec srv ip addr add $GW_IP/32 dev lo
ip netns exec srv ip addr add $DNS_IP/32 dev lo

echo "== mtp.py на сервере"
mkdir -p $DIR/mtp
cp $SRC/assets/mtp.py $DIR/mtp/
MTP_DIR=$DIR/mtp python3 $DIR/mtp/mtp.py init --host $SRV_IP --port 28543 \
  --tcp-port $MTP_PORT --mask-port 28443 --wg-port 28544 --ss-base 28700 >/dev/null
MTP_DIR=$DIR/mtp python3 $DIR/mtp/mtp.py config-set --key ssh_gateway_port --value $MTP_PORT >/dev/null
KEY="$(MTP_DIR=$DIR/mtp python3 $DIR/mtp/mtp.py add --name=live --days=1 --limit-gb=0 --json \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["key"])')"
# SSH-шлюз и заглушки хоста — тот же тестовый бинарник
ip netns exec srv env USATYI_LIVE_GATEWAY_ADDR=$SRV_IP:$SSH_PORT \
  USATYI_LIVE_SSH_USER=$SSH_USER USATYI_LIVE_SSH_PASS=$SSH_PASS \
  $BIN -test.run '^TestLiveSSHGateway$' -test.v &> $DIR/ssh.log &
ip netns exec srv env USATYI_LIVE_STUBS=1 $BIN -test.run '^TestLiveServerStubs$' -test.v \
  &> $DIR/stubs.log &

# mtp.py поднимает маршрут 172.28.0.0/16 в mtp0 сам (см. setup_network)
ip netns exec srv env MTP_DIR=$DIR/mtp PYTHONPATH=${PYTHONPATH:-} \
  nohup python3 $DIR/mtp/mtp.py serve &> $DIR/mtp.log &

sleep 2

echo "== проверка: SSH по сети из клиентского netns, MTP/2 через канал"
set +e
ip netns exec cli env USATYI_LIVE_KEY="$KEY" \
  USATYI_LIVE_SSH_HOST=$SRV_IP USATYI_LIVE_SSH_PORT=$SSH_PORT \
  USATYI_LIVE_SSH_USER=$SSH_USER USATYI_LIVE_SSH_PASS=$SSH_PASS \
  USATYI_LIVE_MTP_PORT=$MTP_PORT \
  $BIN -test.run '^TestLiveMTPOverSSH$' -test.v
rc=$?
set -e
echo "== журнал SSH-шлюза:"; tail -2 $DIR/ssh.log 2>/dev/null
echo "== журнал заглушек:"; tail -2 $DIR/stubs.log 2>/dev/null
if [ $rc -ne 0 ]; then
  echo "== журнал MTP-сервера:"; tail -8 $DIR/mtp.log 2>/dev/null
fi
exit $rc
