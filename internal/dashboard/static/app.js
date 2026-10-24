const state = { range: "7d", model: "", status: "", limit: 24, offset: 0, data: null, loading: false };
let requestController, requestVersion = 0, filterTimer;
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
let dashboardToken = "";
const token = () => dashboardToken;
// Remove tokens left by older dashboard versions; credentials stay in memory.
try { localStorage.removeItem("nudge_dashboard_token"); } catch {}
if (new URLSearchParams(location.search).has("token")) {
  const cleanURL = new URL(location.href); cleanURL.searchParams.delete("token");
  history.replaceState(null, "", cleanURL.pathname + cleanURL.search + cleanURL.hash);
}
const initials = (text) => text.trim().slice(0, 2).toUpperCase();

function setText(id, value) { $(id).textContent = value; }
function formatLatency(ms) { return ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(1).replace(".", ",")} sn`; }
function formatTime(value) { if (!value) return "—"; const date = new Date(value); return date.toLocaleTimeString("tr-TR", { hour: "2-digit", minute: "2-digit" }); }
function formatDate(value) { if (!value) return "—"; const date = new Date(value); return date.toLocaleDateString("tr-TR", { day: "2-digit", month: "short" }); }


async function loadDashboard() {
  clearTimeout(filterTimer);
  requestController?.abort();
  const version = ++requestVersion;
  requestController = new AbortController();
  const query = new URLSearchParams({ range: state.range, limit: state.limit, offset: state.offset });
  if (state.model) query.set("model", state.model);
  if (state.status) query.set("status", state.status);
  const headers = {}; if (token()) headers["X-Dashboard-Token"] = token();
  clearData("Veriler yükleniyor…");
  setLoading(true);
  setConnection("Veriler yükleniyor", "loading");
  try {
    const response = await fetch(`/api/dashboard?${query}`, { headers, signal: requestController.signal });
    if (version !== requestVersion) return;
    if (!response.ok) {
      if (response.status === 401) dashboardToken = "";
      throw new Error(response.status === 401
        ? "Erişim anahtarı eksik veya geçersiz. Anahtarını girerek bağlan."
        : response.status === 503 ? "Dashboard şu anda kullanılamıyor. Sunucu yapılandırmasını kontrol et."
        : "Dashboard verisi yüklenemedi. Yenile ile tekrar deneyebilirsin.");
    }
    const data = await response.json();
    if (version !== requestVersion) return;
    state.data = data;
    showNotice(false);
    render(data);
    setConnection("Dashboard bağlı", "connected");
    $("updated-label").textContent = "Son güncelleme: " + new Date().toLocaleTimeString("tr-TR");
  } catch (error) {
    if (version !== requestVersion || error.name === "AbortError") return;
    clearData("Veri alınamadı. Bağlantını kontrol edip yeniden dene.");
    showNotice(true, error.message);
    setConnection("Veriye erişilemiyor", "disconnected");
  } finally {
    if (version === requestVersion) setLoading(false);
  }
}

function setLoading(loading) {
  state.loading = loading;
  $("refresh-button").classList.toggle("loading", loading);
  $("refresh-button").disabled = loading;
  $("overview").setAttribute("aria-busy", String(loading));
  updatePagination();
}
function showNotice(show, text = "Erişim anahtarını girerek canlı metrikleri yükleyebilirsin.") {
  $("notice").classList.toggle("hidden", !show);
  $("notice-message").textContent = text;
}
function setConnection(label, status) {
  $("connection-label").textContent = label;
  $("workspace-status").textContent = label;
  $("connection-dot").dataset.status = status;
  document.querySelector(".workspace-dot").dataset.status = status;
}
function clearData(message = "Canlı verileri görmek için bağlan.") {
  state.data = null;
  $("detail-dialog").close();
  $("dialog-content").textContent = "";
  for (const id of ["total-messages", "avg-latency", "total-tokens", "error-rate", "active-users",
    "completed-count", "fastest-model", "top-model", "model-total", "prompt-percent", "completion-percent",
    "prompt-value", "completion-value", "nav-count"]) setText(id, "—");
  setText("success-rate", "— başarıyla işlendi"); setText("error-count", "— hata kaydı");
  setText("token-split", "prompt — · completion —");
  $("prompt-bar").style.width = $("completion-bar").style.width = "0%";
  $("activity-chart").innerHTML = "";
  $("model-donut").style.background = "#f0e6df";
  $("model-legend").innerHTML = '<div class="empty-mini">Henüz veri alınmadı.</div>';
  $("conversation-rows").innerHTML = `<tr><td colspan="6" class="empty-state">${esc(message)}</td></tr>`;
  $("notification-dot").classList.add("hidden");
  $("table-caption").textContent = message;
  updatePagination();
}
function updatePagination() {
  const total = Number(state.data?.total_messages || 0);
  const unavailable = state.loading || !state.data;
  $("previous-page").disabled = unavailable || state.offset === 0;
  $("next-page").disabled = unavailable || state.offset + state.limit >= total;
  $("view-all").disabled = unavailable || total === 0;
  $("view-all").textContent = state.limit === 100 ? "Kısa görünüm" : "Tümünü gör →";
}

function render(data) {
  const total = data.total_messages || 0, success = data.successful_messages || 0, errors = data.error_count || 0;
  const prompt = data.prompt_tokens || 0, completion = data.completion_tokens || 0, tokens = data.total_tokens || prompt + completion;
  setText("total-messages", number.format(total)); setText("nav-count", total || "—");
  setText("success-rate", `${success} başarıyla işlendi`); setText("avg-latency", formatLatency(data.average_latency_ms || 0)); setText("total-tokens", shortNumber(tokens));
  setText("token-split", `prompt ${shortNumber(prompt)} · completion ${shortNumber(completion)}`); setText("error-rate", `${total ? Math.round(errors / total * 100) : 0}%`); setText("error-count", `${errors} hata kaydı`);
  setText("active-users", number.format(data.active_users || 0)); setText("completed-count", number.format(success));
  $("notification-dot").classList.toggle("hidden", errors === 0);
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
  if (!models.length) { $("model-donut").style.background = "conic-gradient(#f0e6df 0 100%)"; legend.innerHTML = `<div class="empty-mini">Henüz model verisi yok.</div>`; return; }
  const colors = ["#a82a19", "#d49a50", "#9bac71", "#c87843", "#78483a"], stops = []; let cursor = 0;

  legend.innerHTML = models.slice(0, 5).map((model, i) => {
    const percentage = total ? model.messages / total * 100 : 0, start = cursor;
    cursor += percentage; stops.push(`${colors[i]} ${start}% ${cursor}%`);
    return `<button class="model-row" data-model="${esc(model.model)}" ${model.model ? "" : "disabled"} title="${esc(model.model || "Model bilgisi yok")} konuşmalarını göster"><i style="background:${colors[i]}"></i><strong>${esc(model.model || "Bilinmeyen model")}</strong><small>${Math.round(percentage)}%</small></button>`;
  }).join("");
  if (models.length > 5) legend.innerHTML += '<button class="view-all" data-action="models">Diğer modelleri göster →</button>';
  if (cursor < 100) stops.push(`#ddd2d6 ${cursor}% 100%`);
  $("model-donut").style.background = `conic-gradient(${stops.join(",")})`;

}

