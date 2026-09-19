"use strict";

const $ = (id) => document.getElementById(id);

const state = {
  keys: [],
  requests: [],
  logs: [],
  view: "keys",
  keyFilter: "all",
  keySearch: "",
  requestFilter: "pending",
  mails: [],
  mailCounts: null,
  mailFilter: { status: "", key: "", q: "" },
  mailHasMore: false,
  dialogMode: null, // { type: "create" } | { type: "edit", key } | { type: "approve", request }
};

const VIEWS = {
  keys: { title: "API keys", sub: "Create keys, set limits and watch usage." },
  requests: { title: "Access requests", sub: "Review API key requests submitted from the public site." },
  logs: { title: "Request log", sub: "Every authenticated request, newest first." },
  mails: { title: "Mail history", sub: "Every mail sent, with its sender, recipient, message and delivery status." },
  compose: { title: "Compose mail", sub: "Send an email as any API key, through its SMTP server and limits." },
};

// ---------- helpers ----------

async function api(method, path, body) {
  const res = await fetch("/admin/api" + path, {
    method,
    credentials: "same-origin",
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  if (res.status === 401 && path !== "/login") {
    showLogin();
    throw new Error("Your session has ended. Please sign in again.");
  }
  if (res.status === 204) return null;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || "Request failed (" + res.status + ")");
  return data;
}

// el builds DOM nodes. Text is always set with textContent so user data is never parsed as HTML.
function el(tag, props, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "text") node.textContent = v;
    else if (k === "class") node.className = v;
    else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v === true ? "" : v);
  }
  appendChildren(node, children);
  return node;
}

// appendChildren flattens nested arrays and skips null, undefined and false, so callers can
// pass conditional children like `cond && el(...)`.
function appendChildren(node, children) {
  for (const c of children.flat(Infinity)) {
    if (c === null || c === undefined || c === false) continue;
    node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
}

// setChildren replaces a node's children using the same rules as el(). Use it instead of
// replaceChildren, which neither flattens arrays nor skips empty values.
function setChildren(node, ...children) {
  node.replaceChildren();
  appendChildren(node, children);
}

const ICONS = {
  copy: '<rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>',
  edit: '<path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/>',
  pause: '<circle cx="12" cy="12" r="10"/><path d="M10 15V9M14 15V9"/>',
  play: '<circle cx="12" cy="12" r="10"/><path d="m10 8 6 4-6 4Z"/>',
  logs: '<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>',
  trash: '<path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>',
  key: '<circle cx="7.5" cy="15.5" r="5.5"/><path d="m21 2-9.6 9.6M15.5 7.5l3 3L22 7l-3-3"/>',
  inbox: '<path d="M22 12h-6l-2 3h-4l-2-3H2"/><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/>',
  activity: '<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>',
  mail: '<rect x="2" y="4" width="20" height="16" rx="2"/><path d="m22 7-10 6L2 7"/>',
  send: '<path d="m22 2-7 20-4-9-9-4Z"/><path d="M22 2 11 13"/>',
  history: '<path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5"/><path d="M12 7v5l4 2"/>',
  eye: '<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/>',
};

// icon returns an SVG built from the fixed ICONS table above (never from user data).
function icon(name) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "2");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  svg.innerHTML = ICONS[name];
  return svg;
}

function iconButton(name, label, onclick, extra) {
  return el("button", { type: "button", class: "btn btn-ghost btn-icon " + (extra || ""), title: label, "aria-label": label, onclick }, icon(name));
}

function relTime(value) {
  if (!value) return "Never";
  const diff = (Date.now() - new Date(value).getTime()) / 1000;
  if (diff < 60) return "Just now";
  if (diff < 3600) return Math.floor(diff / 60) + " min ago";
  if (diff < 86400) return Math.floor(diff / 3600) + " h ago";
  if (diff < 86400 * 30) return Math.floor(diff / 86400) + " d ago";
  return new Date(value).toLocaleDateString();
}

function fullTime(value) {
  return value ? new Date(value).toLocaleString() : "";
}

function fmt(n) {
  return Number(n || 0).toLocaleString();
}

function toast(message, tone = "success") {
  const node = el("div", { class: "toast toast-" + tone, role: tone === "error" ? "alert" : "status" }, el("span", { class: "dot" }), el("span", { text: message }));
  $("toasts").append(node);
  setTimeout(() => node.remove(), tone === "error" ? 6000 : 3500);
}

function emptyState(iconName, title, text, colspan) {
  const box = el("div", { class: "empty" }, icon(iconName), el("strong", { text: title }), el("span", { text }));
  return colspan ? el("tr", {}, el("td", { colspan: String(colspan) }, box)) : box;
}

function setError(id, message) {
  $(id).textContent = message || "";
  $(id).classList.toggle("hidden", !message);
}

function confirmAction(title, text, okLabel) {
  return new Promise((resolve) => {
    const dialog = $("confirm-dialog");
    $("confirm-title").textContent = title;
    $("confirm-text").textContent = text;
    $("confirm-ok").textContent = okLabel;
    const done = (value) => {
      dialog.removeEventListener("close", onClose);
      $("confirm-ok").onclick = null;
      resolve(value);
    };
    const onClose = () => done(false);
    $("confirm-ok").onclick = () => { dialog.removeEventListener("close", onClose); dialog.close(); done(true); };
    dialog.addEventListener("close", onClose);
    dialog.showModal();
  });
}

// ---------- auth / shell ----------

function showLogin() {
  $("app-view").classList.add("hidden");
  $("login-view").classList.remove("hidden");
  document.title = "Sign in · Mail Service";
}

