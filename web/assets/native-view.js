'use strict';
// Shared source-page session; this view never submits a native apply request.
const NativePreview = (() => {
  const el = id => document.getElementById(id);
  let context = null, plan = null, generation = 0;
  const node = (tag, text) => { const n = document.createElement(tag); if (text !== undefined) n.textContent = text; return n; };
  function sync() {
    el('native-recheck').disabled = !context || context.busy() || context.pending() || !plan;
    el('native-apply').disabled = true;
  }
  function invalidate() {
    generation++;
    plan = null;
    el('native-result').hidden = true;
    sync();
  }
  function render(p) {
    el('native-result').hidden = false;
    el('native-summary').textContent = `${p.blocked ? '存在阻断项' : '预览已生成'} · ${p.targets.length} 个工具 · 写入未开放`;
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
      const technical = node('details'); technical.append(node('summary', '目标目录与适配器证据'), node('code', t.root), node('p', `适配器 ${t.adapter.version} · 官方契约核对 ${t.adapter.contract_checked} · 观察版本 ${t.adapter.observed_cli_version}（尚未验收）`));
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
    invalidate();
    const requestGeneration = generation;
    context.setBusy(true);
    try {
      const data = await context.api(recheck ? '/api/native/diff' : '/api/native/plan', recheck ? {plan_id: previous.id, digest: previous.digest} : {tools: selected});
      if (requestGeneration !== generation) return;
      plan = data.items;
      render(plan);
      context.message(recheck ? '预览输入仍一致；工具目标未修改，写入尚未开放。' : '已根据磁盘保存版本生成预览；页面未保存草稿保留，不进入此计划。');
    } catch (error) {
      if (requestGeneration === generation) { invalidate(); context.message(error.message, true); }
    } finally { context.setBusy(false); sync(); }
  }
  function init(ctx, app) {
    context = ctx;
    el('nav-native').hidden = !app.native_root;
    el('native-binding').textContent = app.native_root ? `已绑定工具项目根：${app.native_root}` : '启动控制台时用 --native-root 显式绑定专用测试项目。';
    sync();
  }
  el('native-preview').addEventListener('click', () => run(false));
  el('native-recheck').addEventListener('click', () => run(true));
  for (const id of ['claude-code', 'codex-cli']) el(`native-${id}`).addEventListener('change', invalidate);
  return {init, invalidate, sync};
})();
