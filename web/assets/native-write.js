'use strict';
const NativeWrite = (() => {
  const el = id => document.getElementById(id);
  const node = (tag, text) => { const n = document.createElement(tag); n.textContent = text; return n; };
  const labels = {apply:'安装工具文件', rollback:'回退工具应用', 'undo-rollback':'撤销回退'};
  let context, getPlan, changed, journals = [], confirmation = null, generation = 0;
  const available = () => context && !context.busy();
  const pending = () => journals.some(j => j.state === 'prepared');
  const latest = () => journals.find(j => j.state === 'committed');
  const canApply = p => p?.write_supported && !p.blocked && (p.profile_application ? p.profile_application.changes > 0 : p.targets?.some(t=>t.changes?.length || t.rule?.changes?.length));
  function sync() {
    const ready = available(), p = getPlan?.();
    el('native-apply').disabled = !ready || context.pending() || pending() || !canApply(p);
    el('native-tx-load').disabled = !ready;
    el('native-tx-recover').disabled = !ready;
    el('native-tx-reverse').disabled = !ready || pending() || !latest();
    el('native-tx-reverse').textContent = latest()?.plan.action === 'rollback' ? '撤销本次回退' : '回退最近一次工具应用';
    el('native-write-cancel').disabled = Boolean(context?.busy());
    el('native-write-confirm-run').disabled = !ready || !confirmation;
  }
  function close() { el('native-write-confirm').close?.(); el('native-write-confirm').hidden = true; confirmation = null; sync(); }
  function invalidate() { generation++; close(); }
  function unavailableHistory() {
    journals = [];
    el('native-tx-history').replaceChildren();
    el('native-tx-status').textContent = '应用记录读取失败；请重新读取，不使用上次记录回退。';
    sync();
  }
  function render(items) {
    journals = items || [];
    el('native-tx-status').textContent = pending() ? '有未完成事务，请先恢复。' : journals.length ? `${journals.length} 个项目工具事务 · 文件安装与会话生效分别检查` : '当前绑定没有项目工具事务。';
    el('native-tx-history').replaceChildren();
    for (const tx of journals) {
      const row = node('article'); row.className = 'native-check-card';
      row.append(node('p', `${tx.application_kind==='profile'?'联合应用 · ':tx.application_kind==='mcp'?'MCP 连接 · ':''}${labels[tx.plan.action] || tx.plan.action} · ${{committed:'成功', recovered:'已恢复', prepared:'未完成'}[tx.state] || tx.state}`), node('small', `${new Date(tx.updated).toLocaleString('zh-CN')} · ${tx.plan.id}`));
      const details = node('details'); details.append(node('summary','变更目录与记录'));
      for (const t of tx.plan.targets) details.append(node('code',`${tx.plan.root}/${t.path}`));
      if (tx.plan.reference) details.append(node('code',`引用事务 ${tx.plan.reference}`));
      row.append(details); el('native-tx-history').append(row);
    }
    sync();
  }
  async function load() {
    if (!available()) return;
    const g = ++generation; context.setBusy(true);
    try { const data = await context.api('/api/native/transactions'); if (g === generation) render(data.items); }
    catch (e) { if (g === generation) { unavailableHistory(); context.message(e.message,true); } }
    finally { context.setBusy(false); sync(); }
  }
  function show(kind, p) {
    confirmation = {kind, plan:p};
    el('native-write-title').textContent = kind === 'apply' ? '确认安装到工具项目' : p.action === 'rollback' ? '确认回退工具应用' : '确认撤销回退';
    el('native-write-summary').textContent = kind === 'apply' ? '按此预览安装完整技能包及经过独立规则检查的托管区块，自动备份并记录联合恢复事务。原生客户端以后执行技能属于 external；实际会话是否加载仍需检查。' : '恢复本次事务涉及的文件。外部修改会阻断，无关手写文件保留；技能源不会回退。';
    if (kind === 'apply') el('native-write-summary').textContent = (p.application_scope === 'skills-rules' ? '本次仅安装技能与规则；MCP 连接保持原值，由独立流程管理。' : '本次按完整 Profile 检查范围确认。') + el('native-write-summary').textContent;
    if (kind === 'apply' && p.profile_application) { el('native-write-title').textContent='确认联合应用'; el('native-write-summary').textContent=p.profile_application.boundary; }
    el('native-write-confirm-run').textContent = kind === 'apply' ? (p.profile_application ? '确认联合安装' : '确认安装工具文件') : p.action === 'rollback' ? '确认回退' : '确认撤销回退';
    el('native-write-changes').replaceChildren();
    for (const t of p.profile_application?.transaction.targets || p.targets) {
      el('native-write-changes').append(node('h3',t.root || `${p.root}/${t.path}`));
      if (kind === 'apply' && p.profile_application) {
        el('native-write-changes').append(node('p',JSON.stringify(t.before)===JSON.stringify(t.after)?'已一致':t.after.exists?'将更新；全部目标共用恢复事务':'将移除；可从共同恢复点回退'));
      } else if (kind === 'apply') {
        if (t.rule) { el('native-write-changes').append(node('h3',t.rule.path)); for (const c of t.rule.changes || []) el('native-write-changes').append(node('p', `${c.after ? c.before ? '修改托管区块' : '新增规则文件' : '移除规则文件'} · ${t.rule.path} · 区块外手写内容保留`)); }
        for (const c of t.changes || []) el('native-write-changes').append(node('p',`${c.after ? c.before ? '修改' : '新增' : '移除'} · ${c.path}`));
      } else {
        for (const path of [...new Set([...Object.keys(t.before.entries), ...Object.keys(t.after.entries)])].sort()) {
          if (JSON.stringify(t.before.entries[path]) !== JSON.stringify(t.after.entries[path])) el('native-write-changes').append(node('p',`${t.after.entries[path] ? '恢复' : '移除'} · ${t.file ? t.path : path}`));
        }
      }
    }
    el('native-write-confirm').hidden = false; el('native-write-confirm').showModal?.(); sync();
  }
  async function reverse() {
    if (!available() || pending() || !latest()) return;
    const g = ++generation, tx = latest(); context.setBusy(true);
    try { const data = await context.api('/api/native/rollback/plan', {transaction_id:tx.plan.id}); if (g === generation) show('reverse',data.items); }
    catch (e) { if (g === generation) context.message(e.message,true); }
    finally { context.setBusy(false); sync(); }
  }
  async function confirm() {
    if (!available() || !confirmation) return;
    const c = confirmation;
    if (c.kind === 'apply' && (!canApply(getPlan()) || getPlan()?.digest !== c.plan.digest)) { close(); context.message('应用预览已变化或已无文件变化，请重新检查。',true); return; }
    const g = ++generation; close(); context.setBusy(true);
    try {
      await context.api(c.kind === 'apply' ? '/api/native/apply' : '/api/native/rollback/apply', {plan_id:c.plan.id,digest:c.plan.digest});
      if (g === generation) { changed(); context.message(c.kind === 'apply' ? '工具文件已安装。可查看事务并回退；实际工具会话生效待验证。' : c.plan.action === 'rollback' ? '工具文件已回退；可撤销本次回退。' : '回退已撤销，工具文件恢复到应用状态。'); }
    } catch (e) { if (g === generation) { changed(); context.message(e.message,true); } }
    finally {
      try { const data = await context.api('/api/native/transactions'); render(data.items); } catch (_) { unavailableHistory(); /* Preserve operation result. */ }
      context.setBusy(false); sync();
    }
  }
  async function recover() {
    if (!available()) return;
    context.setBusy(true);
    try { const data = await context.api('/api/native/recover',{}); changed(); context.message(`已恢复 ${data.items} 个未完成工具事务。`); const history = await context.api('/api/native/transactions'); render(history.items); }
    catch (e) { unavailableHistory(); context.message(e.message,true); }
    finally { context.setBusy(false); sync(); }
  }
  el('native-apply').addEventListener('click', () => { const p = getPlan?.(); if (available() && !context.pending() && !pending() && canApply(p)) show('apply',p); });
  el('native-tx-load').addEventListener('click',load);
  el('native-tx-reverse').addEventListener('click',reverse);
  el('native-tx-recover').addEventListener('click',recover);
  el('native-write-confirm-run').addEventListener('click',confirm);
  el('native-write-cancel').addEventListener('click',()=>{ if (!context?.busy()) close(); });
  el('native-write-confirm').addEventListener('cancel',e=>{ e.preventDefault(); if (!context?.busy()) close(); });
  return {init(ctx,reader,onChanged) {context=ctx;getPlan=reader;changed=onChanged;sync();}, sync,invalidate,load};
})();
