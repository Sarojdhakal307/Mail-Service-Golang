"use strict";

const $ = (id) => document.getElementById(id);

document.querySelectorAll(".base-url").forEach((node) => { node.textContent = location.origin; });

function formatUptime(seconds) {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d) return d + "d " + h + "h";
  if (h) return h + "h " + m + "m";
  return m + "m";
}

function setStatus(tone, title) {
  const cls = { ok: "dot dot-live", warn: "dot dot-warn", bad: "dot" };
  $("status-dot").className = cls[tone];
  $("hero-dot").className = cls[tone];
  $("status-title").textContent = title;
  $("hero-status-text").textContent = title;
}

function setValue(id, text, tone) {
  const node = $(id);
  node.textContent = text;
  node.className = tone || "";
}

async function loadStatus() {
  try {
    const res = await fetch("/api/status", { cache: "no-store" });
    if (!res.ok) throw new Error("status " + res.status);
    const s = await res.json();
    const healthy = s.status === "operational";
    setStatus(healthy ? "ok" : "warn", healthy ? "All systems operational" : "Degraded performance");
    setValue("st-api", "Online", "ok");
    setValue("st-db", s.database === "ok" ? "Connected" : "Unavailable", s.database === "ok" ? "ok" : "bad");
    setValue("st-delivery", s.delivery === "smtp" ? "SMTP" : "Simulation", s.delivery === "smtp" ? "ok" : "warn");
    setValue("st-workers", String(s.workers));
    setValue("st-uptime", formatUptime(s.uptime_seconds));
    setValue("st-version", "v" + s.version);
    $("status-updated").textContent = "Updated " + new Date().toLocaleTimeString();
  } catch {
    setStatus("bad", "Status unavailable");
    setValue("st-api", "Unreachable", "bad");
  }
}

loadStatus();
setInterval(loadStatus, 30000);

// API key request form
const form = $("request-form");

function showFormError(message) {
  $("request-error").textContent = message;
  $("request-error").classList.toggle("hidden", !message);
}

function clientValidate(data) {
  if (!data.name.trim()) return ["name", "Please enter your name."];
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(data.email.trim())) return ["email", "Please enter a valid email address."];
  const phone = data.phone.trim();
  const digits = phone.replace(/\D/g, "").length;
  if (!/^\+?[0-9 ()./-]+$/.test(phone) || digits < 7 || digits > 15) {
    return ["phone", "Please enter a valid phone number, e.g. +977 9812345678."];
  }
  if (data.use_case.trim().length < 20) return ["use_case", "Please describe what you will send in at least 20 characters."];
  return null;
}

form.elements.message.addEventListener("input", (e) => {
  $("message-count").textContent = String(e.target.value.length);
});

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  const data = Object.fromEntries(new FormData(form).entries());
  form.querySelectorAll("[aria-invalid]").forEach((n) => n.removeAttribute("aria-invalid"));

  const problem = clientValidate(data);
  if (problem) {
    const field = form.elements[problem[0]];
    field.setAttribute("aria-invalid", "true");
    field.focus();
    showFormError(problem[1]);
    return;
  }

  const button = $("request-submit");
  button.disabled = true;
  button.textContent = "Sending…";
  showFormError("");
  try {
    const res = await fetch("/api/key-requests", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body.error || "Something went wrong. Please try again.");
    $("request-done-text").textContent = body.message;
    form.classList.add("hidden");
    $("request-done").classList.remove("hidden");
  } catch (err) {
    showFormError(err.message);
  } finally {
    button.disabled = false;
    button.textContent = "Send request";
  }
});
