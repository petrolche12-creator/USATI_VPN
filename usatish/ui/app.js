// Усатый VPN: логика интерфейса. Все данные от программы выводятся через textContent.
(() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const KEEP = "keep";
  const params = new URLSearchParams(location.search);
  let token = params.get("t") || "";

  let state = null;       // /api/state
  let keys = null;        // /api/keys
  let srv = null;         // /api/server
  let serverKeys = null;  // /api/server/keys
  let lastKeysSig = "";
  let lastSrvSig = "";
  let jobTimer = null;

  // ---------- связь с программой

  async function api(path, body) {
    const opt = { method: body === undefined ? "GET" : "POST", headers: { "X-Token": token } };
    if (body !== undefined) {
      opt.headers["Content-Type"] = "application/json";
      opt.body = JSON.stringify(body);
    }
    const res = await fetch("/api/" + path, opt);
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || "ошибка " + res.status);
    return data;
  }

  function el(tag, attrs, ...children) {
    const n = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === false || v === null || v === undefined) continue;
      if (k === "class") n.className = v;
      else if (k === "text") n.textContent = v;
      else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
      else if (v === true) n.setAttribute(k, "");
      else n.setAttribute(k, String(v));
    }
    for (const c of children.flat()) {
      if (c === null || c === undefined || c === false) continue;
      n.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
    return n;
  }

  let toastTimer = null;
  function toast(msg, ms = 3200) {
    const t = $("toast");
    t.textContent = msg;
    t.classList.remove("hidden");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => t.classList.add("hidden"), ms);
  }

  function fail(e) {
    toast(e && e.message ? e.message : String(e), 5000);
  }

  // ---------- окна (диалоги)

  let modalLocked = false; // пока ставится сервер, окно нельзя закрыть клавишей Esc

  // Окно с кнопками. У кнопки с onClick во время работы она заблокирована; при ошибке
  // показываем сообщение и окно не закрываем. Если обработчик вернул KEEP, окно остаётся
  // открытым (например, когда внутри открылось следующее окно).
  function modal(title, body, buttons) {
    const d = $("dlg");
    if (d.open) d.close();
    d.innerHTML = "";
    const close = () => d.close();
    const foot = el("div", { class: "modal-foot" });
    const btnRefs = {};
    for (const b of buttons) {
      const btn = el("button", { class: b.cls || "outline", type: "button", disabled: !!b.disabled }, b.text);
      btn.addEventListener("click", async () => {
        if (!b.onClick) return close();
        const wasDisabled = btn.disabled;
        btn.disabled = true;
        try {
          const r = await b.onClick(close, btnRefs);
          if (r !== KEEP) close();
        } catch (e) {
          fail(e);
        } finally {
          btn.disabled = wasDisabled;
        }
      });
      btnRefs[b.key || b.text] = btn;
      foot.append(btn);
    }
    d.append(el("h2", { text: title }), el("div", { class: "modal-body" }, body), foot);
    d.showModal();
    return { close, refs: btnRefs };
  }
  $("dlg").addEventListener("cancel", (e) => { if (modalLocked) e.preventDefault(); });

  // ---------- настройки

  let settingsData = null;

  async function refreshSettings() {
    try {
      settingsData = await api("settings");
      renderSettings();
    } catch (e) { fail(e); }
  }

  const MODE_PROTOCOL = {
    game: "UDP (MTP/1, иначе WireGuard)",
    normal: "как в ключе",
    anon: "MTP/3 (маскировка под TLS)",
  };

  function protocolText(s) {
    if (!s.advanced) {
      return "Сейчас протокол выбирает режим: " + (MODE_PROTOCOL[s.mode] || "") + ". Чтобы выбрать протокол самому, включите переключатель ниже.";
    }
    if (s.protocol === "auto") {
      return "Протокол выбирает режим: " + (MODE_PROTOCOL[s.mode] || "") + ".";
    }
    const picked = s.protocols.find((p) => p.id === s.protocol);
    return "Выбран вручную: " + (picked ? picked.label : s.protocol) + ". Работает для всех ключей, где этот способ есть.";
  }

  function renderSettings() {
    const s = settingsData;
    if (!s) return;
    // режим: доступен всегда
    const modeBox = $("modeList");
    modeBox.innerHTML = "";
    for (const m of s.modes) {
      const input = el("input", { type: "radio", name: "mode", value: m.id, checked: m.id === s.mode });
      input.addEventListener("change", () => saveSettings({ mode: m.id }));
      modeBox.append(el("label", { class: "opt" }, input,
        el("span", {}, el("strong", { text: m.label }), el("small", { text: m.hint }))));
    }
    // протокол: вручную, только если переключатель включён
    $("advOn").checked = s.advanced;
    const shownProto = s.advanced ? s.protocol : "auto";
    const protoBox = $("protoList");
    protoBox.innerHTML = "";
    for (const p of s.protocols) {
      const input = el("input", { type: "radio", name: "proto", value: p.id, checked: p.id === shownProto });
      input.addEventListener("change", () => saveSettings({ advanced: true, protocol: p.id }));
      protoBox.append(el("label", { class: "opt" }, input,
        el("span", {}, el("strong", { text: p.label }), el("small", { text: p.hint }))));
    }
    protoBox.classList.toggle("hidden", !s.advanced);
    $("advText").textContent = protocolText(s);
    const busy = state && (state.phase === "connected" || state.phase === "connecting");
    $("modeNotice").classList.toggle("hidden", !busy);
  }

  async function saveSettings(patch) {
    const body = Object.assign({ advanced: settingsData.advanced, mode: settingsData.mode, protocol: settingsData.protocol }, patch);
    try {
      settingsData = await api("settings/save", body);
      renderSettings();
      toast("Сохранено. Применится при следующем подключении", 3000);
    } catch (e) {
      fail(e);
      await refreshSettings();
    }
  }

  $("advOn").addEventListener("change", (e) => saveSettings({ advanced: e.target.checked }));

  // ---------- вкладки

  for (const tab of document.querySelectorAll(".tab")) {
    tab.addEventListener("click", () => {
      for (const t of document.querySelectorAll(".tab")) t.classList.toggle("is-active", t === tab);
      const name = tab.dataset.tab;
      $("page-connect").classList.toggle("is-active", name === "connect");
      $("page-server").classList.toggle("is-active", name === "server");
      $("page-settings").classList.toggle("is-active", name === "settings");
      if (name === "server") refreshServer(true);
      if (name === "settings") refreshSettings();
    });
  }

  // ---------- состояние VPN

  const TONE = { idle: "neutral", connecting: "working", connected: "active", error: "error" };
  const TITLE = { idle: "Не подключено", connecting: "Подключаюсь…", connected: "Подключено", error: "Не получилось подключиться" };

  function selectedKey() {
    if (!keys) return null;
    return keys.keys.find((k) => k.id === keys.selected) || null;
  }

  function duration(sec) {
    const h = Math.floor(sec / 3600), m = Math.floor(sec / 60) % 60, s = sec % 60;
    return h + ":" + String(m).padStart(2, "0") + ":" + String(s).padStart(2, "0");
  }

  function bytes(n) {
    if (n < 1024) return n + " Б";
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " КБ";
    if (n < 1024 * 1024 * 1024) return (n / (1024 * 1024)).toFixed(1) + " МБ";
    return (n / (1024 * 1024 * 1024)).toFixed(2) + " ГБ";
  }

  function dateText(ts) {
    if (!ts) return "без срока";
    const d = new Date(ts * 1000);
    return String(d.getDate()).padStart(2, "0") + "." + String(d.getMonth() + 1).padStart(2, "0") + "." + d.getFullYear();
  }

  function renderStatus() {
    if (!state) return;
    const phase = state.phase;
    const card = $("statusCard");
    card.className = "card status tone-" + (TONE[phase] || "neutral");
    $("stTitle").textContent = TITLE[phase] || "Не подключено";

    const s = state.session;
    const sel = selectedKey();
    $("stKey").textContent = s ? "Ключ «" + s.keyName + "» · сервер " + s.host
      : sel ? "Выбран ключ «" + sel.name + "»" : "Ключ не выбран";
    // во время подключения показываем сеанс; иначе — что будет использовано при следующем подключении
    const effErr = keys && keys.effective && keys.effective.error ? keys.effective.error : "";
    const eff = keys && keys.effective && !effErr ? keys.effective : null;
    const live = s && (phase === "connected" || phase === "connecting");
    let shown = null;
    if (live) shown = { t: s.transportLabel, r: s.routingLabel, m: s.modeLabel };
    else if (sel && eff) shown = { t: eff.transportLabel, r: eff.routingLabel, m: eff.modeLabel };
    else if (sel && !effErr) shown = { t: sel.transportLabel, r: sel.routingLabel };
    else if (s) shown = { t: s.transportLabel, r: s.routingLabel, m: s.modeLabel };
    const modeNote = shown && shown.m && shown.m !== "Обычный" ? " · " + shown.m.toLowerCase() + " режим" : "";
    $("stDetails").textContent = shown ? "Способ: " + shown.t + " · " + shown.r + modeNote
      : sel && effErr ? "Для этого ключа выбранный режим недоступен" : "Добавьте ключ, который вам прислали";

    const preErr = phase === "idle" && sel && keys && keys.effective && keys.effective.error ? keys.effective.error : "";
    const msg = (phase === "error" || (phase !== "connected" && state.message && state.message !== "Не подключено" && state.message !== "Отключено"))
      ? state.message : preErr;
    $("stMsg").textContent = msg;
    $("stMsg").classList.toggle("hidden", !msg);

    if (phase === "connected") {
      const sec = Math.max(0, Math.floor((state.nowMs - state.startedAt) / 1000));
      $("stTraffic").textContent = "Сеанс " + duration(sec) + " · получено " + bytes(state.rx) + " · отправлено " + bytes(state.tx);
    } else {
      $("stTraffic").textContent = "";
    }

    let quota = "";
    if (state.used !== null && state.used !== undefined) {
      const lim = state.limit || 0;
      quota = "На сервере: " + (lim > 0 ? bytes(state.used) + " из " + bytes(lim) : bytes(state.used) + ", без лимита") +
        " · срок: " + dateText(state.expires || 0);
    } else if (phase === "connected" && s && !s.transport.startsWith("MTP")) {
      quota = "Остаток на сервере виден при подключении по MTP";
    } else if (phase === "connected") {
      quota = "Жду данные сервера…";
    }
    $("stQuota").textContent = quota;
    $("stQuota").classList.toggle("hidden", !quota);
    const lim = state.limit || 0;
    const showBar = state.used !== null && state.used !== undefined && lim > 0;
    $("quotaWrap").classList.toggle("hidden", !showBar);
    if (showBar) $("quotaBar").style.width = Math.min(100, Math.round(state.used * 100 / lim)) + "%";

    const main = $("mainBtn");
    if (phase === "connected") {
      main.textContent = "Отключить";
      main.className = "danger wide big";
      main.disabled = false;
    } else if (phase === "connecting") {
      main.textContent = "Отменить подключение";
      main.className = "danger wide big";
      main.disabled = false;
    } else {
      main.className = "primary wide big";
      if (!sel) {
        main.textContent = keys && keys.keys.length ? "Выберите ключ" : "Сначала добавьте ключ";
        main.disabled = true;
      } else if (sel.transport === "SS") {
        main.textContent = "Скопировать ссылку ss:// для Shadowsocks";
        main.disabled = false;
      } else {
        main.textContent = "Подключить «" + sel.name + "»";
        main.disabled = false;
      }
    }
  }

  async function onMainClick() {
    if (!state) return;
    try {
      if (state.phase === "connected" || state.phase === "connecting") {
        await api("disconnect", {});
      } else {
        const sel = selectedKey();
        if (!sel) return;
        if (sel.transport === "SS") return copySS(sel.id);
        await api("connect", { id: sel.id });
      }
    } catch (e) {
      fail(e);
    }
    pollState();
  }

  async function pollState() {
    try {
      state = await api("state");
      renderStatus();
      renderKeys();
    } catch (e) {
      // программа ещё не ответила: подождём следующего опроса
    }
  }

  // ---------- ключи

  function keySig() {
    return JSON.stringify([keys.selected, state && state.phase, keys.keys.map((k) => [k.id, k.name, k.transportLabel, k.routingLabel, k.active, k.supports, k.stat])]);
  }

  function renderKeys(force) {
    if (!keys) return;
    const sig = keySig();
    if (!force && sig === lastKeysSig) return;
    lastKeysSig = sig;
    const list = $("keyList");
    list.innerHTML = "";
    $("keysEmpty").classList.toggle("hidden", keys.keys.length > 0);
    for (const k of keys.keys) list.append(keyRow(k));
  }

  // Трафик по ключу: последние данные сервера, которые пришли при подключении.
  function trafficText(k) {
    if (!k.stat) return "трафик: появится после подключения";
    const lim = k.stat.limit || 0;
    const used = bytes(k.stat.used) + (lim > 0 ? " из " + bytes(lim) : ", без лимита");
    return "трафик: " + used + " · срок: " + dateText(k.stat.expires) + " · данные на " + stampText(k.stat.at);
  }

  function stampText(ms) {
    const d = new Date(ms);
    const p = (n) => String(n).padStart(2, "0");
    return p(d.getDate()) + "." + p(d.getMonth() + 1) + " " + p(d.getHours()) + ":" + p(d.getMinutes());
  }

  function keyRow(k) {
    const phase = state ? state.phase : "idle";
    const connected = k.active && phase === "connected";
    const working = k.active && phase === "connecting";
    const busyNow = connected || working;
    return el("div", { class: "key-row" + (k.selected ? " is-selected" : ""), onclick: () => selectKey(k.id) },
      el("div", { class: "radio" + (k.selected ? " on" : "") }),
      el("div", { class: "grow" },
        el("div", { class: "row-title" },
          el("strong", { text: k.name }),
          connected ? el("span", { class: "badge ok", text: "подключён" }) : null,
          working ? el("span", { class: "badge", text: "подключаюсь…" }) : null,
        ),
        el("div", { class: "muted small", text: "сервер " + (k.host || "—") }),
        el("div", { class: "muted small", text: k.transportLabel + " · " + k.routingLabel }),
        el("div", { class: "muted small", text: trafficText(k) }),
      ),
      el("button", {
        class: busyNow ? "danger small-btn" : "primary small-btn",
        onclick: (e) => { e.stopPropagation(); if (busyNow) disconnect(); else if (k.transport === "SS") copySS(k.id); else connectKey(k.id); },
      }, busyNow ? "Отключить" : k.transport === "SS" ? "Скопировать ss://" : "Подключить"),
      el("button", { class: "icon", title: "Настройки ключа", onclick: (e) => { e.stopPropagation(); keySettings(k.id); } }, "⋮"),
    );
  }

  async function refreshKeys() {
    try {
      keys = await api("keys");
      renderKeys();
      renderStatus();
    } catch (e) {
      // программа ещё не ответила
    }
  }

  async function selectKey(id) {
    try {
      await api("keys/select", { id });
      await refreshKeys();
    } catch (e) { fail(e); }
  }

  async function connectKey(id) {
    try {
      await api("connect", { id });
    } catch (e) { fail(e); }
    pollState();
  }

  async function disconnect() {
    try { await api("disconnect", {}); } catch (e) { fail(e); }
    pollState();
  }

  async function copySS(id) {
    try {
      const r = await api("copy-ss", { id });
      await navigator.clipboard.writeText(r.link);
      toast("Ссылка ss:// скопирована. Вставьте её в приложение Shadowsocks", 4500);
    } catch (e) { fail(e); }
  }

  function keySettings(id) {
    const k = keys.keys.find((x) => x.id === id);
    if (!k) return;
    let transport = k.transport;
    let routing = k.routing;
    const trBox = el("div");
    const rtBox = el("div");
    const alias = el("input", { type: "text", value: k.alias || "", placeholder: "как назвать у себя" });
    const hostLine = el("p", { class: "muted", text: "Сервер " + (k.host || "—") + " · ключ «" + k.name + "»" });
    const haveLine = el("p", { class: "small muted", text: "В ключе: " + keys.transports.filter((t) => k.supports[t.id]).map((t) => t.label).join(", ") });

    const drawTransports = () => {
      trBox.innerHTML = "";
      for (const t of keys.transports) {
        const ok = !!k.supports[t.id];
        const input = el("input", { type: "radio", name: "tr", value: t.id, checked: t.id === transport, disabled: !ok });
        input.addEventListener("change", () => { transport = t.id; });
        trBox.append(el("label", { class: "opt" + (ok ? "" : " disabled") }, input,
          el("span", {}, el("strong", { text: t.label }), " ", el("span", { class: "small muted", text: t.own ? "свой протокол" : "стандарт" }),
            el("small", { text: t.hint + (ok ? "" : " — в этом ключе нет") }))));
      }
    };
    const drawRoutings = () => {
      rtBox.innerHTML = "";
      for (const r of keys.routings) {
        const unsupported = r.id === "APPS";
        const input = el("input", { type: "radio", name: "rt", value: r.id, checked: r.id === routing, disabled: unsupported });
        input.addEventListener("change", () => { routing = r.id; });
        rtBox.append(el("label", { class: "opt" + (unsupported ? " disabled" : "") }, input,
          el("span", {}, el("strong", { text: r.label }), el("small", { text: r.hint }))));
      }
    };
    drawTransports();
    drawRoutings();

    const body = el("div", {},
      hostLine, haveLine,
      el("label", { class: "field" }, "Название у вас"), alias,
      el("label", { class: "field" }, "Способ подключения"), trBox,
      el("label", { class: "field" }, "Что идёт через VPN"), rtBox,
    );
    const buttons = [
      {
        text: "Удалить с этого компьютера", cls: "danger", onClick: async (close) => {
          if (!confirm("Удалить ключ «" + k.name + "» с этого компьютера? На сервере он останется.")) return KEEP;
          await api("keys/delete", { id: k.id });
          toast("Ключ удалён с компьютера");
          await refreshKeys();
          return;
        },
      },
      k.supports.SS ? { text: "Скопировать ss://", cls: "outline", onClick: async () => { await copySS(k.id); return KEEP; } } : null,
      { text: "Отмена", cls: "ghost" },
      {
        text: "Сохранить", cls: "primary", onClick: async () => {
          await api("keys/update", { id: k.id, alias: alias.value, transport, routing, apps: k.apps || [] });
          toast("Настройки сохранены. Они применятся при следующем подключении", 4000);
          await refreshKeys();
        },
      },
    ].filter(Boolean);
    modal("Настройки ключа", body, buttons);
  }

  // ---------- добавление ключа

  function addKeyDialog(prefill) {
    const area = el("textarea", { placeholder: "mtp://…", spellcheck: "false" });
    if (prefill) area.value = prefill;
    const preview = el("div", { class: "preview", text: "Вставьте строку, которая начинается с mtp://" });
    let addEl = null; // кнопка «Добавить»: появляется сразу после открытия окна
    let timer = null;
    let seq = 0;      // номер проверки: ответы на устаревший текст игнорируются
    const pasteBtn = el("button", { class: "outline small-btn", type: "button" }, "Вставить из буфера");
    pasteBtn.addEventListener("click", async () => {
      try {
        area.value = await navigator.clipboard.readText();
        area.dispatchEvent(new Event("input"));
      } catch (e) {
        toast("Не получилось прочитать буфер: вставьте строку сочетанием Ctrl+V");
      }
    });
    const setOk = (v) => { if (addEl) addEl.disabled = !v; };
    const check = async () => {
      const text = area.value.trim();
      const my = ++seq;
      if (!text) {
        preview.textContent = "Вставьте строку, которая начинается с mtp://";
        return setOk(false);
      }
      try {
        const r = await api("keys/check", { text });
        if (my !== seq) return;
        const ways = (keys ? keys.transports : []).filter((t) => r.supports[t.id]).map((t) => t.label).join(", ");
        let note = "";
        if (r.existing) note = r.same ? "\nТакой ключ уже есть в списке" : "\nКлюч уже есть: строка обновится, настройки сохранятся";
        preview.textContent = "Имя: " + (r.name || "без имени") + "\nСервер: " + r.host + "\nСпособы: " + ways + note;
        setOk(!r.same);
      } catch (e) {
        if (my !== seq) return;
        preview.textContent = "Это не ключ mtp://…";
        setOk(false);
      }
    };
    const addKey = async () => {
      const r = await api("keys/add", { text: area.value.trim() });
      await api("keys/select", { id: r.id });
      toast(r.created ? "Ключ «" + r.name + "» добавлен" : r.replaced ? "Ключ «" + r.name + "» обновлён" : "Этот ключ уже есть в списке");
      await refreshKeys();
    };
    const body = el("div", {},
      el("p", { text: "Вставьте строку mtp://…, которую прислал владелец сервера." }),
      area,
      el("div", { class: "row" }, pasteBtn),
      el("label", { class: "field" }, "Что внутри"),
      preview,
    );
    const dlg = modal("Добавить ключ", body, [
      { text: "Добавить", cls: "primary", key: "add", disabled: true, onClick: addKey },
      { text: "Отмена", cls: "ghost" },
    ]);
    addEl = dlg.refs.add;
    area.addEventListener("input", () => { clearTimeout(timer); timer = setTimeout(check, 250); });
    if (prefill) check();
    area.focus();
  }

  // ---------- сервер

  function renderServer() {
    if (!srv) return;
    const sig = JSON.stringify(srv) + (serverKeys ? serverKeys.length : "-");
    if (sig === lastSrvSig) return;
    lastSrvSig = sig;
    if (!srv.configured) {
      $("srvTitle").textContent = "Сервер не настроен";
      $("srvStatus").textContent = "Укажите SSH-доступ к VPS. Установщик поставит сервер сам: MTP/1, MTP/2, MTP/3, WireGuard и Shadowsocks.";
      $("srvSetupBtn").textContent = "Настроить сервер";
      $("srvCheckBtn").classList.add("hidden");
      $("srvForgetBtn").classList.add("hidden");
      $("createKeyBtn").disabled = true;
    } else {
      $("srvTitle").textContent = "Сервер " + srv.host;
      $("srvSetupBtn").textContent = "Переустановить или обновить";
      $("srvCheckBtn").classList.remove("hidden");
      $("srvForgetBtn").classList.remove("hidden");
      $("createKeyBtn").disabled = !serverKeys;
      if (srv.error) {
        $("srvStatus").textContent = "Нет связи с сервером: " + srv.error + ". Проверьте адрес и SSH-доступ, затем нажмите «Обновить».";
      } else if (srv.checked) {
        const t = new Date(srv.checked);
        $("srvStatus").textContent = "Работает · VPN UDP " + srv.vpnPort + " · ключей: " + srv.users + ", сейчас в сети: " + srv.online +
          " · проверено в " + String(t.getHours()).padStart(2, "0") + ":" + String(t.getMinutes()).padStart(2, "0");
      } else {
        $("srvStatus").textContent = "Нажмите «Обновить», чтобы проверить сервер.";
      }
    }
    renderServerKeys();
  }

  function renderServerKeys() {
    const list = $("srvKeyList");
    list.innerHTML = "";
    if (!srv || !srv.configured) {
      $("srvKeysEmpty").textContent = "Сначала настройте сервер.";
      $("srvKeysEmpty").classList.remove("hidden");
      return;
    }
    if (!serverKeys) {
      $("srvKeysEmpty").textContent = "Список появится после проверки сервера.";
      $("srvKeysEmpty").classList.remove("hidden");
      return;
    }
    $("srvKeysEmpty").classList.toggle("hidden", serverKeys.length > 0);
    $("srvKeysEmpty").textContent = "Ключей пока нет. Нажмите «+ Создать ключ».";
    for (const u of serverKeys) {
      const lim = u.limit || 0;
      const badgeClass = u.status === "активен" ? "ok" : u.status === "отключён" ? "off" : "bad";
      list.append(el("div", { class: "card server-key", style: "margin:0" },
        el("div", { class: "head" },
          el("strong", { text: u.name }),
          el("span", { class: "badge " + badgeClass, text: u.status })),
        el("div", { class: "muted small", text: (lim > 0 ? bytes(u.used) + " из " + bytes(lim) : bytes(u.used) + ", без лимита") + " · срок: " + dateText(u.expires) }),
        el("div", { class: "row", style: "margin-top:6px;align-items:center" },
          el("span", { class: "small", text: u.online ? "● сейчас в сети" : "не в сети" }),
          el("button", { class: "outline small-btn", style: "margin-left:auto", onclick: () => keyActions(u) }, "Действия")),
      ));
    }
  }

  async function refreshServer(loadKeys) {
    try {
      srv = await api("server");
      renderServer();
      if (srv.configured && loadKeys) await refreshServerKeys();
    } catch (e) { /* программа ещё не ответила */ }
  }

  async function refreshServerKeys() {
    try {
      $("srvStatus").textContent = "Проверяю связь с сервером…";
      serverKeys = await api("server/keys");
      serverKeys = serverKeys.users || [];
      srv = await api("server");
      lastSrvSig = "";
      renderServer();
    } catch (e) {
      serverKeys = null;
      srv = await api("server").catch(() => srv);
      lastSrvSig = "";
      renderServer();
      fail(e);
    }
  }

  function setupDialog() {
    const s = srv || {};
    const host = el("input", { type: "text", value: s.host || "", placeholder: "IP или домен сервера" });
    const user = el("input", { type: "text", value: s.sshUser || "root" });
    const sshPort = el("input", { type: "number", value: s.sshPort || 22, min: 1, max: 65535 });
    const pass = el("input", { type: "password", placeholder: "пароль SSH" });
    const vpnPort = el("input", { type: "number", value: s.vpnPort || 443, min: 1, max: 65535 });
    const body = el("div", {},
      el("label", { class: "field" }, "Адрес сервера"), host,
      el("div", { class: "grid2" },
        el("div", {}, el("label", { class: "field" }, "Пользователь SSH"), user),
        el("div", {}, el("label", { class: "field" }, "Порт SSH"), sshPort)),
      el("label", { class: "field" }, "Пароль SSH"), pass,
      el("label", { class: "field" }, "Порт VPN (UDP)"), vpnPort,
      el("p", { class: "small muted", text: "Пароль нужен только для установки. Он хранится на этом компьютере в зашифрованном виде; после установки лучше сменить пароль root." }),
    );
    modal(s.configured ? "Переустановить сервер" : "Настроить сервер", body, [
      { text: "Отмена", cls: "ghost" },
      {
        text: "Установить", cls: "primary", onClick: async () => {
          await api("server/setup", {
            host: host.value.trim(), sshUser: user.value.trim(), sshPort: Number(sshPort.value),
            sshPass: pass.value, vpnPort: Number(vpnPort.value),
          });
          installLog();
          return KEEP;
        },
      },
    ]);
  }

  function installLog() {
    const pre = el("pre", { class: "logbox" });
    const dlg = modal("Установка сервера", pre, [{ text: "Готово", cls: "primary", key: "done", disabled: true }]);
    const done = dlg.refs.done;
    done.disabled = true;
    modalLocked = true;
    const tick = async () => {
      try {
        const job = await api("job");
        pre.textContent = job.lines.join("\n");
        pre.scrollTop = pre.scrollHeight;
        if (!job.running && job.done) {
          clearInterval(jobTimer);
          modalLocked = false;
          done.disabled = false;
                    if (!job.error) toast("Сервер установлен", 4000);
          refreshServer(true);
        }
      } catch (e) { /* ждём */ }
    };
    clearInterval(jobTimer);
    jobTimer = setInterval(tick, 800);
    tick();
  }

  function keyActions(u) {
    const enabled = u.status !== "отключён";
    const body = el("p", { class: "muted", text: "Ключ «" + u.name + "» на сервере." });
    const run = (fn, msg) => async () => { await fn(); toast(msg); await refreshServerKeys(); };
    modal("Ключ «" + u.name + "»", body, [
      { text: "Показать ключ", cls: "outline", onClick: async () => { const r = await api("server/key/show", { name: u.name }); showKeyResult(u.name, r.key, false); return KEEP; } },
      { text: enabled ? "Выключить" : "Включить", cls: "outline", onClick: run(() => api("server/key/action", { name: u.name, action: enabled ? "disable" : "enable" }), enabled ? "Ключ выключен" : "Ключ включён") },
      { text: "Сбросить трафик", cls: "outline", onClick: async () => {
        if (!confirm("Обнулить счётчик трафика ключа «" + u.name + "»?")) return KEEP;
        await api("server/key/action", { name: u.name, action: "reset" });
        toast("Счётчик сброшен");
        await refreshServerKeys();
      } },
      { text: "Удалить ключ", cls: "danger", onClick: async () => {
        if (!confirm("Удалить ключ «" + u.name + "» на сервере? Друг потеряет доступ.")) return KEEP;
        await api("server/key/action", { name: u.name, action: "delete" });
        toast("Ключ удалён");
        await refreshServerKeys();
      } },
      { text: "Закрыть", cls: "ghost" },
    ]);
  }

  function createKeyDialog() {
    const name = el("input", { type: "text", placeholder: "имя ключа, например Вася" });
    const days = el("input", { type: "number", value: 30, min: 0, max: 3650 });
    const limit = el("input", { type: "number", value: 10, min: 0, step: "0.1" });
    const body = el("div", {},
      el("label", { class: "field" }, "Имя"), name,
      el("div", { class: "grid2" },
        el("div", {}, el("label", { class: "field" }, "Срок, дней (0 — без срока)"), days),
        el("div", {}, el("label", { class: "field" }, "Лимит, ГБ (0 — без лимита)"), limit)),
      el("p", { class: "small muted", text: "Лимит считает весь трафик ключа, по любому способу подключения." }),
    );
    modal("Новый ключ", body, [
      { text: "Отмена", cls: "ghost" },
      {
        text: "Создать", cls: "primary", onClick: async () => {
          const r = await api("server/key/add", { name: name.value.trim(), days: Number(days.value), limit: Number(limit.value) });
          await refreshServerKeys();
          showKeyResult(name.value.trim(), r.key, true);
          return KEEP;
        },
      },
    ]);
  }

  function showKeyResult(name, key, created) {
    const pre = el("pre", { class: "keytext" }, key);
    const buttons = [{
      text: "Копировать", cls: "outline", onClick: async () => {
        try {
          await navigator.clipboard.writeText(key);
          toast("Ключ скопирован");
        } catch (e) { fail(e); }
        return KEEP;
      },
    }];
    if (created) {
      buttons.push({
        text: "Добавить себе на этот компьютер", cls: "primary", onClick: async () => {
          const r = await api("keys/add", { text: key });
          await api("keys/select", { id: r.id });
          toast(r.created ? "Ключ добавлен на этот компьютер" : "Этот ключ уже есть на компьютере");
          await refreshKeys();
        },
      });
    }
    buttons.push({ text: "Закрыть", cls: "ghost" });
    const body = el("div", {},
      el("p", { class: "muted", text: created ? "Ключ создан. Отправьте строку другу или добавьте себе." : "Строка ключа." }),
      pre,
    );
    modal(created ? "Ключ «" + name + "» создан" : "Ключ «" + name + "»", body, buttons);
  }

  // ---------- кнопки верхнего уровня

  $("mainBtn").addEventListener("click", onMainClick);
  $("addKeyBtn").addEventListener("click", () => addKeyDialog(null));
  $("srvSetupBtn").addEventListener("click", setupDialog);
  $("srvCheckBtn").addEventListener("click", () => refreshServerKeys());
  $("srvForgetBtn").addEventListener("click", async () => {
    if (!confirm("Забыть сервер на этом компьютере? Сам сервер не меняется, ключи на нём продолжают работать.")) return;
    try {
      await api("server/forget", {});
      serverKeys = null;
      await refreshServer(false);
    } catch (e) { fail(e); }
  });
  $("createKeyBtn").addEventListener("click", createKeyDialog);
  $("quitBtn").addEventListener("click", async () => {
    if (state && (state.phase === "connected" || state.phase === "connecting")) {
      if (!confirm("VPN сейчас подключён. Отключить и закрыть программу?")) return;
    }
    try { await api("quit", {}); } catch (e) { /* программа уже закрывается */ }
    $("overlay").classList.remove("hidden");
  });

  // ---------- запуск и опросы

  async function start() {
    await pollState();
    await refreshKeys();
    await refreshServer(false);
    setInterval(pollState, 1000);
    setInterval(refreshKeys, 3000);
    setInterval(() => api("ping", {}).catch(() => {}), 2000);
  }
  start();
})();
