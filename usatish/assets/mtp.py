#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Усатый VPN: сервер и админ-утилита в одном файле (версия 2).

  mtp.py init   --host HOST [--port 443] [--tcp-port 8443] [--mask-port 443]
                [--wg-port 51820] [--ss-base 20001] [--dns 1.1.1.1]
  mtp.py add    --name ИМЯ [--days 30] [--limit-gb 10] [--json]
  mtp.py list   [--json]
  mtp.py show   ID_ИЛИ_ИМЯ [--json]
  mtp.py set    ID_ИЛИ_ИМЯ [--days N] [--limit-gb G] [--enable | --disable] [--reset] [--json]
  mtp.py delete ID_ИЛИ_ИМЯ [--json]
  mtp.py serve                 запуск сервера (нужен root, его запускает systemd)

Пять транспортов. Один ключ содержит параметры всех:
  1. MTP/1  свой протокол поверх UDP            (порт --port)
  2. MTP/2  тот же протокол поверх TCP          (порт --tcp-port), кадры с длиной
  3. MTP/3  свой протокол с маскировкой по TCP  (порт --mask-port), кадры в записях,
            похожих на записи TLS, со случайным паддингом
  4. WireGuard (стандартный), интерфейс uwg0    (порт --wg-port, UDP)
  5. Shadowsocks (стандартный, ss-server), свой порт на каждый ключ (от --ss-base)

Срок и лимит трафика действуют на ключ целиком: счётчики всех транспортов суммируются.
Зависимости: python3, python3-cryptography, iptables, wireguard-tools, shadowsocks-libev.

