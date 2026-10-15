'use strict';
const ProjectsView = (() => {
  const $ = id => document.getElementById(id);
  let ctx, app, items = [], plan = null, recovery = null;
  const route = item => `/source.html?binding=${encodeURIComponent(item.id)}`;
  function node(tag, text, cls) {
    const n = document.createElement(tag);
    if (text !== undefined) n.textContent = text;
    if (cls) n.className = cls;
    return n;
  }
  function open(id) { const n = $(id); n.hidden = false; if (!n.open) n.showModal?.(); }
  function close(id) { $(id).close?.(); $(id).hidden = true; }
  function say(text, error = false) {
    for (const id of ['projects-message', 'projects-home-message']) {
      $(id).textContent = text; $(id).classList.toggle('error', error);
    }
  }
  function invalidate() { plan = recovery = null; $('projects-plan').hidden = true; sync(); }
  function sync() {
    if (!ctx) return;
    for (const id of ['projects-dialog', 'projects-draft-dialog', 'project-home', 'project-shortcuts']) {
      for (const n of $(id).querySelectorAll('button,input,select')) n.disabled = ctx.busy();
    }
    for (const id of ['projects-manage', 'projects-add']) $(id).disabled = ctx.busy();
    $('projects-confirm').disabled = ctx.busy() || !(plan || recovery);
  }
  function navigate(item) {
    if (ctx.busy()) return;
    if (ctx.hasDrafts()) {
      $('projects-draft-target').textContent = `${item.name} · ${item.root}`;
      $('projects-draft-open').href = route(item);
      close('projects-dialog'); open('projects-draft-dialog');
    } else location.assign(route(item));
  }
  function render() {
    for (const id of ['projects-list', 'projects-home-list']) {
      const target = $(id); target.replaceChildren();
      if (!items.length) target.append(node('p', '还没有登记项目。接入后会在这里显示。', 'caption'));
      for (const item of items) {
        const card = node('article', undefined, 'project-card');
        const title = node('div', undefined, 'list-heading');
        title.append(node('h3', item.name), node('span', item.status === 'READY' ? item.scope_id : '需要检查', 'badge'));
        card.append(title, node('p', item.root, 'project-path'));
        if (item.reason) card.append(node('p', item.reason, 'caption error'));
        const current = ctx.binding === item.id || !ctx.binding && app.source_root === item.root;
        const b = node('button', current ? '当前项目' : item.status === 'READY' ? '打开项目' : '打开并检查', 'btn');
        b.type = 'button'; b.dataset.current = String(current);
        b.addEventListener('click', () => { if (!current) navigate(item); });
        const remove = node('button', '移除登记', 'btn ghost'); remove.type = 'button';
        remove.addEventListener('click', () => removeItem(item));
        card.append(b, remove); target.append(card);
      }
    }
    $('project-shortcuts').replaceChildren();
    for (const item of items) {
      const b = node('button', item.name, 'nav-item project-shortcut'); b.type = 'button';
      b.title = `${item.name} · ${item.scope_id} · ${item.root}`;
      b.classList.toggle('active', ctx.binding === item.id);
      b.addEventListener('click', () => navigate(item)); $('project-shortcuts').append(b);
    }
    sync();
  }
  function preview(p) {
    plan = p; recovery = null;
    const removing = p.kind === 'project-removal';
    $('projects-plan-title').textContent = `${removing ? '移除登记' : p.request.create ? '新建' : '打开'} · ${p.binding.name}`;
    const box = $('projects-plan-scope'); box.replaceChildren();
    box.append(node('p', `项目：${p.binding.root}`, 'project-path'), node('p', `作用域：${p.binding.scope_id}`));
    if (removing) box.append(node('p', '解除此项目副本的本机登记。保留项目目录、技能包、工具文件和历史资产；旧成员身份与删除记录继续保留。'));
    else if (p.request.create) box.append(node('p', '创建：project.yaml、skills.yaml、profile.yaml'));
    else box.append(node('p', '读取已有项目配置，保留项目身份与完整技能包。'));
    box.append(node('p', `本机登记：${p.batch.bound_roots.find(x => x.path === 'registry').root}`, 'project-path'));
    $('projects-confirm').textContent = removing ? '确认移除登记' : '确认接入项目'; $('projects-plan').hidden = false; sync();
  }
  function isCurrent(item) { return ctx.binding === item.id || !ctx.binding && app.source_root === item.root; }
  function showReferences(e) {
    if (!e.bindingReferences) return;
    $('projects-plan-title').textContent = '保留登记：恢复快照仍在引用';
    const box = $('projects-plan-scope'); box.replaceChildren();
    for (const ref of e.bindingReferences) box.append(node('p', `${ref.kind === 'source' ? '技能源' : '工具'} · ${ref.id} · ${ref.reason}`, 'project-path'));
    $('projects-confirm').textContent = '保留登记';
    $('projects-plan').hidden = false; sync();
  }
  async function removeItem(item) {
    if (ctx.busy()) return;
    open('projects-dialog');
    if (isCurrent(item) && ctx.hasDrafts()) { say('当前项目有未保存修改，请先保存或处理草稿，再移除登记。', true); return; }
    await run(async () => {
      invalidate();
      const out = await ctx.api('/api/projects/remove', {binding_id:item.id});
      preview(out.items); say('请核对要移除的项目副本。确认后只解除登记，文件保留。');
    });
  }
  async function removeCopy(copy) {
    if (!ctx || ctx.busy()) return;
    await run(refresh);
    const item = items.find(x => x.copy_id === copy.id && x.root === copy.root);
    if (item) await removeItem(item);
    else { open('projects-dialog'); say('副本登记已变化，请刷新后重新选择。', true); }
  }
  function recoverPreview(h) {
    if (ctx.busy()) return;
    plan = null; recovery = h.plan;
    const removing = h.plan.kind === 'project-removal';
    $('projects-plan-title').textContent = `恢复中断的${removing ? '移除' : '接入'} · ${h.plan.binding.name}`;
    const box = $('projects-plan-scope'); box.replaceChildren();
    for (const root of h.plan.batch.bound_roots) box.append(node('p', root.root, 'project-path'));
    box.append(node('p', h.state === 'committed' ? '操作已提交，清理剩余恢复标记，保留已提交结果。' : removing ? '恢复本次移除之前的登记。项目文件保留；外部修改会阻止覆盖。' : '恢复这次接入写入前的项目文件与本机登记。外部修改会阻止覆盖。'));
    $('projects-confirm').textContent = '确认恢复中断操作'; $('projects-plan').hidden = false; sync();
  }
  async function refresh() {
    // Recovery history must remain available even when a registry is pending.
    const results = await Promise.allSettled([ctx.api('/api/projects/list'), ctx.api('/api/projects/history')]);
    if (results[0].status === 'fulfilled') { items = results[0].value.items; render(); }
    $('projects-history').replaceChildren();
    if (results[1].status === 'fulfilled') {
      const entries = results[1].value.items;
      if (!entries.length) $('projects-history').append(node('p', '暂无接入记录。', 'caption'));
      const labels = {PREVIEW:'待确认', committed:'已接入', prepared:'中断待恢复', recovered:'已恢复'};
      for (const h of entries.slice().reverse()) {
        const row = node('div', undefined, 'project-history-row');
        const removing = h.plan.kind === 'project-removal';
        row.append(node('span', `${h.plan.binding.name} · ${removing ? '移除登记 · ' : ''}${removing && h.state === 'committed' ? '已移除' : labels[h.state] || h.state}`));
        if (h.pending) {
          const b = node('button', '预览恢复范围', 'btn'); b.type = 'button';
          b.addEventListener('click', () => recoverPreview(h)); row.append(b);
        }
        $('projects-history').append(row);
      }
      if (entries.some(h => h.pending)) say('项目操作曾中断。请展开“项目操作记录与中断恢复”，核对范围后恢复。', true);
    }
    const failure = results.find(r => r.status === 'rejected');
    if (failure) say(failure.reason.message, true);
    sync();
  }
  async function run(fn) {
    if (ctx.busy()) return;
    ctx.setBusy(true);
    try { await fn(); } catch (e) { say(e.message, true); showReferences(e); }
    finally { ctx.setBusy(false); }
  }
  async function init(context, state) {
    ctx = context; app = state;
    $('projects-manage').addEventListener('click', () => { if (!ctx.busy()) { open('projects-dialog'); run(refresh); } });
    $('projects-add').addEventListener('click', () => { if (!ctx.busy()) open('projects-dialog'); });
    for (const id of ['projects-refresh','projects-home-refresh']) $(id).addEventListener('click', () => run(refresh));
    $('projects-close').addEventListener('click', () => { if (!ctx.busy()) close('projects-dialog'); });
    $('projects-dialog').addEventListener('cancel', e => { e.preventDefault(); if (!ctx.busy()) close('projects-dialog'); });
    $('projects-draft-stay').addEventListener('click', () => close('projects-draft-dialog'));
    $('projects-draft-open').addEventListener('click', () => close('projects-draft-dialog'));
    $('projects-draft-dialog').addEventListener('cancel', e => { e.preventDefault(); close('projects-draft-dialog'); });
    $('projects-mode').addEventListener('change', () => {
      invalidate();
      $('projects-mode-note').textContent = $('projects-mode').value === 'create' ? '选择已存在的项目目录。确认后创建 .aidock 中的项目、技能索引与 Profile；已有文件会阻止新建。' : '选择包含 .aidock 的项目目录。保留原项目和作用域身份，仅添加本机入口。';
    });
    $('projects-form').addEventListener('input', invalidate);
    $('projects-form').addEventListener('submit', e => {
      e.preventDefault(); run(async () => {
        invalidate();
        const out = await ctx.api('/api/projects/plan', {root:$('projects-root').value.trim(),name:$('projects-name').value.trim(),create:$('projects-mode').value === 'create'});
        preview(out.items); say('请核对项目目录与写入范围，再确认接入。');
      });
    });
    $('projects-cancel').addEventListener('click', () => { if (!ctx.busy()) { invalidate(); say('已取消预览，项目目录未修改。'); } });
    $('projects-confirm').addEventListener('click', () => run(async () => {
      const p = plan, h = recovery; invalidate();
      if (!p && !h) return;
      if (p?.kind === 'project-removal' && isCurrent(p.binding) && ctx.hasDrafts()) { say('当前项目有未保存修改，请先处理草稿，再重新预览移除。', true); return; }
      await ctx.api(h ? '/api/projects/recover' : '/api/projects/apply', h ? {plan_id:h.id} : {plan_id:p.id,digest:p.digest});
      await refresh();
      if (p?.kind === 'project-removal') {
        say(`已移除“${p.binding.name}”的登记，项目文件保留。`);
        if (isCurrent(p.binding)) location.assign('/source.html');
      } else if (p) { $('projects-form').reset(); say(`已接入“${p.binding.name}”，点击“打开项目”开始管理技能。`); }
      else say('中断操作已恢复。请检查登记状态后继续。');
    }));
    if (app.project_workspace) {
      $('project-home').hidden = false; $('source-page-view').hidden = true;
      $('rightPanel').hidden = true;
      $('source-details').hidden = true; $('project-name').textContent = '选择项目';
      $('crumb-name').textContent = '项目'; $('crumb-sub').textContent = '项目与技能管理';
      $('search-trigger').hidden = true;
      for (const id of ['nav-source','nav-history','nav-native','nav-replica','context-toggle']) $(id).hidden = true;
    }
    await refresh();
    if (ctx.bindingError) say(ctx.bindingError, true);
  }
  return {init,sync,removeCopy};
})();