function showApp(username) {
  $("login-view").classList.add("hidden");
  $("app-view").classList.remove("hidden");
  $("whoami").textContent = username;
  $("avatar").textContent = (username || "A").charAt(0);
  setView(location.hash.slice(1));
  refresh();
}

function setView(name) {
  if (!VIEWS[name]) name = "keys";
  state.view = name;
  for (const v of Object.keys(VIEWS)) $("view-" + v).classList.toggle("hidden", v !== name);
  document.querySelectorAll(".side-nav a[data-view]").forEach((a) => {
    const active = a.dataset.view === name;
    a.classList.toggle("active", active);
    if (active) a.setAttribute("aria-current", "page"); else a.removeAttribute("aria-current");
  });
  document.querySelector(".stats").classList.toggle("hidden", name === "compose" || name === "mails");
  $("page-title").textContent = VIEWS[name].title;
  $("page-sub").textContent = VIEWS[name].sub;
  document.title = VIEWS[name].title + " · Mail Service Admin";
  setSidebar(false);
  if (name === "mails" && !$("app-view").classList.contains("hidden")) loadMails();
}

function setSidebar(open) {
  $("sidebar").classList.toggle("open", open);
  $("scrim").hidden = !open;
  $("menu-btn").setAttribute("aria-expanded", String(open));
}

async function refresh() {
  try {
    const [keys, requests, logs] = await Promise.all([
      api("GET", "/keys"),
      api("GET", "/requests"),
      api("GET", "/logs?limit=200&key_id=" + encodeURIComponent($("log-filter").value || "0")),
    ]);
    state.keys = keys;
    state.requests = requests;
    state.logs = logs;
    renderAll();
  } catch (err) {
    toast(err.message, "error");
  }
  loadMails();
}

function renderAll() {
  renderStats();
  renderKeys();
  renderRequests();
  renderLogFilter();
  renderLogs();
  renderComposeKeys();
  renderMailKeyFilter();
}

// ---------- stats ----------

function renderStats() {
  const keys = state.keys;
  const active = keys.filter((k) => k.active).length;
  const supers = keys.filter((k) => k.is_super).length;
  const pending = state.requests.filter((r) => r.status === "pending").length;
  const mails = keys.reduce((sum, k) => sum + (k.usage ? k.usage.day : 0), 0);
  const hour = keys.reduce((sum, k) => sum + (k.usage ? k.usage.hour : 0), 0);

  $("stat-keys").textContent = fmt(keys.length);
  $("stat-keys-sub").textContent = supers ? supers + " super key" + (supers > 1 ? "s" : "") : "No super keys";
  $("stat-active").textContent = fmt(active);
  $("stat-active-sub").textContent = keys.length - active ? keys.length - active + " disabled" : "All keys enabled";
  $("stat-pending").textContent = fmt(pending);
  $("stat-mails").textContent = fmt(mails);
  $("stat-mails-sub").textContent = fmt(hour) + " in the last hour";

  $("nav-keys-count").textContent = keys.length ? String(keys.length) : "";
  $("nav-pending-count").textContent = String(pending);
  $("nav-pending-count").classList.toggle("hidden", pending === 0);
}

// ---------- keys ----------

function usageCell(key) {
  if (key.is_super) {
    return el("span", { class: "usage-unlimited" }, fmt(key.usage ? key.usage.day : 0) + " today · no limits");
  }
  const rows = [["H", "hour"], ["D", "day"], ["W", "week"], ["M", "month"]].map(([short, w]) => {
    const used = key.usage ? key.usage[w] : 0;
    const limit = key.limits[w];
    const pct = limit ? Math.min(100, (used / limit) * 100) : 0;
    const bar = el("span", { class: "bar" + (!limit ? " none" : pct >= 100 ? " full" : pct >= 80 ? " warn" : "") }, el("i"));
    bar.firstChild.style.width = pct + "%";
    return el("div", { class: "usage-row", title: w + ": " + fmt(used) + " of " + (limit ? fmt(limit) : "unlimited") },
      el("span", { class: "w", text: short }), bar, el("span", { class: "v", text: fmt(used) + " / " + (limit ? fmt(limit) : "∞") }));
  });
  return el("div", { class: "usage" }, rows);
}

function keyMatches(key) {
  const f = state.keyFilter;
  if (f === "active" && !key.active) return false;
  if (f === "disabled" && key.active) return false;
  if (f === "super" && !key.is_super) return false;
  const q = state.keySearch.trim().toLowerCase();
  if (!q) return true;
  return [key.name, key.address, key.key_prefix].some((v) => (v || "").toLowerCase().includes(q));
}

