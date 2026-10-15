'use strict';
const ProfileView = (() => {
  const $ = id => document.getElementById(id);
  const copy = value => JSON.parse(JSON.stringify(value));
  let ctx, loaded = null, draft = null, plan = null, discardArmed = false;
  const dirty = () => Boolean(draft && loaded && JSON.stringify(draft) !== JSON.stringify(loaded.profile));
  function say(text, error = false) { $('profile-message').textContent = text; $('profile-message').classList.toggle('error', error); }
  function element(tag, text, cls) { const n = document.createElement(tag); if (text !== undefined) n.textContent = text; if (cls) n.className = cls; return n; }
  function invalidate() { plan = null; $('profile-plan').hidden = true; sync(); }
  function changed() { discardArmed = false; $('profile-reload').textContent = '重读已保存版本'; invalidate(); $('profile-draft').textContent = dirty() ? '有未保存修改' : '与已保存版本一致'; }
  function sync() {
    if (!ctx) return;
    for (const n of $('profile-dialog').querySelectorAll('button,input,textarea')) n.disabled = ctx.busy();
    for (const n of $('profile-form').querySelectorAll('button,input,textarea')) n.disabled = ctx.busy() || !draft;
    $('nav-profile').disabled = ctx.busy();
    $('profile-preview').disabled = ctx.busy() || ctx.pending() || !loaded || !dirty() || loaded.profile.schema_version !== 2;
    $('profile-confirm').disabled = ctx.busy() || ctx.pending() || !plan;
  }
  function setSwitch(id, enabled) {
    const skills = draft.skills ||= {};
    skills.enable = (skills.enable || []).filter(x => x !== id);
    skills.disable = (skills.disable || []).filter(x => x !== id);
    skills[enabled ? 'enable' : 'disable'].push(id);
    skills.enable.sort(); skills.disable.sort();
  }
  function renderRules() {
    const target = $('profile-rules'); target.replaceChildren();
    const rules = draft.rules || [];
    if (!rules.length) target.append(element('p','还没有共享规则文本。','caption'));
    rules.forEach((rule, i) => {
      const row = element('div',undefined,'profile-rule');
      const label = element('label',`规则 ${i+1}`), text = element('textarea'); text.value = rule;
      text.setAttribute('aria-label',`规则 ${i+1}`); text.rows = 3;
      text.addEventListener('input',()=>{ draft.rules[i]=text.value; changed(); }); label.append(text);
      const remove = element('button','移除此规则','btn ghost'); remove.type='button';
      remove.addEventListener('click',()=>{ if(ctx.busy())return; draft.rules.splice(i,1); changed(); renderRules(); });
      row.append(label,remove); target.append(row);
    });
    sync();
  }
  function render() {
    if (!draft) return;
    $('profile-name').value = draft.name;
    $('profile-description').value = draft.description || '';
    $('profile-rule-mode').checked = Boolean(draft.rules_override);
    const target=$('profile-skills'); target.replaceChildren();
    for(const item of loaded.skills) {
      const s=item.skill, row=element('label',undefined,'profile-skill'), input=element('input'); input.type='checkbox';
      input.checked=Boolean(draft.skills?.enable?.includes(s.id)); input.setAttribute('aria-label',`Profile 启用 ${s.native_name}`);
      input.addEventListener('change',()=>{if(ctx.busy())return;setSwitch(s.id,input.checked);changed();});
      const layer=({owned:'项目自定义',inherited:'全局继承',external:'外部批准',override:'项目覆盖',fork:'独立分叉'})[item.layer]||item.layer;
      const text=element('span'); text.append(element('strong',s.display_name||s.native_name),element('small',`${s.native_name} · ${layer} · 固定版本 ${s.revision.slice(7,19)}`));
      row.append(input,text);target.append(row);
    }
    if(!loaded.skills.length)target.append(element('p','先在技能库创建或采纳技能。','caption'));
    $('profile-checks').replaceChildren();
    for(const check of loaded.checks || []) $('profile-checks').append(element('p',`${check.status} · ${check.code}：${check.detail}`,'caption'));
    $('profile-boundary').textContent=loaded.boundary;
    $('profile-report').textContent=JSON.stringify(loaded.report || loaded.profile,null,2);
    $('profile-draft').textContent=dirty()?'有未保存修改':'与已保存版本一致';
    renderRules(); sync();
  }
  async function run(fn) {
    if(ctx.busy())return;
    ctx.setBusy(true);
    try {await fn();} catch(e) {invalidate();say(e.message,true);} finally {ctx.setBusy(false);}
  }
  async function load(replace = false) {
    const next=(await ctx.api('/api/profile/state')).items;
    if(!replace && dirty()) {if(next.sha256!==loaded.sha256)say('已保存版本发生变化；当前草稿保留。请核对后重读，再重新预览。',true);return;}
    loaded=next;draft=copy(next.profile);invalidate();render();say('当前共享 Profile 已读取。保存后保留回退点。');
  }
  function close() {
    if(ctx.busy())return;
    $('profile-dialog').close?.();$('profile-dialog').hidden=true;
    ctx.message(dirty()?'已关闭 Profile；未保存修改保留在当前标签页。':'Profile 已关闭。');
  }
  function init(context) {
    ctx=context;
    $('nav-profile').addEventListener('click',()=>{
      if(ctx.busy())return;
      $('profile-dialog').hidden=false;if(!$('profile-dialog').open)$('profile-dialog').showModal?.();
      run(()=>load());
    });
    $('profile-close').addEventListener('click',close);
    $('profile-dialog').addEventListener('cancel',e=>{e.preventDefault();close();});
    $('profile-name').addEventListener('input',()=>{draft.name=$('profile-name').value;changed();});
    $('profile-description').addEventListener('input',()=>{draft.description=$('profile-description').value;changed();});
    $('profile-rule-mode').addEventListener('change',()=>{draft.rules_override=$('profile-rule-mode').checked;changed();});
    $('profile-add-rule').addEventListener('click',()=>{if(ctx.busy()||!draft)return;(draft.rules ||= []).push('');changed();renderRules();});
    $('profile-all-on').addEventListener('click',()=>{if(ctx.busy()||!draft)return;for(const item of loaded.skills)setSwitch(item.skill.id,true);changed();render();});
    $('profile-all-off').addEventListener('click',()=>{if(ctx.busy()||!draft)return;for(const item of loaded.skills)setSwitch(item.skill.id,false);changed();render();});
    $('profile-reload').addEventListener('click',()=>{
      if(ctx.busy())return;
      invalidate();
      if(dirty()&&!discardArmed){discardArmed=true;$('profile-reload').textContent='确认放弃 Profile 草稿并重读';say('再次点击将放弃当前 Profile 草稿；技能文件草稿保留。',true);return;}
      discardArmed=false;$('profile-reload').textContent='重读已保存版本';run(()=>load(true));
    });
    $('profile-form').addEventListener('submit',e=>{
      e.preventDefault();if(!loaded||!dirty()||ctx.pending())return;
      run(async()=>{
        invalidate();
        const out=(await ctx.api('/api/profile/plan',{expected_sha256:loaded.sha256,profile:copy(draft)})).items;
        plan=out.plan;
        $('profile-changes').replaceChildren();
        if(loaded.profile.name!==draft.name)$('profile-changes').append(element('p',`Profile 名称：${loaded.profile.name} → ${draft.name}`));
        if(loaded.profile.description!==draft.description)$('profile-changes').append(element('p',`说明：${draft.description || '（空）'}`));
        if(JSON.stringify(loaded.profile.rules||[])!==JSON.stringify(draft.rules||[]) || Boolean(loaded.profile.rules_override)!==Boolean(draft.rules_override))$('profile-changes').append(element('p',`规则：${(loaded.profile.rules||[]).length} → ${(draft.rules||[]).length} 条 · ${draft.rules_override?'整体替换上层列表':'按继承顺序追加'}`));
        const old=new Set(loaded.profile.skills?.enable||[]),next=new Set(draft.skills?.enable||[]);
        for(const item of loaded.skills)if(old.has(item.skill.id)!==next.has(item.skill.id))$('profile-changes').append(element('p',`${next.has(item.skill.id)?'启用':'停用'} · ${item.skill.native_name}`));
        for(const change of plan.changes)$('profile-changes').append(element('p',`${change.kind || change.action || '更新'} · ${change.path}`,'caption'));
        for(const check of out.preflight?.profile_checks||[])$('profile-changes').append(element('p',`${check.status} · ${check.detail}`,'caption'));
        $('profile-plan').hidden=false;say('请核对整批变化。确认只保存共享源，不应用到工具或其他副本。');sync();
      });
    });
    $('profile-cancel').addEventListener('click',()=>{if(ctx.busy())return;invalidate();say('已取消预览；Profile 草稿保留，源未修改。');});
    $('profile-confirm').addEventListener('click',()=>{
      if(!plan||ctx.pending())return;
      run(async()=>{
        const p=plan;invalidate();
        await ctx.api('/api/source/apply',{plan_id:p.id,digest:p.digest});
        draft=null;await ctx.refresh();await load(true);
        say('Profile 已保存，整批变化共用一个回退点。工具应用与副本发布需单独确认。');
      });
    });
    sync();
  }
  return {init,sync,hasDraft:dirty};
})();
