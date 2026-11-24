package main

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
)

// tunOffset — отступ данных в буфере пакета (как в wireguard-go).
const tunOffset = 16

const (
	keepaliveEvery = 20 * time.Second
	linkDeadAfter  = 90 * time.Second
)

// runOpts — параметры запуска туннеля.
type runOpts struct {
	Key       *ServerKey
	Transport Transport
	Routing   Routing
	NotRU     []string
	Adapter   string
	// Ставим системный DNS на резолвер в туннеле (в режиме Amnezia-провайдера DNS уже внутри Amnezia).
	SetDNS bool
	// Только для проверок на Linux: маршруты через туннель вместо режима маршрутизации.
	TestRoutes []string
	// Сервер MTP на этом же компьютере (проверки цепочки): маршрут до сервера не нужен,
	// шлюз — адрес сервера в туннеле.
	LocalServer bool
}

// tunnelRun — работающий туннель (MTP или WireGuard).
type tunnelRun interface {
	Stop()
	Wait() error
	Stats() (rx, tx int64)
	Status() (serverStatus, bool)
}

// mtpRun — туннель по протоколу MTP/1-3: читает пакеты из адаптера, шифрует и шлёт на сервер.
type mtpRun struct {
	key     *ServerKey
	link    Link
	sess    *Session
	dev     awgtun.Device
	hostDst net.IP
	routes  []string
	gateway net.IP
	adapter string
	rx, tx  atomic.Int64
	stopped atomic.Bool
	wg      sync.WaitGroup
	done    chan struct{}
	errMu   sync.Mutex
	err     error
	stMu    sync.Mutex
	st      serverStatus
	hasSt   bool
}

// startMTP поднимает адаптер, настраивает адрес, маршруты и запускает обмен пакетами.
func startMTP(o runOpts) (*mtpRun, error) {
	if !o.Transport.IsMTP() {
		return nil, errors.New("это не способ MTP")
	}
	port, ok := o.Key.Port(o.Transport)
	if !ok {
		return nil, fmt.Errorf("в ключе нет порта для «%s»", o.Transport.Label())
	}
	var link Link
	var err error
	switch o.Transport {
	case TMTP1:
		link, err = dialUDP(o.Key.Host, port)
	case TMTP2:
		link, err = dialTCP(o.Key.Host, port, false)
	default:
		link, err = dialTCP(o.Key.Host, port, true)
	}
	if err != nil {
		return nil, fmt.Errorf("не удалось связаться с сервером: %w", err)
	}
	sess, err := Handshake(o.Key, link)
	if err != nil {
		link.Close()
		return nil, err
	}
	return startMTPWith(o, link, sess)
}

// startMTPWith поднимает MTP-туннель поверх готового канала с уже выполненным рукопожатием.
// Так программа работает через SSH-канал: MTP/2 поверх SSH, без UDP-портов наружу.
func startMTPWith(o runOpts, link Link, sess *Session) (*mtpRun, error) {
	serverIP := net.IPv4(127, 0, 0, 1)

	// шлюз физической сети нужен до того, как маршруты по умолчанию уйдут в адаптер
	gw, gwErr := netDefaultGateway()
	if gwErr != nil && len(o.TestRoutes) == 0 && !o.LocalServer {
		link.Close()
		return nil, gwErr
	}
	gateway := mtpGatewayIP
	if o.LocalServer {
		gateway = net.IP(sess.IP).To4() // сам сервер MTP — адрес шлюза в туннеле
	}

	dev, err := awgtun.CreateTUN(o.Adapter, sess.MTU)
	if err != nil {
		link.Close()
		return nil, fmt.Errorf("не удалось создать сетевой адаптер (нужны права администратора и драйвер WinTun): %w", err)
	}
	name, err := dev.Name()
	if err != nil {
		dev.Close()
		link.Close()
		return nil, err
	}

	r := &mtpRun{key: o.Key, link: link, sess: sess, dev: dev, done: make(chan struct{}), adapter: name}
	if err := netSetAddress(name, sess.IP, 24); err != nil {
		r.abort()
		return nil, err
	}
	if err := netSetMTU(name, sess.MTU); err != nil {
		r.abort()
		return nil, err
	}
	dns := sess.DNS
	if o.SetDNS {
		dns = mtpDNSIP
	}
	_ = netSetDNS(name, dns)
	if gw != nil && !serverIP.IsLoopback() && !o.LocalServer {
		if err := netAddHostRoute(serverIP, gw); err != nil {
			r.abort()
			return nil, err
		}
		r.hostDst = serverIP
	}
	tunRoutes := o.TestRoutes
	if len(tunRoutes) == 0 {
		_, routes, err := routesFor(o.Routing, o.NotRU)
		if err != nil {
			r.abort()
			return nil, err
		}
		tunRoutes = routes
	}
	if err := netAddRoutes(name, gateway, tunRoutes); err != nil {
		r.abort()
		return nil, err
	}
	r.routes, r.gateway = tunRoutes, gateway

	r.wg.Add(2)
	go r.tunLoop()
	go r.linkLoop()
	go func() {
		r.wg.Wait()
		close(r.done)
	}()
	return r, nil
}

