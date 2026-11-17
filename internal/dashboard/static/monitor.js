/* Same-origin, session-bound polling. Only server-rendered, escaped HTML is used. */
(function () {
  'use strict';
  const view = document.getElementById('monitor-view');
  const status = document.getElementById('monitor-status');
  const toggle = document.getElementById('monitor-toggle');
  if (!view || !status || !toggle) return;
  const endpoint = new URL(view.dataset.url, window.location.origin);
  if (endpoint.origin !== window.location.origin || endpoint.pathname !== '/dashboard/live/data') return;
  let paused = false, stopped = false, timer, controller, generation = 0;
  function schedule() {
    clearTimeout(timer);
    if (!paused && !stopped && !document.hidden) timer = setTimeout(refresh, 5000);
  }
  function cancel() { generation++; clearTimeout(timer); if (controller) controller.abort(); }
  async function refresh() {
    if (paused || stopped || document.hidden) return;
    const current = ++generation;
    controller = new AbortController();
    const activeController = controller;
    const timeout = setTimeout(() => activeController.abort(), 12000);
    try {
      const response = await fetch(endpoint.href, {credentials: 'same-origin', cache: 'no-store', signal: controller.signal, headers: {'Accept': 'application/json'}});
      if (current !== generation) return;
      if (response.status === 401 || response.status === 403) {
        stopped = true;
        view.replaceChildren();
        status.textContent = 'Oturum veya erişim sona erdi. Sayfayı yenileyip giriş yap.';
        toggle.disabled = true;
        return;
      }
      if (!response.ok) throw new Error('unavailable');
      const data = await response.json();
      if (current !== generation || paused || stopped || document.hidden) return;
      if (typeof data.html !== 'string' || typeof data.updated_at !== 'string') throw new Error('invalid response');
      const expanded = new Set(Array.from(view.querySelectorAll('details[open][data-job-id]'), node => node.dataset.jobId));
      const panels = new Set(Array.from(view.querySelectorAll('details[open][data-panel-id]'), node => node.dataset.panelId));
      const scrolls = Array.from(view.querySelectorAll('.monitor-events, .wire-grid pre, .bounded-list')).map(node => node.scrollTop);
      const template = document.createElement('template');
      template.innerHTML = data.html;
      // There are no scripts or event handlers in this server-owned fragment.
      view.replaceChildren(template.content.cloneNode(true));
      view.querySelectorAll('details[data-job-id]').forEach(node => { node.open = expanded.has(node.dataset.jobId); });
      view.querySelectorAll('details[data-panel-id]').forEach(node => { node.open = panels.has(node.dataset.panelId); });
      view.querySelectorAll('.monitor-events, .wire-grid pre, .bounded-list').forEach((node, i) => { node.scrollTop = scrolls[i] || 0; });
      status.textContent = 'Canlı · 5 saniyede bir yenilenir';
    } catch (error) {
      if (current === generation && !paused && !stopped && !document.hidden) status.textContent = 'Bağlantı kurulamadı · Ekrandaki kayıtlar güncel olmayabilir. Yeniden denenecek.';
    } finally { clearTimeout(timeout); if (current === generation) schedule(); }
  }
  toggle.addEventListener('click', () => {
    paused = !paused; cancel();
    toggle.setAttribute('aria-pressed', String(paused));
    toggle.textContent = paused ? 'Canlı izlemeyi sürdür' : 'Duraklat';
    status.textContent = paused ? 'Duraklatıldı · Son görüntü gösteriliyor' : 'Yenileniyor…';
    if (!paused) refresh();
  });
  document.addEventListener('visibilitychange', () => { cancel(); if (!document.hidden && !paused && !stopped) refresh(); });
  document.querySelectorAll('form').forEach(form => form.addEventListener('submit', () => { stopped = true; cancel(); if (form.getAttribute('action') === '/dashboard/logout') view.replaceChildren(); }));
  window.addEventListener('pagehide', () => { stopped = true; cancel(); });
  window.addEventListener('pageshow', event => { if (event.persisted) { view.replaceChildren(); window.location.reload(); } });
  schedule();
}());