function renderKeys() {
  const body = $("keys-body");
  setChildren(body);
  if (!state.keys.length) {
    body.append(emptyState("key", "No API keys yet", "Create your first key, or approve an access request.", 7));
    return;
  }
  const keys = state.keys.filter(keyMatches);
  if (!keys.length) {
    body.append(emptyState("key", "No matching keys", "Try a different search or filter.", 7));
    return;
  }

  for (const key of keys) {
    const status = el("div", { class: "row" },
      el("span", { class: "badge " + (key.active ? "badge-success" : "badge-danger"), text: key.active ? "Active" : "Disabled" }),
      key.is_super && el("span", { class: "badge badge-primary plain", text: "Super" }));

    const ips = key.is_super
      ? el("span", { class: "muted small", text: "Any (super)" })
      : el("div", { class: "ips" }, key.allowed_ips.map((ip) => el("span", { class: "ip", text: ip === "*" ? "Any IP" : ip })));

    const actions = el("div", { class: "actions" },
      key.recoverable
        ? iconButton("copy", "Copy API key", (e) => copyKey(key, e.currentTarget))
        : el("button", { type: "button", class: "btn btn-ghost btn-icon", disabled: true, title: "Created before keys could be revealed", "aria-label": "Key cannot be revealed" }, icon("copy")),
      iconButton("mail", "Send test email", () => openTestDialog(key)),
      iconButton("edit", "Edit key", () => openKeyDialog({ type: "edit", key })),
      iconButton(key.active ? "pause" : "play", key.active ? "Disable key" : "Enable key", () => toggleKey(key)),
      iconButton("logs", "View request log", () => { $("log-filter").value = String(key.id); location.hash = "logs"; loadLogs(); }),
      iconButton("history", "View sent mail", () => showMailsFor(String(key.id))),
      iconButton("trash", "Delete key", () => deleteKey(key), "danger-hover"));

    body.append(el("tr", {},
      el("td", {},
        el("div", { class: "key-name", text: key.name }),
        key.address && el("span", { class: "sub", text: key.address }),
        el("code", { class: "key-prefix", text: key.key_prefix + "…" })),
      el("td", {}, ips),
      el("td", {}, usageCell(key)),
      el("td", {}, smtpCell(key)),
      el("td", {}, status),
      el("td", { class: "nowrap", title: fullTime(key.last_used_at) },
        el("span", { text: relTime(key.last_used_at) }),
        key.last_used_ip && el("span", { class: "sub mono", text: key.last_used_ip })),
      el("td", {}, actions)));
  }
}

function smtpCell(key) {
  if (!key.smtp) return el("span", { class: "muted small", text: "Default server" });
  return el("div", { class: "smtp-cell", title: key.smtp.host + ":" + key.smtp.port + " · " + key.smtp.from },
    el("span", { class: "mono", text: key.smtp.host + ":" + key.smtp.port }),
    el("span", { class: "sub", text: key.smtp.from }));
}

async function copyKey(key, button) {
  try {
    const { api_key } = await api("GET", "/keys/" + key.id + "/secret");
    try {
      await navigator.clipboard.writeText(api_key);
      toast("API key for “" + key.name + "” copied to clipboard");
    } catch {
      showSecret("API key: " + key.name, api_key, "Your browser blocked the clipboard, so copy the key below.");
    }
  } catch (err) {
    toast(err.message, "error");
  } finally {
    button.blur();
  }
}

async function toggleKey(key) {
  if (key.active) {
    const ok = await confirmAction("Disable “" + key.name + "”?",
      "Requests using this key will be rejected until you enable it again. Nothing is deleted.", "Disable key");
    if (!ok) return;
  }
  try {
    await api("POST", "/keys/" + key.id + (key.active ? "/disable" : "/enable"));
    toast(key.active ? "Key disabled" : "Key enabled");
    await refresh();
  } catch (err) {
    toast(err.message, "error");
  }
}

async function deleteKey(key) {
  const ok = await confirmAction("Delete “" + key.name + "”?",
    "Clients using this key will stop working immediately and its request log will be removed. Its mail history is kept. This cannot be undone.", "Delete key");
  if (!ok) return;
  try {
    await api("DELETE", "/keys/" + key.id);
    toast("Key deleted");
    await refresh();
  } catch (err) {
    toast(err.message, "error");
  }
}

// ---------- key dialog ----------

function openKeyDialog(mode) {
  state.dialogMode = mode;
  const form = $("key-form");
  const f = form.elements;
  form.reset();
  setError("key-form-error", "");
  $("approve-summary").classList.add("hidden");

  if (mode.type === "edit") {
    const k = mode.key;
    $("key-dialog-title").textContent = "Edit “" + k.name + "”";
    $("key-dialog-sub").textContent = "Changes apply to the next request made with this key.";
    $("key-submit").textContent = "Save changes";
    f.name.value = k.name;
    f.address.value = k.address;
    f.allowed_ips.value = k.allowed_ips.join(", ");
    f.limit_hour.value = k.limits.hour;
    f.limit_day.value = k.limits.day;
    f.limit_week.value = k.limits.week;
    f.limit_month.value = k.limits.month;
    f.is_super.checked = k.is_super;
    if (k.smtp) {
      f.smtp_enabled.checked = true;
      f.smtp_host.value = k.smtp.host;
      f.smtp_port.value = k.smtp.port;
      f.smtp_username.value = k.smtp.username;
      f.smtp_from.value = k.smtp.from;
    }
  } else if (mode.type === "approve") {
    const r = mode.request;
    $("key-dialog-title").textContent = "Approve request";
    $("key-dialog-sub").textContent = "Review the settings, then create the key for this requester.";
    $("key-submit").textContent = "Approve & create key";
    f.name.value = r.organization ? r.name + " (" + r.organization + ")" : r.name;
    f.address.value = [r.email, r.phone, r.address].filter(Boolean).join(" · ");
    f.allowed_ips.value = r.caller_ips || "*";
    const summary = $("approve-summary");
    setChildren(summary,
      el("strong", { text: r.name }), " <" + r.email + ">", r.phone && " · " + r.phone,
      r.expected_volume && el("span", { text: " · expects " + r.expected_volume + " mails/month" }));
    summary.classList.remove("hidden");
  } else {
    $("key-dialog-title").textContent = "New API key";
    $("key-dialog-sub").textContent = "The key is generated for you. You can copy it at any time later.";
    $("key-submit").textContent = "Create key";
    f.allowed_ips.value = "*";
  }
  const hasPassword = mode.type === "edit" && mode.key.smtp && mode.key.smtp.has_password;
  f.smtp_password.placeholder = hasPassword ? "•••••••• (saved)" : "App password";
  $("smtp-password-hint").textContent = hasPassword
    ? "Leave blank to keep the saved password. Clearing the username removes it."
    : "Stored encrypted. Never shown again.";
  syncSuper();
  syncSMTP();
  $("key-dialog").showModal();
  f.name.focus();
}

