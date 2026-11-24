package main

// Маршрутизация MTP-туннеля.
//
// Клиент MTP всегда живёт на одном компьютере с MTP-сервером (цепочка AmneziaWG → SSH → MTP),
// а сервер отдаёт в туннель только адрес клиента. MTP-шлюз (10.8.0.1) — это сам хост:
// его петлевой адрес подключён внутрь MTP-туннеля на сервере (10.8.0.1 = 127.0.0.1 хоста
// через «ip addr add» и маршрут в петлю), поэтому «весь трафик» уходит в MTP и выходит из хоста,
// а DNS идёт на 172.28.0.53 — тот же петлевой адрес через dnsmasq на хосте.
// Так NAT и адрес выхода всегда совпадают с сервером, где стоит VPN.

import (
	"errors"
	"fmt"
	"net"
)

var (
	// mtpGatewayIP — адрес MTP-шлюза в туннеле (он же 127.0.0.1 сервера).
	mtpGatewayIP = net.IPv4(10, 8, 0, 1)
	// mtpDNSIP — адрес DNS-резолвера на сервере (172.28.0.53 = 127.0.0.1 через NAT).
	mtpDNSIP = net.IPv4(172, 28, 0, 53)
)

// resolveIPv4 — IPv4-адрес хоста (нужен для маршрута к серверу, чтобы сам туннель не ушёл в себя).
func resolveIPv4(host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4, nil
		}
		return nil, fmt.Errorf("нужен IPv4-адрес сервера, получен %s", host)
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("не удалось найти адрес сервера %s: %w", host, err)
	}
	for _, a := range addrs {
		if v4 := a.To4(); v4 != nil {
			return v4, nil
		}
	}
	return nil, fmt.Errorf("у сервера %s нет IPv4-адреса", host)
}

// routesFor — что маршрутизирует MTP-туннель в выбранном режиме.
// allowed — AllowedIPs для UDP-способа (MTP/1), tun — маршруты, которые направляются в адаптер.
func routesFor(routing Routing, notRu []string) (allowed []string, tun []string, err error) {
	switch routing {
	case RAll:
		return []string{"0.0.0.0/0"}, []string{"0.0.0.0/1", "128.0.0.0/1"}, nil
	case RNotRU:
		if len(notRu) == 0 {
			return nil, nil, errors.New("не найден список российских сетей (assets/notru.txt)")
		}
		allowed = append([]string{}, notRu...)
		tun = append([]string{}, notRu...)
		return allowed, tun, nil
	case RApps:
		return nil, nil, errors.New("режим «Только выбранные приложения» в Windows не поддерживается")
	}
	return nil, nil, fmt.Errorf("неизвестный режим маршрутизации: %s", routing)
}
