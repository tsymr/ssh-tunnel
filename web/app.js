// SSH Tunnel 前端逻辑
const $ = (s) => document.querySelector(s);
const tunnelsEl = $("#tunnels");
const rowTpl = $("#rowTpl");
const toastEl = $("#toast");

let tunnels = [];
let status = {};
let selectedId = null;     // 当前选中查看日志的隧道
let currentLogId = null;   // 正在轮询日志的隧道
let logTimer = null;
let pendingSelectId = null; // 新建/编辑后自动选中

async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...opts,
  });
  if (res.status === 204) return null;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `请求失败 (${res.status})`);
  return data;
}

function toast(msg, kind = "info") {
  toastEl.textContent = msg;
  toastEl.className = "toast " + kind;
  toastEl.hidden = false;
  clearTimeout(toast._t);
  toast._t = setTimeout(() => (toastEl.hidden = true), 3500);
}

function fmtMode(m) { return m === "remote" ? "远程" : "本地"; }
function fmtAuth(a) { return a === "password" ? "密码" : "私钥"; }
function nameOf(t) { return t.label || `${t.user}@${t.host}`; }

function specOf(t) {
  if (t.forward_mode === "remote") {
    if (t.bind_addr) return `${t.host}:${t.bind_addr}:${t.remote_port} → 127.0.0.1:${t.local_port}`;
    return `${t.host}:${t.remote_port} → 127.0.0.1:${t.local_port}`;
  }
  const bind = t.bind_addr || "127.0.0.1";
  return `${bind}:${t.local_port} → ${t.host}:127.0.0.1:${t.remote_port}`;
}

// ===== 左侧列表（行节点复用，避免刷新跳动）=====
const rowNodes = {}; // id -> { el, tunnel, els }

function createRow(t) {
  const el = rowTpl.content.firstElementChild.cloneNode(true);
  const entry = {
    el,
    tunnel: t,
    els: {
      row: el,
      dot: el.querySelector(".rdot"),
      title: el.querySelector(".rtitle"),
      sub: el.querySelector(".rsub"),
      toggle: el.querySelector(".act-toggle"),
    },
  };
  el.addEventListener("click", () => selectTunnel(t.id));
  entry.els.toggle.addEventListener("click", (ev) => { ev.stopPropagation(); onToggle(t.id); });
  rowNodes[t.id] = entry;
  return entry;
}

function updateRow(entry, t) {
  entry.tunnel = t;
  const running = !!t.running;
  const e = entry.els;
  e.row.classList.toggle("running", running);
  e.row.classList.toggle("selected", t.id === selectedId);
  e.title.textContent = nameOf(t);
  const bits = [`${fmtMode(t.forward_mode)} · ${t.local_port}→${t.remote_port}`];
  if (running) bits.push(`PID ${t.pid}`);
  if (t.auth_method === "password" && !t.auth_ready) bits.push("⚠需密码");
  e.sub.textContent = bits.join(" · ");
  e.toggle.textContent = running ? "停止" : "启动";
  e.toggle.classList.toggle("primary", !running);
}

function render() {
  // 移除已不存在的行；若删的是当前选中项，清空详情
  for (const id of Object.keys(rowNodes)) {
    if (!tunnels.some((t) => t.id === id)) {
      rowNodes[id].el.remove();
      delete rowNodes[id];
      if (id === selectedId) clearSelection();
    }
  }

  let empty = tunnelsEl.querySelector(".empty");
  if (tunnels.length === 0) {
    if (!empty) {
      empty = document.createElement("div");
      empty.className = "empty";
      empty.textContent = "还没有隧道。点击右上角「新增隧道」开始。";
      tunnelsEl.appendChild(empty);
    }
    return;
  }
  if (empty) empty.remove();

  // 按后端顺序定位：位置已正确的行不动，新行按序插入
  let prev = null;
  for (const t of tunnels) {
    let entry = rowNodes[t.id];
    if (!entry) entry = createRow(t);
    updateRow(entry, t);
    const expectedNext = prev ? prev.nextSibling : tunnelsEl.firstChild;
    if (entry.el !== expectedNext) tunnelsEl.insertBefore(entry.el, expectedNext);
    prev = entry.el;
  }
}