function syncSuper() {
  const f = $("key-form").elements;
  $("limits-fieldset").disabled = f.is_super.checked;
  f.allowed_ips.disabled = f.is_super.checked;
}

function syncSMTP() {
  $("smtp-fields").classList.toggle("hidden", !$("key-form").elements.smtp_enabled.checked);
}

function formInput() {
  const f = $("key-form").elements;
  const num = (name) => Math.max(0, parseInt(f[name].value, 10) || 0);
  return {
    name: f.name.value,
    address: f.address.value,
    allowed_ips: f.allowed_ips.value.split(/[\s,]+/).filter(Boolean),
    is_super: f.is_super.checked,
    limits: { hour: num("limit_hour"), day: num("limit_day"), week: num("limit_week"), month: num("limit_month") },
    smtp: f.smtp_enabled.checked ? {
      host: f.smtp_host.value,
      port: f.smtp_port.value,
      username: f.smtp_username.value,
      password: f.smtp_password.value,
      from: f.smtp_from.value,
    } : null,
  };
}

async function submitKeyForm(e) {
  e.preventDefault();
  const mode = state.dialogMode;
  const input = formInput();
  if (!input.name.trim()) {
    setError("key-form-error", "Name is required.");
    $("key-form").elements.name.focus();
    return;
  }
  if (input.smtp && (!input.smtp.host.trim() || !input.smtp.from.trim())) {
    setError("key-form-error", "SMTP host and From address are required when using a dedicated SMTP server.");
    $("key-form").elements[input.smtp.host.trim() ? "smtp_from" : "smtp_host"].focus();
    return;
  }
  const button = $("key-submit");
  button.disabled = true;
  setError("key-form-error", "");
  try {
    if (mode.type === "edit") {
      await api("PUT", "/keys/" + mode.key.id, input);
      $("key-dialog").close();
      toast("Changes saved");
    } else {
      const path = mode.type === "approve" ? "/requests/" + mode.request.id + "/approve" : "/keys";
      const created = await api("POST", path, input);
      $("key-dialog").close();
      if (mode.type === "approve") {
        showSecret("Request approved", created.api_key,
          "Send this key to " + mode.request.email + " over a secure channel.");
      } else {
        showSecret("API key created", created.api_key, "Share it securely. You can copy it again later from the keys table.");
      }
    }
    await refresh();
  } catch (err) {
    setError("key-form-error", err.message);
  } finally {
    button.disabled = false;
  }
}

function showSecret(title, value, sub) {
  $("secret-title").textContent = title;
  $("secret-sub").textContent = sub;
  $("secret-value").textContent = value;
  $("secret-copy").textContent = "Copy";
  $("secret-dialog").showModal();
}

// ---------- SMTP test ----------

let testKey = null;

function openTestDialog(key) {
  testKey = key;
  const f = $("test-form").elements;
  $("test-sub").textContent = key.smtp
    ? "Sends through " + key.smtp.host + ":" + key.smtp.port + " as " + key.smtp.from + "."
    : "This key uses the default SMTP server from the service settings.";
  setError("test-error", "");
  $("test-ok").classList.add("hidden");
  $("test-dialog").showModal();
  f.to.focus();
}

$("test-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const to = $("test-form").elements.to.value.trim();
  if (!to) {
    setError("test-error", "Enter an email address to send the test to.");
    return;
  }
  const button = $("test-submit");
  button.disabled = true;
  button.textContent = "Sending…";
  setError("test-error", "");
  $("test-ok").classList.add("hidden");
  try {
    const res = await api("POST", "/keys/" + testKey.id + "/smtp-test", { to });
    $("test-ok").textContent = res.message;
    $("test-ok").classList.remove("hidden");
  } catch (err) {
    setError("test-error", err.message);
  } finally {
    button.disabled = false;
    button.textContent = "Send test";
  }
});

// ---------- compose ----------

const EMAIL_RE = /^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/;

// parseRecipients splits the To field into unique addresses and reports invalid entries.
// "Name <a@b.c>" is accepted; the server validates again.
function parseRecipients(text) {
  const valid = [];
  const invalid = [];
  const seen = new Set();
  for (const part of text.split(/[,;\n]+/)) {
    const entry = part.trim();
    if (!entry) continue;
    const match = entry.match(/<([^>]+)>\s*$/);
    const addr = (match ? match[1] : entry).trim();
    if (!EMAIL_RE.test(addr)) {
      invalid.push(entry);
      continue;
    }
    const key = addr.toLowerCase();
    if (!seen.has(key)) {
      seen.add(key);
      valid.push(entry);
    }
  }
  return { valid, invalid };
}

function selectedComposeKey() {
  const id = Number($("compose-key").value);
  return state.keys.find((k) => k.id === id) || null;
}

function renderComposeKeys() {
  const select = $("compose-key");
  const current = select.value;
  const active = state.keys.filter((k) => k.active);
  const disabled = state.keys.filter((k) => !k.active);
  setChildren(select,
    el("option", { value: "", text: state.keys.length ? "Choose a key…" : "No API keys yet" }),
    ...active.map((k) => el("option", { value: String(k.id), text: k.name + " · " + k.key_prefix + "…" + (k.smtp ? " · " + k.smtp.host : "") })),
    ...(disabled.length ? [el("optgroup", { label: "Disabled keys" },
      disabled.map((k) => el("option", { value: String(k.id), disabled: true, text: k.name + " · " + k.key_prefix + "…" })))] : []));
  select.value = active.some((k) => String(k.id) === current) ? current : "";
  renderComposeSide();
}

