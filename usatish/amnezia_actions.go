package main

// Действия с файлом Amnezia и настройка сервера до загрузки конфига:
//   - загрузка .conf (файл, буфер обмена или путь);
//   - «настроить сервер» без Amnezia (прямой SSH) — чтобы загрузить install.sh до первого конфига;
//   - установка AmneziaWG на хост (amneziawg-go + amneziawg-tools + awg0.conf).

import (
	"errors"
	"fmt"
	"strings"
)

// AmneziaInfo — что показать про загруженный конфиг.
type AmneziaInfo struct {
	Loaded     bool     `json:"loaded"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Endpoint   string   `json:"endpoint"`
	Address    string   `json:"address"`
	Obfuscated bool     `json:"obfuscated"`
	Lines      []string `json:"lines"`
}

// AmneziaStatus — сведения о конфиге для интерфейса.
func (a *App) AmneziaStatus() AmneziaInfo {
	c := activeAwg()
	if c == nil {
		return AmneziaInfo{Lines: []string{"Файл .conf Amnezia не загружен."}}
	}
	return AmneziaInfo{
		Loaded:     true,
		Name:       c.DisplayName(),
		Kind:       c.Kind(),
		Endpoint:   c.Peer.Endpoint,
		Address:    strings.Join(c.Iface.Address, ", "),
		Obfuscated: c.hasObf(),
		Lines:      amneziaSummary(c),
	}
}

// LoadAmneziaFile сохраняет и проверяет .conf из выбранного файла.
func (a *App) LoadAmneziaFile(path string) (AmneziaInfo, error) {
	if strings.TrimSpace(path) == "" {
		return AmneziaInfo{}, errors.New("не выбран файл")
	}
	cfg, err := SaveAmnezia(a.dir, path)
	if err != nil {
		return AmneziaInfo{}, err
	}
	a.logf("загружен конфиг Amnezia «%s»: %s, сервер %s", cfg.DisplayName(), cfg.Kind(), cfg.Peer.Endpoint)
	return a.AmneziaStatus(), nil
}

// LoadAmneziaText разбирает текст конфига (вставка из буфера обмена).
func (a *App) LoadAmneziaText(text string) (AmneziaInfo, error) {
	if _, err := ParseAwgConf(text); err != nil {
		return AmneziaInfo{}, err
	}
	tmp := a.dir + "/amnezia-incoming.conf"
	if err := writeFilePrivate(tmp, text); err != nil {
		return AmneziaInfo{}, err
	}
	return a.LoadAmneziaFile(tmp)
}

// ForgetAmneziaFile убирает сохранённый конфиг.
func (a *App) ForgetAmneziaFile() error {
	if err := ForgetAmnezia(a.dir); err != nil {
		return err
	}
	a.logf("конфиг Amnezia удалён с этого компьютера")
	return nil
}

// SetupSSHServer — первая настройка сервера без загруженного Amnezia: прямой SSH и установка MTP.
// После этого загрузите .conf Amnezia, и дальше всё пойдёт через туннель.
func (a *App) SetupSSHServer(form Owner) error {
	mtpPy, err := readAsset("assets/mtp.py")
	if err != nil {
		return err
	}
	installSh, err := readAsset("assets/install.sh")
	if err != nil {
		return err
	}
	return a.startJob("install", func(logf func(string)) error {
		if c := activeAwg(); c != nil {
			logf("Внимание: конфиг Amnezia загружен, но установка идёт прямым SSH: " +
				"на этом шаге туннеля ещё нет")
		}
		form.HostFP = ""
		o, err := installServer(form, mtpPy, installSh, logf)
		if err != nil {
			return err
		}
		if err := a.SetOwner(o); err != nil {
			return err
		}
		logf("сервер сохранён на этом компьютере")
		if err := a.addOwnerKey(o, logf, adminAddKey); err != nil {
			logf("ключ владельца не создан: " + err.Error() + ". Создайте его на вкладке «Сервер».")
		}
		return nil
	})
}

// InstallAmneziaWG ставит на хост сервер AmneziaWG (если его там нет): userspace-реализацию
// amneziawg-go и amneziawg-tools, поднимает awg0 и читает готовый .conf.
// Так первый .conf можно получить прямо из программы, не ставя клиент Amnezia.
func (a *App) InstallAmneziaWG() error {
	o := a.Owner()
	if o == nil {
		return errors.New("сначала настройте сервер (IP, пользователь, пароль SSH, порт)")
	}
	script, err := readAsset("assets/amneziawg_setup.sh")
	if err != nil {
		return err
	}
	return a.startJob("amneziawg", func(logf func(string)) error {
		c, err := openServerSSH(o, func(f string, args ...any) { logf(fmt.Sprintf(f, args...)) }, false)
		if err != nil {
			return err
		}
		defer c.Close()
		res, err := sshRun(c, "cat > /tmp/usatyi-awg-setup.sh && bash /tmp/usatyi-awg-setup.sh '"+o.Host+"'", script, logf)
		if err != nil {
			return err
		}
		if res.Code != 0 {
			return errors.New("установка AmneziaWG не удалась: " + errorText(res))
		}
		conf, err := sshRun(c, "cat /etc/amnezia/amneziawg/awg0.conf", nil, nil)
		if err != nil {
			return err
		}
		if conf.Code != 0 || !strings.Contains(conf.Stdout, "[Interface]") {
			return errors.New("AmneziaWG установлен, но конфиг не найден: " + errorText(conf))
		}
		tmp := a.dir + "/amnezia-awg0.conf"
		if err := writeFilePrivate(tmp, conf.Stdout); err != nil {
			return err
		}
		_, err = a.LoadAmneziaFile(tmp)
		return err
	})
}

// writeFilePrivate — записать файл с правами 0600 (пароли и ключи).
func writeFilePrivate(path, text string) error {
	return writeFileMode(path, []byte(text), 0o600)
}