async function refresh() {
  try {
    const [s, list] = await Promise.all([api("/api/status"), api("/api/tunnels")]);
    status = s;
    tunnels = list;
    const installedTxt = s.service_installed ? "· 已安装服务" : "· 未安装服务";
    $("#meta").textContent = `127.0.0.1:${s.port} · 运行中 ${s.running}/${s.total} · sshpass ${s.sshpass_available ? "可用" : "未装"} ${installedTxt}`;
    $("#sidebarCount").textContent = `${s.running}/${s.total}`;
    $("#btnInstall").hidden = s.service_installed;
    render();
    if (selectedId) updateDetail();
    if (pendingSelectId && rowNodes[pendingSelectId]) {
      selectTunnel(pendingSelectId);
      pendingSelectId = null;
    }
  } catch (e) {
    toast("加载失败：" + e.message, "error");
  }
}

async function onToggle(id) {
  const running = rowNodes[id]?.tunnel.running;
  try {
    await api(`/api/tunnels/${id}/${running ? "stop" : "start"}`, { method: "POST" });
    toast(running ? "已停止" : "已启动");
    await refresh();
    if (id === selectedId) await loadLog();
  } catch (e) {
    toast(e.message, "error");
  }
}

async function onDelete() {
  const t = tunnels.find((x) => x.id === selectedId);
  if (!t) return;
  if (!confirm(`确定删除隧道「${nameOf(t)}」？`)) return;
  try {
    await api(`/api/tunnels/${t.id}`, { method: "DELETE" });
    toast("已删除");
    clearSelection();
    await refresh();
  } catch (e) {
    toast(e.message, "error");
  }
}

// ===== 选中 & 右侧详情/日志 =====
function selectTunnel(id) {
  selectedId = id;
  for (const rid in rowNodes) rowNodes[rid].els.row.classList.toggle("selected", rid === id);
  updateDetail();
  startLogPolling(id);
}

function clearSelection() {
  selectedId = null;
  for (const rid in rowNodes) rowNodes[rid].els.row.classList.remove("selected");
  stopLogPolling();
  $("#detailName").textContent = "未选择隧道";
  $("#detailSpec").textContent = "从左侧选择一个隧道查看详情与日志";
  $("#logBox").textContent = "";
  $("#btnRefreshLog").disabled = true;
  $("#btnDetailEdit").disabled = true;
  $("#btnDetailDelete").disabled = true;
}

function updateDetail() {
  const t = tunnels.find((x) => x.id === selectedId);
  if (!t) { clearSelection(); return; }
  $("#detailName").textContent = nameOf(t);
  const meta = [specOf(t), `${fmtMode(t.forward_mode)} · ${fmtAuth(t.auth_method)}`];
  if (t.running) meta.push(`PID ${t.pid}`);
  if (t.auth_method === "password" && !t.auth_ready) meta.push("⚠需要密码");
  $("#detailSpec").textContent = meta.join("   ·   ");
  $("#btnRefreshLog").disabled = false;
  $("#btnDetailEdit").disabled = false;
  $("#btnDetailDelete").disabled = false;
}

function startLogPolling(id) {
  stopLogPolling();
  currentLogId = id;
  loadLog();
  logTimer = setInterval(() => { if (currentLogId) loadLog(); }, 2000);
}
function stopLogPolling() {
  if (logTimer) { clearInterval(logTimer); logTimer = null; }
  currentLogId = null;
}