function remaining(key) {
  if (key.is_super) return null;
  let best = null;
  for (const w of ["hour", "day", "week", "month"]) {
    const limit = key.limits[w];
    if (!limit) continue;
    const left = Math.max(0, limit - (key.usage ? key.usage[w] : 0));
    if (best === null || left < best.left) best = { left, window: w };
  }
  return best;
}

function renderComposeSide() {
  const side = $("compose-side");
  const key = selectedComposeKey();
  if (!key) {
    setChildren(side, el("h3", { text: "Sending as" }),
      emptyState("key", "Choose an API key", "The message is sent through that key's SMTP server and counted against its limits."));
    return;
  }
  const left = remaining(key);
  const recipients = parseRecipients($("compose-form").elements.recipients.value).valid.length;
  let quota;
  if (!left) {
    quota = el("dd", { text: key.is_super ? "Unlimited (super key)" : "Unlimited" });
  } else {
    const enough = recipients <= left.left;
    quota = el("dd", {},
      el("span", { class: enough ? "" : "bad", text: fmt(left.left) + " mail" + (left.left === 1 ? "" : "s") + " left this " + left.window }),
      recipients > 0 && !enough && el("span", { class: "sub bad", text: "This message needs " + fmt(recipients) + ", so it will be rejected." }));
  }
  setChildren(side,
    el("h3", { text: "Sending as" }),
    el("div", { class: "side-key" },
      el("div", { class: "secret-icon" }, icon("key")),
      el("div", {}, el("strong", { text: key.name }), el("code", { text: key.key_prefix + "…" }))),
    el("dl", { class: "side-rows" },
      el("div", {}, el("dt", { text: "From" }), el("dd", { text: key.smtp ? key.smtp.from : "Default From address (service settings)" })),
      el("div", {}, el("dt", { text: "SMTP server" }), el("dd", { class: key.smtp ? "mono" : "", text: key.smtp ? key.smtp.host + ":" + key.smtp.port : "Default server" })),
      el("div", {}, el("dt", { text: "Limits" }), quota)),
    !key.is_super && el("div", { class: "usage-box" }, usageCell(key)),
    el("p", { class: "side-note", text: "Counted against this key's limits, recorded in its request log as /admin/compose and kept in Mail history. The key's IP allow list does not apply to mail you send from here." }));
}

function updateComposeHints() {
  const f = $("compose-form").elements;
  const { valid, invalid } = parseRecipients(f.recipients.value);
  const hint = $("compose-count");
  if (!valid.length && !invalid.length) {
    hint.textContent = "Separate addresses with commas or new lines. Up to 500 per message.";
  } else {
    setChildren(hint,
      el("span", { class: valid.length ? "good" : "", text: valid.length + " recipient" + (valid.length === 1 ? "" : "s") }),
      invalid.length ? el("span", { class: "bad", text: " · invalid: " + invalid.slice(0, 3).join(", ") + (invalid.length > 3 ? "…" : "") }) : null);
  }
  if (invalid.length) f.recipients.setAttribute("aria-invalid", "true"); else f.recipients.removeAttribute("aria-invalid");
  $("compose-chars").textContent = fmt(f.body.value.length);
  $("compose-submit-label").textContent = valid.length > 1 ? "Send to " + valid.length : "Send";
  renderComposeSide();
}

function clearCompose() {
  const f = $("compose-form").elements;
  f.recipients.value = "";
  f.subject.value = "";
  f.body.value = "";
  setError("compose-error", "");
  updateComposeHints();
}

$("compose-key").addEventListener("change", renderComposeSide);
$("compose-form").addEventListener("input", (e) => { if (e.target.name !== "key_id") updateComposeHints(); });
$("compose-clear").addEventListener("click", clearCompose);

$("compose-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const f = $("compose-form").elements;
  const key = selectedComposeKey();
  const { valid, invalid } = parseRecipients(f.recipients.value);
  const fail = (message, field) => { setError("compose-error", message); if (field) field.focus(); };

  if (!key) return fail("Choose the API key to send with.", f.key_id);
  if (invalid.length) return fail("Fix the invalid address" + (invalid.length > 1 ? "es" : "") + ": " + invalid.join(", "), f.recipients);
  if (!valid.length) return fail("Add at least one recipient.", f.recipients);
  if (valid.length > 500) return fail("Send to at most 500 recipients at a time.", f.recipients);
  if (!f.body.value.trim()) return fail("Write a message.", f.body);
  if (!f.subject.value.trim()) {
    const ok = await confirmAction("Send without a subject?", "Messages without a subject are more likely to be ignored or marked as spam.", "Send anyway");
    if (!ok) return;
  } else if (valid.length >= 10) {
    const ok = await confirmAction("Send to " + valid.length + " recipients?",
      "Each recipient gets a separate email from “" + key.name + "”, and " + valid.length + " mails are counted against its limits.", "Send " + valid.length + " emails");
    if (!ok) return;
  }

  const button = $("compose-submit");
  button.disabled = true;
  $("compose-submit-label").textContent = "Sending…";
  setError("compose-error", "");
  try {
    const res = await api("POST", "/compose", { key_id: key.id, recipients: valid, subject: f.subject.value, body: f.body.value });
    toast(res.message);
    clearCompose();
    await refresh();
  } catch (err) {
    fail(err.message);
  } finally {
    button.disabled = false;
    updateComposeHints();
  }
});

// ---------- requests ----------

const REQUEST_BADGE = { pending: "badge-warning", approved: "badge-success", rejected: "badge-neutral" };

