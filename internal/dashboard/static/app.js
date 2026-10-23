const state = { range: "7d", model: "", data: null };
const $ = (id) => document.getElementById(id);
const number = new Intl.NumberFormat("tr-TR");
const shortNumber = (value) => {
  if (value == null || Number.isNaN(Number(value))) return "—";
  const n = Number(value);
  if (n >= 1000000) return `${(n / 1000000).toFixed(1).replace(".", ",")}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10000 ? 0 : 1).replace(".", ",")}K`;
  return number.format(Math.round(n));
};
const esc = (value) => String(value ?? "").replace(/[&<>'"]/g, (c) => ({"&":"&amp;","<":"&lt;",">":"&gt;","'":"&#39;","\"":"&quot;"}[c]));
const token = () => localStorage.getItem("nudge_dashboard_token") || new URLSearchParams(location.search).get("token") || "";
const initials = (text) => text.trim().slice(0, 2).toUpperCase();

function setText(id, value) { $(id).textContent = value; }
function formatLatency(ms) { return ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(1).replace(".", ",")} sn`; }
function formatTime(value) { if (!value) return "—"; const date = new Date(value); return date.toLocaleTimeString("tr-TR", { hour: "2-digit", minute: "2-digit" }); }
function formatDate(value) { if (!value) return "—"; const date = new Date(value); return date.toLocaleDateString("tr-TR", { day: "2-digit", month: "short" }); }

async function loadDashboard() {
  const query = new URLSearchParams({ range: state.range });
  if (state.model) query.set("model", state.model);
  const headers = {}; if (token()) headers["X-Dashboard-Token"] = token();
  setLoading(true);
  try {
    const response = await fetch(`/api/dashboard?${query}`, { headers });
    if (response.status === 401) { showNotice(true); return; }
    if (!response.ok) throw new Error("dashboard unavailable");
    showNotice(false);
    state.data = await response.json();
    render(state.data);
  } catch (error) {
    showNotice(true, "Dashboard verisi yüklenemedi. API bağlantısını kontrol et.");
  } finally { setLoading(false); }
}

function setLoading(loading) { $("refresh-button").classList.toggle("loading", loading); $("refresh-button").disabled = loading; }
function showNotice(show, text) { $("notice").classList.toggle("hidden", !show); if (text) document.querySelector("#notice p").textContent = text; }

function render(data) {
  const total = data.total_messages || 0, success = data.successful_messages || 0, errors = data.error_count || 0;
  const prompt = data.prompt_tokens || 0, completion = data.completion_tokens || 0, tokens = data.total_tokens || prompt + completion;
  setText("total-messages", number.format(total)); setText("nav-count", total || "—");
  setText("success-rate", `${success} başarıyla işlendi`); setText("avg-latency", formatLatency(data.average_latency_ms || 0)); setText("total-tokens", shortNumber(tokens));
  setText("token-split", `prompt ${shortNumber(prompt)} · completion ${shortNumber(completion)}`); setText("error-rate", `${total ? Math.round(errors / total * 100) : 0}%`); setText("error-count", `${errors} hata kaydı`);
  setText("active-users", number.format(data.active_users || 0)); setText("completed-count", number.format(success));
  renderChart(data.daily || []); renderModels(data.models || []); renderRows(data.recent || []); renderTokens(prompt, completion, tokens); renderFastest(data.models || []);
}

function renderChart(days) {
  const svg = $("activity-chart"); if (!days.length) { svg.innerHTML = `<text x="50%" y="50%" text-anchor="middle" class="chart-label">Bu aralıkta henüz etkileşim yok.</text>`; return; }
  const width = 760, height = 270, left = 30, right = 13, top = 18, bottom = 34, chartW = width - left - right, chartH = height - top - bottom;
  const maxMessages = Math.max(...days.map(d => d.messages), 1), maxLatency = Math.max(...days.map(d => d.average_latency_ms), 1);
  const step = chartW / days.length, barW = Math.min(34, step * .52);
  let html = "";
  [0, .5, 1].forEach((level) => { const y = top + chartH * (1 - level); html += `<line x1="${left}" y1="${y}" x2="${width - right}" y2="${y}" class="chart-grid"/><text x="0" y="${y + 4}" class="chart-label">${Math.round(maxMessages * level)}</text>`; });
  const line = [];
  days.forEach((day, i) => { const x = left + step * i + step / 2, barH = (day.messages / maxMessages) * chartH, y = top + chartH - barH, pointY = top + chartH - (day.average_latency_ms / maxLatency) * chartH; html += `<rect x="${x - barW / 2}" y="${y}" width="${barW}" height="${barH}" rx="4" class="chart-bar"><title>${esc(day.date)} · ${day.messages} mesaj</title></rect>`; line.push(`${x},${pointY}`); html += `<text x="${x}" y="${height - 9}" text-anchor="middle" class="chart-label">${esc(day.date.slice(5))}</text>`; });
  if (days.length > 1) { html += `<polyline points="${line.join(" ")}" class="chart-line"/>`; days.forEach((day, i) => { const x = left + step * i + step / 2, y = top + chartH - (day.average_latency_ms / maxLatency) * chartH; html += `<circle cx="${x}" cy="${y}" r="3.2" class="chart-point"><title>${formatLatency(day.average_latency_ms)}</title></circle>`; }); }
  svg.innerHTML = html;
}

function renderModels(models) {
  const legend = $("model-legend"), total = models.reduce((sum, m) => sum + Number(m.messages || 0), 0); setText("model-total", total || 0); setText("top-model", models[0]?.model || "—");
  if (!models.length) { $("model-donut").style.background = "conic-gradient(#243247 0 100%)"; legend.innerHTML = `<div class="empty-mini">Henüz model verisi yok.</div>`; return; }
  const colors = ["#65e2d6", "#9c8cff", "#b8e76d", "#f3a66c", "#ee7891"], stops = []; let cursor = 0;
  legend.innerHTML = models.slice(0, 5).map((model, i) => { const percentage = total ? model.messages / total * 100 : 0; const start = cursor; cursor += percentage; stops.push(`${colors[i]} ${start}% ${cursor}%`); return `<div class="model-row"><i style="background:${colors[i]}"></i><strong title="${esc(model.model)}">${esc(model.model || "Bilinmeyen model")}</strong><small>${Math.round(percentage)}%</small></div>`; }).join("");
  if (cursor < 100) stops.push(`#243247 ${cursor}% 100%`); $("model-donut").style.background = `conic-gradient(${stops.join(",")})`;
}

function renderRows(rows) {
  const body = $("conversation-rows"); setText("table-caption", `${rows.length} son kayıt gösteriliyor`);
  if (!rows.length) { body.innerHTML = `<tr><td colspan="6"><div class="empty-mini" style="padding:24px 0">Bu aralıkta henüz konuşma kaydı yok.</div></td></tr>`; return; }
  body.innerHTML = rows.map((row) => `<tr><td class="conversation-cell"><strong title="${esc(row.request)}">${esc(row.request || "İsimsiz mesaj")}</strong><small title="${esc(row.response)}">↳ ${esc(row.response || "Yanıt yok")}</small></td><td class="model-cell" title="${esc(row.model)}">${esc(row.model || "—")}</td><td><span class="status ${esc(row.status)}">${row.status === "success" ? "Başarılı" : row.status === "degraded" ? "Kısmi" : "Hata"}</span></td><td class="latency-cell">${formatLatency(row.latency_ms)}</td><td class="tokens-cell">${shortNumber(row.total_tokens)}</td><td class="time-cell">${formatDate(row.created_at)}<br>${formatTime(row.created_at)}</td></tr>`).join("");
}

function renderTokens(prompt, completion, total) { const promptPct = total ? Math.round(prompt / total * 100) : 0, completionPct = total ? 100 - promptPct : 0; setText("prompt-percent", `${promptPct}%`); setText("completion-percent", `${completionPct}%`); setText("prompt-value", number.format(prompt)); setText("completion-value", number.format(completion)); $("prompt-bar").style.width = `${promptPct}%`; $("completion-bar").style.width = `${completionPct}%`; }
function renderFastest(models) { const fastest = [...models].sort((a, b) => a.average_latency_ms - b.average_latency_ms)[0]; setText("fastest-model", fastest ? `${fastest.model} · ${formatLatency(fastest.average_latency_ms)}` : "—"); }

$("range-select").addEventListener("change", (event) => { state.range = event.target.value; loadDashboard(); });
$("refresh-button").addEventListener("click", loadDashboard);
$("model-filter").addEventListener("change", (event) => { state.model = event.target.value.trim(); loadDashboard(); });
$("clear-filter").addEventListener("click", () => { $("model-filter").value = ""; state.model = ""; loadDashboard(); });
$("save-token").addEventListener("click", () => { const value = $("token-input").value.trim(); if (value) localStorage.setItem("nudge_dashboard_token", value); loadDashboard(); });
$("token-input").addEventListener("keydown", (event) => { if (event.key === "Enter") $("save-token").click(); });
$("today-label").textContent = new Date().toLocaleDateString("tr-TR", { day: "numeric", month: "long", year: "numeric" });
loadDashboard();
