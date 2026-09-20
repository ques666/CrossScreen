// CrossScreen control panel: polls the node state and renders the
// arrangement editor with draggable peer screens.
(function () {
  "use strict";

  var state = null;
  var cfg = null;
  var mode = "server";
  var pollTimer = null;
  var msgTimer = null;
  var drag = { active: false, id: null, el: null, offX: 0, offY: 0 };
  var PEER_COLORS = ["#64b4ff", "#c792ea", "#ff8a65", "#f7d154", "#4dd0e1", "#e57373"];

  var $ = function (id) { return document.getElementById(id); };

  function api(path, body) {
    return fetch(path, {
      method: body !== undefined ? "POST" : "GET",
      headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined
    }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (data) {
        if (!r.ok) { throw new Error(data.error || ("HTTP " + r.status)); }
        return data;
      });
    });
  }

  // showMsg displays a transient save/error message under the form.
  function showMsg(text, kind) {
    var el = $("cfgMsg");
    el.textContent = text;
    el.className = "hint cfg-msg" + (kind ? " " + kind : "");
    if (msgTimer) { clearTimeout(msgTimer); }
    if (text) {
      msgTimer = setTimeout(function () { el.textContent = ""; el.className = "hint cfg-msg"; }, 3000);
    }
  }

  // loadConfig prefills the form fields with the persisted settings.
  function loadConfig() {
    return api("/api/config").then(function (c) {
      cfg = c;
      if (c.device_name) { $("name").value = c.device_name; }
      if (c.listen_addr) { $("srvAddr").value = c.listen_addr; }
      if (c.client_addr) { $("cliAddr").value = c.client_addr; }
    }).catch(function () {});
  }

  function collectConfig() {
    return {
      device_name: $("name").value.trim(),
      listen_addr: $("srvAddr").value.trim() || "0.0.0.0:53317",
      client_addr: $("cliAddr").value.trim()
    };
  }

  function peerColor(hostname) {
    var h = 0;
    for (var i = 0; i < hostname.length; i++) { h = (h * 31 + hostname.charCodeAt(i)) >>> 0; }
    return PEER_COLORS[h % PEER_COLORS.length];
  }

  function refresh() {
    api("/api/state").then(function (s) {
      state = s;
      render();
    }).catch(function () {});
  }

  function startPoll() {
    stopPoll();
    pollTimer = setInterval(refresh, 600);
  }
  function stopPoll() {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
  }

  // ---------- header / controls ----------
  function renderHeader() {
    var pill = $("rolePill");
    var cap = $("capPill");
    if (!state || state.role === "none") {
      pill.textContent = "未连接";
      pill.className = "pill";
    } else {
      pill.textContent = (state.role === "server" ? "服务器 · " : "客户端 · ") + state.node_addr;
      pill.className = "pill on";
    }
    cap.textContent = state && state.capture ? "捕获已启用" : "捕获未启用";
    cap.className = "pill muted";

    var isServer = state && state.role === "server";
    var isClient = state && state.role === "client";
    $("btnStop").classList.toggle("hidden", !(isServer || isClient));
    $("btnStart").classList.toggle("hidden", isServer || isClient);
    $("btnConnect").classList.toggle("hidden", isServer || isClient);
    $("serverFields").classList.toggle("hidden", isServer || isClient);
    $("clientFields").classList.toggle("hidden", isServer || isClient);
    $("nodeInfo").textContent = isServer
      ? "监听 " + state.node_addr + "，共 " + state.peers.length + " 台对端"
      : isClient
        ? "已连接服务器 " + (state.server || "") + "（被动接收输入）"
        : "";
  }

  function renderDevices() {
    var box = $("devices");
    box.innerHTML = "";
    if (!state || state.role === "none") {
      box.innerHTML = '<p class="hint">暂无设备</p>';
      return;
    }
    var list = [];
    (state.local_screens || []).forEach(function (s, i) {
      list.push({ hostname: state.name, os: "本机", screens: [s], self: true });
    });
    (state.peers || []).forEach(function (p) { list.push(p); });

    if (list.length === 0) { box.innerHTML = '<p class="hint">暂无设备</p>'; return; }

    var seen = {};
    list.forEach(function (d) {
      var key = d.hostname + (d.self ? ".self" : "");
      if (seen[key]) return;
      seen[key] = true;
      var isActive = state.active === d.hostname && d.self === undefined ? false : state.active === d.hostname;
      var el = document.createElement("div");
      el.className = "device";
      var dot = document.createElement("span");
      dot.className = "dot " + (d.self ? "self" : "peer") + (isActive ? " active" : "");
      var info = document.createElement("div");
      info.className = "info";
      var n = document.createElement("div");
      n.className = "n";
      n.textContent = d.hostname + (d.self ? "（本机）" : "");
      var m = document.createElement("div");
      m.className = "m";
      m.textContent = (d.os || "") + " · " + (d.screens || []).length + " 屏" + (isActive ? " · 光标在此" : "");
      info.appendChild(n);
      info.appendChild(m);
      var tag = document.createElement("span");
      tag.className = "tag";
      tag.textContent = d.self ? "local" : (d.role || "client");
      el.appendChild(dot);
      el.appendChild(info);
      el.appendChild(tag);
      box.appendChild(el);
    });
  }

  // ---------- file transfers ----------

  var finishedXferSeen = {}; // key -> first-seen ms, to fade/expire terminal rows

  function humanBytes(n) {
    if (n == null || isNaN(n)) return "";
    if (n < 1024) return n + " B";
    var units = ["KB", "MB", "GB"];
    var v = n / 1024, i = 0;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return v.toFixed(v >= 10 ? 0 : 1) + " " + units[i];
  }

  function xferKey(t) { return t.direction + ":" + t.id + ":" + t.peer; }

  function fileSummary(t) {
    var n = t.files ? t.files.length : 0;
    var firstName = n === 1 ? t.files[0].name : (n + " 个文件");
    return firstName + " · " + humanBytes(t.total);
  }

  function respondTransfer(tid, accept) {
    api("/api/transfer/respond", { tid: tid, accept: accept }).then(refresh).catch(function (e) {
      showMsg("操作失败：" + e.message, "err");
    });
  }

  function renderTransfers() {
    var card = $("transfersCard");
    var box = $("transfers");
    var now = Date.now();
    var list = (state && state.transfers) || [];

    // Drop terminal rows ~8s after they first appear.
    var visible = list.filter(function (t) {
      var k = xferKey(t);
      var terminal = ["done", "declined", "failed", "canceled"].indexOf(t.state) >= 0;
      if (!terminal) { delete finishedXferSeen[k]; return true; }
      if (!(k in finishedXferSeen)) { finishedXferSeen[k] = now; return true; }
      return now - finishedXferSeen[k] < 8000;
    });

    if (visible.length === 0) {
      card.hidden = true;
      box.innerHTML = "";
      return;
    }
    card.hidden = false;

    var frag = document.createDocumentFragment();
    visible.forEach(function (t) {
      var terminal = ["done", "declined", "failed", "canceled"].indexOf(t.state) >= 0;
      var el = document.createElement("div");
      el.className = "xfer" + (terminal ? " fade" : "");

      var arrow = t.direction === "in" ? "⇣" : "⇡";
      var top = document.createElement("div");
      top.className = "xfer-top";
      var title = document.createElement("div");
      title.className = "xfer-title";
      title.textContent = arrow + " " + fileSummary(t);
      var st = document.createElement("div");
      st.className = "xfer-state";
      var pct = t.total > 0 ? Math.min(100, Math.round(t.done / t.total * 100)) : 0;

      if (t.state === "pending") {
        st.textContent = t.peer + " 请求发送";
      } else if (t.state === "receiving") {
        st.textContent = "接收中 " + pct + "%";
      } else if (t.state === "sending") {
        st.textContent = "发送中 " + pct + "% · " + t.peer;
      } else if (t.state === "done") {
        st.textContent = t.direction === "in" ? "已接收，可粘贴" : "已发送";
        st.classList.add("ok");
      } else if (t.state === "declined") {
        st.textContent = "已拒绝";
      } else if (t.state === "failed") {
        st.textContent = "失败" + (t.error ? "：" + t.error : "");
        st.classList.add("err");
      } else {
        st.textContent = "已取消";
      }
      top.appendChild(title);
      top.appendChild(st);
      el.appendChild(top);

      if (t.state === "receiving" || t.state === "sending") {
        var bar = document.createElement("div");
        bar.className = "xfer-bar";
        var fill = document.createElement("i");
        fill.style.width = pct + "%";
        bar.appendChild(fill);
        el.appendChild(bar);
      }

      if (t.state === "pending") {
        var acts = document.createElement("div");
        acts.className = "xfer-actions";
        var okBtn = document.createElement("button");
        okBtn.textContent = "接收";
        okBtn.addEventListener("click", function () { respondTransfer(t.id, true); });
        var noBtn = document.createElement("button");
        noBtn.className = "danger";
        noBtn.textContent = "拒绝";
        noBtn.addEventListener("click", function () { respondTransfer(t.id, false); });
        acts.appendChild(okBtn);
        acts.appendChild(noBtn);
        el.appendChild(acts);
      }
      frag.appendChild(el);
    });
    box.innerHTML = "";
    box.appendChild(frag);
  }

  // ---------- arrangement canvas ----------
  var PAD = 40;
  var SCALE_CAP = 0.42;

  function bounds(screens) {
    var minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    screens.forEach(function (s) {
      minX = Math.min(minX, s.x); minY = Math.min(minY, s.y);
      maxX = Math.max(maxX, s.x + s.w); maxY = Math.max(maxY, s.y + s.h);
    });
    return { minX: minX, minY: minY, w: maxX - minX, h: maxY - minY };
  }

  function renderCanvas() {
    var canvas = $("canvas");
    if (!state || !state.layout || !state.layout.screens.length) {
      canvas.innerHTML = '<p class="canvas-empty">启动服务器或连接后显示屏幕排列</p>';
      $("activeInfo").textContent = "";
      return;
    }
    if (drag.active) return; // don't rebuild while dragging

    var screens = state.layout.screens;
    var b = bounds(screens);
    var availW = canvas.clientWidth - PAD * 2;
    var availH = canvas.clientHeight - PAD * 2;
    var scale = Math.min(availW / b.w, availH / b.h, SCALE_CAP);
    if (!isFinite(scale) || scale <= 0) scale = 0.2;
    var ox = PAD - b.minX * scale;
    var oy = PAD - b.minY * scale;

    var selfHost = state.name;
    var frag = document.createDocumentFragment();
    screens.forEach(function (s) {
      var el = document.createElement("div");
      el.className = "screen";
      var isLocal = s.device === selfHost;
      if (isLocal) el.classList.add("local");
      var color = isLocal ? "#32f08c" : peerColor(s.device || "peer");
      el.style.left = (ox + s.x * scale) + "px";
      el.style.top = (oy + s.y * scale) + "px";
      el.style.width = Math.max(s.w * scale - 4, 40) + "px";
      el.style.height = Math.max(s.h * scale - 4, 28) + "px";
      el.style.borderColor = color;
      el.style.background = "rgba(0,0,0,.28)";
      el.style.color = "#fff";
      el.dataset.id = s.id;
      el.dataset.device = s.device || "";
      el.dataset.x = s.x;
      el.dataset.y = s.y;

      var name = document.createElement("div");
      name.className = "s-name";
      name.textContent = isLocal ? state.name + " 本机" : (s.device || "peer");
      var meta = document.createElement("div");
      meta.className = "s-meta";
      meta.textContent = s.w + "×" + s.h;
      el.appendChild(name);
      el.appendChild(meta);
      if (s.primary) {
        var badge = document.createElement("span");
        badge.className = "s-badge";
        badge.textContent = isLocal ? "主屏" : "主";
        el.appendChild(badge);
      }
      if (!isLocal) {
        el.addEventListener("pointerdown", onDragStart);
      }
      frag.appendChild(el);
    });
    canvas.innerHTML = "";
    canvas.appendChild(frag);

    var act = state.active;
    $("activeInfo").textContent = act && act !== selfHost
      ? "光标位于 " + act
      : act === selfHost ? "光标在本机" : "";
  }

  function onDragStart(e) {
    if (e.button !== 0) return;
    var el = e.currentTarget;
    var canvas = $("canvas");
    drag.active = true;
    drag.id = el.dataset.id;
    drag.el = el;
    el.classList.add("dragging");
    el.setPointerCapture(e.pointerId);
    var b = bounds(state.layout.screens);
    var scale = currentScale(canvas, b);
    var ox = PAD - b.minX * scale;
    var oy = PAD - b.minY * scale;
    drag.offX = e.clientX - (ox + parseInt(el.dataset.x, 10) * scale);
    drag.offY = e.clientY - (oy + parseInt(el.dataset.y, 10) * scale);

    var move = function (ev) {
      var b2 = bounds(state.layout.screens);
      var s2 = currentScale(canvas, b2);
      var o2x = PAD - b2.minX * s2;
      var o2y = PAD - b2.minY * s2;
      var vx = Math.round((ev.clientX - drag.offX - o2x) / s2);
      var vy = Math.round((ev.clientY - drag.offY - o2y) / s2);
      el.style.left = (o2x + vx * s2) + "px";
      el.style.top = (o2y + vy * s2) + "px";
      el.dataset.x = vx;
      el.dataset.y = vy;
    };
    var up = function (ev) {
      el.classList.remove("dragging");
      el.releasePointerCapture(ev.pointerId);
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", up);
      el.removeEventListener("pointercancel", up);
      drag.active = false;
      // persist placement
      api("/api/placement", { id: drag.id, x: parseInt(el.dataset.x, 10), y: parseInt(el.dataset.y, 10) })
        .then(refresh);
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", up);
    el.addEventListener("pointercancel", up);
  }

  function currentScale(canvas, b) {
    var availW = canvas.clientWidth - PAD * 2;
    var availH = canvas.clientHeight - PAD * 2;
    var s = Math.min(availW / b.w, availH / b.h, SCALE_CAP);
    return (isFinite(s) && s > 0) ? s : 0.2;
  }

  // ---------- actions ----------
  function bindActions() {
    $("roleSeg").addEventListener("click", function (e) {
      var btn = e.target.closest("button");
      if (!btn) return;
      mode = btn.dataset.role;
      document.querySelectorAll("#roleSeg button").forEach(function (b) {
        b.classList.toggle("active", b === btn);
      });
      $("serverFields").classList.toggle("hidden", mode !== "server");
      $("clientFields").classList.toggle("hidden", mode !== "client");
    });

    $("btnStart").addEventListener("click", function () {
      var name = $("name").value.trim();
      var addr = $("srvAddr").value.trim() || "0.0.0.0:53317";
      $("srvAddr").value = addr;
      showMsg("正在启动…", "");
      api("/api/server/start", { name: name, addr: addr }).then(function () {
        showMsg("服务器已启动，配置已保存", "ok");
        refresh();
      }).catch(function (e) { showMsg("启动失败：" + e.message, "err"); });
    });

    $("btnConnect").addEventListener("click", function () {
      var addr = $("cliAddr").value.trim();
      var name = $("name").value.trim();
      if (!addr) { showMsg("请填写服务器地址", "err"); return; }
      showMsg("正在连接…", "");
      api("/api/client/connect", { addr: addr, name: name }).then(function () {
        showMsg("已连接，服务器地址已保存", "ok");
        refresh();
      }).catch(function (e) { showMsg("连接失败：" + e.message, "err"); });
    });

    $("btnStop").addEventListener("click", function () {
      api("/api/stop", {}).then(refresh).catch(function (e) { showMsg(e.message, "err"); });
    });

    $("btnSave").addEventListener("click", function () {
      var data = collectConfig();
      if (!data.device_name) { showMsg("请填写设备名", "err"); return; }
      api("/api/config", data).then(function () {
        showMsg("配置已保存", "ok");
      }).catch(function (e) { showMsg("保存失败：" + e.message, "err"); });
    });

    var svcInstall = $("btnSvcInstall");
    var svcUninstall = $("btnSvcUninstall");
    if (svcInstall) svcInstall.addEventListener("click", function () {
      svcAction(svcInstall.textContent.indexOf("启动") >= 0 ? "install" : "install");
    });
    if (svcUninstall) svcUninstall.addEventListener("click", function () { svcAction("uninstall"); });

    $("btnReset").addEventListener("click", function () {
      if (!state || !state.layout) return;
      var ids = state.layout.screens
        .filter(function (s) { return s.device !== state.name; })
        .map(function (s) { return s.id; });
      var chain = Promise.resolve();
      ids.forEach(function (id) {
        chain = chain.then(function () { return api("/api/placement", { id: id, reset: true }); });
      });
      chain.then(refresh);
    });
  }

  function render() {
    renderHeader();
    renderDevices();
    renderTransfers();
    renderCanvas();
  }

  // ---------- windows secure-desktop service ----------

  function loadSvcStatus() {
    api("/api/win/service").then(function (v) {
      var field = $("svcField");
      if (!v.supported) { field.hidden = true; return; }
      field.hidden = false;
      var st = $("svcState");
      var inst = $("btnSvcInstall");
      var uninst = $("btnSvcUninstall");
      if (v.installed && v.running) {
        st.textContent = "已安装 · 运行中";
        st.className = "hint ok";
        inst.hidden = true; uninst.hidden = false;
      } else if (v.installed) {
        st.textContent = "已安装 · 未运行";
        st.className = "hint err";
        inst.hidden = false; inst.textContent = "启动服务"; uninst.hidden = false;
      } else {
        st.textContent = "未安装（锁屏键盘不可用）";
        st.className = "hint";
        inst.hidden = false; inst.textContent = "安装服务"; uninst.hidden = true;
      }
    }).catch(function () { $("svcField").hidden = true; });
  }

  function svcAction(action) {
    var st = $("svcState");
    st.textContent = "正在操作…（可能弹出 UAC 确认）";
    api("/api/win/service", { action: action }).then(function () {
      showMsg(action === "install" ? "服务已安装并启动" : "服务已卸载", "ok");
      loadSvcStatus();
    }).catch(function (e) {
      showMsg("服务操作失败：" + e.message, "err");
      loadSvcStatus();
    });
  }

  window.addEventListener("resize", function () { renderCanvas(); });
  bindActions();
  startPoll();
  loadConfig().then(refresh);
  loadSvcStatus();
})();
