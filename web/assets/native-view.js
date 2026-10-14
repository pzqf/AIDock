'use strict';
// Shared source-page session. Probes run only after explicit confirmation;
// File installation needs a fresh explicit discovery report and confirmation.
const NativePreview = (() => {
  const el = id => document.getElementById(id);
  let context = null, plan = null, lastCheck = null, generation = 0, checkGeneration = 0, probeSelection = null;
  const node = (tag, text) => { const n = document.createElement(tag); if (text !== undefined) n.textContent = text; return n; };
  function sync() {
    el('native-recheck').disabled = !context || context.busy() || context.pending() || !plan;
    el('native-apply').disabled = !context || context.busy() || context.pending() || !plan?.write_supported || plan.blocked;
    for (const id of ['native-check', 'native-probe', 'native-last-check', 'native-probe-confirm-run']) el(id).disabled = !context || context.busy() || context.pending();
    el('native-probe-cancel').disabled = Boolean(context?.busy());
    if (typeof NativeRehearsal !== 'undefined') NativeRehearsal.sync();
    if (typeof NativeWrite !== 'undefined') NativeWrite.sync();
  }
  function invalidate(clearCheck = true) {
    generation++;
    plan = null;
    el('native-result').hidden = true;
    if (clearCheck) { lastCheck = null; checkGeneration++; el('native-check-result').hidden = true; closeProbe(); }
    if (typeof NativeRehearsal !== 'undefined') NativeRehearsal.invalidate();
    if (typeof NativeWrite !== 'undefined') NativeWrite.invalidate();
    sync();
  }
  function closeProbe() {
    el('native-probe-confirm').close?.(); el('native-probe-confirm').hidden = true; probeSelection = null;
  }
  function selection() { return ['claude-code', 'codex-cli'].filter(id => el(`native-${id}`).checked); }
  function renderCheck(report) {
    lastCheck = report;
    el('native-check-result').hidden = !report;
    if (!report) return;
    el('native-check-summary').textContent = report.probe ? '工具发现检查' : '项目文件检查';
    el('native-check-freshness').textContent = report.stale ? '已过期 · 请重新检查' : '检查记录';
    el('native-check-freshness').className = report.stale ? 'badge check-stale' : 'badge';
    el('native-check-time').textContent = `${new Date(report.created).toLocaleString('zh-CN')} · ${report.stale ? report.stale_reason : '实际项目会话生效尚未验证'}`;
    el('native-check-boundary').textContent = report.boundary;
    el('native-check-id').textContent = `检查 ${report.id} · ${report.digest}`;
    el('native-check-tools').replaceChildren();
    const label = {PASS:'已验证', FAIL:'检查失败', UNKNOWN:'未验证', SKIPPED:'无需检查', NOT_APPLIED:'待应用'};
    for (const tool of report.tools) {
      const card = node('article'); card.className = 'native-check-card';
      const head = node('div'); head.className = 'list-heading';
      head.append(node('h3', {'claude-code':'Claude Code', 'codex-cli':'Codex CLI'}[tool.tool] || tool.tool));
      const installed = node('span', tool.installed ? tool.version ? `v${tool.version}` : '已安装' : '未找到程序'); installed.className = 'badge'; head.append(installed); card.append(head);
      const layers = node('dl'); layers.className = 'native-evidence-layers';
      for (const [level, title] of [['L1','项目文件一致'], ['L2','隔离项目发现'], ['L3','实际会话加载'], ['L4','能力验证']]) {
        const state = node('dd', report.stale ? `${label[tool[level]] || tool[level]}（历史）` : label[tool[level]] || tool[level]);
        state.className = report.stale ? 'check-unknown' : tool[level] === 'PASS' ? 'check-pass' : tool[level] === 'FAIL' ? 'check-fail' : 'check-unknown';
        layers.append(node('dt', title), state);
      }
      card.append(layers);
      for (const skill of tool.skills || []) {
        const line = node('p', `${skill.native_name} · ${label[skill.status] || skill.status}${report.stale ? '（历史）' : ''}`);
        line.className = skill.status === 'FAIL' ? 'check-fail' : ''; card.append(line);
        const detail = node('small', skill.detail); card.append(detail);
      }
      const details = node('details'); details.append(node('summary', '目录与诊断详情'), node('code', tool.target));
      if (tool.executable) details.append(node('code', tool.executable));
      for (const finding of tool.findings || []) {
        const line = node('p', `${finding.code}：${finding.detail}`);
        if (finding.status === 'FAIL') { line.className = 'check-fail'; card.append(line); } else details.append(line);
      }
      card.append(details); el('native-check-tools').append(card);
    }
  }
  async function restore() {
    if (!context || context.busy() || context.pending()) return;
    const requestGeneration = ++checkGeneration;
    try {
      const data = await context.api('/api/native/check/latest');
      if (requestGeneration === checkGeneration) {
        renderCheck(data.items);
        if (!data.items) context.message('尚无检查记录，请先检查文件状态或验证工具发现。');
      }
    } catch (error) { if (requestGeneration === checkGeneration) context.message(error.message, true); }
  }
  async function check(probe, chosen = selection()) {
    if (!context || context.busy() || context.pending()) return;
    if (!chosen.length) { context.message('请选择至少一个工具。', true); return; }
    closeProbe();
    const requestGeneration = ++checkGeneration;
    context.setBusy(true);
    el('native-probe').textContent = probe ? '正在验证…' : '验证工具发现';
    try {
      const data = await context.api('/api/native/check', {tools:chosen, probe});
      if (requestGeneration !== checkGeneration) return;
      renderCheck(data.items);
      context.message(probe ? '隔离发现检查已完成；请查看各项结果。实际项目文件和会话状态分别显示。' : '项目文件状态已检查，工具进程未启动。');
    } catch (error) {
      if (requestGeneration === checkGeneration) { el('native-check-result').hidden = true; context.message(error.message, true); }
    } finally { context.setBusy(false); el('native-probe').textContent = '验证工具发现'; sync(); }
  }
  function render(p) {
    el('native-result').hidden = false;
    el('native-summary').textContent = `${p.blocked ? '存在阻断项' : '预览已生成'} · ${p.targets.length} 个工具 · ${p.write_supported ? '可安装技能文件' : '需要当前工具发现检查'}`;
    el('native-plan-id').textContent = `计划 ${p.id} · ${p.digest}`;
    el('native-evidence').textContent = `L1 ${p.evidence.L1} · L2 ${p.evidence.L2} · L3 ${p.evidence.L3} · L4 ${p.evidence.L4}。生成预览不证明实际生效。`;
    el('native-boundary').textContent = p.boundary;
    el('native-targets').replaceChildren();
    for (const t of p.targets) {
      const card = node('article'); card.className = 'native-target';
      const toolName = {'claude-code':'Claude Code', 'codex-cli':'Codex CLI'}[t.adapter.id] || t.adapter.id;
      card.append(node('h3', toolName));
      const health = node('p', t.status === 'FAIL' ? '存在目标冲突或静态错误，预览已阻断' : '完整包预览已生成，实际工具生效待验证');
      health.className = t.status === 'FAIL' ? 'native-health error' : 'native-health'; card.append(health);
      card.append(node('p', `${t.packages.length} 个已启用完整包 · ${(t.changes || []).length} 项拟议路径变化`));
      const technical = node('details'); technical.append(node('summary', '目标目录与适配器证据'), node('code', t.root), node('p', `适配器 ${t.adapter.version} · 官方契约核对 ${t.adapter.contract_checked} · 文件安装版本 ${(t.adapter.verified_versions || []).join('、') || '尚未验收'}`));
      const reference = node('a', '官方技能文档'); reference.href = t.adapter.reference; reference.target = '_blank'; reference.rel = 'noopener noreferrer'; technical.append(reference); card.append(technical);
      for (const pkg of t.packages) {
        const detail = node('details');
        detail.append(node('summary', `${pkg.native_name} · ${pkg.layer} · ${Object.keys(pkg.tree.entries).length} 项包内容`), node('code', pkg.revision));
        for (const path of Object.keys(pkg.tree.entries).sort()) {
          const e = pkg.tree.entries[path];
          detail.append(node('p', e.directory ? `${path}/ · 目录` : `${path} · ${e.bytes} B · ${e.sha256}`));
        }
        card.append(detail);
      }
      if (t.changes?.length) {
        const changes = node('details'); changes.append(node('summary', `查看 ${t.changes.length} 项拟议路径变化`));
        for (const c of t.changes) changes.append(node('p', `${c.before ? (c.after ? '修改' : '移除已归属输出') : '新增'} · ${c.path} · ${c.before?.bytes || 0} → ${c.after?.bytes || 0} B`));
        card.append(changes);
      }
      const diagnostics = node('details'); diagnostics.append(node('summary', '未验证条件与检查详情'));
      for (const f of t.findings || []) {
        const line = node('p', `${f.status} · ${f.code}：${f.detail}`);
        if (f.status === 'FAIL') line.className = 'error';
        if (f.status === 'FAIL') card.append(line); else diagnostics.append(line);
      }
      if (diagnostics.children.length > 1) card.append(diagnostics);
      el('native-targets').append(card);
    }
    sync();
  }
  async function run(recheck) {
    if (!context || context.busy() || context.pending() || recheck && !plan) return;
    const selected = ['claude-code', 'codex-cli'].filter(id => el(`native-${id}`).checked);
    if (!recheck && !selected.length) { invalidate(); context.message('请选择至少一个工具。', true); return; }
    const previous = plan;
    const eligibleCheck = lastCheck?.probe && !lastCheck.stale && selected.length === lastCheck.tools.length && selected.every(id => lastCheck.tools.some(t => t.tool === id)) ? lastCheck.id : undefined;
    invalidate(false);
    const requestGeneration = generation;
    context.setBusy(true);
    try {
      const data = await context.api(recheck ? '/api/native/diff' : '/api/native/plan', recheck ? {plan_id: previous.id, digest: previous.digest} : {tools: selected, ...(eligibleCheck ? {check_id:eligibleCheck} : {})});
      if (requestGeneration !== generation) return;
      plan = data.items;
      render(plan);
      context.message(recheck ? '预览输入仍一致；请查看文件安装条件和实际生效边界。' : '已根据磁盘保存版本生成预览；页面未保存草稿保留，不进入此计划。');
    } catch (error) {
      if (requestGeneration === generation) { invalidate(false); context.message(error.message, true); }
    } finally { context.setBusy(false); sync(); }
  }
  function init(ctx, app) {
    context = ctx;
    el('nav-native').hidden = !app.native_root;
    el('native-binding').textContent = app.native_root ? `已绑定工具项目根：${app.native_root}` : '启动控制台时用 --native-root 显式绑定专用测试项目。';
    if (typeof NativeRehearsal !== 'undefined') NativeRehearsal.init(ctx, () => plan);
    if (typeof NativeWrite !== 'undefined') NativeWrite.init(ctx, () => plan, () => invalidate());
    sync();
  }
  el('native-preview').addEventListener('click', () => run(false));
  el('native-recheck').addEventListener('click', () => run(true));
  el('native-check').addEventListener('click', () => check(false));
  el('native-last-check').addEventListener('click', restore);
  el('native-probe').addEventListener('click', () => {
    if (!context || context.busy() || context.pending()) return;
    probeSelection = selection();
    if (!probeSelection.length) { context.message('请选择至少一个工具。', true); return; }
    el('native-probe-confirm').hidden = false; el('native-probe-confirm').showModal?.();
  });
  el('native-probe-cancel').addEventListener('click', () => { if (!context?.busy()) closeProbe(); });
  el('native-probe-confirm').addEventListener('cancel', event => { event.preventDefault(); if (!context?.busy()) closeProbe(); });
  el('native-probe-confirm-run').addEventListener('click', () => probeSelection && check(true, probeSelection));
  for (const id of ['claude-code', 'codex-cli']) el(`native-${id}`).addEventListener('change', () => { invalidate(); checkGeneration++; closeProbe(); });
  return {init, invalidate, sync, restore};
})();
