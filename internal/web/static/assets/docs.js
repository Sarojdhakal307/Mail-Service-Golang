"use strict";

const LANG_LABELS = { bash: "Shell", js: "JavaScript", python: "Python", go: "Go", json: "JSON", http: "HTTP", text: "Text" };

const STR = "'(?:[^'\\\\\\n]|\\\\.)*'|\"(?:[^\"\\\\\\n]|\\\\.)*\"";
// Each language is a list of [pattern, token class]; the first matching alternative wins.
const RULES = {
  bash: [["#[^\\n]*", "c"], ["'[^']*'|\"(?:[^\"\\\\]|\\\\.)*\"", "s"], ["\\b(?:curl|export)\\b", "k"], ["\\$[A-Z_]+", "h"], ["(?<=\\s)-{1,2}[A-Za-z][\\w-]*", "n"]],
  js: [["\\/\\/[^\\n]*", "c"], ["`(?:[^`\\\\]|\\\\.)*`|" + STR, "s"],
    ["\\b(?:const|let|await|async|function|return|if|for|throw|new|break|null|true|false)\\b", "k"], ["\\b\\d+\\b", "n"]],
  python: [["#[^\\n]*", "c"], ["f?" + STR, "s"], ["\\b(?:import|def|return|if|raise|from|None|True|False)\\b", "k"], ["\\b\\d+\\b", "n"]],
  go: [["\\/\\/[^\\n]*", "c"], ["`[^`]*`|" + STR, "s"],
    ["\\b(?:package|import|func|return|if|err|nil|const|defer|var|type|struct|string|error)\\b", "k"], ["\\b\\d+\\b", "n"]],
  json: [["\"(?:[^\"\\\\]|\\\\.)*\"(?=\\s*:)", "h"], ["\"(?:[^\"\\\\]|\\\\.)*\"", "s"], ["\\b(?:true|false|null)\\b", "k"], ["-?\\b\\d+(?:\\.\\d+)?\\b", "n"]],
  http: [["^(?:GET|POST|PUT|DELETE|HTTP\\/1\\.1)[^\\n]*", "k"], ["^[A-Za-z-]+(?=:)", "h"]],
};

// highlight fills `target` with text and token spans. Text is only ever set via textContent.
function highlight(target, code, lang) {
  const rules = RULES[lang];
  if (!rules) {
    target.textContent = code;
    return;
  }
  const re = new RegExp(rules.map(([p]) => "(" + p + ")").join("|"), "gm");
  let last = 0;
  for (const match of code.matchAll(re)) {
    if (!match[0]) continue;
    target.append(code.slice(last, match.index));
    const group = match.slice(1).findIndex((g) => g !== undefined);
    const span = document.createElement("span");
    span.className = "tok-" + rules[group][1];
    span.textContent = match[0];
    target.append(span);
    last = match.index + match[0].length;
  }
  target.append(code.slice(last));
}

function copyButton(getText) {
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "btn btn-ghost btn-sm copy-btn";
  btn.textContent = "Copy";
  btn.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(getText());
      btn.textContent = "Copied";
      btn.classList.add("copied");
    } catch {
      btn.textContent = "Press Ctrl+C";
    }
    setTimeout(() => { btn.textContent = "Copy"; btn.classList.remove("copied"); }, 1600);
  });
  return btn;
}

// Turn every <pre data-lang> into a highlighted code block with a header and copy button.
document.querySelectorAll("pre[data-lang]").forEach((pre) => {
  const code = pre.textContent.replaceAll("{{BASE}}", location.origin);
  const lang = pre.dataset.lang;

  const block = document.createElement("div");
  block.className = "codeblock";
  if (pre.getAttribute("role") === "tabpanel") {
    block.setAttribute("role", "tabpanel");
    block.hidden = pre.hidden;
  }

  const head = document.createElement("div");
  head.className = "codeblock-head";
  head.textContent = pre.dataset.title || LANG_LABELS[lang] || lang;
  const copy = copyButton(() => code);
  if (pre.getAttribute("role") === "tabpanel") block.append(copy);
  else head.append(copy);

  const newPre = document.createElement("pre");
  newPre.tabIndex = 0;
  highlight(newPre, code, lang);
  block.append(head, newPre);
  pre.replaceWith(block);
});

// Tabs
document.querySelectorAll("[data-tabs]").forEach((tabs) => {
  const buttons = [...tabs.querySelectorAll('[role="tab"]')];
  const panels = [...tabs.querySelectorAll('[role="tabpanel"]')];
  const select = (i) => {
    buttons.forEach((b, j) => {
      b.setAttribute("aria-selected", String(i === j));
      b.tabIndex = i === j ? 0 : -1;
    });
    panels.forEach((p, j) => { p.hidden = i !== j; });
  };
  buttons.forEach((b, i) => {
    b.addEventListener("click", () => select(i));
    b.addEventListener("keydown", (e) => {
      const dir = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
      if (!dir) return;
      const next = (i + dir + buttons.length) % buttons.length;
      select(next);
      buttons[next].focus();
    });
  });
  select(0);
});

// Highlight the section currently in view in the sidebar.
const links = new Map([...document.querySelectorAll(".toc a")].map((a) => [a.getAttribute("href").slice(1), a]));
const visible = new Set();
const observer = new IntersectionObserver((entries) => {
  entries.forEach((e) => (e.isIntersecting ? visible.add(e.target.id) : visible.delete(e.target.id)));
  const current = [...links.keys()].find((id) => visible.has(id));
  if (!current) return;
  links.forEach((a, id) => a.classList.toggle("active", id === current));
}, { rootMargin: "-80px 0px -65% 0px" });
links.forEach((_, id) => {
  const section = document.getElementById(id);
  if (section) observer.observe(section);
});

// Mobile sidebar
const sidebar = document.getElementById("docs-sidebar");
const menu = document.getElementById("docs-menu");
const setMenu = (open) => {
  sidebar.classList.toggle("open", open);
  menu.setAttribute("aria-expanded", String(open));
};
menu.addEventListener("click", () => setMenu(!sidebar.classList.contains("open")));
sidebar.addEventListener("click", (e) => { if (e.target.closest("a")) setMenu(false); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape") setMenu(false); });