function renderRows(rows) {
  if (location.hash === "#operations") { renderOperationRows(rows); return; }
  const body = $("conversation-rows"); setText("table-caption", rows.length ? `${number.format(state.offset + 1)}–${number.format(state.offset + rows.length)} / ${number.format(state.data.total_messages)} kayıt` : "Bu filtrelerde kayıt yok.");
  if (!rows.length) { body.innerHTML = `<tr><td colspan="6"><div class="empty-mini" style="padding:24px 0">Bu aralıkta henüz konuşma kaydı yok.</div></td></tr>`; return; }
  body.innerHTML = rows.map((row, index) => `<tr><td class="conversation-cell"><button class="conversation-open" data-row="${index}" aria-haspopup="dialog" title="Konuşmanın tamamını aç">${esc(row.request || "İsimsiz mesaj")}</button><small title="${esc(row.response)}">↳ ${esc(row.response || "Yanıt yok")}</small></td><td class="model-cell" title="${esc(row.model)}">${esc(row.model || "—")}</td><td><span class="status ${esc(row.status)}">${row.status === "success" ? "Başarılı" : row.status === "degraded" ? "Kısmi" : row.status === "processing" ? "İşleniyor" : "Hata"}</span></td><td class="latency-cell">${formatLatency(row.latency_ms)}</td><td class="tokens-cell">${shortNumber(row.total_tokens)}</td><td class="time-cell">${formatDate(row.created_at)}<br>${formatTime(row.created_at)}</td></tr>`).join("");
}