// abort — откат при ошибке во время запуска.
func (r *mtpRun) abort() {
	r.link.Close()
	r.dev.Close()
	if r.hostDst != nil {
		netDelHostRoute(r.hostDst)
	}
}

// tunLoop: пакеты из адаптера -> шифрование -> сервер.
func (r *mtpRun) tunLoop() {
	defer r.wg.Done()
	batch := r.dev.BatchSize()
	if batch < 1 {
		batch = 1
	}
	bufs := make([][]byte, batch)
	sizes := make([]int, batch)
	for i := range bufs {
		bufs[i] = make([]byte, tunOffset+65535)
	}
	for {
		n, err := r.dev.Read(bufs, sizes, tunOffset)
		if err != nil {
			if !r.stopped.Load() {
				r.fail(fmt.Errorf("ошибка адаптера: %w", err))
			}
			return
		}
		for i := 0; i < n; i++ {
			pkt := bufs[i][tunOffset : tunOffset+sizes[i]]
			// IPv6 не туннелируем: сервер его не принимает. Но пакет не теряем молча: приложение получит
			// «адрес недоступен» и сразу перейдёт на IPv4 (см. icmp6.go).
			if len(pkt) >= 40 && pkt[0]>>4 == 6 {
				r.answerIPv6(pkt)
				continue
			}
			if len(pkt) < 20 || pkt[0]>>4 != 4 {
				continue
			}
			frame := r.sess.Encrypt(kIP, pkt)
			if err := r.link.Send(frame); err != nil {
				if errors.Is(err, ErrClosed) || r.stopped.Load() {
					return
				}
				continue
			}
			r.tx.Add(int64(len(pkt)))
		}
		if r.stopped.Load() {
			return
		}
	}
}

// answerIPv6 отвечает на IPv6-пакет из адаптера ICMPv6 «адрес недоступен» (см. icmp6.go).
func (r *mtpRun) answerIPv6(pkt []byte) {
	reply := icmp6Unreachable(pkt)
	if reply == nil {
		return
	}
	out := make([]byte, tunOffset+len(reply))
	copy(out[tunOffset:], reply)
	_, _ = r.dev.Write([][]byte{out}, tunOffset)
}

// linkLoop: ответы сервера -> адаптер; keepalive; контроль связи.
func (r *mtpRun) linkLoop() {
	defer r.wg.Done()
	lastRx := time.Now()
	lastTx := time.Now()
	for {
		if r.stopped.Load() {
			return
		}
		f, err := r.link.Recv(time.Second)
		if errors.Is(err, ErrClosed) {
			if !r.stopped.Load() {
				r.fail(errors.New("соединение с сервером разорвано"))
			}
			return
		}
		if errors.Is(err, ErrTimeout) {
			now := time.Now()
			if now.Sub(lastRx) > linkDeadAfter {
				r.fail(errors.New("сервер молчит больше 90 секунд"))
				return
			}
			if now.Sub(lastTx) > keepaliveEvery {
				_ = r.link.Send(r.sess.Encrypt(kKeepalive, nil))
				lastTx = now
			}
			continue
		}
		if err != nil {
			r.fail(err)
			return
		}
		kind, payload, ok := r.sess.Decrypt(f)
		if !ok {
			continue
		}
		lastRx = time.Now()
		switch kind {
		case kIP:
			out := make([]byte, tunOffset+len(payload))
			copy(out[tunOffset:], payload)
			if _, err := r.dev.Write([][]byte{out}, tunOffset); err == nil {
				r.rx.Add(int64(len(payload)))
			}
		case kStatus:
			if st, ok := parseStatus(payload); ok {
				st.At = time.Now().UnixMilli()
				r.stMu.Lock()
				r.st = st
				r.hasSt = true
				r.stMu.Unlock()
			}
		case kKick:
			reason := 0
			if len(payload) > 0 {
				reason = int(payload[0])
			}
			r.fail(&KickError{Reason: reason})
			return
		}
	}
}

func (r *mtpRun) fail(err error) {
	r.errMu.Lock()
	if r.err == nil {
		r.err = err
	}
	r.errMu.Unlock()
	r.Stop()
}

// Stop останавливает туннель. Можно вызывать несколько раз.
func (r *mtpRun) Stop() {
	if !r.stopped.CompareAndSwap(false, true) {
		return
	}
	r.link.Close()
	r.dev.Close()
	if r.hostDst != nil {
		netDelHostRoute(r.hostDst)
	}
	if len(r.routes) > 0 && r.adapter != "" {
		netRemoveRoutesFast(r.adapter, r.gateway, r.routes)
	}
}

// Wait ждёт окончания туннеля и возвращает причину (nil — остановлен пользователем).
func (r *mtpRun) Wait() error {
	<-r.done
	r.errMu.Lock()
	defer r.errMu.Unlock()
	return r.err
}

// Stats — байты за сеанс: получено и отправлено (IP-пакеты).
func (r *mtpRun) Stats() (int64, int64) {
	return r.rx.Load(), r.tx.Load()
}

// Status — данные сервера из STATUS (использовано, лимит, срок).
func (r *mtpRun) Status() (serverStatus, bool) {
	r.stMu.Lock()
	defer r.stMu.Unlock()
	return r.st, r.hasSt
}