function renderRequests() {
  const list = $("requests-list");
  setChildren(list);
  const items = state.requests.filter((r) => state.requestFilter === "all" || r.status === state.requestFilter);
  if (!items.length) {
    const msg = state.requestFilter === "pending" ? "You're all caught up. New requests from the public site will appear here." : "Nothing to show for this filter.";
    list.append(el("div", { class: "card" }, emptyState("inbox", "No " + (state.requestFilter === "all" ? "" : state.requestFilter + " ") + "requests", msg)));
    return;
  }

  for (const r of items) {
    const linkedKey = r.api_key_id && state.keys.find((k) => k.id === r.api_key_id);
    const meta = el("dl", { class: "meta" },
      r.organization && el("div", {}, el("dt", { text: "Organization" }), el("dd", { text: r.organization })),
      r.expected_volume && el("div", {}, el("dt", { text: "Volume" }), el("dd", { text: r.expected_volume + " / month" })),
      r.caller_ips && el("div", {}, el("dt", { text: "Server IPs" }), el("dd", { class: "mono", text: r.caller_ips })),
      r.address && el("div", {}, el("dt", { text: "Address" }), el("dd", { text: r.address })),
      el("div", {}, el("dt", { text: "Submitted from" }), el("dd", { class: "mono", text: r.ip })));

    let actions = null;
    if (r.status === "pending") {
      actions = el("div", { class: "request-actions" },
        el("button", { type: "button", class: "btn btn-primary btn-sm", onclick: () => openKeyDialog({ type: "approve", request: r }) }, icon("key"), "Approve & create key"),
        el("button", { type: "button", class: "btn btn-danger btn-sm", onclick: () => rejectRequest(r), text: "Reject" }));
    } else if (r.status === "approved") {
      actions = el("p", { class: "muted small" }, "Approved " + relTime(r.reviewed_at) + " · key ",
        linkedKey ? el("strong", { text: linkedKey.name + " (" + linkedKey.key_prefix + "…)" }) : "deleted");
    } else {
      actions = el("p", { class: "muted small", text: "Rejected " + relTime(r.reviewed_at) });
    }

    list.append(el("article", { class: "card request-card" },
      el("div", { class: "request-top" },
        el("span", { class: "avatar", "aria-hidden": "true", text: r.name.charAt(0) }),
        el("div", { class: "request-who" },
          el("strong", { text: r.name }),
          el("div", { class: "muted" },
            el("a", { href: "mailto:" + r.email, text: r.email }),
            r.phone && [" · ", el("a", { href: "tel:" + r.phone.replace(/[^\d+]/g, ""), text: r.phone })],
            " · " + relTime(r.created_at))),
        el("span", { class: "badge " + REQUEST_BADGE[r.status], text: r.status.charAt(0).toUpperCase() + r.status.slice(1) })),
      el("div", { class: "request-block" }, el("span", { class: "request-label", text: "Use case" }), el("p", { class: "request-use", text: r.use_case })),
      r.message && el("div", { class: "request-block" }, el("span", { class: "request-label", text: "Message / questions" }), el("p", { class: "request-use", text: r.message })),
      meta,
      actions));
  }
}

async function rejectRequest(r) {
  const ok = await confirmAction("Reject request from " + r.name + "?", "The request will be marked as rejected. No key is created.", "Reject request");
  if (!ok) return;
  try {
    await api("POST", "/requests/" + r.id + "/reject");
    toast("Request rejected");
    await refresh();
  } catch (err) {
    toast(err.message, "error");
  }
}

// ---------- logs ----------

const LOG_BADGE = { accepted: "badge-success", limit_exceeded: "badge-warning", ip_denied: "badge-danger", key_disabled: "badge-danger" };
const LOG_LABEL = { accepted: "Accepted", limit_exceeded: "Limit exceeded", ip_denied: "IP denied", key_disabled: "Key disabled" };

function renderLogFilter() {
  const select = $("log-filter");
  const current = select.value;
  setChildren(select, el("option", { value: "0", text: "All keys" }),
    state.keys.map((k) => el("option", { value: String(k.id), text: k.name })));
  select.value = state.keys.some((k) => String(k.id) === current) ? current : "0";
}

async function loadLogs() {
  try {
    state.logs = await api("GET", "/logs?limit=200&key_id=" + encodeURIComponent($("log-filter").value || "0"));
    renderLogs();
  } catch (err) {
    toast(err.message, "error");
  }
}

function renderLogs() {
  const body = $("logs-body");
  setChildren(body);
  const status = $("log-status").value;
  const logs = state.logs.filter((l) => !status || l.status === status);
  if (!logs.length) {
    body.append(emptyState("activity", "No requests recorded", "Requests made with an API key will show up here.", 7));
    return;
  }
  for (const l of logs) {
    body.append(el("tr", {},
      el("td", { class: "nowrap", title: fullTime(l.created_at), text: relTime(l.created_at) }),
      el("td", { text: l.key_name }),
      el("td", { class: "mono small", text: l.ip }),
      el("td", { class: "nowrap" }, el("span", { class: "muted", text: l.method + " " }), el("code", { text: l.path })),
      el("td", { class: "right num", text: fmt(l.mail_count) }),
      el("td", {}, el("span", { class: "badge " + (LOG_BADGE[l.status] || "badge-neutral"), text: LOG_LABEL[l.status] || l.status })),
      el("td", { class: "muted small", text: l.message || "—" })));
  }
}

// ---------- mail history ----------

const MAIL_BADGE = { queued: "badge-info", sending: "badge-primary", sent: "badge-success", failed: "badge-danger", simulated: "badge-neutral" };
const MAIL_LABEL = { queued: "Queued", sending: "Sending", sent: "Sent", failed: "Failed", simulated: "Simulated" };
const SOURCE_LABEL = { api: "API", compose: "Admin compose", system: "Auto-reply" };
const MAIL_PAGE = 100;

let mailSeq = 0;