Криптография MTP (только стандартные примитивы): ECDH P-256 с эфемерными ключами,
HKDF-SHA256, HMAC-SHA256 (аутентификация по секрету ключа), ChaCha20-Poly1305 (данные).
"""

import argparse
import asyncio
import base64
import contextlib
import fcntl
import glob
import hashlib
import hmac
import json
import os
import re
import secrets
import shutil
import signal
import socket
import struct
import subprocess
import sys
import threading
import time

from cryptography.exceptions import InvalidTag
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec, x25519
from cryptography.hazmat.primitives.ciphers.aead import ChaCha20Poly1305
from cryptography.hazmat.primitives.kdf.hkdf import HKDF

BASE = os.environ.get("MTP_DIR", "/opt/mtp")
CONFIG_PATH = os.path.join(BASE, "config.json")
USERS_PATH = os.path.join(BASE, "users.json")
USAGE_PATH = os.path.join(BASE, "usage.json")
STATE_PATH = os.path.join(BASE, "state.json")
LOCK_PATH = os.path.join(BASE, ".lock")
RUN_DIR = os.path.join(BASE, "run")

# Шлюз для сессий через SSH-туннель программы: DNS 172.28.0.53 и шлюз 172.28.0.1 —
# настоящие адреса на петле хоста (их ставит install.sh).
TUNNEL_DNS = socket.inet_aton("172.28.0.53")
# Адрес шлюза для «всего трафика»: настоящий адрес на петле хоста (ставит install.sh)
TUNNEL_GW = "172.28.0.1"
TUNNEL_NAT_NET = "172.28.0.0/16"

TUN_NAME = "mtp0"
SERVER_IP = "10.8.0.1"
SUBNET = "10.8.0.0/24"
MTU = 1380
WG_IF = "uwg0"
WG_SERVER_IP = "10.9.0.1"
WG_SUBNET = "10.9.0.0/24"
WG_MTU = 1380
SS_METHOD = "chacha20-ietf-poly1305"
SS_CHAIN = "USATYI_SS"
GB = 1024 ** 3
CURVE = ec.SECP256R1()

DEFAULT_CFG = {"tcp_port": 8443, "mask_port": 443, "wg_port": 51820, "ss_base": 20001, "dns": "1.1.1.1"}

T_HELLO, T_WELCOME, T_REJECT, T_DATA = 0x01, 0x02, 0x03, 0x04
K_IP, K_KEEPALIVE, K_STATUS, K_KICK = 0x00, 0x01, 0x02, 0x03
R_DISABLED, R_EXPIRED, R_QUOTA = 1, 2, 3
REASON_TEXT = {
    R_DISABLED: "ключ отключён",
    R_EXPIRED: "срок действия ключа истёк",
    R_QUOTA: "лимит трафика исчерпан",
}

HELLO_LEN = 98       # тип 1 + эфемерный ключ 65 + uid 8 + время 8 + тег 16
WELCOME_LEN = 96     # тип 1 + sid 4 + ip 4 + dns 4 + mtu 2 + эфемерный ключ 65 + тег 16
REJECT_LEN = 18      # тип 1 + причина 1 + тег 16
MIN_DATA_LEN = 13 + 16 + 1
MAX_FRAME = 2048
HELLO_SKEW = 120     # допустимое расхождение времени HELLO, сек
SESSION_IDLE = 120   # сессия без пакетов дольше этого срока закрывается
STATUS_EVERY = 30    # как часто сервер шлёт клиенту статус (трафик, срок)
READ_TIMEOUT = 150   # соединение TCP без кадров дольше этого срока закрывается
WG_EVERY = 2         # как часто синхронизировать пиров WireGuard, сек
SS_EVERY = 2         # как часто синхронизировать процессы Shadowsocks, сек
STREAM_BUFFER_LIMIT = 256 * 1024


# ---------------------------------------------------------------- утилиты

def now_ts():
    return int(time.time())


def b64u(data):
    return base64.urlsafe_b64encode(data).decode("ascii").rstrip("=")


def b64u_dec(text):
    return base64.urlsafe_b64decode(text + "=" * (-len(text) % 4))


def die(msg, code=1):
    print(f"ошибка: {msg}", file=sys.stderr)
    sys.exit(code)


def log(msg):
    print(time.strftime("%Y-%m-%d %H:%M:%S"), msg, flush=True)


def read_json(path, default):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except (FileNotFoundError, ValueError):
        return default


def write_json_atomic(path, data, mode=0o600):
    tmp = path + ".tmp"
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, mode)
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=1)
    os.replace(tmp, path)


def write_secret(path, text):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        f.write(text)


def run_quiet(*cmd):
    try:
        return subprocess.run(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                              timeout=30).returncode == 0
    except (OSError, subprocess.SubprocessError):
        return False


def run_out(*cmd):
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        return p.returncode, p.stdout
    except (OSError, subprocess.SubprocessError):
        return 1, ""


def default_iface():
    out = subprocess.run(["ip", "-4", "route", "show", "default"], capture_output=True, text=True).stdout.split()
    if "dev" not in out:
        # Без маршрута по умолчанию (тестовое пространство) NAT только внутри туннеля:
        # выхода наружу всё равно нет, а сам сервер должен запуститься.
        log("внимание: нет маршрута по умолчанию, NAT наружу не настраивается")
        return ""
    return out[out.index("dev") + 1]


def ensure_rules(rules):
    for check, add in rules:
        if not run_quiet("iptables", *check) and not run_quiet("iptables", *add):
            log(f"внимание: не удалось добавить правило iptables: {' '.join(add)}")


def nat_rules(wan, subnet, iface, mss_clamp=False):
    rules = [
        (["-t", "nat", "-C", "POSTROUTING", "-s", subnet, "-o", wan, "-j", "MASQUERADE"],
         ["-t", "nat", "-A", "POSTROUTING", "-s", subnet, "-o", wan, "-j", "MASQUERADE"]),
        (["-C", "FORWARD", "-i", iface, "-j", "ACCEPT"],
         ["-I", "FORWARD", "1", "-i", iface, "-j", "ACCEPT"]),
        (["-C", "FORWARD", "-o", iface, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"],
         ["-I", "FORWARD", "1", "-o", iface, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"]),
    ]
    if mss_clamp:
        rules.append((
            ["-t", "mangle", "-C", "FORWARD", "-o", iface, "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN",
             "-j", "TCPMSS", "--clamp-mss-to-pmtu"],
            ["-t", "mangle", "-I", "FORWARD", "1", "-o", iface, "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN",
             "-j", "TCPMSS", "--clamp-mss-to-pmtu"],
        ))
    return rules


def wg_keypair():
    priv = x25519.X25519PrivateKey.generate()
    priv_raw = priv.private_bytes(serialization.Encoding.Raw, serialization.PrivateFormat.Raw,
                                  serialization.NoEncryption())
    pub_raw = priv.public_key().public_bytes(serialization.Encoding.Raw, serialization.PublicFormat.Raw)
    return base64.b64encode(priv_raw).decode("ascii"), base64.b64encode(pub_raw).decode("ascii")


# ---------------------------------------------------------------- протокол MTP

def tag16(key, label, *parts):
    h = hmac.new(key, label, hashlib.sha256)
    for part in parts:
        h.update(part)
    return h.digest()[:16]


def pub_bytes(pub):
    return pub.public_bytes(serialization.Encoding.X962, serialization.PublicFormat.UncompressedPoint)


def derive_keys(shared, secret, cpub, spub):
    okm = HKDF(algorithm=hashes.SHA256(), length=64, salt=cpub + spub, info=b"mtp1 keys").derive(shared + secret)
    return okm[:32], okm[32:]  # ключ клиент->сервер, ключ сервер->клиент


def nonce(ctr):
    return b"\x00" * 4 + struct.pack(">Q", ctr)


class Replay:
    """Окно защиты от повторов: помнит последние 64 номера пакетов."""
    __slots__ = ("top", "bits")

    def __init__(self):
        self.top = -1
        self.bits = 0

    def ok(self, ctr):
        if self.top < 0 or ctr > self.top:
            return True
        d = self.top - ctr
        return d < 64 and not (self.bits >> d) & 1

    def mark(self, ctr):
        if self.top < 0:
            self.top, self.bits = ctr, 1
        elif ctr > self.top:
            d = ctr - self.top
            self.bits = (((self.bits << d) | 1) & 0xFFFFFFFFFFFFFFFF) if d < 64 else 1
            self.top = ctr
        else:
            self.bits |= 1 << (self.top - ctr)


class UdpConn:
    """Адрес клиента для MTP/1 (UDP): без установления соединения."""
    stream = False
    __slots__ = ("tr", "addr")

    @property
    def local(self):
        return False

    def __init__(self, tr, addr):
        self.tr = tr
        self.addr = addr

    def send(self, data, droppable=False):
        self.tr.sendto(data, self.addr)
        return True

    def close(self):
        pass

    def label(self):
        return f"{self.addr[0]}:{self.addr[1]} (MTP/1 UDP)"


class StreamConn:
    """TCP-соединение клиента: MTP/2 (кадры с длиной) или MTP/3 (записи, похожие на TLS)."""
    stream = True

    def __init__(self, writer, mask):
        self.writer = writer
        self.mask = mask
        peer = writer.get_extra_info("peername") or ("?", 0)
        self.peer = (peer[0], peer[1])

    def send(self, data, droppable=False):
        if self.writer.is_closing():
            return False
        if droppable and self.writer.transport.get_write_buffer_size() > STREAM_BUFFER_LIMIT:
            return False  # клиент не успевает читать: пакет туннеля теряем, как UDP
        self.writer.write(encode_frame(data, self.mask))
        return True

    def close(self):
        with contextlib.suppress(Exception):
            self.writer.close()

    def label(self):
        name = "MTP/3 маскировка" if self.mask else "MTP/2 TCP"
        return f"{self.peer[0]}:{self.peer[1]} ({name})"

    @property
    def local(self):
        # Клиент пришёл с этого же сервера — значит, это SSH-туннель программы Usatyi VPN,
        # а не удалённое устройство.
        return self.peer[0] in ("127.0.0.1", "::1") or self.peer[0].startswith("172.28.")


def encode_frame(data, mask):
    """Кадр для TCP: MTP/2 = [длина 2][кадр]; MTP/3 = запись [0x16|0x17 0x03 0x03][длина 2][паддинг-длина 1][кадр][случайный паддинг]."""
    if not mask:
        return struct.pack(">H", len(data)) + data
    rtype = 0x17 if data[0] == T_DATA else 0x16
    pad = secrets.randbelow(48)
    body = bytes([pad]) + data + os.urandom(pad)
    return bytes([rtype, 0x03, 0x03]) + struct.pack(">H", len(body)) + body


async def read_frame(reader, mask):
    """Читает один кадр из TCP-потока. Бросает ValueError при неверном формате."""
    if not mask:
        n = struct.unpack(">H", await reader.readexactly(2))[0]
        if n == 0 or n > MAX_FRAME:
            raise ValueError("длина кадра")
        return await reader.readexactly(n)
    hdr = await reader.readexactly(5)
    if hdr[0] not in (0x16, 0x17) or hdr[1:3] != b"\x03\x03":
        raise ValueError("не запись маскировки")
    n = struct.unpack(">H", hdr[3:5])[0]
    if n < 2 or n > MAX_FRAME + 64:
        raise ValueError("длина записи")
    body = await reader.readexactly(n)
    pad = body[0]
    if pad >= n - 1:
        raise ValueError("паддинг")
    return body[1:n - pad]


class Session:
    def __init__(self, sid, uid, ip, conn, tx_key, rx_key):
        self.sid = sid
        self.uid = uid
        self.ip = ip
        self.ip_str = socket.inet_ntoa(ip)
        self.conn = conn
        self.tx = ChaCha20Poly1305(tx_key)
        self.rx = ChaCha20Poly1305(rx_key)
        self.tx_ctr = 0
        self.replay = Replay()
        self.last_rx = time.monotonic()
        self.last_status = 0.0
        self.since = now_ts()
        self.local = bool(getattr(conn, "local", False))


# ---------------------------------------------------------------- сервер

class TunnelIP:
    """Адреса для сессий через SSH-туннель: 172.28.x.y, освобождаются при отключении."""

    def __init__(self):
        self.lock = threading.Lock()
        self.used = {}  # ip -> метка владельца
        self.free = []  # освободившиеся адреса
        self.next = 0   # сколько адресов уже выдавали

    def take(self, owner):
        with self.lock:
            if self.free:
                ip = self.free.pop()
            else:
                n = self.next
                self.next += 1
                ip = f"172.28.{n // 254 + 1}.{n % 254 + 1}"
            self.used[ip] = owner
            return ip

    def release(self, owner):
        with self.lock:
            for ip, who in list(self.used.items()):
                if who == owner:
                    del self.used[ip]
                    self.free.append(ip)
                    break


class Server:
    def __init__(self, cfg):
        self.tunnel_ip = TunnelIP()
        self.cfg = cfg
        self.dns = socket.inet_aton(cfg.get("dns", "1.1.1.1"))
        self.users = {}        # uid(hex) -> запись пользователя
        self.secrets = {}      # uid(hex) -> секрет MTP (bytes)
        self.usage = {}        # uid(hex) -> {"used": байт, "reset": счётчик, "wg_last": ..., "ss_last": ...}
        self.sessions = {}     # sid(bytes) -> Session
        self.by_ip = {}        # ip(bytes) -> Session
        self.by_uid = {}       # uid(hex) -> Session
        self.seen_hello = {}   # эфемерный ключ клиента -> время (защита от повторов)
        self.users_sig = None
        self.dirty = False
        self.transport = None
        self.tun_fd = None
        self.wg_ok = False
        self.ss_ok = False
        self.wg_pubs = {}      # публичный ключ WireGuard -> uid
        self.ss_procs = {}     # uid -> {"proc": Popen, "port": int}
        self.ss_next_try = {}  # uid -> монотонное время следующей попытки запуска

    # ---- учётные записи и трафик
    def urec(self, uid):
        rec = self.usage.get(uid)
        if rec is None:
            rec = self.usage[uid] = {"used": 0, "reset": self.users.get(uid, {}).get("reset", 0),
                                     "wg_last": 0, "ss_last": 0}
        return rec

    def reload_users(self):
        try:
            st = os.stat(USERS_PATH)
        except FileNotFoundError:
            return
        sig = (st.st_mtime_ns, st.st_size)
        if sig == self.users_sig:
            return
        self.users_sig = sig
        raw = read_json(USERS_PATH, {}).get("users", {})
        users, secrets_map = {}, {}
        for uid, rec in raw.items():
            try:
                secrets_map[uid] = b64u_dec(rec["secret"])
            except (KeyError, ValueError, TypeError):
                continue
            users[uid] = rec
            seen = self.urec(uid)
            if seen.get("reset", 0) != rec.get("reset", 0):
                # администратор нажал «сброс трафика»: счётчики WireGuard и Shadowsocks не трогаем
                seen["used"] = 0
                seen["reset"] = rec.get("reset", 0)
                self.dirty = True
        self.users, self.secrets = users, secrets_map
        self.wg_pubs = {rec["wg_pub"]: uid for uid, rec in users.items() if rec.get("wg_pub")}
        for s in list(self.sessions.values()):
            if s.uid not in self.users:
                self.drop(s, "ключ удалён")
        log(f"загружено ключей: {len(self.users)}")

    def account(self, uid, n):
        self.urec(uid)["used"] += n
        self.dirty = True

    def block_reason(self, uid, now):
        rec = self.users.get(uid)
        if rec is None or not rec.get("enabled", True):
            return R_DISABLED
        if rec.get("expires", 0) and now >= rec["expires"]:
            return R_EXPIRED
        if rec.get("limit", 0) and self.usage.get(uid, {}).get("used", 0) >= rec["limit"]:
            return R_QUOTA
        return 0

    def allowed(self, s):
        reason = self.block_reason(s.uid, now_ts())
        if reason:
            self.send(s, K_KICK, bytes([reason]))
            self.drop(s, REASON_TEXT[reason])
            return False
        return True

    def drop(self, s, why, keep_conn=False):
        if self.sessions.get(s.sid) is s:
            del self.sessions[s.sid]
        if self.by_ip.get(s.ip) is s:
            del self.by_ip[s.ip]
        if getattr(s, "local", False):
            self.tunnel_ip.release(s.conn)
        if self.by_uid.get(s.uid) is s:
            del self.by_uid[s.uid]
        if why:
            name = self.users.get(s.uid, {}).get("name", s.uid)
            log(f"отключился: {name} ({s.conn.label()}): {why}")
        if s.conn.stream and not keep_conn:
            s.conn.close()

    def conn_closed(self, conn):
        for s in list(self.sessions.values()):
            if s.conn is conn:
                self.drop(s, "соединение закрыто")

    def flush_usage(self):
        if not self.dirty:
            return
        write_json_atomic(USAGE_PATH, self.usage)
        self.dirty = False

    def write_state(self):
        st = {
            "ts": now_ts(),
            "online": [{"id": s.uid, "ip": s.ip_str, "since": s.since} for s in self.sessions.values()],
        }
        write_json_atomic(STATE_PATH, st, mode=0o644)

    # ---- отправка
    def send(self, s, kind, payload, droppable=False):
        ctr = s.tx_ctr
        s.tx_ctr += 1
        hdr = bytes([T_DATA]) + s.sid + struct.pack(">Q", ctr)
        ct = s.tx.encrypt(nonce(ctr), bytes([kind]) + payload, hdr)
        return s.conn.send(hdr + ct, droppable)

    def send_status(self, s):
        rec = self.users.get(s.uid, {})
        st = {
            "name": rec.get("name", ""),
            "used": self.usage.get(s.uid, {}).get("used", 0),
            "limit": rec.get("limit", 0),
            "expires": rec.get("expires", 0),
            "ip": s.ip_str,
        }
        self.send(s, K_STATUS, json.dumps(st, ensure_ascii=False).encode("utf-8"))
        s.last_status = time.monotonic()

    def new_sid(self):
        while True:
            sid = secrets.token_bytes(4)
            if sid not in self.sessions:
                return sid

    # ---- входящие кадры (общие для UDP, TCP и маскировки)
    def on_udp(self, data, addr):
        self.on_frame(data, UdpConn(self.transport, addr))

    def on_frame(self, data, conn):
        if not data:
            return
        if data[0] == T_DATA:
            self.on_data(data, conn)
        elif data[0] == T_HELLO and len(data) == HELLO_LEN:
            self.on_hello(data, conn)

    def on_hello(self, data, conn):
        uid = data[66:74].hex()
        secret = self.secrets.get(uid)
        if secret is None:
            return  # неизвестный ключ: молчим
        if not hmac.compare_digest(tag16(secret, b"mtp1-hello", data[:82]), data[82:98]):
            return  # подделка: молчим
        now = now_ts()
        ts = struct.unpack(">Q", data[74:82])[0]
        if abs(now - ts) > HELLO_SKEW:
            return  # повтор старого пакета
        cpub_b = data[1:66]
        if cpub_b in self.seen_hello:
            return
        self.seen_hello[cpub_b] = time.monotonic()

        reason = self.block_reason(uid, now)
        if reason:
            body = bytes([T_REJECT, reason])
            conn.send(body + tag16(secret, b"mtp1-reject", cpub_b, body))
            return
        try:
            cpub = ec.EllipticCurvePublicKey.from_encoded_point(CURVE, cpub_b)
        except ValueError:
            return
        priv = ec.generate_private_key(CURVE)
        spub_b = pub_bytes(priv.public_key())
        shared = priv.exchange(ec.ECDH(), cpub)
        k_c2s, k_s2c = derive_keys(shared, secret, cpub_b, spub_b)
        sid = self.new_sid()
        # Через SSH-туннель сессия получает адрес из 172.28.0.0/16: один ключ может работать
        # одновременно и напрямую (10.8.0.x), и через туннель, не выбивая вторую сессию.
        if conn.local:
            ip = socket.inet_aton(self.tunnel_ip.take(sid))
        else:
            ip = socket.inet_aton(self.users[uid]["ip"])
        dns = TUNNEL_DNS if conn.local else self.dns
        body = bytes([T_WELCOME]) + sid + ip + dns + struct.pack(">H", MTU) + spub_b
        conn.send(body + tag16(secret, b"mtp1-welcome", cpub_b, body))

        old = self.by_uid.get(uid)
        if old is not None:
            # Повторный HELLO на том же TCP-соединении: клиент не дождался WELCOME (медленная сеть)
            # и отправил новый. Старую сессию заменяем, соединение не закрываем: иначе клиент
            # получит обрыв сразу после рукопожатия.
            same = old.conn is conn
            self.drop(old, "" if same else "подключение с другого устройства", keep_conn=same)
        s = Session(sid, uid, ip, conn, k_s2c, k_c2s)
        self.sessions[sid] = s
        self.by_ip[ip] = s
        self.by_uid[uid] = s
        log(f"подключился: {self.users[uid]['name']} ({conn.label()}) -> {s.ip_str}")
        self.send_status(s)

    def on_data(self, data, conn):
        if len(data) < MIN_DATA_LEN:
            return
        s = self.sessions.get(data[1:5])
        if s is None:
            return
        ctr = struct.unpack(">Q", data[5:13])[0]
        if not s.replay.ok(ctr):
            return
        try:
            pt = s.rx.decrypt(nonce(ctr), data[13:], data[:13])
        except InvalidTag:
            return
        s.replay.mark(ctr)
        s.last_rx = time.monotonic()
        if not conn.stream and not s.conn.stream:
            s.conn = conn  # UDP-клиент мог сменить сеть (Wi-Fi <-> мобильный интернет)
        if not pt:
            return
        kind, body = pt[0], pt[1:]
        if kind == K_IP:
            # принимаем только пакеты с адресом, выданным этому ключу
            if len(body) < 20 or body[0] >> 4 != 4 or body[12:16] != s.ip:
                return
            if not self.allowed(s):
                return
            self.account(s.uid, len(body))
            try:
                os.write(self.tun_fd, body)
            except OSError:
                pass

    def on_tun(self):
        for _ in range(256):
            try:
                pkt = os.read(self.tun_fd, 65535)
            except (BlockingIOError, InterruptedError):
                return
            except OSError:
                return
            if len(pkt) < 20 or pkt[0] >> 4 != 4:
                continue
            s = self.by_ip.get(pkt[16:20])
            if s is None:
                continue
            if not self.allowed(s):
                continue
            self.account(s.uid, len(pkt))
            self.send(s, K_IP, pkt, droppable=True)

    # ---- WireGuard: учёт трафика и пиры
    def wg_sync(self):
        if not self.wg_ok:
            return
        rc, out = run_out("wg", "show", WG_IF, "dump")
        if rc != 0:
            return
        peers = {}  # публичный ключ -> (rx, tx, allowed-ips)
        for line in out.splitlines()[1:]:
            f = line.split("\t")
            if len(f) >= 7:
                try:
                    peers[f[0]] = (int(f[5]), int(f[6]), f[3])
                except ValueError:
                    continue
        # учёт: счётчики есть только у поднятых пиров
        for pub, (rx, tx, _) in peers.items():
            uid = self.wg_pubs.get(pub)
            if uid is None:
                continue
            u = self.urec(uid)
            cur = rx + tx
            last = u.get("wg_last", 0)
            delta = cur - last if cur >= last else cur  # счётчик сбросился, если пир был перенастроен
            u["wg_last"] = cur
            if delta:
                self.account(uid, delta)
        # желаемое состояние: пир есть только у разрешённых ключей
        now = now_ts()
        want = {}
        for uid, rec in self.users.items():
            if rec.get("wg_pub") and self.block_reason(uid, now) == 0:
                want[rec["wg_pub"]] = f"{rec['wg_ip']}/32"
        for pub in peers:
            if pub not in want:
                run_quiet("wg", "set", WG_IF, "peer", pub, "remove")
                uid = self.wg_pubs.get(pub)
                if uid is not None:
                    self.urec(uid)["wg_last"] = 0
        for pub, aips in want.items():
            if pub not in peers:
                if run_quiet("wg", "set", WG_IF, "peer", pub, "allowed-ips", aips):
                    uid = self.wg_pubs.get(pub)
                    if uid is not None:
                        self.urec(uid)["wg_last"] = 0
            elif peers[pub][2] != aips:
                run_quiet("wg", "set", WG_IF, "peer", pub, "allowed-ips", aips)

    # ---- Shadowsocks: по одному ss-server на ключ, учёт через счётчики iptables
    def ss_start(self, uid, rec):
        port = int(rec["ss_port"])
        conf = os.path.join(RUN_DIR, f"ss-{port}.json")
        write_secret(conf, json.dumps({
            "server": "0.0.0.0", "server_port": port, "password": rec["ss_password"],
            "method": SS_METHOD, "mode": "tcp_and_udp", "timeout": 300,
        }))
        ss_rules_add(port)
        try:
            proc = subprocess.Popen(["ss-server", "-c", conf], stdout=subprocess.DEVNULL,
                                    stderr=subprocess.DEVNULL, start_new_session=True)
        except OSError as e:
            ss_rules_del(port)
            log(f"Shadowsocks: не удалось запустить порт {port}: {e}")
            self.ss_next_try[uid] = time.monotonic() + 30
            return
        time.sleep(0.2)
        if proc.poll() is not None:
            ss_rules_del(port)
            log(f"Shadowsocks: порт {port} не поднялся (занят?)")
            self.ss_next_try[uid] = time.monotonic() + 30
            return
        with open(os.path.join(RUN_DIR, f"ss-{port}.pid"), "w") as f:
            f.write(str(proc.pid))
        self.ss_procs[uid] = {"proc": proc, "port": port}
        self.urec(uid)["ss_last"] = 0
        log(f"Shadowsocks: ключ «{rec.get('name', uid)}» слушает порт {port}")

    def ss_stop(self, uid):
        ent = self.ss_procs.pop(uid, None)
        if ent is None:
            return
        proc, port = ent["proc"], ent["port"]
        if proc.poll() is None:
            proc.terminate()
            with contextlib.suppress(subprocess.TimeoutExpired):
                proc.wait(5)
            if proc.poll() is None:
                proc.kill()
        ss_rules_del(port)
        for name in (f"ss-{port}.json", f"ss-{port}.pid"):
            with contextlib.suppress(OSError):
                os.remove(os.path.join(RUN_DIR, name))
        self.urec(uid)["ss_last"] = 0

    def ss_stop_all(self):
        for uid in list(self.ss_procs):
            self.ss_stop(uid)

    def ss_sync(self):
        if not self.ss_ok:
            return
        counters = ss_counters()
        now = now_ts()
        t = time.monotonic()
        for uid in list(self.ss_procs):
            ent = self.ss_procs[uid]
            port, proc = ent["port"], ent["proc"]
            u = self.urec(uid)
            cur = counters.get(port, 0)
            last = u.get("ss_last", 0)
            delta = cur - last if cur >= last else cur
            u["ss_last"] = cur
            if delta:
                self.account(uid, delta)
            rec = self.users.get(uid)
            if rec is None or rec.get("ss_port") != port or proc.poll() is not None or self.block_reason(uid, now):
                self.ss_stop(uid)
        for uid, rec in self.users.items():
            if uid in self.ss_procs or not rec.get("ss_port") or not rec.get("ss_password"):
                continue
            if self.block_reason(uid, now) or t < self.ss_next_try.get(uid, 0):
                continue
            self.ss_start(uid, rec)

    # ---- фоновые задачи
    async def housekeeping(self):
        last_flush = last_state = last_wg = last_ss = 0.0
        while True:
            await asyncio.sleep(1)
            self.reload_users()
            t = time.monotonic()
            for s in list(self.sessions.values()):
                if t - s.last_rx > SESSION_IDLE:
                    self.drop(s, "нет активности")
                elif self.allowed(s) and t - s.last_status >= STATUS_EVERY:
                    self.send_status(s)
            if t - last_wg >= WG_EVERY:
                self.wg_sync()
                last_wg = t
            if t - last_ss >= SS_EVERY:
                self.ss_sync()
                last_ss = t
            cutoff = t - 2 * HELLO_SKEW
            if self.seen_hello and len(self.seen_hello) > 1000:
                self.seen_hello = {k: v for k, v in self.seen_hello.items() if v > cutoff}
            if self.dirty and t - last_flush >= 5:
                self.flush_usage()
                last_flush = t
            if t - last_state >= 10:
                self.write_state()
                last_state = t


class UdpProto(asyncio.DatagramProtocol):
    def __init__(self, srv):
        self.srv = srv

    def datagram_received(self, data, addr):
        try:
            self.srv.on_udp(data, addr)
        except Exception:  # один битый пакет не должен ронять сервер
            pass


async def handle_stream(srv, reader, writer, mask):
    conn = StreamConn(writer, mask)
    try:
        while True:
            try:
                frame = await asyncio.wait_for(read_frame(reader, mask), READ_TIMEOUT)
            except (asyncio.IncompleteReadError, asyncio.TimeoutError, ValueError, OSError):
                break
            try:
                srv.on_frame(frame, conn)
            except Exception:  # один битый кадр не должен рвать соединение с хорошим клиентом
                pass
    finally:
        srv.conn_closed(conn)
        conn.close()


# ---------------------------------------------------------------- сеть

def open_tun(name):
    tunsetiff = 0x400454CA
    iff_tun, iff_no_pi = 0x0001, 0x1000
    fd = os.open("/dev/net/tun", os.O_RDWR)
    ifr = struct.pack("16sH", name.encode("ascii"), iff_tun | iff_no_pi)
    fcntl.ioctl(fd, tunsetiff, ifr)
    return fd


def setup_network():
    try:
        subprocess.run(["ip", "addr", "replace", f"{SERVER_IP}/24", "dev", TUN_NAME], check=True)
        subprocess.run(["ip", "link", "set", TUN_NAME, "mtu", str(MTU), "up"], check=True)
    except (OSError, subprocess.CalledProcessError) as e:
        die(f"не удалось поднять интерфейс {TUN_NAME}: {e}")
    try:
        with open("/proc/sys/net/ipv4/ip_forward", "w") as f:
            f.write("1")
    except OSError as e:
        log(f"не удалось включить ip_forward: {e}")
    # Ответы из туннеля (172.28.0.0/16) уходят в mtp0: адрес шлюза 172.28.0.1 и DNS 172.28.0.53
    # висят на петле хоста, а сервер отвечает клиенту через туннель.
    run_quiet("ip", "route", "replace", TUNNEL_NAT_NET, "dev", TUN_NAME)
    wan = default_iface()
    ensure_rules(nat_rules(wan, SUBNET, TUN_NAME, mss_clamp=True))
    if wan:
        ensure_rules(tunnel_nat_rules(wan, TUN_NAME))
    log(f"MTP: NAT через интерфейс {wan}")


def tunnel_nat_rules(wan, iface):
    """NAT для сессий через SSH-туннель (адреса 172.28.0.0/16).

    Весь трафик по умолчанию выходит через сам хост: шлюз 172.28.0.1 и DNS 172.28.0.53 —
    настоящие адреса на петле хоста (их ставит install.sh), поэтому никакого DNAT не нужно.
    SNAT на 127.0.0.1 оставлен для случаев, когда что-то на хосте само ходит на петлю.
    """
    loop = "127.0.0.0/8"
    return [
        (["-t", "nat", "-C", "POSTROUTING", "-s", TUNNEL_NAT_NET, "-o", wan, "-j", "MASQUERADE"],
         ["-t", "nat", "-A", "POSTROUTING", "-s", TUNNEL_NAT_NET, "-o", wan, "-j", "MASQUERADE"]),
        (["-t", "nat", "-C", "POSTROUTING", "-s", TUNNEL_NAT_NET, "-d", loop, "-j", "MASQUERADE"],
         ["-t", "nat", "-A", "POSTROUTING", "-s", TUNNEL_NAT_NET, "-d", loop, "-j", "MASQUERADE"]),
        (["-C", "FORWARD", "-i", iface, "-d", loop, "-j", "ACCEPT"],
         ["-I", "FORWARD", "1", "-i", iface, "-d", loop, "-j", "ACCEPT"]),
        (["-C", "FORWARD", "-o", iface, "-j", "ACCEPT"],
         ["-I", "FORWARD", "1", "-o", iface, "-j", "ACCEPT"]),
    ]


def wg_setup(cfg):
    if shutil.which("wg") is None:
        log("WireGuard: нет утилиты wg (пакет wireguard-tools)")
        return False
    rc, _ = run_out("ip", "link", "show", WG_IF)
    if rc != 0:
        if not run_quiet("ip", "link", "add", WG_IF, "type", "wireguard"):
            # нет модуля ядра: поднимаем userspace-реализацию wireguard-go
            if not run_quiet("wireguard-go", WG_IF):
                log("WireGuard: нет ни модуля ядра, ни wireguard-go")
                return False
            for _ in range(50):
                if run_quiet("ip", "link", "show", WG_IF):
                    break
                time.sleep(0.1)
    keyfile = os.path.join(RUN_DIR, "wg.key")
    write_secret(keyfile, cfg["wg_priv"])
    ok = (run_quiet("ip", "addr", "replace", f"{WG_SERVER_IP}/24", "dev", WG_IF)
          and run_quiet("ip", "link", "set", WG_IF, "mtu", str(WG_MTU), "up")
          and run_quiet("wg", "set", WG_IF, "private-key", keyfile, "listen-port", str(cfg["wg_port"])))
    if not ok:
        log("WireGuard: не удалось настроить интерфейс")
        return False
    ensure_rules(nat_rules(default_iface(), WG_SUBNET, WG_IF))
    log(f"WireGuard: интерфейс {WG_IF}, UDP {cfg['wg_port']}")
    return True


def ss_rules_add(port):
    for proto in ("tcp", "udp"):
        for opt in ("--dport", "--sport"):
            rule = ["-p", proto, opt, str(port), "-j", "RETURN"]
            if not run_quiet("iptables", "-C", SS_CHAIN, *rule):
                run_quiet("iptables", "-A", SS_CHAIN, *rule)


def ss_rules_del(port):
    for proto in ("tcp", "udp"):
        for opt in ("--dport", "--sport"):
            rule = ["-p", proto, opt, str(port), "-j", "RETURN"]
            for _ in range(10):
                if not run_quiet("iptables", "-D", SS_CHAIN, *rule):
                    break


def ss_counters():
    """Байты по портам Shadowsocks (вход и выход) из счётчиков цепочки iptables."""
    rc, out = run_out("iptables", "-w", "-x", "-n", "-v", "-L", SS_CHAIN)
    res = {}
    if rc != 0:
        return res
    for line in out.splitlines():
        m = re.search(r"\b(?:dpt|spt):(\d+)", line)
        if not m:
            continue
        fields = line.split()
        try:
            nbytes = int(fields[1])
        except (IndexError, ValueError):
            continue
        port = int(m.group(1))
        res[port] = res.get(port, 0) + nbytes
    return res


def is_ss_process(pid):
    """Убеждаемся, что pid принадлежит ss-server, а не чужому процессу, которому достался тот же номер."""
    try:
        with open(f"/proc/{pid}/cmdline", "rb") as f:
            return b"ss-server" in f.read()
    except OSError:
        return False


def ss_setup():
    if shutil.which("ss-server") is None:
        log("Shadowsocks: нет ss-server (пакет shadowsocks-libev)")
        return False
    os.makedirs(RUN_DIR, exist_ok=True)
    for pidfile in glob.glob(os.path.join(RUN_DIR, "ss-*.pid")):
        with contextlib.suppress(OSError, ValueError):
            with open(pidfile) as f:
                pid = int(f.read().strip())
            if is_ss_process(pid):
                os.kill(pid, signal.SIGTERM)
        with contextlib.suppress(OSError):
            os.remove(pidfile)
    for conf in glob.glob(os.path.join(RUN_DIR, "ss-*.json")):
        with contextlib.suppress(OSError):
            os.remove(conf)
    run_quiet("iptables", "-N", SS_CHAIN)
    run_quiet("iptables", "-F", SS_CHAIN)
    for parent in ("INPUT", "OUTPUT"):
        if not run_quiet("iptables", "-C", parent, "-j", SS_CHAIN):
            run_quiet("iptables", "-I", parent, "1", "-j", SS_CHAIN)
    return True


def clear_ss_chain():
    run_quiet("iptables", "-F", SS_CHAIN)
    for parent in ("INPUT", "OUTPUT"):
        while run_quiet("iptables", "-D", parent, "-j", SS_CHAIN):
            pass
    run_quiet("iptables", "-X", SS_CHAIN)


async def run_server(cfg):
    os.makedirs(RUN_DIR, exist_ok=True)
    srv = Server(cfg)
    srv.usage = read_json(USAGE_PATH, {})
    srv.reload_users()
    loop = asyncio.get_running_loop()
    srv.tun_fd = open_tun(TUN_NAME)
    setup_network()
    os.set_blocking(srv.tun_fd, False)
    loop.add_reader(srv.tun_fd, srv.on_tun)
    srv.wg_ok = wg_setup(cfg)
    srv.ss_ok = ss_setup()
    for rec in srv.usage.values():
        rec["ss_last"] = 0  # счётчики iptables новые, старую базу не используем

    # MTP/1 (UDP) снаружи выключен: единственный вход в MTP-VPN — SSH-шлюз на localhost.
    # Внутри шлюза работает MTP/2, потери пакетов исключены (TCP).
    transport, _ = await loop.create_datagram_endpoint(lambda: UdpProto(srv), local_addr=("127.0.0.1", cfg["port"]))
    srv.transport = transport
    sock = transport.get_extra_info("socket")
    for opt in (socket.SO_RCVBUF, socket.SO_SNDBUF):
        with contextlib.suppress(OSError):
            sock.setsockopt(socket.SOL_SOCKET, opt, 4 << 20)
    # MTP/2 слушает только localhost: клиент ходит к нему по SSH, порт наружу не открыт.
    tcp_srv = await asyncio.start_server(lambda r, w: handle_stream(srv, r, w, False), "127.0.0.1", cfg["tcp_port"])
    mask_srv = await asyncio.start_server(lambda r, w: handle_stream(srv, r, w, True), "0.0.0.0", cfg["mask_port"])

    stop = asyncio.Event()
    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, stop.set)
    task = asyncio.create_task(srv.housekeeping())
    log(f"MTP/1 UDP {cfg['port']}, MTP/2 TCP {cfg['tcp_port']}, MTP/3 TCP {cfg['mask_port']}, "
        f"туннель {TUN_NAME} {SERVER_IP}/24")
    await stop.wait()
    task.cancel()
    tcp_srv.close()
    mask_srv.close()
    for s in list(srv.sessions.values()):
        srv.drop(s, "")
    srv.wg_sync()
    srv.ss_stop_all()
    srv.flush_usage()
    loop.remove_reader(srv.tun_fd)
    transport.close()
    os.close(srv.tun_fd)
    log("сервер остановлен")


# ---------------------------------------------------------------- админ-команды

@contextlib.contextmanager
def locked():
    os.makedirs(BASE, exist_ok=True)
    fd = os.open(LOCK_PATH, os.O_CREAT | os.O_RDWR, 0o600)
    fcntl.flock(fd, fcntl.LOCK_EX)
    try:
        yield
    finally:
        os.close(fd)


def load_config():
    raw = read_json(CONFIG_PATH, None)
    if raw is None:
        die("сервер не настроен: сначала выполни mtp.py init --host ВАШ_IP")
    cfg = {**DEFAULT_CFG, **raw}
    if "wg_priv" not in cfg:
        die("настройка устарела: выполни mtp.py init --host ВАШ_IP заново")
    return cfg


def read_users():
    return read_json(USERS_PATH, {}).get("users", {})


def write_users(users):
    write_json_atomic(USERS_PATH, {"users": users})


def read_usage():
    return read_json(USAGE_PATH, {})


def read_online():
    st = read_json(STATE_PATH, {})
    if now_ts() - st.get("ts", 0) > 60:
        return set()
    return {x["id"] for x in st.get("online", [])}


def next_ss_port(users, cfg):
    taken = {u.get("ss_port") for u in users.values()}
    reserved = {cfg["port"], cfg["tcp_port"], cfg["mask_port"], cfg["wg_port"]}
    port = int(cfg["ss_base"])
    while port in taken or port in reserved:
        port += 1
    if port > 65000:
        die("закончились порты для Shadowsocks")
    return port


def fix_users(cfg, users):
    """Дополняет записи старых ключей полями WireGuard и Shadowsocks. Возвращает True, если что-то добавлено."""
    changed = False
    for uid, u in users.items():
        if not u.get("wg_priv"):
            u["wg_priv"], u["wg_pub"] = wg_keypair()
            changed = True
        if not u.get("wg_ip"):
            u["wg_ip"] = u["ip"].replace("10.8.0.", "10.9.0.")
            changed = True
        if not u.get("ss_port"):
            u["ss_port"] = next_ss_port(users, cfg)
            changed = True
        if not u.get("ss_password"):
            u["ss_password"] = secrets.token_urlsafe(16)
            changed = True
    return changed


def next_ip(users):
    taken = {u["ip"] for u in users.values()}
    for host in range(2, 255):
        ip = f"10.8.0.{host}"
        if ip not in taken:
            return ip
    die("все адреса заняты (максимум 253 ключа)")


def find_user(users, ident):
    if ident in users:
        return ident
    hits = [uid for uid, u in users.items() if u.get("name") == ident]
    if len(hits) == 1:
        return hits[0]
    if not hits:
        die(f"ключ не найден: {ident}")
    die("несколько ключей с таким именем, укажи id")


def make_key(cfg, uid, u):
    body = {
        "v": 2,
        "n": u["name"],
        "h": "127.0.0.1",  # MTP-VPN живёт за SSH-шлюзом: ключ работает только через туннель программы
        "u": uid,
        "s": u["secret"],
        "p": {"udp": cfg["port"], "tcp": cfg["tcp_port"], "mask": cfg["mask_port"],
              "wg": cfg["wg_port"], "ss": u["ss_port"]},
        "w": {"k": u["wg_priv"], "s": cfg["wg_pub"], "a": f"{u['wg_ip']}/32", "d": cfg["dns"]},
        "ss": {"m": SS_METHOD, "k": u["ss_password"]},
    }
    text = json.dumps(body, ensure_ascii=False, separators=(",", ":"))
    return "mtp://" + b64u(text.encode("utf-8"))


def status_of(u, used, now):
    if not u.get("enabled", True):
        return "отключён"
    if u.get("expires", 0) and now >= u["expires"]:
        return "истёк"
    if u.get("limit", 0) and used >= u["limit"]:
        return "лимит"
    return "активен"


def fmt_gb(n):
    return f"{n / GB:.2f} ГБ"


def fmt_date(ts):
    return time.strftime("%Y-%m-%d", time.localtime(ts)) if ts else "без срока"


def emit(args, data, text):
    if getattr(args, "json", False):
        print(json.dumps(data, ensure_ascii=False))
    else:
        print(text)


def cmd_init(args):
    os.makedirs(BASE, exist_ok=True)
    old = read_json(CONFIG_PATH, {})
    cfg = {
        "host": args.host, "port": args.port, "tcp_port": args.tcp_port, "mask_port": args.mask_port,
        "wg_port": args.wg_port, "ss_base": args.ss_base, "dns": args.dns,
    }
    if "wg_priv" in old and "wg_pub" in old:
        cfg["wg_priv"], cfg["wg_pub"] = old["wg_priv"], old["wg_pub"]  # ключ сервера WireGuard не меняем
    else:
        cfg["wg_priv"], cfg["wg_pub"] = wg_keypair()
    write_json_atomic(CONFIG_PATH, cfg, mode=0o600)
    with locked():
        users = read_users()
        if not os.path.exists(USERS_PATH):
            write_users({})
        elif fix_users(cfg, users):
            write_users(users)
    print(f"готово: сервер {args.host}: MTP/1 UDP {args.port}, MTP/2 TCP {args.tcp_port}, "
          f"MTP/3 TCP {args.mask_port}, WireGuard UDP {args.wg_port}, Shadowsocks от порта {args.ss_base}")


def cmd_add(args):
    cfg = load_config()
    name = args.name.strip()
    if not name or len(name) > 40:
        die("имя должно быть от 1 до 40 символов")
    with locked():
        users = read_users()
        if any(u.get("name") == name for u in users.values()):
            die("ключ с таким именем уже есть")
        uid = secrets.token_bytes(8).hex()
        secret = secrets.token_bytes(32)
        ip = next_ip(users)
        expires = now_ts() + args.days * 86400 if args.days > 0 else 0
        limit = int(args.limit_gb * GB) if args.limit_gb > 0 else 0
        wg_priv, wg_pub = wg_keypair()
        users[uid] = {
            "name": name, "secret": b64u(secret), "ip": ip,
            "wg_priv": wg_priv, "wg_pub": wg_pub, "wg_ip": ip.replace("10.8.0.", "10.9.0."),
            "ss_port": next_ss_port(users, cfg), "ss_password": secrets.token_urlsafe(16),
            "limit": limit, "expires": expires, "enabled": True, "created": now_ts(), "reset": 0,
        }
        write_users(users)
    key = make_key(cfg, uid, users[uid])
    data = {"key": key, "id": uid, "name": name, "ip": ip, "limit": limit, "expires": expires,
            "ss_port": users[uid]["ss_port"]}
    text = (f"Ключ «{name}» создан: трафик {'без лимита' if not limit else f'{args.limit_gb:g} ГБ'}, "
            f"срок {fmt_date(expires)}\n{key}")
    emit(args, data, text)


def cmd_list(args):
    cfg = load_config()
    users = read_users()
    usage = read_usage()
    online = read_online()
    now = now_ts()
    rows = []
    for uid, u in sorted(users.items(), key=lambda kv: kv[1].get("name", "")):
        used = usage.get(uid, {}).get("used", 0)
        rows.append({
            "id": uid, "name": u.get("name", ""), "ip": u.get("ip", ""),
            "status": status_of(u, used, now), "used": used, "limit": u.get("limit", 0),
            "expires": u.get("expires", 0), "online": uid in online,
        })
    if args.json:
        print(json.dumps({"host": cfg["host"], "port": cfg["port"], "users": rows}, ensure_ascii=False))
        return
    if not rows:
        print("ключей пока нет. Создай: mtp.py add --name ИМЯ")
        return
    print(f"{'ИМЯ':<18}{'IP':<12}{'СТАТУС':<11}{'ТРАФИК':<26}{'СРОК':<12}ОНЛАЙН")
    for r in rows:
        traffic = f"{fmt_gb(r['used'])} / {fmt_gb(r['limit']) if r['limit'] else 'без лимита'}"
        print(f"{r['name']:<18}{r['ip']:<12}{r['status']:<11}{traffic:<26}{fmt_date(r['expires']):<12}"
              f"{'да' if r['online'] else 'нет'}")


def cmd_show(args):
    cfg = load_config()
    with locked():
        users = read_users()
        if fix_users(cfg, users):
            write_users(users)
    uid = find_user(users, args.ident)
    key = make_key(cfg, uid, users[uid])
    emit(args, {"key": key, "name": users[uid]["name"], "id": uid}, key)


def cmd_set(args):
    load_config()
    if args.days is None and args.limit_gb is None and not (args.enable or args.disable or args.reset):
        die("нечего менять: укажи --days, --limit-gb, --enable, --disable или --reset")
    with locked():
        users = read_users()
        uid = find_user(users, args.ident)
        u = users[uid]
        if args.days is not None:
            u["expires"] = now_ts() + args.days * 86400 if args.days > 0 else 0
        if args.limit_gb is not None:
            u["limit"] = int(args.limit_gb * GB) if args.limit_gb > 0 else 0
        if args.enable:
            u["enabled"] = True
        if args.disable:
            u["enabled"] = False
        if args.reset:
            u["reset"] = u.get("reset", 0) + 1
        write_users(users)
    emit(args, {"ok": True, "id": uid, "name": u["name"]}, f"ключ «{u['name']}» обновлён")


def cmd_delete(args):
    with locked():
        users = read_users()
        uid = find_user(users, args.ident)
        name = users[uid]["name"]
        del users[uid]
        write_users(users)
    emit(args, {"ok": True, "name": name}, f"ключ «{name}» удалён")


def cmd_config_set(args):
    with locked():
        cfg = load_config()
        cfg[args.key] = args.value
        write_json_atomic(CONFIG_PATH, cfg, mode=0o600)
    emit(args, {"ok": True, "key": args.key, "value": args.value}, f"{args.key} = {args.value}")


def cmd_serve(args):
    if os.geteuid() != 0:
        die("serve нужно запускать от root (нужны TUN, iptables и WireGuard)")
    cfg = load_config()
    with locked():
        users = read_users()
        if fix_users(cfg, users):
            write_users(users)
    asyncio.run(run_server(cfg))


def build_parser():
    p = argparse.ArgumentParser(description="Усатый VPN (MTP/1, MTP/2, MTP/3, WireGuard, Shadowsocks)")
    sub = p.add_subparsers(dest="cmd", required=True)

    s = sub.add_parser("init", help="первичная настройка")
    s.add_argument("--host", required=True, help="публичный IP или домен сервера")
    s.add_argument("--port", type=int, default=443, help="UDP-порт MTP/1")
    s.add_argument("--tcp-port", type=int, default=8443, help="TCP-порт MTP/2")
    s.add_argument("--mask-port", type=int, default=443, help="TCP-порт MTP/3 (маскировка)")
    s.add_argument("--wg-port", type=int, default=51820, help="UDP-порт WireGuard")
    s.add_argument("--ss-base", type=int, default=20001, help="первый порт Shadowsocks")
    s.add_argument("--dns", default="1.1.1.1")
    s.set_defaults(func=cmd_init)

    s = sub.add_parser("add", help="создать ключ")
    s.add_argument("--name", required=True)
    s.add_argument("--days", type=int, default=30, help="срок в днях, 0 = без срока")
    s.add_argument("--limit-gb", type=float, default=10, help="лимит трафика в ГБ, 0 = без лимита")
    s.add_argument("--json", action="store_true")
    s.set_defaults(func=cmd_add)

    s = sub.add_parser("list", help="список ключей")
    s.add_argument("--json", action="store_true")
    s.set_defaults(func=cmd_list)

    s = sub.add_parser("show", help="показать ключ")
    s.add_argument("ident")
    s.add_argument("--json", action="store_true")
    s.set_defaults(func=cmd_show)

    s = sub.add_parser("set", help="изменить срок, лимит, включить/выключить, сбросить трафик")
    s.add_argument("ident")
    s.add_argument("--days", type=int, help="новый срок в днях от текущего момента, 0 = без срока")
    s.add_argument("--limit-gb", type=float, help="новый лимит в ГБ, 0 = без лимита")
    g = s.add_mutually_exclusive_group()
    g.add_argument("--enable", action="store_true")
    g.add_argument("--disable", action="store_true")
    s.add_argument("--reset", action="store_true", help="обнулить счётчик трафика")
    s.add_argument("--json", action="store_true")
    s.set_defaults(func=cmd_set)

    s = sub.add_parser("delete", help="удалить ключ")
    s.add_argument("ident")
    s.add_argument("--json", action="store_true")
    s.set_defaults(func=cmd_delete)

    s = sub.add_parser("config-set", help="изменить значение в конфиге сервера")
    s.add_argument("--key", required=True)
    s.add_argument("--value", required=True)
    s.add_argument("--json", action="store_true")
    s.set_defaults(func=cmd_config_set)

    s = sub.add_parser("serve", help="запустить сервер (для systemd)")
    s.set_defaults(func=cmd_serve)
    return p


def main():
    args = build_parser().parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
