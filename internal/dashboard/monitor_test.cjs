const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(__dirname + '/static/monitor.js', 'utf8');
function fixture() {
  const timers = new Map(), events = {}, windowEvents = {}, requests = [];
  let timerID = 0;
  const details = {dataset: {jobId: '42'}, open: true};
  const panel = {dataset: {panelId: 'call-0'}, open: true}, scroller = {scrollTop: 120};
  const view = {dataset: {url: '/dashboard/live/data?section=operations&scope=personal&status=active&model=daily'}, html: 'initial',
    querySelectorAll(selector) { if (selector.includes('.monitor-events')) return [scroller]; const node = selector.includes('data-panel-id') ? panel : details; return selector.includes('[open]') && !node.open ? [] : [node]; },
    replaceChildren(node) { this.html = node ? node.html : ''; details.open = false; panel.open = false; scroller.scrollTop = 0; }};
  const status = {textContent: ''};
  const toggle = {textContent: '', attrs: {}, addEventListener(_, fn) { this.click = fn; }, setAttribute(k, v) { this.attrs[k] = v; }};
  const logout = {getAttribute() {return '/dashboard/logout';}, addEventListener(_, fn) {this.submit = fn;}};
  const document = {hidden: false, getElementById(id) {return {'monitor-view': view, 'monitor-status': status, 'monitor-toggle': toggle}[id];},
    querySelectorAll() {return [logout];}, addEventListener(k, fn) {events[k] = fn;},
    createElement() {const node = {innerHTML: ''}; node.content = {cloneNode() {return {html: node.innerHTML};}}; return node;}};
  const window = {location: {origin: 'https://nudgy.test', reload() {}}, addEventListener(k, fn) {windowEvents[k] = fn;}};
  vm.runInNewContext(source, {document, window, URL, AbortController, Set, Array,
    setTimeout(fn, ms) {const id = ++timerID; timers.set(id, {fn, ms}); return id;}, clearTimeout(id) {timers.delete(id);},
    fetch(url, options) {return new Promise(resolve => requests.push({url, options, resolve}));}});
  return {view, status, toggle, logout, document, details, panel, scroller, events, requests, timers,
    poll() {const [id, timer] = [...timers].find(([, t]) => t.ms === 5000); timers.delete(id); timer.fn();}};
}
const flush = () => new Promise(resolve => setImmediate(resolve));
const ok = html => ({status: 200, ok: true, json: async () => ({html, updated_at: '2026-11-16T10:00:00Z'})});
test('monitor preserves filters and open details during polling', async () => {
  const f = fixture(); f.poll(); assert.match(f.requests[0].url, /status=active&model=daily/);
  assert.equal(f.requests[0].options.cache, 'no-store');
  f.requests[0].resolve(ok('fresh')); await flush();
  assert.equal(f.view.html, 'fresh'); assert.equal(f.details.open, true); assert.equal(f.panel.open, true); assert.equal(f.scroller.scrollTop, 120); assert.match(f.status.textContent, /Canlı/);
});
test('expired monitor session clears previously displayed data', async () => {
  const f = fixture(); f.poll(); f.requests[0].resolve({status: 401}); await flush();
  assert.equal(f.view.html, ''); assert.equal(f.toggle.disabled, true); assert.equal(f.timers.size, 0);
});
test('monitor failure retains last snapshot and marks it stale', async () => {
  const f = fixture(); f.poll(); f.requests[0].resolve({status: 503, ok: false}); await flush();
  assert.equal(f.view.html, 'initial'); assert.match(f.status.textContent, /güncel olmayabilir/);
});
test('late response cannot restore data after logout', async () => {
  const f = fixture(); f.poll(); f.logout.submit(); f.requests[0].resolve(ok('private')); await flush();
  assert.equal(f.view.html, ''); assert.equal(f.timers.size, 0);
});
test('pause and hidden tabs stop refresh and discard in-flight results', async () => {
  const f = fixture(); f.poll(); f.toggle.click(); f.requests[0].resolve(ok('late')); await flush();
  assert.equal(f.view.html, 'initial'); assert.equal(f.timers.size, 0);
  f.toggle.click(); assert.equal(f.requests.length, 2); f.document.hidden = true; f.events.visibilitychange();
  f.requests[1].resolve(ok('hidden')); await flush(); assert.equal(f.view.html, 'initial'); assert.equal(f.timers.size, 0);
});