function mailQuery(before) {
  const f = state.mailFilter;
  const p = new URLSearchParams({ limit: String(MAIL_PAGE) });
  if (f.status) p.set("status", f.status);
  if (f.q.trim()) p.set("q", f.q.trim());
  if (f.key === "system") p.set("source", "system");
  else if (f.key) p.set("key_id", f.key);
  if (before) p.set("before", String(before));
  return "/mails?" + p;
}

// loadMails reloads the first page, or appends the next one when `more` is set. Only the
// newest call's result is shown, so a slow response cannot overwrite a newer filter.
async function loadMails(more) {
  const seq = ++mailSeq;
  const before = more && state.mails.length ? state.mails[state.mails.length - 1].id : 0;
  try {
    const res = await api("GET", mailQuery(before));
    if (seq !== mailSeq) return;
    state.mails = more ? state.mails.concat(res.mails) : res.mails;
    state.mailHasMore = res.mails.length === MAIL_PAGE;
    state.mailCounts = res.counts;
    renderMailBadge(res.failed_total);
    renderMails();
  } catch (err) {
    if (seq === mailSeq) toast(err.message, "error");
  }
}

function renderMailBadge(failed) {
  $("nav-failed-count").textContent = String(failed || 0);
  $("nav-failed-count").classList.toggle("hidden", !failed);
}

function renderMailKeyFilter() {
  const select = $("mail-key");
  const current = select.value;
  setChildren(select,
    el("option", { value: "", text: "All senders" }),
    el("option", { value: "system", text: "Auto-replies (system)" }),
    state.keys.map((k) => el("option", { value: String(k.id), text: k.name })));
  const valid = current === "" || current === "system" || state.keys.some((k) => String(k.id) === current);
  select.value = valid ? current : "";
  state.mailFilter.key = select.value;
}

function showMailsFor(key) {
  $("mail-key").value = key;
  state.mailFilter.key = key;
  if (location.hash === "#mails") loadMails(); else location.hash = "mails";
}

function mailSender(m) {
  if (m.source === "system") return el("span", {}, el("span", { text: "Auto-reply" }), el("span", { class: "sub", text: "Key request" }));
  const deleted = m.api_key_id === null;
  return el("span", {},
    el("span", { class: deleted ? "muted" : "", text: m.key_name + (deleted ? " (deleted)" : "") }),
    el("span", { class: "sub", text: SOURCE_LABEL[m.source] || m.source }));
}

function mailBadge(status) {
  return el("span", { class: "badge " + (MAIL_BADGE[status] || "badge-neutral"), text: MAIL_LABEL[status] || status });
}

function renderMails() {
  const counts = state.mailCounts || {};
  const total = Object.values(counts).reduce((a, b) => a + b, 0);
  document.querySelectorAll("#mail-status [data-count]").forEach((n) => {
    const v = n.dataset.count === "all" ? total : counts[n.dataset.count] || 0;
    n.textContent = state.mailCounts ? fmt(v) : "";
  });
  const open = (counts.queued || 0) + (counts.sending || 0);
  $("mail-live").textContent = open ? fmt(open) + " in progress · updating automatically" : "";

  const body = $("mails-body");
  setChildren(body);
  if (!state.mails.length) {
    const filtered = state.mailFilter.status || state.mailFilter.key || state.mailFilter.q.trim();
    body.append(emptyState("mail", filtered ? "No matching mail" : "No mail sent yet",
      filtered ? "Try a different search or filter." : "Mail sent through the API, Compose or the request auto-reply shows up here.", 7));
  }
  for (const m of state.mails) {
    body.append(el("tr", { class: "mail-row", onclick: () => openMail(m.id) },
      el("td", { class: "nowrap", title: fullTime(m.created_at), text: relTime(m.created_at) }),
      el("td", {}, mailSender(m)),
      el("td", { class: "mail-cell", title: m.sender || "Simulation (no SMTP server)" },
        m.sender ? el("span", { text: m.sender }) : el("span", { class: "muted", text: "Simulation" }),
        m.smtp_host && el("span", { class: "sub mono", text: m.smtp_host })),
      el("td", { class: "mail-cell", title: m.recipient, text: m.recipient }),
      el("td", { class: "mail-subject" },
        el("span", { class: "mail-cell block", text: m.subject || "(no subject)" }),
        m.status === "failed" && m.error && el("span", { class: "sub mail-cell bad", title: m.error, text: m.error })),
      el("td", {}, mailBadge(m.status)),
      el("td", {}, el("div", { class: "actions" },
        iconButton("eye", "View mail", (e) => { e.stopPropagation(); openMail(m.id); })))));
  }
  $("mail-more-wrap").classList.toggle("hidden", !state.mailHasMore);
}

let openMailId = null;

async function openMail(id) {
  openMailId = id;
  setError("mail-error", "");
  $("mail-title").textContent = "Loading…";
  $("mail-sub").textContent = "";
  setChildren($("mail-meta"));
  $("mail-body").textContent = "";
  $("mail-retry").classList.add("hidden");
  $("mail-retry-note").classList.add("hidden");
  if (!$("mail-dialog").open) $("mail-dialog").showModal();
  try {
    renderMailDetail(await api("GET", "/mails/" + id));
  } catch (err) {
    $("mail-title").textContent = "Mail";
    setError("mail-error", err.message);
  }
}

