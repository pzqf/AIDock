/* State/interaction regression for the inline prototype. No browser, network or packages. */
'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const html = fs.readFileSync(path.join(__dirname, '..', 'index.html'), 'utf8');
const source = html.match(/<script>([\s\S]*?)<\/script>/)[1];
const page = html.replace(/<script>[\s\S]*?<\/script>/, '');
const decode = s => s.replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
function harness() {
  const elements = new Map(), timers = new Map(), confirmations = [], answers = [];
  let timerId = 0;
  function element(id = '', attrs = {}) {
    const classes = new Set((attrs.class || '').split(/\s+/).filter(Boolean));
    let markup = '', nodes = [];
    const el = { id, value: attrs.value || '', textContent: '', style: {}, dataset: {}, children: [], listeners: {},
      tagName: 'DIV', selectionStart: 0, scrollHeight: 100, scrollTop: 0, disabled: false, type: attrs.type || '',
      classList: { add: (...a) => a.forEach(x => classes.add(x)), remove: (...a) => a.forEach(x => classes.delete(x)),
        contains: x => classes.has(x), toggle: (x, force) => { const yes = force ?? !classes.has(x); yes ? classes.add(x) : classes.delete(x); return yes; } },
      addEventListener: (name, fn) => { el.listeners[name] = fn; },
      setAttribute: (name, val) => { attrs[name] = val; }, getAttribute: name => attrs[name],
      appendChild: child => el.children.push(child), remove() {}, focus() {},
      click: () => (el.listeners.click || el.onclick)?.({ target: el, currentTarget: el, stopPropagation() {} }),
      getBoundingClientRect: () => ({ top: 0, right: 200, bottom: 30 }),
      querySelectorAll: selector => nodes.filter(x => matches(x, selector)),
      querySelector: selector => el.querySelectorAll(selector)[0] || null
    };
    Object.entries(attrs).filter(([k]) => k.startsWith('data-')).forEach(([k,v]) => el.dataset[k.slice(5).replace(/-([a-z])/g, (_,c)=>c.toUpperCase())] = v);
    Object.defineProperty(el, 'innerHTML', { get: () => markup, set: text => { markup = String(text); nodes = parse(markup); } });
    if(id) elements.set(id, el);
    return el;
  }
  function parse(markup) {
    const out = [];
    for(const tag of markup.matchAll(/<([a-z][\w-]*)\b([^>]*)>/gi)) {
      const attrs = {};
      for(const a of tag[2].matchAll(/([\w-]+)="([^"]*)"/g)) attrs[a[1]] = decode(a[2]);
      const el = attrs.id && elements.get(attrs.id) || element(attrs.id, attrs);
      el.tagName = tag[1].toUpperCase(); out.push(el);
    }
    return out;
  }
  function matches(el, selector) {
    if(selector.includes(' ')) return false; // Descendant layout is outside this harness.
    const attr = selector.match(/\[([\w-]+)\]/)?.[1];
    if(attr && el.getAttribute(attr) === undefined) return false;
    const id = selector.match(/^#([\w-]+)/)?.[1]; if(id && el.id !== id) return false;
    return [...selector.matchAll(/\.([\w-]+)/g)].every(m => el.classList.contains(m[1]));
  }
  const initial = parse(page);
  for(const match of html.matchAll(/<select\b([^>]*)>([\s\S]*?)<\/select>/g)) {
    const id = match[1].match(/id="([^"]+)"/)?.[1]; if(!id) continue;
    const opts = [...match[2].matchAll(/<option\b([^>]*)>([^<]*)<\/option>/g)];
    const opt = opts.find(o=>o[1].includes('selected')) || opts[0];
    if(opt) elements.get(id).value = opt[1].match(/value="([^"]+)"/)?.[1] || decode(opt[2]);
  }
  const document = { getElementById: id => elements.get(id) || null, createElement: () => element(),
    querySelectorAll: selector => initial.filter(el=>matches(el,selector)), querySelector: selector => initial.find(el=>matches(el,selector)) || null,
    addEventListener() {}, documentElement: element('html') };
  const context = vm.createContext({ document, console, navigator: { clipboard: { writeText: () => Promise.resolve() } },
    window: { innerHeight: 900, matchMedia: () => ({ matches: false }) }, matchMedia: () => ({ matches: false }),
    localStorage: { getItem: () => null, setItem() {} }, requestAnimationFrame: fn => fn(),
    setTimeout: (fn, ms) => { timers.set(++timerId, { fn, ms }); return timerId; }, clearTimeout: id => timers.delete(id),
    confirm: msg => { confirmations.push(msg); return answers.length ? answers.shift() : true; }, prompt: () => '测试技能'
  });
  new vm.Script(source, {filename:'demo/index.html'}).runInContext(context);
  const run = code => vm.runInContext(code, context);
  return { run, el: id => elements.get(id), answers, confirmations, timers,
    flush: () => { const tasks=[...timers.values()]; timers.clear(); tasks.forEach(t=>t.fn()); },
    json: code => JSON.parse(JSON.stringify(run(code))), html: () => [page,...elements.values()].map(x=>typeof x==='string'?x:x.innerHTML).join('\n') };
}
let passed = 0;
function test(name, fn) {
  try { fn(harness()); passed++; console.log('PASS ' + name); }
  catch(error) { console.error('FAIL ' + name); throw error; }
}
test('Full script initializes all initial views; initial net diff is three', h => {
  assert.equal(h.run('cur().pending'), 3);
  assert.equal(h.run('cur().changes.length'), 3);
  assert.equal(h.run('state.projects.find(p=>p.id==="admin").pending'), 0);
  assert.ok(h.el('skillList').innerHTML.includes('api-conventions'));
});
test('Project skill toggle preserves shared definition and other projects', h => {
  h.run('toggleSkill("code-rev")');
  assert.equal(h.run('effectiveSkill(cur(),"code-rev").on'), false);
  assert.equal(h.run('effectiveSkill(state.projects[1],"code-rev").on'), true);
  assert.equal(h.run('state.skills.find(s=>s.id==="code-rev").on'), true);
  assert.equal(h.run('state.projects[1].pending'), 0);
});
test('Apply, toggle, toggle back calculates net changes and apply is idempotent', h => {
  h.run('openApplyModal();doApply();toggleSkill("commit")'); assert.equal(h.run('cur().pending'), 1);
  h.run('toggleSkill("commit")'); assert.equal(h.run('cur().pending'), 0);
  const before = h.json('cur().applied'); h.run('openApplyModal();doApply()');
  assert.deepEqual(h.json('cur().applied'), before);
});
test('Changing configuration after preview rejects apply', h => {
  const before=h.json('cur().applied'); h.run('openApplyModal();toggleSkill("commit");doApply()');
  assert.deepEqual(h.json('cur().applied'),before); assert.ok(h.run('cur().pending')>0);
});
test('Apply does not clear sync changes; sync does not clear pending apply', h => {
  h.run('openApplyModal();doApply();toggleSkill("commit");openApplyModal();doApply()');
  assert.equal(h.run('cur().git'),'dirty'); assert.equal(h.run('cur().pending'),0);
  h.run('toggleSkill("commit");doSync("push")');
  assert.equal(h.run('cur().git'),'clean'); assert.equal(h.run('cur().pending'),1);
});
test('Project Profile changes capabilities without changing another project', h => {
  h.run('applyProfile("前端项目")');
  assert.ok(h.run('cur().mcpIds.includes("browser")'));
  assert.equal(h.run('state.projects[1].profile'),'默认开发');assert.equal(h.run('state.projects[1].pending'),0);
  h.el('chatProfile').value='数据分析';h.run('setChatOptions()');
  assert.equal(h.run('currentChat().profile'),'数据分析');assert.equal(h.run('cur().profile'),'前端项目');
});
test('Rejecting low trust Profile authorizes nothing and changes no configuration', h => {
  h.answers.push(true,false);const before=h.json('projectSnapshot(cur())');h.run('applyProfile("前端项目")');
  assert.deepEqual(h.json('projectSnapshot(cur())'),before);assert.equal(h.run('!!installedPlugin("browser")'),false);
  h.run('currentChat().provider="本地 Ollama";renderChat()');h.el('chatProvider').value='Anthropic';h.el('chatProfile').value='数据分析';h.answers.push(false);h.run('setChatOptions()');
  assert.equal(h.el('chatProvider').value,'本地 Ollama');assert.equal(h.run('currentChat().provider'),'本地 Ollama');assert.equal(h.run('currentChat().profile'),'默认开发');
});
for(const level of ['public','internal','confidential','restricted']) test('Memory saves selected '+level+' with source/project; no apply change', h => {
  const before=h.run('cur().pending');h.run('openMemoryModal()');assert.equal(h.el('memLevel').value,'internal');
  h.el('memTitle').value='新决策';h.el('memBody').value='正文';h.el('memLevel').value=level;h.el('memSource').value='skopus';h.run('saveMemory()');
  assert.equal(h.run('state.memories[0].level'),level);assert.equal(h.run('state.memories[0].proj'),'shop');assert.equal(h.run('state.memories[0].source'),'skopus');
  assert.equal(h.run('cur().pending'),before);assert.equal(h.run('cur().knowledgeDirty'),1);
});
test('Memory save rejects changed target and removed source', h => {
  const n=h.run('state.memories.length');h.run('openMemoryModal();switchProject("gw");saveMemory()');assert.equal(h.run('state.memories.length'),n);
  h.run('switchProject("shop");openMemoryModal();cur().mcpOverrides.cgh=false;markDirty()');
  h.el('memTitle').value='a';h.el('memBody').value='b';h.el('memSource').value='cgh';h.run('saveMemory()');assert.equal(h.run('state.memories.length'),n);
});
test('Provider policy excludes Internal from remote and all automatic Confidential/Restricted', h => {
  assert.equal(h.run('memoryAllowed({proj:"shop",level:"internal"},"shop","Anthropic")'),false);
  assert.equal(h.run('memoryAllowed({proj:"shop",level:"internal"},"shop","本地 Ollama")'),true);
  for(const lv of ['confidential','restricted'])assert.equal(h.run(`memoryAllowed({proj:"shop",level:"${lv}"},"shop","本地 Ollama")`),false);
});
test('Gateway has no shop sessions or private memory mentions; global Public remains available', h => {
  h.run('switchProject("gw");renderChat()');assert.equal(h.run('currentChat()'),undefined);assert.equal(h.el('chatMessages').innerHTML,'');
  h.run('newChatSession();renderMentionItems("支付")');assert.equal(h.run('state.mention.items.length'),0);
  h.run('renderMentionItems("分页")');assert.equal(h.run('state.mention.items.length'),1);
});
test('Local-only mention candidates disappear when switching to remote', h => {
  h.run('state.memories.push({id:99,title:"内部约定",body:"x",proj:"shop",level:"internal",source:"cgh"})');
  h.el('chatProvider').value='本地 Ollama';h.run('setChatOptions();renderMentionItems("内部")');assert.equal(h.run('state.mention.items.length'),1);
  h.el('chatProvider').value='Anthropic';h.run('setChatOptions();renderMentionItems("内部")');assert.equal(h.run('state.mention.items.length'),0);
});
test('Delete last session clears input and context; new IDs never collide', h => {
  h.run('deleteChat("s1");deleteChat("s2")');assert.equal(h.el('chatMessages').innerHTML,'');assert.equal(h.el('chatInput').disabled,true);
  h.run('newChatSession();newChatSession()');assert.equal(h.run('new Set(state.chatSessions.map(s=>s.id)).size'),2);assert.equal(h.run('currentChat().project'),'shop');
  assert.ok(!h.el('sendWrap').innerHTML.includes('disabled'));
});
test('Drafts are kept by project and session', h => {
  h.el('chatInput').value='shop draft';h.run('onChatInput();switchChat("s2")');assert.equal(h.el('chatInput').value,'');
  h.run('switchProject("gw");newChatSession()');h.el('chatInput').value='gw draft';h.run('onChatInput();switchProject("shop");renderChat()');
  assert.equal(h.el('chatInput').value,'shop draft');
});
test('Double send during wait creates one request; stop invalidates queued callback', h => {
  const before=h.run('currentChat().messages.length');h.el('chatInput').value='first';h.run('sendChat()');
  h.el('chatInput').value='second';h.run('sendChat()');assert.equal(h.run('currentChat().messages.length'),before+1);
  const callbacks=[...h.timers.values()].map(t=>t.fn);h.run('stopTyping()');callbacks.forEach(fn=>fn());assert.equal(h.run('currentChat().messages.length'),before+1);
});
for(const action of ['switchProject("gw")','switchChat("s2")'])test('Expired response cannot write after '+action, h => {
  h.el('chatInput').value='question';h.run('sendChat()');const before=h.run('state.chatSessions.find(s=>s.id==="s1").messages.length');
  const callbacks=[...h.timers.values()].map(t=>t.fn);h.run(action);callbacks.forEach(fn=>fn());
  assert.equal(h.run('state.chatSessions.find(s=>s.id==="s1").messages.length'),before);assert.equal(h.run('state.typing.active'),false);
});
test('Empty mention Enter and IME Enter are safe', h => {
  h.run('state.mention.active=true;state.mention.items=[];chatKey({key:"Enter",preventDefault(){}})');
  const before=h.run('currentChat().messages.length');h.el('chatInput').value='中文';h.run('chatKey({key:"Enter",isComposing:true,preventDefault(){}})');assert.equal(h.run('currentChat().messages.length'),before);
});
test('Trust setting governs install, rejection preserves library, acceptance audits', h => {
  h.run('setTrustMin(4);openInstall("context7")');assert.ok(h.el('insGate').innerHTML.includes('L4'));
  h.answers.push(true,false);h.el('insConfirm').onclick();assert.equal(h.run('!!installedPlugin("context7")'),false);
  h.answers.push(true,true);h.el('insConfirm').onclick();assert.equal(h.run('installedPlugin("context7").version'),'1.5.0');assert.ok(h.run('state.audit.some(a=>a.title.includes("授权装载 Context7"))'));
  assert.equal(h.run('trustOf({}).level'),0);
});
test('Project MCP addition shows permissions and is bound only after authorization', h => {
  h.run('openAdd("mcp")');h.answers.push(false);h.run('confirmAdd("mcp","browser")');assert.equal(h.run('cur().mcpIds.includes("browser")'),false);
  assert.ok(h.confirmations[0].includes('网络：*'));h.answers.push(true,true);h.run('confirmAdd("mcp","browser")');assert.equal(h.run('cur().mcpIds.includes("browser")'),true);
  const n=h.run('cur().mcpIds.length');h.run('confirmAdd("mcp","browser")');assert.equal(h.run('cur().mcpIds.length'),n);
});
test('Quoted/special group names use data and event listeners', h => {
  h.run('state.projects[0].group="O\'Reilly <团队>";renderProjects()');
  const group=h.el('projectNav').querySelectorAll('[data-group]').find(el=>el.dataset.group==="O'Reilly <团队>");
  assert.ok(group);group.click();assert.equal(h.run('state.collapsedGroups["O\'Reilly <团队>"]'),true);
  assert.ok(!h.el('projectNav').innerHTML.includes('onclick='));
});
test('Local skill body save does not mutate global or another project', h => {
  h.run('openSkillDetail("code-rev")');h.el('sdContent').value='本项目正文';h.run('saveSkillDetail()');
  assert.equal(h.run('effectiveSkill(cur(),"code-rev").content'),'本项目正文');assert.notEqual(h.run('effectiveSkill(state.projects[1],"code-rev").content'),'本项目正文');
});
test('Other project skills are absent from global introduction list', h => {
  h.run('switchProject("gw");openAdd("skill")');assert.ok(!h.el('addList').innerHTML.includes('api-conventions'));
});
test('MCP runtime and saved config are project-local; raw secrets are rejected', h => {
  h.run('mcpOp("cgh","stop");openMcp("cgh")');h.el('mcpSecret').value='plain secret';h.run('saveMcpConfig()');assert.equal(h.run('cur().mcpConfig.cgh'),undefined);
  h.el('mcpSecret').value='${SECRET:openai_api_key}';h.el('mcpTier').value='2';h.run('saveMcpConfig()');assert.equal(h.run('cur().mcpConfig.cgh.tier'),2);
  assert.equal(h.run('state.projects[1].mcpRuntime.cgh.status'),'running');assert.equal(h.run('state.projects[1].mcpConfig.cgh'),undefined);
});
test('Global plugin update refreshes every affected project', h => {
  h.run('updateAll()');assert.equal(h.run('installedPlugin("skopus").version'),'1.2.0');assert.ok(h.run('state.projects[1].pending')>0);assert.equal(h.run('state.projects[2].pending'),0);
});
test('File tree shows current source, external file shows applied version and keeps handwritten text', h => {
  h.run('cur().rules="新正文";markDirty();renderFileTree()');assert.equal(h.run('findNode(".aidock/rules/coding-standards.md").content'),'新正文');assert.ok(!h.run('findNode("CLAUDE.md").content').includes('新正文'));
  h.run('openApplyModal();doApply();renderFileTree()');assert.ok(h.run('findNode("CLAUDE.md").content').includes('新正文'));assert.ok(h.run('findNode("CLAUDE.md").content').includes('用户手写，保留'));
  h.run('switchProject("gw");renderFileTree()');assert.ok(!h.run('findNode(".aidock/project.yaml").content').includes('shop'));
});
test('Snapshots contain actual config; rollback restores source and applied targets', h => {
  h.run('openApplyModal();doApply()');const id=h.run('state.timeline[0].id'),before=h.json('cur().applied');
  h.run('cur().rules="later";markDirty();openApplyModal();doApply()');
  h.run(`openReplayConfirm(${JSON.stringify(id)})`);h.el('replayConfirm').onclick();assert.deepEqual(h.json('cur().applied'),before);assert.equal(h.run('cur().pending'),0);
  assert.deepEqual(h.json('cur().targetFiles["Cursor"]'),before);assert.ok(h.run('!!cur().undoRollback'));
});
test('Every static and exercised generated inline handler compiles', h => {
  h.run('openProfiles();openAdd("mcp");openPluginDetail("cgh");openApplyModal();renderCmd();openSkillDetail("code-rev")');
  for(const attr of h.html().matchAll(/\bon\w+="([^"]*)"/g)) new vm.Script('(function(event){'+decode(attr[1])+'})');
});
test('Changing trust threshold after preview invalidates apply', h => {
  const before=h.json('cur().applied');h.run('openApplyModal();setTrustMin(4);doApply()');assert.deepEqual(h.json('cur().applied'),before);
});
test('Stop during streaming keeps partial output and expires the next step', h => {
  h.el('chatInput').value='question';h.run('sendChat()');h.flush();assert.equal(h.run('state.typing.active'),true);
  const before=h.run('currentChat().messages.at(-1).content');assert.ok(before.length>0);
  const callbacks=[...h.timers.values()].map(t=>t.fn);h.run('stopTyping()');callbacks.forEach(fn=>fn());assert.equal(h.run('currentChat().messages.at(-1).content'),before);
  assert.equal(h.run('currentChat().messages.at(-1).tools[0].status'),'cancelled');
});
test('Global definition editing updates only projects without local body overrides', h => {
  h.run('state.projects[1].skillContent.commit="局部覆盖";goGlobal("skills");openSkillDetail("commit")');h.el('sdContent').value='全局新正文';h.run('saveSkillDetail()');
  assert.equal(h.run('effectiveSkill(cur(),"commit").content'),'全局新正文');assert.equal(h.run('effectiveSkill(state.projects[1],"commit").content'),'局部覆盖');assert.ok(h.run('state.projects[2].pending')>0);
});
test('Sync summary and graph follow the selected project', h => {
  h.run('switchProject("gw");goGlobal("sync");drawGraph()');assert.ok(h.el('syncMeta').textContent.includes('Go 网关'));assert.ok(!h.el('graph').innerHTML.includes('支付'));
});
test('Apply automatically snapshots the old applied state before changing it', h => {
  const before=h.json('cur().applied');h.run('openApplyModal();doApply()');
  assert.deepEqual(h.json('state.timeline.find(t=>t.project==="shop"&&t.tags.includes("before-apply")).config'),before);
});
test('Injected apply failure restores every target, keeps old applied state and retains draft', h => {
  const before=h.json('cur().applied'),targets=h.json('cur().targetFiles');
  h.run('openApplyModal()');h.el('applyFailDemo').checked=true;h.run('doApply()');
  assert.deepEqual(h.json('cur().applied'),before);assert.deepEqual(h.json('cur().targetFiles'),targets);assert.equal(h.run('cur().pending'),3);
  assert.ok(h.run('state.audit.some(a=>a.title.includes("自动恢复模拟目标"))'));
  h.run('openApplyModal();doApply()');assert.equal(h.run('cur().pending'),0);
});
test('Undo rollback restores applied configuration and the saved pending draft', h => {
  h.run('openApplyModal();doApply()');const first=h.run('state.timeline[0].id');
  h.run('cur().rules="second";markDirty();openApplyModal();doApply();cur().rules="unsent draft";markDirty()');
  const applied=h.json('cur().applied'),draft=h.json('projectSnapshot(cur())');
  h.run(`openReplayConfirm(${JSON.stringify(first)});doReplay(${JSON.stringify(first)})`);
  const undo=h.run('cur().undoRollback');h.run(`openReplayConfirm(${JSON.stringify(undo)},true);doReplay(${JSON.stringify(undo)})`);
  assert.deepEqual(h.json('cur().applied'),applied);assert.deepEqual(h.json('projectSnapshot(cur())'),draft);assert.equal(h.run('cur().pending'),1);
});
test('Rollback can pin an archived plugin version without downgrading other projects', h => {
  h.run('openApplyModal();doApply()');const old=h.run('state.timeline[0].id');
  h.run('updateAll();openApplyModal();doApply()');assert.equal(h.run('pluginVersion(cur(),"skopus")'),'1.2.0');
  h.run(`openReplayConfirm(${JSON.stringify(old)});doReplay(${JSON.stringify(old)})`);
  assert.equal(h.run('pluginVersion(cur(),"skopus")'),'1.1.0');assert.equal(h.run('installedPlugin("skopus").version'),'1.2.0');assert.equal(h.run('pluginVersion(state.projects[1],"skopus")'),'1.2.0');assert.equal(h.run('cur().pending'),0);
});
test('Stale, cross-project and missing-dependency rollback leave source and targets intact', h => {
  h.run('openApplyModal();doApply()');const id=h.run('state.timeline[0].id');
  h.run(`openReplayConfirm(${JSON.stringify(id)});cur().rules="changed";markDirty()`);const before=h.json('projectSnapshot(cur())'),targets=h.json('cur().targetFiles');
  h.run(`doReplay(${JSON.stringify(id)})`);assert.deepEqual(h.json('projectSnapshot(cur())'),before);assert.deepEqual(h.json('cur().targetFiles'),targets);
  h.run('switchProject("gw")');const gw=h.json('projectSnapshot(cur())');h.run(`openReplayConfirm(${JSON.stringify(id)});doReplay(${JSON.stringify(id)})`);assert.deepEqual(h.json('projectSnapshot(cur())'),gw);
  h.run('switchProject("shop");delete state.pluginVersions.skopus["1.1.0"]');h.run(`openReplayConfirm(${JSON.stringify(id)});doReplay(${JSON.stringify(id)})`);assert.deepEqual(h.json('projectSnapshot(cur())'),before);
});
test('Rollback rechecks current trust gate and rejection changes no configuration', h => {
  h.run('openApplyModal();doApply()');const id=h.run('state.timeline[0].id');h.run('cur().rules="bad revision";markDirty();openApplyModal();doApply();setTrustMin(4)');
  const before=h.json('projectSnapshot(cur())'),targets=h.json('cur().targetFiles');h.answers.push(false);
  h.run(`openReplayConfirm(${JSON.stringify(id)});doReplay(${JSON.stringify(id)})`);assert.deepEqual(h.json('projectSnapshot(cur())'),before);assert.deepEqual(h.json('cur().targetFiles'),targets);
});
test('Injected rollback failure restores targets and does not discard pending draft', h => {
  h.run('openApplyModal();doApply()');const old=h.run('state.timeline[0].id');
  h.run('cur().rules="second";markDirty();openApplyModal();doApply();cur().rules="pending";markDirty()');
  const before=h.json('projectSnapshot(cur())'),applied=h.json('cur().applied'),targets=h.json('cur().targetFiles');
  h.run(`openReplayConfirm(${JSON.stringify(old)})`);h.el('replayFailDemo').checked=true;h.el('replayConfirm').onclick();
  assert.deepEqual(h.json('projectSnapshot(cur())'),before);assert.deepEqual(h.json('cur().applied'),applied);assert.deepEqual(h.json('cur().targetFiles'),targets);assert.equal(h.run('cur().undoRollback'),null);
});
test('A rolled-back plugin can later select a newer cached version for this project', h => {
  h.run('openApplyModal();doApply()');const old=h.run('state.timeline[0].id');
  h.run(`updateAll();openApplyModal();doApply();openReplayConfirm(${JSON.stringify(old)});doReplay(${JSON.stringify(old)});openMcp("skopus")`);
  h.el('mcpVersion').value='1.2.0';h.run('saveMcpConfig()');assert.equal(h.run('pluginVersion(cur(),"skopus")'),'1.2.0');assert.ok(h.run('cur().pending')>0);
});
test('Replica preview detects both writers and cannot apply unresolved conflicts', h => {
  const before=h.json('replicaConfig(replicaGroup())');h.run('openReplicaPlan()');
  assert.equal(h.run('replicaDemo.plan.rows.find(r=>r.id==="work3").status'),'conflict');
  assert.equal(h.el('replicaConfirm').disabled,true);assert.equal(h.run('doReplicaSync()'),false);
  assert.deepEqual(h.json('replicaConfig(replicaGroup())'),before);
});
test('Replica selection and explicit skip preserve local configuration, other scopes and project state', h => {
  const locals=h.json('replicaGroup().copies.map(c=>c.local)'),client=h.json('replicaDemo.groups.client'),projects=h.json('state.projects');
  const work3=h.json('replicaCopy("work3").bundle'),work5=h.json('replicaCopy("work5").bundle');
  h.run('setReplicaTarget("work5",false);openReplicaPlan();setReplicaResolution("work3","skip")');
  assert.equal(h.el('replicaConfirm').disabled,false);assert.equal(h.run('doReplicaSync()'),true);
  assert.deepEqual(h.json('replicaCopy("work2").bundle'),h.json('replicaCopy("work").bundle'));
  assert.deepEqual(h.json('replicaCopy("work2").generated'),h.json('replicaCopy("work2").bundle'));
  assert.deepEqual(h.json('replicaCopy("work3").bundle'),work3);assert.deepEqual(h.json('replicaCopy("work5").bundle'),work5);
  assert.deepEqual(h.json('replicaGroup().copies.map(c=>c.local)'),locals);assert.deepEqual(h.json('replicaDemo.groups.client'),client);assert.deepEqual(h.json('state.projects'),projects);
});
test('Any replica can publish its skill body, toggle and complete bundle', h => {
  h.run('setReplicaSource("work5");editReplicaSource();replicaCopy("work5").bundle.skill.enabled=false;replicaCopy("work5").bundle.skill.attachment="更新附件";replicaCopy("work5").generated=clone(replicaCopy("work5").bundle);replicaDemo.targets=["work2"];openReplicaPlan()');
  assert.equal(h.run('replicaDemo.plan.rows[0].status'),'publish');assert.equal(h.run('doReplicaSync()'),true);
  assert.deepEqual(h.json('replicaCopy("work2").bundle'),h.json('replicaCopy("work5").bundle'));
});
test('Target-only changes and unknown baseline require explicit decisions', h => {
  h.run('setReplicaSource("work2");replicaDemo.targets=["work3"];openReplicaPlan()');
  assert.equal(h.run('replicaDemo.plan.rows[0].status'),'target-only');assert.equal(h.run('doReplicaSync()'),false);
  h.run('delete replicaGroup().bases[replicaPair("work2","work3")];openReplicaPlan()');
  assert.equal(h.run('replicaDemo.plan.rows[0].status'),'unknown');assert.equal(h.run('doReplicaSync()'),false);
  h.run('setReplicaResolution("work3","source")');assert.equal(h.run('doReplicaSync()'),true);
});
for(const change of ['editReplicaSource()','replicaCopy("work2").bundle.skill.body="外部目标修改"','replicaGroup().bases["work:work2"].skill.body="基线变化"','setTrustMin(4)'])test('Replica confirmation rejects stale plan after '+change, h => {
  h.run('openReplicaPlan();setReplicaResolution("work3","source");'+change);const before=h.json('replicaConfig(replicaGroup())');
  assert.equal(h.run('doReplicaSync()'),false);assert.deepEqual(h.json('replicaConfig(replicaGroup())'),before);
});
test('Switching scope or clearing targets cannot reuse a replica plan', h => {
  h.run('openReplicaPlan();setReplicaScope("client")');assert.equal(h.run('doReplicaSync()'),false);
  h.run('replicaDemo.targets=[]');assert.equal(h.run('openReplicaPlan()'),false);
});
test('Replica failure restores all shared sources, generated targets and baselines, preserving publisher edits', h => {
  h.run('editReplicaSource();openReplicaPlan();setReplicaResolution("work3","source")');const before=h.json('replicaConfig(replicaGroup())');
  h.el('replicaFailDemo').checked=true;assert.equal(h.run('doReplicaSync()'),false);assert.deepEqual(h.json('replicaConfig(replicaGroup())'),before);
  assert.equal(h.run('replicaDemo.undo.server'),undefined);assert.ok(h.run('state.audit.some(a=>a.title.includes("自动恢复模拟副本批次"))'));
});
test('Replica batch undo restores bundle and baseline while retaining current sessions and local settings', h => {
  const before=h.json('replicaConfig(replicaGroup())'),locals=h.json('replicaGroup().copies.map(c=>c.local)');
  h.run('openReplicaPlan();setReplicaResolution("work3","source");doReplicaSync();simulateReplicaProbe()');
  const loaded=h.json('replicaCopy("work2").loaded');assert.equal(h.run('openReplicaUndo()'),true);assert.equal(h.run('doReplicaSync()'),true);
  assert.deepEqual(h.json('replicaConfig(replicaGroup())'),before);assert.deepEqual(h.json('replicaCopy("work2").loaded'),loaded);
  assert.deepEqual(h.json('replicaGroup().copies.map(c=>c.local)'),locals);assert.ok(h.run('replicaEvidence(replicaCopy("work2"))[2]').startsWith('STALE'));
});
test('Changed target blocks undo and injected undo failure keeps successful sync intact', h => {
  h.run('openReplicaPlan();setReplicaResolution("work3","source");doReplicaSync();openReplicaUndo()');const applied=h.json('replicaConfig(replicaGroup())');
  h.el('replicaFailDemo').checked=true;assert.equal(h.run('doReplicaSync()'),false);assert.deepEqual(h.json('replicaConfig(replicaGroup())'),applied);
  h.run('replicaCopy("work2").bundle.skill.body="同步后的新改动"');const changed=h.json('replicaConfig(replicaGroup())');
  assert.equal(h.run('openReplicaUndo()'),false);assert.deepEqual(h.json('replicaConfig(replicaGroup())'),changed);
});
test('Replica generation success leaves session and actual capability evidence pending', h => {
  h.run('openReplicaPlan();setReplicaResolution("work3","source");doReplicaSync()');
  let evidence=h.json('replicaEvidence(replicaCopy("work2"))');assert.ok(evidence[0].startsWith('PASS'));assert.ok(evidence[2].startsWith('STALE'));assert.ok(evidence[3].startsWith('UNKNOWN'));
  h.run('simulateReplicaProbe()');evidence=h.json('replicaEvidence(replicaCopy("work2"))');assert.ok(evidence[3].includes('模拟只读探针'));
  assert.ok(h.run('replicaEvidence(replicaCopy("work4"))[1]').startsWith('UNKNOWN'));assert.ok(h.run('replicaEvidence(replicaCopy("work4"))[3]').startsWith('UNKNOWN'));
  h.run('setReplicaSource("work2");editReplicaSource()');assert.ok(h.run('replicaEvidence(replicaCopy("work2"))[3]').startsWith('UNKNOWN'));
});
test('Matching shared sources still repair generated drift; repeat sync is a no-op', h => {
  h.run('replicaDemo.targets=["work2"];openReplicaPlan();doReplicaSync();openReplicaPlan()');
  assert.equal(h.run('replicaDemo.plan.rows[0].status'),'same');assert.equal(h.run('doReplicaSync()'),false);
  h.run('replicaCopy("work2").generated.skill.body="镜像手改";openReplicaPlan()');assert.equal(h.run('replicaDemo.plan.rows[0].status'),'regenerate');
  assert.equal(h.run('doReplicaSync()'),true);assert.deepEqual(h.json('replicaCopy("work2").generated'),h.json('replicaCopy("work2").bundle'));
});
test('Replica generated controls compile and source never appears as a target', h => {
  h.run('openReplicaPlan();setReplicaResolution("work3","skip")');
  assert.equal(h.run('replicaDemo.plan.rows.some(r=>r.id===replicaDemo.source)'),false);
  for(const attr of h.html().matchAll(/\bon\w+="([^"]*)"/g))new vm.Script('(function(event){'+decode(attr[1])+'})');
});
console.log(`\n${passed} regression cases passed. DOM/timers are substitutes; browser and external integration are not tested.`);
