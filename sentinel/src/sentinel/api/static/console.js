"use strict";
// All server data is inserted with textContent / DOM APIs - never innerHTML - to prevent XSS.
const $ = (id) => document.getElementById(id);
let apiKey = null; let selected = null; let canOperate = false;
try { apiKey = sessionStorage.getItem("sentinelKey"); } catch (e) {}

function el(tag, attrs = {}, ...children) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") n.className = v; else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v);
  }
  for (const c of children) n.append(c instanceof Node ? c : document.createTextNode(c ?? ""));
  return n;
}
async function api(path, opts = {}) {
  const r = await fetch(path, { ...opts, headers: { "X-API-Key": apiKey || "",
    "Content-Type": "application/json", ...(opts.headers || {}) } });
  if (!r.ok) { const body = await r.json().catch(() => ({})); throw new Error(body?.error?.message || r.statusText); }
  const ct = r.headers.get("content-type") || "";
  return ct.includes("json") ? r.json() : r.text();
}
async function connect() {
  try {
    const me = await api("/api/v1/whoami");
    $("who").textContent = `${me.name} (${me.role})`;
    canOperate = me.role !== "viewer";
    refresh(); if (!window.__timer) window.__timer = setInterval(refresh, 5000);
  } catch (e) { $("who").textContent = "auth failed: " + e.message; }
}
async function refresh() {
  try {
    const s = await api("/api/v1/stats");
    $("stats").replaceChildren(...Object.entries(s).map(([k, v]) =>
      el("span", {}, k.replace(/_/g, " ") + ": ", el("b", {}, String(v)))));
    const f = $("filter").value;
    const page = await api("/api/v1/incidents?limit=100" + (f ? "&status=" + f : ""));
    const items = page.items.map((i) => el("li", { class: i.id === selected ? "sel" : "",
        onclick: () => { selected = i.id; showIncident(i.id); refresh(); } },
      el("div", {}, el("span", { class: "sev-" + i.severity }, i.severity.toUpperCase()), ` #${i.id} ${i.title}`),
      el("div", { class: "muted" }, `${i.status} · ${i.services.join(", ")}` +
        (i.root_cause_service ? ` · root cause: ${i.root_cause_service}` : ""))));
    $("list").replaceChildren(...(items.length ? items : [el("li", { class: "muted" }, "No incidents.")]));
    if (selected) showIncident(selected, true);
  } catch (e) { $("list").replaceChildren(el("li", { class: "err" }, e.message)); }
}
function table(headers, rows) {
  return el("table", {}, el("tr", {}, ...headers.map((h) => el("th", {}, h))),
    ...rows.map((r) => el("tr", {}, ...r.map((c) => el("td", {}, c)))));
}
async function decide(id, action) {
  const comment = action === "reject" ? prompt("Reason for rejecting?") : prompt("Approval comment (optional)") ;
  if (comment === null) return;
  try { await api(`/api/v1/remediations/${id}/${action}`, { method: "POST", body: JSON.stringify({ comment }) }); }
  catch (e) { alert(e.message); }
  showIncident(selected);
}
async function rerun(id) {
  try { await api(`/api/v1/incidents/${id}/investigations`, { method: "POST" }); } catch (e) { alert(e.message); }
  showIncident(id);
}
async function showIncident(id, quiet) {
  let d; try { d = await api(`/api/v1/incidents/${id}`); } catch (e) { if (!quiet) $("detail").replaceChildren(el("p", { class: "err" }, e.message)); return; }
  const inv = d.latest_investigation; const res = inv?.result || {};
  const parts = [
    el("h2", {}, `#${d.id} ${d.title}`),
    el("div", {}, el("span", { class: "tag" }, d.status), el("span", { class: "tag sev-" + d.severity }, d.severity),
      ...d.services.map((s) => el("span", { class: "tag" }, s))),
    el("p", {}, d.summary || "Awaiting investigation…"),
  ];
  if (canOperate && d.status !== "resolved") parts.push(el("button", { onclick: () => rerun(d.id) }, "Re-run investigation"));
  parts.push(el("h3", {}, "Remediations (human approval)"));
  parts.push(d.remediations.length ? table(["Action", "Service", "Risk", "Status", ""],
    d.remediations.map((r) => [r.action, r.service, r.risk, r.status + (r.decided_by ? ` by ${r.decided_by}` : ""),
      r.status === "pending_approval" && canOperate
        ? el("span", {}, el("button", { class: "primary", onclick: () => decide(r.id, "approve") }, "Approve"), " ",
             el("button", { class: "danger", onclick: () => decide(r.id, "reject") }, "Reject"))
        : (r.result ? JSON.stringify(r.result).slice(0, 120) : "")]))
    : el("p", { class: "muted" }, "None proposed yet."));
  if (res.hypotheses?.length) {
    parts.push(el("h3", {}, "Root-cause hypotheses"));
    parts.push(table(["Service", "Cause", "Confidence", "Evidence"],
      res.hypotheses.slice(0, 5).map((h) => [h.service, h.cause, Math.round(h.confidence * 100) + "%", h.evidence.join("; ")])));
  }
  if (res.steps?.length) {
    parts.push(el("h3", {}, "Agent execution"));
    parts.push(table(["Agent", "ms", "Result"], res.steps.map((s) => [s.agent, String(Math.round(s.duration_ms)), s.summary])));
  }
  parts.push(el("h3", {}, "Alerts"));
  parts.push(table(["Alert", "Service", "Severity", "Status"], d.alerts.map((a) => [a.alertname, a.service, a.severity, a.status])));
  if (res.report) { parts.push(el("h3", {}, "Report (markdown)")); parts.push(el("pre", {}, res.report)); }
  parts.push(el("h3", {}, "Timeline"));
  parts.push(table(["Time", "Actor", "Event"], d.timeline.map((t) => [new Date(t.ts).toLocaleTimeString(), t.actor, t.action])));
  $("detail").replaceChildren(...parts);
}
$("save").addEventListener("click", () => {
  apiKey = $("key").value.trim(); try { sessionStorage.setItem("sentinelKey", apiKey); } catch (e) {}
  connect();
});
$("filter").addEventListener("change", refresh);
if (apiKey) connect();