function renderTokens(prompt, completion, total) { const promptPct = total ? Math.round(prompt / total * 100) : 0, completionPct = total ? 100 - promptPct : 0; setText("prompt-percent", `${promptPct}%`); setText("completion-percent", `${completionPct}%`); setText("prompt-value", number.format(prompt)); setText("completion-value", number.format(completion)); $("prompt-bar").style.width = `${promptPct}%`; $("completion-bar").style.width = `${completionPct}%`; }
function renderFastest(models) { const fastest = [...models].sort((a, b) => a.average_latency_ms - b.average_latency_ms)[0]; setText("fastest-model", fastest ? `${fastest.model} · ${formatLatency(fastest.average_latency_ms)}` : "—"); }


const dialog = $("detail-dialog");
function openDialog(title, content) {
  $("dialog-title").textContent = title;
  $("dialog-content").innerHTML = content;
  if (!dialog.open) dialog.showModal();
}
function navigate(section) {
  location.hash = section;
  document.getElementById(section)?.scrollIntoView({ behavior: "smooth", block: "start" });
  syncNavigation();
}
function syncNavigation() {
  const section = location.hash.slice(1) || "overview";
  const operations = section === "operations";
  $("overview").classList.toggle("operations-view", operations);
  $("operations-help").classList.toggle("hidden", !operations);
  document.querySelector(".page-heading h1").textContent = operations ? "Yapılan işlemler" : "Nudgy overview";
  document.querySelector(".page-heading p").textContent = operations
    ? "Nudgy ne yaptı? Her mesajın araç adımlarını, sonuçlarını ve sürelerini incele."
    : "Mesajlarını, model performansını ve token kullanımını tek bakışta takip et.";
  document.querySelector("#conversations h2").textContent = operations ? "İşlem geçmişi" : "Son konuşmalar";
  document.querySelector("#conversations .panel-head p").textContent = operations
    ? "Bir isteği açarak işlem zaman çizelgesini ve güvenli log detaylarını gör. Filtreler mesaj durumuna uygulanır."
    : "Kullanıcı mesajı → Nudgy yanıtı detayları";
  const headings = operations ? ["MESAJ / YAPILAN İŞLEM", "MODEL", "İŞLEM ADIMLARI", "TOPLAM SÜRE", "ADIM", "ZAMAN"]
    : ["MESAJ / YANIT", "MODEL", "DURUM", "SÜRE", "TOKEN", "ZAMAN"];
  document.querySelectorAll("#conversations th").forEach((th, index) => { th.textContent = headings[index]; });
  if (state.data) renderRows(state.data.recent || []);
  $(operations ? "overview" : section)?.scrollIntoView({ block: "start" });
  document.querySelectorAll(".nav-item").forEach(link => {
    const selected = link.getAttribute("href") === "#" + section;
    link.classList.toggle("active", selected);
    if (selected) {
      link.setAttribute("aria-current", "location");
      $("breadcrumb-current").textContent = link.getAttribute("aria-label");
    } else link.removeAttribute("aria-current");
  });
}
function changeFilters() { state.offset = 0; return loadDashboard(); }
function filterModel(model) {
  state.model = model; $("model-filter").value = model;
  dialog.close(); navigate("conversations"); changeFilters();
}
function showModels() {
  const models = state.data?.models || [];
  openDialog("Model detayları", models.length
    ? '<p>Bir model seçerek konuşmalarını filtrele.</p><div class="dialog-table"><table><thead><tr><th>Model / Sağlayıcı</th><th>İstek</th><th>Token</th><th>Ort. süre</th></tr></thead><tbody>' +
      models.map(m => `<tr><td><button class="conversation-open" data-model="${esc(m.model)}" ${m.model ? "" : "disabled"}>${esc(m.model || "Bilinmeyen model")}</button><small>${esc(m.provider)}</small></td><td>${number.format(m.messages)}</td><td>${number.format(m.tokens)}</td><td>${formatLatency(m.average_latency_ms)}</td></tr>`).join("") +
      '</tbody></table></div><button class="ghost-button" data-action="clear-model">Model filtresini temizle</button>'
    : '<p>Model detayları için dashboard’a bağlan ve veri bulunan bir tarih aralığı seç.</p>');
}
function showConversation(index) {
  const row = state.data?.recent?.[index]; if (!row) return;
  openDialog("Konuşma detayı",
    `<dl class="detail-meta"><div><dt>Model</dt><dd>${esc(row.model || "—")}</dd></div><div><dt>Sağlayıcı</dt><dd>${esc(row.provider || "—")}</dd></div><div><dt>Mesaj durumu</dt><dd>${esc(({success:"Başarılı",failed:"Hatalı",degraded:"Kısmi",processing:"İşleniyor"})[row.status] || row.status)}</dd></div><div><dt>İşlem</dt><dd>${esc(row.action || "—")}</dd></div><div><dt>Süre</dt><dd>${formatLatency(row.latency_ms)}</dd></div><div><dt>Zaman</dt><dd>${esc(new Date(row.created_at).toLocaleString("tr-TR"))}</dd></div><div><dt>Prompt / Completion / Toplam token</dt><dd>${number.format(row.prompt_tokens || 0)} / ${number.format(row.completion_tokens || 0)} / ${number.format(row.total_tokens || 0)}</dd></div></dl><h3>Kullanıcı mesajı</h3><pre class="message-content">${esc(row.request || "Mesaj yok")}</pre>${operationTimeline(row)}<h3>Nudgy yanıtı</h3><pre class="message-content">${esc(row.response || "Yanıt kaydedilmedi")}</pre>`);
  if (location.hash === "#operations") $("dialog-title").textContent = "İşlem detayı";
}