async function loadLog() {
  if (!currentLogId) return;
  try {
    const data = await api(`/api/tunnels/${currentLogId}/log?tail=131072`);
    const box = $("#logBox");
    box.textContent = data.log || "（暂无日志，启动隧道后将显示 ssh 输出）";
    box.scrollTop = box.scrollHeight;
  } catch (e) {
    toast(e.message, "error");
  }
}

$("#btnRefreshLog").addEventListener("click", loadLog);
$("#btnDetailEdit").addEventListener("click", () => {
  const t = tunnels.find((x) => x.id === selectedId);
  if (t) openForm(t);
});
$("#btnDetailDelete").addEventListener("click", onDelete);

// ===== 表单 =====
const formModal = $("#formModal");
const form = $("#tunnelForm");
let editingId = null;

function closeForm() { formModal.hidden = true; form.reset(); editingId = null; }

async function loadPresets() {
  try {
    const presets = await api("/api/presets");
    const sel = $("#preset");
    presets.forEach((p, i) => {
      const opt = document.createElement("option");
      opt.value = i;
      opt.textContent = p.label;
      sel.appendChild(opt);
    });
  } catch (_) {}
}

function fillForm(t) {
  form.label.value = t.label || "";
  form.host.value = t.host || "";
  form.user.value = t.user || "";
  form.local_port.value = t.local_port || "";
  form.remote_port.value = t.remote_port || "";
  form.bind_addr.value = t.bind_addr || "";
  form.key_file.value = t.key_file || "";
  document.querySelector(`input[name="forward_mode"][value="${t.forward_mode || "local"}"]`).checked = true;
  document.querySelector(`input[name="auth_method"][value="${t.auth_method || "key"}"]`).checked = true;
  form.password.value = "";
  syncPasswordVisibility();
}

function openForm(t = null) {
  editingId = t ? t.id : null;
  $("#formTitle").textContent = t ? "编辑隧道" : "新增隧道";
  form.reset();
  $("#preset").value = "";
  if (t) fillForm(t);
  syncPasswordVisibility();
  formModal.hidden = false;
}

function syncPasswordVisibility() {
  const isPwd = document.querySelector('input[name="auth_method"]:checked').value === "password";
  $("#passwordWrap").hidden = !isPwd;
}

$("#preset").addEventListener("change", async () => {
  const i = $("#preset").value;
  if (i === "") return;
  const presets = await api("/api/presets");
  fillForm({ ...presets[Number(i)], id: "" });
});

document.querySelectorAll('input[name="auth_method"]').forEach((r) =>
  r.addEventListener("change", syncPasswordVisibility)
);

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(form);
  const body = {
    label: fd.get("label").trim(),
    host: fd.get("host").trim(),
    user: fd.get("user").trim(),
    local_port: Number(fd.get("local_port")),
    remote_port: Number(fd.get("remote_port")),
    bind_addr: fd.get("bind_addr").trim(),
    key_file: fd.get("key_file").trim(),
    forward_mode: fd.get("forward_mode"),
    auth_method: fd.get("auth_method"),
    password: fd.get("password"),
  };
  if (body.auth_method !== "password") body.password = "";
  try {
    if (editingId) {
      await api(`/api/tunnels/${editingId}`, { method: "PUT", body: JSON.stringify(body) });
      pendingSelectId = editingId;
      toast("已保存");
    } else {
      const created = await api("/api/tunnels", { method: "POST", body: JSON.stringify(body) });
      pendingSelectId = created.id;
      toast("已创建");
    }
    closeForm();
    await refresh();
  } catch (e) {
    toast(e.message, "error");
  }
});

$("#btnNew").addEventListener("click", () => openForm());
$("#btnInstall").addEventListener("click", () => {
  toast("Web 无法直接安装服务，请在终端运行：\n./ssh-tunnel-web --install", "info");
});

document.addEventListener("click", (e) => {
  if (e.target.matches("[data-close]")) closeForm();
  if (e.target === formModal) closeForm();
});

(async () => {
  await loadPresets();
  await refresh();
  setInterval(refresh, 4000);
})();
