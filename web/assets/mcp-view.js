'use strict';
const MCPView = (() => {
  const $ = id => document.getElementById(id), copy = value => JSON.parse(JSON.stringify(value));
  let ctx, loaded = null, draft = null, plan = null, discardArmed = false;
  const dirty = () => Boolean(loaded && draft && JSON.stringify(draft) !== JSON.stringify(loaded.config));
  const element = (tag, text, cls) => { const n = document.createElement(tag); if (text !== undefined) n.textContent = text; if (cls) n.className = cls; return n; };
  function say(text, error = false) { $('mcp-message').textContent = text; $('mcp-message').classList.toggle('error', error); }
  function invalidate() { plan = null; $('mcp-plan').hidden = true; }
  function changed() { invalidate(); discardArmed = false; $('mcp-reload').textContent = '重读已保存定义'; sync(); }
  function sync() {
    if (!ctx) return;
    for (const n of $('mcp-dialog').querySelectorAll('button,input,textarea,select')) n.disabled = ctx.busy();
    $('nav-mcp').disabled = ctx.busy();
    $('mcp-add').disabled = ctx.busy() || !draft;
    $('mcp-preview').disabled = ctx.busy() || ctx.pending() || !dirty();
    $('mcp-confirm').disabled = ctx.busy() || ctx.pending() || !plan;
    $('mcp-draft').textContent = dirty() ? '有未保存的 MCP 定义修改' : '与已保存定义一致';
  }
  function findings(target, state) {
    target.replaceChildren();
    for (const c of state.checks || []) target.append(element('p', `${c.code}：${c.detail}`, 'caption'));
    for (const s of state.servers || []) {
      const card = element('section', undefined, 'native-check-card');
      card.append(element('h4', `${s.name} · ${s.enabled ? '声明启用' : '声明停用'}`));
      const layer=({'mcp-definition':'定义默认',defaults:'全局默认',project:'项目共享',local:'本机覆盖'})[s.enabled_source]||s.enabled_source?.replace(/^extends:/,'继承 · ');
      card.append(element('p',s.selection_resolved?`Profile 合并后${s.effective_enabled?'启用':'停用'} · 来源：${layer}；授权与运行另行检查。`:'Profile 启停尚未解析，请先修复配置引用；不会推断为已启用。','notice'));
      for (const c of s.checks || []) card.append(element('p', `${({FAIL:'需处理',UNKNOWN:'待验证',WARN:'需注意',PASS:'已通过'})[c.status] || c.status} · ${c.code}：${c.detail}`, 'caption'));
      target.append(card);
    }
    if (!(state.servers || []).length) target.append(element('p', '此项目尚未登记 MCP。添加定义后可预览保存；后续安装和受管运行另行完成。', 'caption'));
  }
  function field(parent, label, value, update, options, kind = 'input') {
    const wrap = element('label', label), input = element(options ? 'select' : kind);
    input.setAttribute('aria-label', label);
    if (options) for (const [key, text] of options) { const option = element('option', text); option.value = key; input.append(option); }
    input.value = value;
    if (!options && kind === 'input') { input.required = true; input.maxLength = label.startsWith('名称 ') ? 64 : 2048; }
    if (label.startsWith('名称 ')) input.pattern = '[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}';
    if (kind === 'textarea') input.rows = 3;
    input.addEventListener(options ? 'change' : 'input', () => { if (ctx.busy()) return; update(input.value); changed(); });
    wrap.append(input); parent.append(wrap); return input;
  }
  function toggle(parent, label, value, update) {
    const wrap = element('label', undefined, 'mcp-toggle'), input = element('input'); input.type = 'checkbox'; input.checked = value; input.setAttribute('aria-label', label);
    input.addEventListener('change', () => { if (ctx.busy()) return; update(input.checked); changed(); });
    wrap.append(input, element('span', label)); parent.append(wrap);
  }
  function render() {
    $('mcp-servers').replaceChildren();
    draft.servers.forEach((s, i) => {
      const card = element('details', undefined, 'mcp-server'); card.open = true;
      card.append(element('summary', `MCP ${i + 1} · ${s.name || '新定义'}`));
      const grid = element('div', undefined, 'mcp-fields'), n = i + 1;
      field(grid, `名称 ${n}`, s.name, v => { s.name = v; card.children[0].textContent = `MCP ${n} · ${v || '新定义'}`; });
      field(grid, `传输方式 ${n}`, s.transport, v => s.transport = v, [['stdio','stdio'],['sse','SSE · 尚不支持'],['http','HTTP · 尚不支持']]);
      field(grid, `来源类型 ${n}`, s.source.type, v => { s.source = {type:v}; render(); }, [['npm','npm'],['github','GitHub'],['registry','Registry'],['local','项目本地目录']]);
      if (s.source.type === 'local') field(grid, `项目相对路径 ${n}`, s.source.path || '', v => s.source.path = v);
      else {
        const key = s.source.type === 'github' ? 'url' : 'package';
        field(grid, `${key === 'url' ? '来源 URL' : '包名'} ${n}`, s.source[key] || '', v => s.source[key] = v);
        field(grid, `精确版本或提交 ${n}`, s.source.version || '', v => s.source.version = v);
      }
      field(grid, `安装目录相对入口 ${n}`, s.entrypoint, v => s.entrypoint = v);
      field(grid, `隔离等级声明 ${n}`, String(s.tier), v => s.tier = Number(v), [['0','Tier 0 · 非隔离'],['1','Tier 1'],['2','Tier 2']]);
      field(grid, `需要的密钥名称 ${n}（每行一个）`, (s.secrets || []).join('\n'), v => { const names=lines(v); if(names.length)s.secrets=names;else delete s.secrets; }, null, 'textarea');
      const permissions = s.permissions;
      field(grid, `允许的文件路径 ${n}`, permissions.fs.paths.join('\n'), v => permissions.fs.paths = lines(v), null, 'textarea');
      field(grid, `文件访问 ${n}`, permissions.fs.mode, v => permissions.fs.mode = v, [['ro','只读'],['rw','读写']]);
      field(grid, `网络域名白名单 ${n}`, permissions.net.allow.join('\n'), v => permissions.net.allow = lines(v), null, 'textarea');
      field(grid, `默认网络策略 ${n}`, permissions.net.default, v => permissions.net.default = v, [['deny','默认拒绝'],['allow','默认允许 · 需注意']]);
      card.append(grid);
      toggle(card, `声明启用 ${n}`, s.enabled, v => s.enabled = v);
      toggle(card, `允许子进程声明 ${n}`, permissions.exec, v => permissions.exec = v);
      const remove = element('button', `移除定义 ${n}`, 'btn'); remove.type = 'button';
      remove.addEventListener('click', () => { if (ctx.busy()) return; draft.servers.splice(i, 1); changed(); render(); }); card.append(remove);
      if (s.source.install) card.append(element('p', '来源中已有安装命令声明，保存时原样保留；本页不执行该命令。', 'caption'));
      $('mcp-servers').append(card);
    });
    findings($('mcp-checks'), loaded); sync();
  }
  const lines = value => value.split(/\r?\n/).map(v => v.trim()).filter(Boolean);
  function describe(s) {
    if (!s) return '未登记';
    const p = s.permissions, source = s.source;
    return [s.enabled ? '声明启用' : '声明停用',s.transport,`${source.type} · ${source.package || source.url || source.path || '来源未填写'}${source.version ? ' @ ' + source.version : ''}`,`入口：${s.entrypoint}`,`Tier ${s.tier}`,`文件：${p.fs.mode === 'rw' ? '读写' : '只读'} · ${p.fs.paths.join('、') || '无'}`,`网络：默认${p.net.default === 'deny' ? '拒绝' : '允许'} · 白名单 ${p.net.allow.join('、') || '无'}`,`子进程：${p.exec ? '声明允许' : '声明拒绝'}`,`安装命令声明：${source.install ? '已保留，不执行' : '无'}`].join('\n');
  }
  function changes(candidate) {
    $('mcp-changes').replaceChildren();
    for (const change of plan.changes) $('mcp-changes').append(element('p', `${change.before ? (change.after ? '修改' : '移除') : '新增'} · ${change.path}`, 'caption'));
    const previous = new Map(loaded.config.servers.map(s => [s.name,s])), next = new Map(candidate.config.servers.map(s => [s.name,s]));
    for (const name of new Set([...previous.keys(),...next.keys()])) {
      const before = describe(previous.get(name)), after = describe(next.get(name));
      if (before === after) continue;
      const section = element('section', undefined, 'native-rule-preview');
      section.append(element('h4', `${previous.has(name) ? (next.has(name) ? '修改' : '移除') : '新增'} · ${name}`));
      section.append(element('p','已保存','caption'),element('pre',before),element('p','确认后','caption'),element('pre',after));
      $('mcp-changes').append(section);
    }
  }
  async function run(fn) {
    if (ctx.busy()) return;
    ctx.setBusy(true);
    try { await fn(); } catch (e) { invalidate(); say(e.message, true); } finally { ctx.setBusy(false); }
  }
  async function load(replace = false) {
    const next = (await ctx.api('/api/mcp/state')).items;
    if (!replace && dirty()) {
      if (next.sha256 !== loaded.sha256 || next.selection_digest !== loaded.selection_digest) { invalidate(); say('已保存定义或 Profile 启停来源发生变化；当前草稿保留，请核对后重读。', true); }
      return;
    }
    loaded = next; draft = copy(next.config); discardArmed = false; invalidate(); render();
    $('mcp-reload').textContent = '重读已保存定义'; say('已读取 MCP 定义。预览后确认保存；安装与受管运行分别检查。');
  }
  function close() { if (ctx.busy()) return; $('mcp-dialog').close?.(); $('mcp-dialog').hidden = true; ctx.message(dirty() ? 'MCP 定义草稿保留在当前标签页。' : 'MCP 管理已关闭。'); }
  async function checkRelay() {
    if (ctx.busy() || $('mcp-relay-prompt').hidden) return;
    $('mcp-relay-prompt').hidden = true;
    ctx.setBusy(true);
    $('mcp-relay-result').hidden = true;
    say('正在检查内置中继；当前定义草稿保留…');
    try {
      const report = (await ctx.api('/api/mcp/relay/check', {probe:true})).items;
      $('mcp-relay-summary').textContent = report.status === 'PASS' ? '固定夹具中继自检通过' : '中继自检未通过';
      $('mcp-relay-boundary').textContent = report.boundary;
      const target = $('mcp-relay-findings'); target.replaceChildren();
      const passed = (report.findings || []).filter(finding => finding.status === 'PASS'), failed = (report.findings || []).filter(finding => finding.status !== 'PASS');
      target.append(element('p', `${passed.length} 项通过 · ${failed.length} 项需处理`));
      for (const finding of failed) target.append(element('p', finding.detail, 'error'));
      if (passed.length) {
        const details = element('details'); details.append(element('summary','查看通过的检查'));
        for (const finding of passed) details.append(element('p', finding.detail, 'caption'));
        target.append(details);
      }
      $('mcp-relay-result').hidden = false;
      say(report.status === 'PASS' ? '内置中继检查通过。实际项目插件的授权、隔离与启动仍需单独验证。' : '中继检查存在问题，定义草稿和保存预览保留。', report.status !== 'PASS');
    } catch (e) { say(`中继检查未完成：${e.message}。当前定义草稿和保存预览保留。`, true); }
    finally { ctx.setBusy(false); }
  }
  async function refreshChecks() {
    const next = (await ctx.api('/api/mcp/state')).items;
    findings($('mcp-checks'), next); invalidate();
    if (loaded && next.sha256 !== loaded.sha256) say('已保存定义发生变化；当前定义草稿保留，请核对后重读。', true);
    else if (loaded) loaded = {...loaded, servers:next.servers, checks:next.checks, status:next.status};
    sync();
  }
  function init(context) {
    ctx = context;
    $('mcp-relay-start').addEventListener('click', () => { if (!ctx.busy()) $('mcp-relay-prompt').hidden = false; });
    $('mcp-relay-cancel').addEventListener('click', () => { if (!ctx.busy()) $('mcp-relay-prompt').hidden = true; });
    $('mcp-relay-confirm').addEventListener('click', checkRelay);
    $('nav-mcp').hidden = false;
    $('nav-mcp').addEventListener('click', () => { if (ctx.busy()) return; $('mcp-dialog').hidden = false; if (!$('mcp-dialog').open) $('mcp-dialog').showModal?.(); $('mcp-dialog').scrollTop = 0; run(() => load()); });
    $('mcp-close').addEventListener('click', close);
    $('mcp-dialog').addEventListener('cancel', e => { e.preventDefault(); close(); });
    $('mcp-add').addEventListener('click', () => {
      if (ctx.busy() || !draft) return;
      let n = 1; while (draft.servers.some(s => s.name === `mcp-${n}`)) n++;
      draft.servers.push({name:`mcp-${n}`,source:{type:'npm',package:'',version:''},transport:'stdio',entrypoint:'dist/index.js',tier:1,enabled:false,permissions:{fs:{paths:['${PROJECT}'],mode:'ro'},net:{allow:[],default:'deny'},exec:false}}); changed(); render();
    });
    $('mcp-reload').addEventListener('click', () => {
      if (ctx.busy()) return;
      if (dirty() && !discardArmed) { discardArmed = true; $('mcp-reload').textContent = '确认放弃 MCP 草稿并重读'; say('再次点击将放弃本页 MCP 草稿。', true); return; }
      run(() => load(true));
    });
    $('mcp-form').addEventListener('submit', e => {
      e.preventDefault(); if (ctx.busy() || ctx.pending() || !dirty()) return;
      run(async () => {
        invalidate(); const out = (await ctx.api('/api/mcp/plan', {expected_sha256:loaded.sha256,expected_selection_digest:loaded.selection_digest,config:copy(draft)})).items; plan = out.plan;
        changes(out.candidate);
        findings($('mcp-candidate-checks'), out.candidate); $('mcp-plan').hidden = false; say('核对候选定义与检查结果，再确认保存。定义可先登记，未通过的运行门槛会保留。');
      });
    });
    $('mcp-cancel').addEventListener('click', () => { if (ctx.busy()) return; invalidate(); say('已取消保存预览，MCP 草稿保留。'); sync(); });
    $('mcp-confirm').addEventListener('click', () => {
      if (ctx.busy() || ctx.pending() || !plan) return;
      run(async () => { await ctx.api('/api/mcp/apply', {plan_id:plan.id,digest:plan.digest}); invalidate(); loaded = null; draft = null; await ctx.refresh(); await load(true); say('MCP 定义已保存，完整源回退点已保留。可在时间线回退并撤销；安装与受管运行继续验收。'); });
    });
    $('mcp-history').addEventListener('click', () => { if (ctx.busy()) return; close(); $('nav-history').click(); });
    sync();
  }
  return {init,sync,hasDraft:dirty,refreshChecks};
})();