function noOperations(row) {
  return row.trace_version ? "Bu istekte araç adımı kaydedilmedi."
    : "Bu eski mesaj için işlem detayı kaydedilmemiş.";
}
function renderOperationRows(rows) {
  setText("table-caption", rows.length ? `${number.format(state.offset + 1)}–${number.format(state.offset + rows.length)} / ${number.format(state.data.total_messages)} istek` : "Bu filtrelerde kayıt yok.");
  $("conversation-rows").innerHTML = rows.length ? rows.map((row, index) => {
    const ops = row.operations || [], failed = ops.filter(op => op.status === "failed").length;
    const summary = ops.length ? ops.map(op => op.title).join(" → ") : noOperations(row);
    return `<tr><td class="conversation-cell"><button class="conversation-open" data-row="${index}" aria-haspopup="dialog">${esc(row.request || "İsimsiz mesaj")}</button><small title="${esc(summary)}">${esc(summary)}</small></td><td class="model-cell">${esc(row.model || "—")}</td><td><span class="status ${failed ? "failed" : ops.length ? "success" : ""}">${failed ? `${failed} adım hatalı` : ops.length ? "Adımlar tamamlandı" : row.trace_version ? "Adım yok" : "Kayıt yok"}</span></td><td>${formatLatency(row.latency_ms || 0)}</td><td>${row.trace_version || ops.length ? number.format(ops.length) : "—"}</td><td class="time-cell">${formatDate(row.created_at)}<br>${formatTime(row.created_at)}</td></tr>`;
  }).join("") : '<tr><td colspan="6" class="empty-state">Bu aralıkta henüz işlem kaydı yok.</td></tr>';
}
function operationTimeline(row) {
  const ops = row.operations || [];
  if (!ops.length) return `<h3>Yapılan işlemler</h3><p>${esc(noOperations(row))}</p>`;
  return '<h3>Yapılan işlemler</h3><ol class="operation-timeline">' + ops.map((op, index) => {
    const status = op.status === "success" ? "Başarılı" : "Başarısız / reddedildi";
    // Only expose the documented trace fields, not future arbitrary payloads.
    const log = { request_id: row.request_id, step: index + 1, tool: op.tool, status: op.status, model: op.model, started_at: op.started_at, duration_ms: op.duration_ms };
    return `<li><div class="operation-heading"><strong>${esc(op.title)}</strong><span class="status ${op.status === "success" ? "success" : "failed"}">${status}</span></div><p class="operation-meta">${formatTime(op.started_at)} · ${formatLatency(op.duration_ms || 0)} · ${esc(op.model || "Model bilgisi yok")}</p><p>${esc(op.detail)}</p>${op.path ? `<h4>Dosya: ${esc(op.path)}</h4>` : ""}${op.diff ? `<pre class="operation-diff">${esc(op.diff)}</pre>` : ""}<details><summary>Teknik log</summary><pre class="message-content">${esc(JSON.stringify(log, null, 2))}</pre></details></li>`;
  }).join("") + '</ol>';
}
function showNotifications() {
  const data = state.data;
  openDialog("Bildirimler", !data ? '<p>Bildirimleri görmek için önce dashboard’a bağlan.</p><button class="ghost-button" data-action="connect">Bağlan</button>'
    : `<p>Seçili tarih aralığı ve filtrelere göre ${number.format(data.error_count || 0)} hatalı veya kısmi işlem var.</p>` +
      (data.error_count ? '<button class="ghost-button" data-action="errors">Hatalı ve kısmi konuşmaları göster</button>' : '<p>Bu görünümde hata kaydı bulunmuyor.</p>'));
}
function showAccount() {
  openDialog("Dashboard erişimi", `<p>${token() ? "Bu sayfada bir erişim anahtarı kullanılıyor." : "Henüz giriş yapılmadı."} Anahtar yalnızca bu sayfa açıkken bellekte tutulur.</p><div class="dialog-actions"><button class="ghost-button" data-action="connect">Erişim anahtarı gir / değiştir</button><button class="ghost-button" data-action="disconnect" ${token() ? "" : "disabled"}>Çıkış yap</button></div>`);
}
function disconnect() {
  clearTimeout(filterTimer);
  requestController?.abort(); requestVersion++;
  dashboardToken = ""; $("token-input").value = "";
  dialog.close(); clearData(); setLoading(false);
  setConnection("Oturum kapatıldı", "disconnected");
  $("updated-label").textContent = "Henüz veri alınmadı";
  showNotice(true); navigate("overview"); $("token-input").focus();
}
function showErrors() {
  state.status = "issues"; $("status-filter").value = "issues";
  dialog.close(); navigate("conversations"); changeFilters();
}
$("close-dialog").addEventListener("click", () => dialog.close());
dialog.addEventListener("click", event => { if (event.target === dialog) dialog.close(); });
document.addEventListener("click", event => {
  const model = event.target.closest("[data-model]");
  if (model && !model.disabled) { filterModel(model.dataset.model); return; }
  const row = event.target.closest("[data-row]");
  if (row) { showConversation(Number(row.dataset.row)); return; }
  const action = event.target.closest("[data-action]")?.dataset.action;
  if (action === "models") showModels();
  if (action === "clear-model") filterModel("");
  if (action === "errors") showErrors();
  if (action === "disconnect") disconnect();
  if (action === "connect") {
    dialog.close(); showNotice(true); navigate("overview"); $("token-input").focus();
  }
  const metric = event.target.closest("[data-metric]")?.dataset.metric;
  if (metric === "errors") showErrors();
  else if (metric) navigate(metric);
});
$("workspace-button").addEventListener("click", () => openDialog("Çalışma alanları",
  '<p>Bu yönetici dashboard’u bağlı tüm Slack çalışma alanlarının toplam verilerini gösterir. Çalışma alanı bazında seçim bulunmuyor.</p><p>Bağlantı: ' + esc($("connection-label").textContent) + '</p><button class="ghost-button" data-action="connect">Dashboard erişimini yönet</button>'));
