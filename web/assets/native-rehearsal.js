'use strict';
const NativeRehearsal = (() => {
  const el = id => document.getElementById(id);
  const node = (tag, text) => { const n = document.createElement(tag); n.textContent = text; return n; };
  let context, getPlan, session = null, confirmation = null, generation = 0;
  const allowed = () => context && !context.busy() && !context.pending();
  const newest = () => session?.transactions?.find(tx => tx.state === 'committed');
  const labels = {apply:'应用完整包', rollback:'回退应用', 'undo-rollback':'撤销回退'};
  function sync() {
    const ready = allowed(); const p = getPlan?.();
    el('rehearsal-run').disabled = !ready || !p || p.blocked;
    el('rehearsal-latest').disabled = !ready;
    el('rehearsal-reverse').disabled = !ready || !session || session.pending || newest()?.state !== 'committed';
    el('rehearsal-reverse').textContent = newest()?.action === 'rollback' ? '撤销本次回退' : '回退本次预演';
    el('rehearsal-recover').hidden = !session?.pending;
    el('rehearsal-recover').disabled = !ready || !session?.pending;
    el('rehearsal-cancel').disabled = Boolean(context?.busy());
    el('rehearsal-confirm-run').disabled = !ready || !confirmation;
    el('native-tx-load').disabled = !context || context.busy();
    el('native-tx-recover').disabled = !context || context.busy();
  }
  function close() { el('rehearsal-confirm').close?.(); el('rehearsal-confirm').hidden = true; confirmation = null; sync(); }
  function invalidate() { generation++; close(); }
  function render(r) {
    session = r; el('rehearsal-result').hidden = !r;
    if (r) {
      el('rehearsal-status').textContent = '最近一次预演';
      el('rehearsal-state').textContent = {READY:'尚未应用', APPLIED:'已应用到预演项目', ROLLED_BACK:'已回退', UNDONE:'已撤销回退', PENDING:'需要恢复', RECOVERED:'失败变更已恢复'}[r.status] || r.status;
      el('rehearsal-boundary').textContent = r.boundary;
      el('rehearsal-root').textContent = r.root;
      el('rehearsal-id').textContent = `预演 ${r.id} · 原预览 ${r.native_plan}`;
      el('rehearsal-history').replaceChildren();
      for (const tx of r.transactions || []) {
        const li = node('li', `${labels[tx.action] || tx.action} · ${{committed:'成功', recovered:'已恢复', prepared:'未完成'}[tx.state] || tx.state} · ${tx.targets} 个工具 · ${tx.changes} 项路径变化`);
        li.append(node('small', `${new Date(tx.updated).toLocaleString('zh-CN')} · ${tx.id}`)); el('rehearsal-history').append(li);
      }
    }
    sync();
  }
  async function restore() {
    if (!allowed()) return;
    const g = ++generation;
    try { const data = await context.api('/api/native/rehearsal/latest'); if (g === generation) render(data.items); }
    catch (e) { if (g === generation) context.message(e.message, true); }
  }
  function show(kind, p) {
    confirmation = {kind, plan:p, session:session?.id};
    const start = kind === 'start';
    el('rehearsal-title').textContent = start ? '预演完整包应用' : p.action === 'rollback' ? '确认回退预演' : '确认撤销回退';
    el('rehearsal-summary').textContent = start ? `将已保存且启用的完整技能包应用到 ${p.targets.length} 个工具的独立目录。使用当前预览中的保存版本。` : `将恢复 ${p.targets.length} 个工具的预演文件。无关的手写文件保留；有外部修改时会阻断。`;
    el('rehearsal-confirm-run').textContent = start ? '确认预演应用' : p.action === 'rollback' ? '确认回退预演' : '确认撤销回退';
    el('rehearsal-diff').hidden = start; el('rehearsal-changes').replaceChildren();
    if (!start) for (const t of p.targets) {
      const keys = new Set([...Object.keys(t.before.entries), ...Object.keys(t.after.entries)]);
      for (const path of [...keys].sort()) {
        const before = t.before.entries[path], after = t.after.entries[path];
        if (JSON.stringify(before) !== JSON.stringify(after)) el('rehearsal-changes').append(node('p', `${after ? before ? '恢复' : '新增' : '移除'} · ${t.path}/${path}`));
      }
    }
    el('rehearsal-confirm').hidden = false; el('rehearsal-confirm').showModal?.(); sync();
  }
  async function reverse() {
    if (!allowed() || !session || session.pending || newest()?.state !== 'committed') return;
    const g = ++generation, current = session;
    context.setBusy(true);
    try { const data = await context.api('/api/native/rehearsal/plan', {session_id:current.id, transaction_id:newest().id}); if (g === generation) show('reverse', data.items); }
    catch (e) { if (g === generation) context.message(e.message, true); }
    finally { context.setBusy(false); sync(); }
  }
  async function confirm() {
    if (!allowed() || !confirmation) return;
    const c = confirmation;
    if (c.kind === 'start' && getPlan()?.digest !== c.plan.digest) { close(); context.message('预览已变化，请重新生成预演。', true); return; }
    const g = ++generation; close(); context.setBusy(true);
    try {
      const data = await context.api(`/api/native/rehearsal/${c.kind === 'start' ? 'run' : 'apply'}`, c.kind === 'start' ? {plan_id:c.plan.id, digest:c.plan.digest} : {session_id:c.session, plan_id:c.plan.id, digest:c.plan.digest});
      if (g === generation) { render(data.items); context.message('预演操作已完成；可检查记录并回退或撤销。当前项目和页面草稿保留。'); }
    } catch (e) {
      if (g === generation) {
        context.message(e.message, true);
        try { const latest = await context.api('/api/native/rehearsal/latest'); if (g === generation) render(latest.items); } catch (_) { /* Keep the original operation error visible. */ }
      }
    } finally { context.setBusy(false); sync(); }
  }
  async function recover() {
    if (!allowed() || !session?.pending) return;
    const g = ++generation; context.setBusy(true);
    try { const data = await context.api('/api/native/rehearsal/recover', {session_id:session.id}); if (g === generation) { render(data.items); context.message('中断的预演已恢复；外部修改不会被覆盖。'); } }
    catch (e) { if (g === generation) context.message(e.message, true); }
    finally { context.setBusy(false); sync(); }
  }
  async function transactions(recover = false) {
    if (!context || context.busy()) return;
    context.setBusy(true);
    try {
      if (recover) {
        const restored = await context.api('/api/native/recover', {});
        context.message(`已恢复 ${restored.items} 个未完成工具事务。`);
      }
      const data = await context.api('/api/native/transactions');
      el('native-tx-history').replaceChildren();
      el('native-tx-status').textContent = data.items.length ? `${data.items.length} 个项目工具事务；预演记录单独显示。` : '当前绑定没有项目工具事务。预演记录单独显示。';
      for (const tx of data.items) el('native-tx-history').append(node('p', `${labels[tx.plan.action] || tx.plan.action} · ${{committed:'成功', recovered:'已恢复', prepared:'未完成'}[tx.state] || tx.state} · ${tx.plan.id}`));
    } catch (e) { context.message(e.message, true); }
    finally { context.setBusy(false); sync(); }
  }
  el('rehearsal-run').addEventListener('click', () => { const p = getPlan?.(); if (allowed() && p && !p.blocked) show('start', p); });
  el('rehearsal-latest').addEventListener('click', restore);
  el('rehearsal-reverse').addEventListener('click', reverse);
  el('rehearsal-recover').addEventListener('click', recover);
  el('native-tx-load').addEventListener('click', () => transactions());
  el('native-tx-recover').addEventListener('click', () => transactions(true));
  el('rehearsal-confirm-run').addEventListener('click', confirm);
  el('rehearsal-cancel').addEventListener('click', () => { if (!context?.busy()) close(); });
  el('rehearsal-confirm').addEventListener('cancel', e => { e.preventDefault(); if (!context?.busy()) close(); });
  return {init(ctx, reader) { context = ctx; getPlan = reader; sync(); }, sync, invalidate, restore};
})();
