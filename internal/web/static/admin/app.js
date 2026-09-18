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
  dialogMode: null, // { type: "create" } | { type: "edit", key } | { type: "approve", request }
};

const VIEWS = {
  keys: { title: "API keys", sub: "Create keys, set limits and watch usage." },
  requests: { title: "Access requests", sub: "Review API key requests submitted from the public site." },
  logs: { title: "Request log", sub: "Every authenticated request, newest first." },
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
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return node;
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
  $("page-title").textContent = VIEWS[name].title;
  $("page-sub").textContent = VIEWS[name].sub;
  document.title = VIEWS[name].title + " · Mail Service Admin";
  setSidebar(false);
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
}

function renderAll() {
  renderStats();
  renderKeys();
  renderRequests();
  renderLogFilter();
  renderLogs();
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
  body.replaceChildren();
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
    "Clients using this key will stop working immediately and its request history will be removed. This cannot be undone.", "Delete key");
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
    summary.replaceChildren(
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

// ---------- requests ----------

const REQUEST_BADGE = { pending: "badge-warning", approved: "badge-success", rejected: "badge-neutral" };

function renderRequests() {
  const list = $("requests-list");
  list.replaceChildren();
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
  select.replaceChildren(el("option", { value: "0", text: "All keys" }),
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
  body.replaceChildren();
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