$("account-button").addEventListener("click", showAccount);
$("notifications-button").addEventListener("click", showNotifications);
$("model-details").addEventListener("click", showModels);
$("range-select").addEventListener("change", event => { state.range = event.target.value; changeFilters(); });
$("refresh-button").addEventListener("click", loadDashboard);
$("model-filter").addEventListener("input", event => {
  state.model = event.target.value.trim(); clearTimeout(filterTimer);
  requestController?.abort(); requestVersion++;
  clearData("Filtre uygulanıyor…"); setLoading(true);
  filterTimer = setTimeout(changeFilters, 300);
});
$("model-filter").addEventListener("keydown", event => { if (event.key === "Enter") changeFilters(); });
$("status-filter").addEventListener("change", event => { state.status = event.target.value; changeFilters(); });
$("clear-filter").addEventListener("click", () => {
  $("model-filter").value = $("status-filter").value = ""; state.model = state.status = ""; changeFilters();
});
$("save-token").addEventListener("click", () => {
  const value = $("token-input").value.trim();
  if (!value) { showNotice(true, "Lütfen erişim anahtarını gir."); $("token-input").focus(); return; }
  dashboardToken = value; $("token-input").value = ""; state.offset = 0; loadDashboard();
});
$("token-input").addEventListener("keydown", event => { if (event.key === "Enter") $("save-token").click(); });
$("previous-page").addEventListener("click", () => { state.offset = Math.max(0, state.offset - state.limit); loadDashboard(); });
$("next-page").addEventListener("click", () => { state.offset += state.limit; loadDashboard(); });
$("view-all").addEventListener("click", () => {
  state.limit = state.limit === 24 ? 100 : 24; state.offset = 0; navigate(location.hash === "#operations" ? "operations" : "conversations"); loadDashboard();
});
window.addEventListener("hashchange", syncNavigation);
$("today-label").textContent = new Date().toLocaleDateString("tr-TR", { day: "numeric", month: "long", year: "numeric" });
syncNavigation();
loadDashboard();
