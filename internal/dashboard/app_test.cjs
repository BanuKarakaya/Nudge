const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function harness() {
  const elements = new Map(), requests = [];
  function element(id) {
    if (!elements.has(id)) elements.set(id, {
      textContent: '', innerHTML: '', value: '', style: {}, dataset: {}, open: false,
      classList: { toggle() {}, add() {} }, handlers: {},
      setAttribute() {}, removeAttribute() {}, focus() {}, scrollIntoView() {},
      addEventListener(event, fn) { this.handlers[event] = fn; },
      close() { this.open = false; }, showModal() { this.open = true; },
    });
    return elements.get(id);
  }
  const sandbox = vm.createContext({
    Intl, Date, URL, URLSearchParams, AbortController, setTimeout, clearTimeout,
    localStorage: { removeItem() {} }, location: { href: 'http://localhost/dashboard', search: '', hash: '' }, history: {},
    window: { addEventListener() {} },
    document: { getElementById: element, querySelector: element, querySelectorAll: () => [], addEventListener() {} },
    fetch: (url, options) => new Promise(resolve => requests.push({ url, options, resolve })),
  });
  vm.runInContext(fs.readFileSync(__dirname + '/static/app.js', 'utf8'), sandbox);
  const run = code => vm.runInContext(code, sandbox);
  const reply = async (index, data = {}, status = 200) => {
    requests[index].resolve({ ok: status === 200, status, json: async () => data });
    await new Promise(resolve => setImmediate(resolve));
  };
  return { element, requests, run, reply };
}

test('pagination retains date, model and error filters', async () => {
  const h = harness();
  await h.reply(0, { total_messages: 250 });
  h.run('state.model = "gpt"; state.status = "issues";');
  h.element('view-all').handlers.click();
  let query = new URL(h.requests[1].url, 'http://localhost').searchParams;
  assert.equal(query.get('limit'), '100'); assert.equal(query.get('offset'), '0');
  assert.equal(query.get('model'), 'gpt'); assert.equal(query.get('status'), 'issues');
  await h.reply(1, { total_messages: 250 });
  h.element('next-page').handlers.click();
  query = new URL(h.requests[2].url, 'http://localhost').searchParams;
  assert.equal(query.get('offset'), '100'); assert.equal(query.get('range'), '7d');
  await h.reply(2, { total_messages: 250 });
  h.element('next-page').handlers.click();
  await h.reply(3, { total_messages: 250 });
  assert.equal(h.element('next-page').disabled, true);
  assert.equal(h.element('previous-page').disabled, false);
});

test('late responses cannot restore data after logout', async () => {
  const h = harness();
  h.run('dashboardToken = "private"; disconnect();');
  await h.reply(0, { total_messages: 99 });
  assert.equal(h.run('state.data'), null);
  assert.equal(h.run('token()'), '');
  assert.equal(h.element('total-messages').textContent, '—');
});

test('unauthorized refresh clears private details and token', async () => {
  const h = harness();
  await h.reply(0, { total_messages: 1, recent: [{ request: 'private message', response: 'private answer' }] });
  h.run('dashboardToken = "bad"; showConversation(0); loadDashboard();');
  await h.reply(1, {}, 401);
  assert.equal(h.run('state.data'), null);
  assert.equal(h.run('token()'), '');
  assert.equal(h.element('detail-dialog').open, false);
  assert.equal(h.element('dialog-content').textContent, '');
  assert.match(h.element('notice-message').textContent, /geçersiz/);
});

test('latest request wins and conversation contents are escaped', async () => {
  const h = harness();
  h.run('state.range = "24h"; loadDashboard();');
  await h.reply(1, { total_messages: 1, recent: [{ request: '<img src=x onerror=alert(1)>', response: '<script>bad()</script>' }] });
  await h.reply(0, { total_messages: 999 });
  assert.equal(h.run('state.data.total_messages'), 1);
  h.run('showConversation(0)');
  assert.ok(!h.element('dialog-content').innerHTML.includes('<script>'));
  assert.match(h.element('dialog-content').innerHTML, /&lt;script&gt;/);
  assert.match(h.element('conversation-rows').innerHTML, /&lt;img/);
});