function renderMailDetail(m) {
  $("mail-title").textContent = m.subject || "(no subject)";
  setChildren($("mail-sub"), mailBadge(m.status), " ", m.recipient);
  const row = (label, value, cls) => value && [el("dt", { text: label }), el("dd", { class: cls || "", text: value })];
  const sentWith = m.source === "system" ? "Auto-reply to a key request"
    : m.key_name + (m.api_key_id === null ? " (deleted)" : "") + " · " + (SOURCE_LABEL[m.source] || m.source);
  setChildren($("mail-meta"),
    row("To", m.recipient),
    row("From", m.sender || "Simulation (no SMTP server configured)"),
    row("SMTP server", m.smtp_host, "mono"),
    row("Sent with", sentWith),
    row("Endpoint", m.path, "mono"),
    row("Client IP", m.ip, "mono"),
    row("Queued", fullTime(m.created_at)),
    m.sent_at ? row(m.status === "simulated" ? "Logged" : "Delivered", fullTime(m.sent_at)) : row("Last update", fullTime(m.updated_at)),
    row("Attempts", String(m.attempts)),
    row("Error", m.error, "bad"));
  $("mail-body").textContent = m.body;

  const canRetry = m.status === "failed";
  $("mail-retry").classList.toggle("hidden", !canRetry);
  $("mail-retry-note").classList.toggle("hidden", !canRetry);
}

$("mail-retry").addEventListener("click", async () => {
  const button = $("mail-retry");
  button.disabled = true;
  setError("mail-error", "");
  try {
    const res = await api("POST", "/mails/" + openMailId + "/retry");
    toast(res.message);
    await Promise.all([openMail(openMailId), loadMails()]);
  } catch (err) {
    setError("mail-error", err.message);
  } finally {
    button.disabled = false;
  }
});

let mailSearchTimer = null;
$("mail-search").addEventListener("input", (e) => {
  state.mailFilter.q = e.target.value;
  clearTimeout(mailSearchTimer);
  mailSearchTimer = setTimeout(() => loadMails(), 300);
});
$("mail-key").addEventListener("change", (e) => { state.mailFilter.key = e.target.value; loadMails(); });
$("mail-more").addEventListener("click", () => loadMails(true));

// While mail is queued or being sent, keep the list current so statuses update on their own.
setInterval(() => {
  const c = state.mailCounts;
  if (state.view !== "mails" || document.hidden || !c || !(c.queued || c.sending)) return;
  if ($("app-view").classList.contains("hidden") || state.mails.length > MAIL_PAGE) return;
  loadMails();
}, 5000);

// ---------- events ----------

function bindSegmented(id, onChange) {
  $(id).addEventListener("click", (e) => {
    const btn = e.target.closest("button[data-filter]");
    if (!btn) return;
    $(id).querySelectorAll("button").forEach((b) => b.setAttribute("aria-pressed", String(b === btn)));
    onChange(btn.dataset.filter);
  });
}

bindSegmented("key-filter", (f) => { state.keyFilter = f; renderKeys(); });
bindSegmented("request-filter", (f) => { state.requestFilter = f; renderRequests(); });
bindSegmented("mail-status", (f) => { state.mailFilter.status = f; loadMails(); });
$("key-search").addEventListener("input", (e) => { state.keySearch = e.target.value; renderKeys(); });
$("log-filter").addEventListener("change", loadLogs);
$("log-status").addEventListener("change", renderLogs);

$("new-key").addEventListener("click", () => openKeyDialog({ type: "create" }));
$("refresh").addEventListener("click", async () => { await refresh(); toast("Data refreshed", "info"); });
$("key-form").addEventListener("submit", submitKeyForm);
$("key-form").elements.is_super.addEventListener("change", syncSuper);
$("key-form").elements.smtp_enabled.addEventListener("change", () => {
  syncSMTP();
  if ($("key-form").elements.smtp_enabled.checked) $("key-form").elements.smtp_host.focus();
});
document.querySelectorAll("[data-port]").forEach((b) => b.addEventListener("click", () => {
  $("key-form").elements.smtp_port.value = b.dataset.port;
}));
document.querySelectorAll("[data-preset]").forEach((b) => b.addEventListener("click", () => {
  const [h, d, w, m] = b.dataset.preset.split(",");
  const f = $("key-form").elements;
  f.limit_hour.value = h; f.limit_day.value = d; f.limit_week.value = w; f.limit_month.value = m;
}));

document.querySelectorAll("dialog [data-close]").forEach((b) => b.addEventListener("click", () => b.closest("dialog").close()));
document.querySelectorAll("dialog").forEach((d) => d.addEventListener("click", (e) => { if (e.target === d) d.close(); }));
$("secret-dialog").addEventListener("close", () => { $("secret-value").textContent = ""; });

$("secret-copy").addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText($("secret-value").textContent);
    $("secret-copy").textContent = "Copied";
  } catch {
    const range = document.createRange();
    range.selectNodeContents($("secret-value"));
    getSelection().removeAllRanges();
    getSelection().addRange(range);
    $("secret-copy").textContent = "Press Ctrl+C";
  }
});

$("menu-btn").addEventListener("click", () => setSidebar(!$("sidebar").classList.contains("open")));
$("scrim").addEventListener("click", () => setSidebar(false));
window.addEventListener("hashchange", () => setView(location.hash.slice(1)));

$("login-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const f = e.target.elements;
  if (!f.username.value || !f.password.value) {
    setError("login-error", "Enter your username and password.");
    return;
  }
  const button = $("login-submit");
  button.disabled = true;
  button.textContent = "Signing in…";
  setError("login-error", "");
  try {
    const me = await api("POST", "/login", { username: f.username.value, password: f.password.value });
    f.password.value = "";
    showApp(me.username);
  } catch (err) {
    setError("login-error", err.message);
    f.password.select();
  } finally {
    button.disabled = false;
    button.textContent = "Sign in";
  }
});

$("logout").addEventListener("click", async () => {
  await api("POST", "/logout").catch(() => {});
  showLogin();
  toast("Signed out", "info");
});

api("GET", "/me").then((me) => showApp(me.username)).catch(() => showLogin());
