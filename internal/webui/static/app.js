"use strict";

const TOKEN_KEY = "remote-explorer-token";
const $ = (id) => document.getElementById(id);

let token = "";
let info = null;
let listing = null;
let sortKey = "name";
let sortDesc = false;

// The server prints a link with the token in the fragment, which browsers
// never send to the server. Move it to session storage and out of the URL.
function takeToken() {
  const m = /(?:^#|&)token=([^&]*)/.exec(location.hash);
  if (m) {
    token = decodeURIComponent(m[1]);
    saveToken();
    history.replaceState(null, "", location.pathname + location.search);
    return;
  }
  try {
    token = sessionStorage.getItem(TOKEN_KEY) || "";
  } catch {
    token = "";
  }
}

function saveToken() {
  try {
    if (token) sessionStorage.setItem(TOKEN_KEY, token);
    else sessionStorage.removeItem(TOKEN_KEY);
  } catch {
    // Storage can be unavailable (private mode); the token then lasts until reload.
  }
}

class ApiError extends Error {
  constructor(status, body) {
    super(body.error || `HTTP ${status}`);
    this.status = status;
    this.code = body.code || "";
    this.allowed = body.allowed;
  }
}

async function api(endpoint, init = {}) {
  const headers = new Headers(init.headers);
  if (token) headers.set("Authorization", "Bearer " + token);
  const res = await fetch(endpoint, { ...init, headers });
  if (res.status === 401) {
    token = "";
    saveToken();
    showLogin();
    throw new ApiError(401, { error: "missing or invalid token", code: "unauthorized" });
  }
  if (!res.ok) {
    let body = {};
    try {
      body = await res.json();
    } catch {
      // Not every error is JSON, e.g. 416 from Go's file server.
    }
    throw new ApiError(res.status, body);
  }
  return res;
}

function currentPath() {
  return new URLSearchParams(location.search).get("path") || "";
}

function pageURL(path) {
  return path ? "?path=" + encodeURIComponent(path) : location.pathname;
}

function downloadURL(path) {
  return "/api/download?path=" + encodeURIComponent(path);
}

function showLogin() {
  $("login").hidden = false;
  $("listing").hidden = true;
  $("upload").hidden = true;
  $("token").focus();
}

function showError(err) {
  $("error").textContent = err.code ? `${err.message} (${err.code})` : err.message;
  $("error").hidden = false;
}

async function load() {
  const path = currentPath();
  const title = "Index of /" + path;
  document.title = title;
  $("heading").textContent = title;
  $("error").hidden = true;
  try {
    if (!info) info = await (await api("/api/info")).json();
    listing = await (await api("/api/list?path=" + encodeURIComponent(path))).json();
  } catch (err) {
    listing = null;
    $("listing").hidden = true;
    $("upload").hidden = true;
    if (err.status !== 401) showError(err);
    return;
  }
  $("login").hidden = true;
  render();
  $("listing").hidden = false;
  $("upload").hidden = !info.writable;
  $("overwrite-label").hidden = !info.overwrite;
}

function compare(a, b) {
  // Folders stay first whichever way the table is sorted, as with Apache's FoldersFirst.
  if (a.type !== b.type) return a.type === "dir" ? -1 : 1;
  let c = 0;
  if (sortKey === "modified") c = Date.parse(a.modified) - Date.parse(b.modified);
  if (sortKey === "size") c = (a.size || 0) - (b.size || 0);
  if (c === 0) c = a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
  return sortDesc ? -c : c;
}

function render() {
  const rows = [];
  if (listing.parent !== null) {
    rows.push(row("back.svg", "[PARENTDIR]", "Parent Directory", pageURL(listing.parent), "", "-"));
  }
  for (const e of [...listing.entries].sort(compare)) {
    if (e.type === "dir") {
      rows.push(
        row("folder.svg", "[DIR]", e.name + "/", pageURL(e.path), formatDate(e.modified), "-"),
      );
    } else {
      const tr = row(
        "file.svg",
        "[   ]",
        e.name,
        downloadURL(e.path),
        formatDate(e.modified),
        formatSize(e.size),
      );
      tr.querySelector("a").dataset.download = e.path;
      rows.push(tr);
    }
  }
  $("entries").replaceChildren(...rows);
}

function row(icon, alt, name, href, modified, size) {
  const tr = document.createElement("tr");
  const img = document.createElement("img");
  img.src = "/ui/icons/" + icon;
  img.alt = alt;
  const a = document.createElement("a");
  a.href = href;
  a.textContent = name;
  for (const [cls, content] of [
    ["icon", img],
    ["name", a],
    ["modified", modified],
    ["size", size],
  ]) {
    const td = document.createElement("td");
    td.className = cls;
    td.append(content);
    tr.append(td);
  }
  return tr;
}

function formatDate(iso) {
  const d = new Date(iso);
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

// Same style as Apache: 512, 4.0K, 12K, 1.3M.
function formatSize(n) {
  if (n < 1024) return String(n);
  const units = ["K", "M", "G", "T", "P"];
  let i = -1;
  do {
    n /= 1024;
    i++;
  } while (n >= 1024 && i < units.length - 1);
  return (n < 10 ? n.toFixed(1) : Math.round(n)) + units[i];
}

// Links cannot carry the Authorization header, so with a token the file is
// fetched by script and handed to the browser as a blob.
async function download(a) {
  $("error").hidden = true;
  try {
    const blob = await (await api(a.href)).blob();
    const url = URL.createObjectURL(blob);
    const save = document.createElement("a");
    save.href = url;
    save.download = a.textContent;
    document.body.append(save);
    save.click();
    save.remove();
    setTimeout(() => URL.revokeObjectURL(url), 60_000);
  } catch (err) {
    if (err.status !== 401) showError(err);
  }
}

async function upload(ev) {
  ev.preventDefault();
  const files = $("files").files;
  if (files.length === 0) return;
  const status = $("upload-status");
  const form = new FormData();
  for (const f of files) form.append("file", f);
  let endpoint = "/api/upload?path=" + encodeURIComponent(currentPath());
  if ($("overwrite").checked) endpoint += "&overwrite=true";
  status.textContent = "Uploading…";
  try {
    const saved = (await (await api(endpoint, { method: "POST", body: form })).json()).files;
    status.textContent = `Uploaded ${saved.length} file${saved.length === 1 ? "" : "s"}.`;
    $("files").value = "";
    await load();
  } catch (err) {
    let msg = err.message;
    if (err.code === "exists" && info.overwrite)
      msg += "; tick “Replace existing files” to overwrite";
    if (err.allowed) msg += ": allowed extensions are " + err.allowed.join(", ");
    status.textContent = "Upload failed: " + msg + ".";
  }
}

document.addEventListener("DOMContentLoaded", () => {
  takeToken();
  $("address").textContent =
    `remote-explorer Server at ${location.hostname} Port ${location.port || (location.protocol === "https:" ? 443 : 80)}`;

  $("login").addEventListener("submit", (ev) => {
    ev.preventDefault();
    token = $("token").value.trim();
    $("token").value = "";
    saveToken();
    load();
  });

  $("upload").addEventListener("submit", upload);

  document.querySelector("thead").addEventListener("click", (ev) => {
    const a = ev.target.closest("a[data-sort]");
    if (!a || !listing) return;
    ev.preventDefault();
    sortDesc = sortKey === a.dataset.sort ? !sortDesc : false;
    sortKey = a.dataset.sort;
    render();
  });

  $("entries").addEventListener("click", (ev) => {
    const a = ev.target.closest("a");
    if (!a || ev.button !== 0 || ev.ctrlKey || ev.metaKey || ev.shiftKey || ev.altKey) return;
    if (a.dataset.download) {
      // Without a token the plain link works and streams straight to disk.
      if (!token) return;
      ev.preventDefault();
      download(a);
      return;
    }
    ev.preventDefault();
    history.pushState(null, "", a.href);
    sortKey = "name";
    sortDesc = false;
    load();
  });

  window.addEventListener("popstate", load);
  load();
});
