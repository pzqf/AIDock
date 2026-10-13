'use strict';
// Synthetic HTTP state regressions; actual browser rendering is checked separately.
const {readFileSync} = require('node:fs');
const {join} = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
class Element {
  constructor(id = '', tag = 'div') { this.id = id; this.tagName = tag; this.textContent = ''; this.hidden = false; this.disabled = false; this.children = []; this.events = {}; this.classList = {toggle(){}}; }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  addEventListener(name, handler) { this.events[name] = handler; }
}
const ids = [...readFileSync(join(__dirname, '../assets/bridge.html'), 'utf8').matchAll(/id="([^"]+)"/g)].map(match => match[1]);
const dom = Object.fromEntries(ids.map(id => [id, new Element(id)]));
const pending = [], requests = [];
let closed = false;
const response = data => ({ok: true, json: async () => data});
const report = status => ({status, checked_at: '2026-10-13T12:00:00Z', bridges: [{project: 'project', id: 'bridge', scope: 'server', copy: 'one', source_role: 'external-source', format: 'native-skill', generator: 'fixture', root: 'synthetic/external', source_path: 'skills', status, current_revision: status === 'FAIL' ? '' : 'sha256:current', approved_revision: 'sha256:old', findings: [{status, code: status === 'STALE' ? 'EXTERNAL_SOURCE_DRIFT' : 'EXTERNAL_SOURCE_MISSING', detail: '<img src=x onerror=alert(1)>'}], candidates: status === 'FAIL' ? [] : [{revision: 'sha256:package', skill: {name: 'guide', path: 'skills/guide', package_digest: 'sha256:files', files: [{path: 'SKILL.md', bytes: 40, type: 'text', sha256: 'doc'}], findings: []}}]}]});
const sandbox = {
  document: {getElementById: id => dom[id], createElement: tag => new Element('', tag)},
  location: {hash: '#token=synthetic', pathname: '/bridge.html'}, history: {replaceState(){}}, URLSearchParams,
  EventSource: class { close() {closed = true;} },
  fetch: async (path, options = {}) => {
    requests.push({path, options});
    if (path === '/api/session') return response({csrf: 'csrf'});
    if (path === '/api/state') return response({version: '0.6.0-dev', bridge_file: 'fixture.bridge.yaml'});
    if (path === '/api/bridge') return new Promise(resolve => pending.push(resolve));
    if (path === '/api/close') return response({closed: true});
    throw new Error(path);
  }
};
vm.createContext(sandbox);
for (const name of ['bridge-view.js', 'bridge-console.js']) vm.runInContext(readFileSync(join(__dirname, '../assets', name), 'utf8'), sandbox);
const tick = () => new Promise(resolve => setImmediate(resolve));
const text = element => element.textContent + element.children.map(text).join(' ');
(async () => {
  await tick();
  assert.equal(pending.length, 1);
  assert.equal(dom['check-bridge'].disabled, true);
  assert.match(dom.binding.textContent, /fixture.bridge.yaml/);
  const count = requests.length;
  await dom['check-bridge'].events.click();
  assert.equal(requests.length, count, 'double click started another scan');
  pending.shift()(response({ok: true, items: report('STALE')}));
  await tick();
  assert.equal(dom['bridge-panel'].hidden, false);
  assert.match(dom['bridge-summary'].textContent, /已漂移/);
  assert.match(text(dom['bridge-results']), /EXTERNAL_SOURCE_DRIFT/);
  assert.match(text(dom['bridge-results']), /sha256:old/);
  assert.match(text(dom['bridge-results']), /SKILL.md/);
  assert.match(text(dom['bridge-results']), /<img src=x onerror=alert\(1\)>/);
  assert.equal(dom['check-bridge'].disabled, false);
  const failed = dom['check-bridge'].events.click();
  assert.equal(dom['bridge-panel'].hidden, true, 'previous scan remained visibly current');
  pending.shift()(response({ok: true, items: report('FAIL')}));
  await failed;
  assert.match(dom.message.textContent, /静态失败/);
  assert.doesNotMatch(text(dom['bridge-results']), /sha256:current/);
  assert.match(text(dom['bridge-results']), /完整候选不可用/);
  const invalid = dom['check-bridge'].events.click();
  pending.shift()(response({ok: false, code: 'INVALID_BRIDGE', summary: 'unknown role'}));
  await invalid;
  assert.equal(dom['bridge-panel'].hidden, true);
  assert.match(dom.message.textContent, /INVALID_BRIDGE/);
  assert.equal(dom['check-bridge'].disabled, false);
  assert(requests.filter(x => x.path === '/api/bridge').every(x => x.options.method === 'GET'), 'scan had a write request');
  assert(!requests.some(x => x.path.startsWith('/api/source')), 'bridge page invoked source writes');
  await dom.close.events.click();
  assert.equal(closed, true);
  assert.equal(dom['check-bridge'].disabled, true);
  assert.equal(requests.at(-1).options.headers['X-AIDock-CSRF'], 'csrf');
  assert.match(dom.message.textContent, /控制台已关闭/);
  console.log('PASS bridge state checks: scoped binding, single in-flight scan, drift and missing source, complete-file detail, safe text, stale clearing, retry, GET-only checks and close');
})().catch(error => {console.error(error); process.exitCode = 1;});
