'use strict';
const ProfileView = (() => {
  const $ = id => document.getElementById(id);
  const copy = value => JSON.parse(JSON.stringify(value));
  let ctx, loaded = null, draft = null, ruleFiles = [], namedProfiles = [], libraryRoot = '', activeLayer = 'shared', plan = null, discardArmed = false, importOrigin = null;
  const localBase = () => loaded?.layers?.local || {schema_version:2,name:'本机覆盖'};
  const dirty = () => Boolean(draft && loaded && (importOrigin || JSON.stringify(draft) !== JSON.stringify(activeLayer==='local'?localBase():loaded.profile) || (activeLayer==='local' ? libraryRoot!==(loaded.layers?.library_root || '') : JSON.stringify(ruleFiles)!==JSON.stringify(loaded.rule_files || []) || JSON.stringify(namedProfiles)!==JSON.stringify(loaded.named_profiles || []))));
  function say(text, error = false) { $('profile-message').textContent = text; $('profile-message').classList.toggle('error', error); }
  function element(tag, text, cls) { const n = document.createElement(tag); if (text !== undefined) n.textContent = text; if (cls) n.className = cls; return n; }
  function renderChecks(target,checks,title='检查结果') {
    if(!checks?.length)return;
    const counts={PASS:0,WARN:0,UNKNOWN:0,FAIL:0};for(const c of checks)counts[c.status]=(counts[c.status]||0)+1;
    const detail=element('details',undefined,'profile-check-detail');detail.open=counts.FAIL>0;
    detail.append(element('summary',`${title}：通过 ${counts.PASS} · 提醒 ${counts.WARN} · 待验证 ${counts.UNKNOWN} · 阻断 ${counts.FAIL}`));
    for(const c of checks)detail.append(element('p',`${c.status} · ${c.code}：${c.detail}`,'caption'));
    target.append(detail);
  }
  function renderCandidateChecks(target,checks){
    if(checks?.some(c=>['MCP_PROJECT_AUTHORIZATION_REQUIRED','MCP_PROJECT_AUTHORIZATION_STALE','MCP_RUNTIME_CHECK_STALE'].includes(c.code)))target.append(element('p','保存后，MCP 授权与运行时证据需要重新审查；本次保存不会自动授权或应用工具。','notice'));
    renderChecks(target,checks,'候选检查');
  }
  function invalidate() { plan = null; $('profile-plan').hidden = true; sync(); }
  function changed() { discardArmed = false; $('profile-reload').textContent = '重读已保存版本'; invalidate(); $('profile-draft').textContent = dirty() ? '有未保存修改' : '与已保存版本一致'; }
  function sync() {
    if (!ctx) return;
    for (const n of $('profile-dialog').querySelectorAll('button,input,textarea,select')) n.disabled = ctx.busy();
    for (const n of $('profile-form').querySelectorAll('button,input,textarea,select')) n.disabled = ctx.busy() || !draft || Boolean(n.ruleBoundary);
    $('profile-migrate-rules').disabled = ctx.busy() || !draft || !(draft.rules || []).length;
    $('nav-profile').disabled = ctx.busy();
    $('profile-preview').disabled = ctx.busy() || ctx.pending() || !loaded || !dirty() || loaded.profile.schema_version !== 2;
    $('profile-confirm').disabled = ctx.busy() || ctx.pending() || !plan;
    $('profile-local-clear').disabled=ctx.busy()||ctx.pending()||!loaded?.layers?.local;
    $('profile-local-reverse').disabled=ctx.busy()||ctx.pending()||loaded?.layers?.history?.[0]?.state!=='committed';
    $('profile-local-recover').disabled=ctx.busy();
    $('profile-layer').disabled=ctx.busy()||!loaded;
    const matches = draft ? matchingSkills().length : 0;
    for (const id of ['profile-filtered-on','profile-filtered-off','profile-filtered-inherit']) $(id).disabled=ctx.busy()||!draft||!matches;
    for (const id of ['profile-all-on','profile-all-off']) $(id).disabled=ctx.busy()||!draft||!loaded?.skills?.length;
  }
  const layerName = id => ({'mcp-definition':'MCP 定义默认',defaults:'全局默认',project:Array.isArray(loaded?.named_profiles)?'全局默认':'项目共享',local:'本机覆盖',session:'临时会话'})[id] || id?.replace(/^extends:/,'继承 · ') || '未设置';
  function localHistoryLabel(item) {
    const action=item.action?.startsWith('rollback:')?'回退 / 撤销':({'profile-local-edit':'保存本机配置','profile-local-clear':'清除本机覆盖'})[item.action]||item.action;
    const state=({committed:'已完成',recovered:'已恢复',prepared:'待恢复',applying:'待恢复',failed:'待恢复'})[item.state]||item.state;
    const date=new Date(item.updated),time=Number.isNaN(date.getTime())?item.updated:date.toLocaleString('zh-CN',{hour12:false});
    return `${time} · ${action} · ${state}`;
  }
  function renderReport(target,report) {
    target.replaceChildren();
    if(!report?.effective){target.append(element('p','有效配置未能解析，请先处理下面的检查结果。','caption'));return;}
    target.append(element('p',(report.layers||[]).map(l=>`${layerName(l.id)}（${l.name}）`).join(' → '),'caption'));
    const rows=[],effective=report.effective,trace=report.trace||[];
    for(const kind of ['skills','plugins','mcp']){
      for(const action of ['enable','disable'])for(const id of effective[kind]?.[action]||[])rows.push([`/${kind}/${id}`,`${action==='enable'?'启用':'停用'} · ${loaded?.skills?.find(s=>s.skill.id===id)?.skill.native_name||id}`]);
    }
    for(const [id,revision]of Object.entries(effective.skills?.pins||{}))rows.push([`/skills/pins/${id}`,revision]);
    function walk(value,path){if(value&&typeof value==='object'&&!Array.isArray(value)&&Object.keys(value).length){for(const [k,v]of Object.entries(value))walk(v,`${path}/${k.replace(/~/g,'~0').replace(/\//g,'~1')}`);}else rows.push([path,JSON.stringify(value)]);}
    for(const [key,value]of Object.entries(effective.config_overrides||{}))walk(value,`/config_overrides/${key.replace(/~/g,'~0').replace(/\//g,'~1')}`);
    (effective.rules||[]).forEach((rule,i)=>rows.push([`/rules/${i}`,rule]));
    if(!rows.length)target.append(element('p','当前没有启用项、规则或额外配置。','caption'));
    const table=element('table',undefined,'profile-values'),head=element('tr');for(const title of ['配置项','有效值','来源与覆盖'])head.append(element('th',title));const thead=element('thead');thead.append(head);table.append(thead);const body=element('tbody');
    for(const [path,value]of rows){const events=trace.filter(e=>e.path===path||(path.startsWith(e.path+'/')&&['replace','set','remove'].includes(e.action))),last=events.at(-1),sources=[...new Set(events.map(e=>layerName(e.layer)))],row=element('tr');let label=path.replace('/config_overrides/','配置 · ').replace(/^\/rules\/(\d+)$/,(_,i)=>`规则 ${Number(i)+1}`);if(path.startsWith('/skills/')){const id=path.split('/').at(-1),name=loaded?.skills?.find(s=>s.skill.id===id)?.skill.native_name||id;label=`${path.includes('/pins/')?'固定版本':'技能'} · ${name}`;}if(path.startsWith('/mcp/'))label=`MCP · ${path.slice(5)}`;row.append(element('td',label),element('td',value),element('td',`${layerName(last?.layer)}${sources.length>1?` · 覆盖顺序：${sources.join(' → ')}`:''}`));body.append(row);}
    table.append(body);if(rows.length)target.append(table);
    const removals=trace.filter((e,i)=>e.action==='remove'||e.action==='replace'&&trace.slice(0,i).some(p=>p.layer!==e.layer&&(p.path===e.path||p.path.startsWith(e.path+'/'))));
    if(removals.length){const details=element('details'),summary=element('summary',`查看被移除或替换的上层配置（${removals.length} 项）`);details.append(summary);for(const event of removals)details.append(element('p',`${event.path} · ${layerName(event.layer)}${event.previous_layer?` · 原来源：${layerName(event.previous_layer)}`:''}`,'caption'));target.append(details);}
    if(trace.some(e=>e.layer==='local'&&e.previous_layer))target.append(element('p','共享配置已参与合并；被本机覆盖的项目，当前有效值仍由本机配置决定。','notice'));
  }
  function renderNamed() {
    const target=$('profile-named-list');target.replaceChildren();
    namedProfiles.forEach((item,i)=>{
      const card=element('article',undefined,'profile-rule-file');
      for(const [key,title]of [['name','名称'],['extends','继承名称'],['description','说明']]){const label=element('label',`${title} ${i+1}`),input=element('input');input.value=item[key]||'';input.setAttribute('aria-label',`可复用 Profile ${title} ${i+1}`);input.addEventListener('input',()=>{if(input.value)item[key]=input.value;else delete item[key];changed();});label.append(input);card.append(label);}
      const mcp=element('div',undefined,'profile-skills');renderMCP(mcp,item,`可复用 Profile ${i+1} MCP`,false);card.append(mcp);
      (item.rules||[]).forEach((body,j)=>{const text=element('textarea');text.value=body;text.rows=3;text.setAttribute('aria-label',`可复用 Profile ${i+1} 规则 ${j+1}`);text.addEventListener('input',()=>{item.rules[j]=text.value;changed();});const remove=element('button','移除此规则','btn ghost');remove.type='button';remove.addEventListener('click',()=>{item.rules.splice(j,1);changed();renderNamed();});card.append(text,remove);});
      const add=element('button','添加规则','btn');add.type='button';add.setAttribute('aria-label',`为 Profile ${i+1} 添加规则`);add.addEventListener('click',()=>{(item.rules||=[]).push('');changed();renderNamed();});
      const remove=element('button','移除此 Profile','btn ghost');remove.type='button';remove.addEventListener('click',()=>{namedProfiles.splice(i,1);changed();renderNamed();});card.append(add,remove);target.append(card);
    });sync();
  }
  function setSwitch(id, enabled) {
    const skills = draft.skills ||= {};
    skills.enable = (skills.enable || []).filter(x => x !== id);
    skills.disable = (skills.disable || []).filter(x => x !== id);
    skills[enabled ? 'enable' : 'disable'].push(id);
    skills.enable.sort(); skills.disable.sort();
  }
  const skillChoice = (value,id) => value.skills?.enable?.includes(id)?'enable':value.skills?.disable?.includes(id)?'disable':'inherit';
  const choiceName = value => ({inherit:'继承上层',enable:'明确启用',disable:'明确停用'})[value];
  const skillLayerName = layer => ({owned:Array.isArray(loaded?.named_profiles)?'全局自定义':'项目自定义',inherited:'全局继承',external:'外部批准',override:'项目覆盖',fork:'独立分叉'})[layer]||layer;
  function matchingSkills() {
    const words=$('profile-skill-search').value.trim().toLowerCase().split(/\s+/).filter(Boolean);
    const layer=$('profile-skill-source').value, choice=$('profile-skill-choice').value;
    return (loaded?.skills||[]).filter(item=>{
      const s=item.skill,text=[s.display_name,s.native_name,s.id,s.description,s.revision,skillLayerName(item.layer)].filter(Boolean).join(' ').toLowerCase();
      return (!layer||layer==='all'||item.layer===layer)&&(!choice||choice==='all'||skillChoice(draft,s.id)===choice)&&words.every(word=>text.includes(word));
    });
  }
  function renderSkillSummary(items=matchingSkills()) {
    const total=loaded?.skills?.length||0, counts={enable:0,disable:0,inherit:0};
    for(const item of loaded?.skills||[])counts[skillChoice(draft,item.skill.id)]++;
    $('profile-skill-summary').textContent=`匹配 ${items.length} / ${total} 项 · 本层草稿：启用 ${counts.enable}、停用 ${counts.disable}、继承 ${counts.inherit}`;
    for(const [id,label]of [['profile-filtered-on','启用筛选项'],['profile-filtered-off','停用筛选项'],['profile-filtered-inherit','筛选项继承上层']])$(id).textContent=`${label}（${items.length}）`;
    $('profile-all-on').textContent=`全部启用（${total}）`;$('profile-all-off').textContent=`全部停用（${total}）`;
  }
  function renderSkills() {
    const target=$('profile-skills'),items=matchingSkills();target.replaceChildren();
    for(const item of items) {
      const s=item.skill,row=element('div',undefined,'profile-skill'),input=element('select');
      row.setAttribute('data-skill-id',s.id);
      for(const value of ['inherit','enable','disable']){const option=element('option',choiceName(value));option.value=value;input.append(option);}
      input.value=skillChoice(draft,s.id);input.setAttribute('aria-label',`Profile 开关 ${s.native_name}`);
      input.addEventListener('change',()=>{
        if(ctx.busy())return;
        setSkillChoice(s.id,input.value);changed();
        if(['enable','disable','inherit'].includes($('profile-skill-choice').value))renderSkills();else renderSkillSummary();
      });
      const text=element('span');text.append(element('strong',s.display_name||s.native_name),element('small',`${s.native_name} · ${skillLayerName(item.layer)} · 固定版本 ${s.revision.slice(7,19)}`),element('small',`ID ${s.id}`));
      row.append(input,text);
      const versions=[...new Map([s,...(item.available_versions||[])].map(v=>[v.revision,v])).values()];
      if(item.layer==='inherited'&&versions.length>1){
        row.classList?.add?.('profile-skill-versions');
        const label=element('label',`${activeLayer==='local'?'本机':'共享'}固定版本`,'profile-version-choice'),version=element('select');
        const inherited=element('option','继承上层版本');inherited.value='';version.append(inherited);
        for(const v of versions){const option=element('option',`${v.native_name} · ${v.revision.slice(7,19)}`);option.value=v.revision;version.append(option);}
        const currentPin=draft.skills?.pins?.[s.id];if(currentPin&&!versions.some(v=>v.revision===currentPin)){const option=element('option',`当前未采纳版本 · ${currentPin.slice(7,19)}（需采纳）`);option.value=currentPin;option.disabled=true;version.append(option);}
        version.value=draft.skills?.pins?.[s.id]||'';version.setAttribute('aria-label',`Profile 固定版本 ${s.native_name}`);
        version.addEventListener('change',()=>{if(ctx.busy())return;const pins=(draft.skills||={}).pins||={};if(version.value)pins[s.id]=version.value;else delete pins[s.id];changed();});
        label.append(version);row.append(label);
      }
      target.append(row);
    }
    if(!items.length)target.append(element('p',loaded.skills.length?'没有匹配技能，可清除筛选重新查找。':'先在技能库创建或采纳技能。','caption'));
    renderSkillSummary(items);sync();
  }
  function setSkillChoice(id,value) {
    if(value!=='inherit'){setSwitch(id,value==='enable');return;}
    for(const key of ['enable','disable'])if(draft.skills?.[key])draft.skills[key]=draft.skills[key].filter(x=>x!==id);
  }
  function batchSkills(value,filtered) {
    if(ctx.busy()||!draft)return;
    const items=filtered?matchingSkills():loaded.skills;
    if(!items.length)return;
    for(const item of items)setSkillChoice(item.skill.id,value);
    changed();renderSkills();
    say(`已将${filtered?'本次筛选的':'全部'} ${items.length} 项设为${choiceName(value)}，仅更新${activeLayer==='local'?'本机':'共享'}草稿；预览确认后保存。`);
  }
  function appendSkillChanges(before,after) {
    for(const item of loaded.skills){const id=item.skill.id,old=skillChoice(before,id),next=skillChoice(after,id);if(old!==next)$('profile-changes').append(element('p',`技能 · ${item.skill.native_name}：${choiceName(old)} → ${choiceName(next)}`));const oldPin=before.skills?.pins?.[id]||'继承上层版本',nextPin=after.skills?.pins?.[id]||'继承上层版本';if(oldPin!==nextPin)$('profile-changes').append(element('p',`固定版本 · ${item.skill.native_name}：${oldPin} → ${nextPin}`));}
  }
  function appendEffectiveSkillChanges(before,after) {
    const old=before?.effective?.skills||{},next=after?.effective?.skills||{};
    const ids=[...new Set([...Object.keys(old.pins||{}),...Object.keys(next.pins||{}),...(old.enable||[]),...(old.disable||[]),...(next.enable||[]),...(next.disable||[])])].sort();
    const choice=(report,id)=>!report?.effective?'未解析':report.effective.skills?.enable?.includes(id)?'启用':'停用';
    for(const id of ids){const name=loaded.skills.find(s=>s.skill.id===id)?.skill.native_name||id,oldPin=before?.effective?(old.pins?.[id]||'未设置'):'未解析',nextPin=after?.effective?(next.pins?.[id]||'未设置'):'未解析';if(oldPin!==nextPin)$('profile-changes').append(element('p',`有效固定版本 · ${name}：${oldPin} → ${nextPin}`));if(choice(before,id)!==choice(after,id))$('profile-changes').append(element('p',`有效开关 · ${name}：${choice(before,id)} → ${choice(after,id)}`));}
  }
  function mcpChoice(value,id) { return value.mcp?.enable?.includes(id)?'enable':value.mcp?.disable?.includes(id)?'disable':value.plugins?.enable?.includes(id)?'enable':value.plugins?.disable?.includes(id)?'disable':'inherit'; }
  function renderMCP(target,value,label='Profile MCP 开关',showCurrent=true) {
    target.replaceChildren();
    const servers=new Map((loaded?.mcp||[]).map(s=>[s.name,s]));
    for(const name of [...(value.mcp?.enable||[]),...(value.mcp?.disable||[]),...(value.plugins?.enable||[]),...(value.plugins?.disable||[])])if(!servers.has(name))servers.set(name,{name});
    for(const [name,server]of servers) {
      const row=element('label',undefined,'profile-skill'),input=element('select');
      for(const [key,title]of [['inherit','继承上层'],['enable','明确启用'],['disable','明确停用']]){const option=element('option',title);option.value=key;input.append(option);}
      input.value=mcpChoice(value,name);input.setAttribute('aria-label',`${label} ${name}`);
      input.addEventListener('change',()=>{if(ctx.busy())return;const section=value.mcp||={};for(const key of ['enable','disable']){section[key]=(section[key]||[]).filter(id=>id!==name);if(value.plugins?.[key])value.plugins[key]=value.plugins[key].filter(id=>id!==name);}if(input.value!=='inherit')section[input.value].push(name);section.enable.sort();section.disable.sort();changed();});
      const text=element('span');text.append(element('strong',name),element('small',server.selection_resolved===undefined?'定义缺失 · 请解除引用或补齐定义':showCurrent?`定义默认${server.enabled?'启用':'停用'} · 已保存有效${server.selection_resolved?(server.effective_enabled?'启用':'停用'):'值未解析'} · ${layerName(server.enabled_source)}`:`定义默认${server.enabled?'启用':'停用'} · 项目引用后按继承链合并`));
      row.append(input,text);target.append(row);
    }
    if(!servers.size)target.append(element('p','先在 MCP 定义中登记服务器，再配置组合。','caption'));
  }
  function renderRules() {
    const target = $('profile-rules'); target.replaceChildren();
    const rules = draft.rules || [];
    if (!rules.length) target.append(element('p',activeLayer==='local'?'还没有本机规则文本。':'还没有共享规则文本。','caption'));
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
  function renderFileRules() {
    const target = $('profile-rule-files'); target.replaceChildren();
    if (!ruleFiles.length) target.append(element('p','还没有规则文件。可新建规则，或把现有内联规则转为文件。','caption'));
    ruleFiles.forEach((rule,i)=>{
      const row=element('article',undefined,'profile-rule-file');
      const title=element('div',undefined,'list-heading'), label=element('label',`规则文件 ${i+1}`), name=element('input');
      name.value=rule.name;name.maxLength=120;name.required=true;name.setAttribute('aria-label',`规则文件名称 ${i+1}`);
      name.addEventListener('input',()=>{rule.name=name.value;changed();});label.append(name);
      const enabledLabel=element('label',undefined,'profile-rule-mode'), enabled=element('input');enabled.type='checkbox';enabled.checked=rule.enabled;
      enabled.setAttribute('aria-label',`启用规则文件 ${i+1}`);enabled.addEventListener('change',()=>{rule.enabled=enabled.checked;changed();});
      enabledLabel.append(enabled,element('span','启用'));title.append(label,enabledLabel);
      const body=element('textarea');body.rows=5;body.value=rule.body;body.setAttribute('aria-label',`规则文件正文 ${i+1}`);
      body.addEventListener('input',()=>{rule.body=body.value;changed();});
      const actions=element('div',undefined,'source-actions');
      for (const [text,offset] of [['上移',-1],['下移',1]]) {
        const move=element('button',text,'btn ghost');move.type='button';move.setAttribute('aria-label',`${text}规则文件 ${i+1}`);move.ruleBoundary=i+offset<0||i+offset>=ruleFiles.length;
        move.addEventListener('click',()=>{if(ctx.busy()||move.ruleBoundary)return;[ruleFiles[i],ruleFiles[i+offset]]=[ruleFiles[i+offset],ruleFiles[i]];changed();renderFileRules();});actions.append(move);
      }
      const remove=element('button','移除规则文件','btn ghost');remove.type='button';remove.setAttribute('aria-label',`移除规则文件 ${i+1}`);
      remove.addEventListener('click',()=>{if(ctx.busy())return;ruleFiles.splice(i,1);changed();renderFileRules();});actions.append(remove);
      row.append(title,element('p',rule.id?`rules/${rule.id}.md`:'新规则 · 预览时分配固定身份','caption'),body,actions);target.append(row);
    });
    $('profile-unmanaged-rules').replaceChildren();
    for(const path of loaded?.unmanaged_rules || [])$('profile-unmanaged-rules').append(element('p',`未登记 · ${path} · 保留原文件，未参与编译`,'caption'));
    sync();
  }
  function render() {
    if (!draft) return;
    $('profile-import-origin').hidden = !importOrigin;
    if (importOrigin) $('profile-import-description').textContent = `已读取 ${importOrigin.tool === 'codex-cli' ? 'AGENTS.md' : 'CLAUDE.md'} 的托管正文。请按需要编辑合并；保存只修改共享源，原生区块的接管或覆盖仍需在 AI 工具页单独确认。`;
    $('profile-name').value = draft.name;
    $('profile-description').value = draft.description || '';
    $('profile-rule-mode').checked = Boolean(draft.rules_override);
    $('profile-layer').value=activeLayer;
    $('profile-layer').querySelectorAll('option').forEach(o=>{if(o.value==='local')o.disabled=!loaded.layers?.local_available;});
    $('profile-layer-note').textContent=activeLayer==='local'?'只影响当前副本，独立保存和恢复，不参与跨副本同步。':'保存共享层；上层未提及的内容继续继承，本机覆盖可遮盖本次更新。';
    $('profile-file-section').hidden=activeLayer==='local';$('profile-migrate-rules').hidden=activeLayer==='local';
    $('profile-inline-title').textContent=activeLayer==='local'?'本机规则文本':'兼容内联规则';$('profile-inline-note').textContent=activeLayer==='local'?'仅追加到此副本。勾选整体替换可清空或替换上层规则。':'已有 Profile.rules 保留兼容，也可在一次源事务中转为规则文件。';
    $('profile-extends-field').hidden=activeLayer==='local';$('profile-library-field').hidden=activeLayer!=='local';
    $('profile-library-root').value=libraryRoot;
    const parents=$('profile-extends');parents.replaceChildren();const none=element('option','不追加命名 Profile');none.value='';parents.append(none);
    const names=new Set((Array.isArray(loaded.named_profiles)?namedProfiles:loaded.layers?.profiles||[]).map(p=>p.name));if(draft.extends)names.add(draft.extends);
    for(const name of names){const option=element('option',name);option.value=name;parents.append(option);}parents.value=draft.extends||'';
    $('profile-named-section').hidden=activeLayer!=='shared'||!Array.isArray(loaded.named_profiles);
    $('profile-local-history').hidden=activeLayer!=='local';
    $('profile-local-history-list').textContent=(loaded.layers?.history||[]).slice(0,5).map(localHistoryLabel).join('\n')||'还没有本机保存记录。';
    renderReport($('profile-effective'),loaded.report);
    renderSkills();
    renderMCP($('profile-mcp'),draft);
    ProfileConfiguration.render($('profile-plugin-configurations'),loaded,draft,changed,ctx.busy);
    $('profile-checks').replaceChildren();
    renderChecks($('profile-checks'),loaded.checks);
    $('profile-boundary').textContent=loaded.boundary;
    $('profile-report').textContent=JSON.stringify(loaded.report || loaded.profile,null,2);
    $('profile-draft').textContent=dirty()?'有未保存修改':'与已保存版本一致';
    renderRules(); renderFileRules(); renderNamed(); sync();
  }
  async function run(fn) {
    if(ctx.busy())return;
    ctx.setBusy(true);
    try {await fn();} catch(e) {invalidate();say(e.message,true);} finally {ctx.setBusy(false);}
  }
  async function load(replace = false) {
    const next=(await ctx.api('/api/profile/state')).items;
    if(!replace && dirty()) {if(next.sha256!==loaded.sha256 || next.rules_sha256!==loaded.rules_sha256 || next.layers?.digest!==loaded.layers?.digest || next.named_sha256!==loaded.named_sha256 || next.mcp_sha256!==loaded.mcp_sha256){invalidate();say('已保存版本或 MCP 定义发生变化；当前草稿保留。请核对后重读，再重新预览。',true);}return;}
    loaded=next;resetDraft();say('已读取保存的配置和来源链。预览确认后才会保存。');
  }
  function resetDraft(){draft=copy(activeLayer==='local'?localBase():loaded.profile);ruleFiles=copy(loaded.rule_files||[]);namedProfiles=copy(loaded.named_profiles||[]);libraryRoot=loaded.layers?.library_root||'';importOrigin=null;invalidate();render();}
  async function previewLocal(extra={}) {
    const request={target:'local',expected_sha256:loaded.layers.local_sha256,expected_layers_digest:loaded.layers.digest,expected_mcp_sha256:loaded.mcp_sha256,...extra};
    await preview(request);
  }
  async function preview(request) {
    invalidate();
    const out=(await ctx.api('/api/profile/plan',request)).items;plan=out.plan;
    $('profile-changes').replaceChildren();
    if(request.profile)appendSkillChanges(localBase(),request.profile);
    if(request.snapshot||request.clear)appendEffectiveSkillChanges(loaded.report,out.profile_report||out.preflight?.profile_report);
    if(request.snapshot)$('profile-changes').append(element('p',loaded.layers?.history?.[0]?.action?.startsWith('rollback:')?'撤销最近的本机回退，确认后仍保留恢复记录。':'恢复到最近一次本机保存之前，确认后仍可撤销。'));
    if(request.library_root!==undefined&&request.library_root!==(loaded.layers?.library_root||''))$('profile-changes').append(element('p',`全局库关联：${loaded.layers?.library_root||'无'} → ${request.library_root||'无'}`));
    if(request.clear)$('profile-changes').append(element('p','清除当前副本的配置覆盖，保留全局库关联。'));
    for(const change of plan.changes)$('profile-changes').append(element('p',`${change.before?(change.after?'修改':'移除'):'新增'} · ${change.path}`,'caption'));
    renderCandidateChecks($('profile-changes'),out.preflight?.profile_checks);
    renderReport($('profile-candidate'),out.profile_report||out.preflight?.profile_report);
    $('profile-plan-note').textContent=activeLayer==='local'?'只更新本机配置；项目共享源和工具文件保持原状。本次变化有独立回退点。':'更新共享源并保留源回退点。工具应用另行预览。';
    $('profile-plan').hidden=false;say('请核对文件变化和确认后的有效配置，再保存。');sync();
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
    $('profile-layer').addEventListener('change',()=>{if(ctx.busy())return;if(dirty()){$('profile-layer').value=activeLayer;say('当前层有未保存草稿，请先保存，或重读已保存版本后切换。',true);return;}activeLayer=$('profile-layer').value;resetDraft();});
    $('profile-library-root').addEventListener('input',()=>{libraryRoot=$('profile-library-root').value;changed();});
    $('profile-extends').addEventListener('change',()=>{if($('profile-extends').value)draft.extends=$('profile-extends').value;else delete draft.extends;changed();});
    $('profile-add-named').addEventListener('click',()=>{if(ctx.busy())return;namedProfiles.push({schema_version:2,name:`Profile ${namedProfiles.length+1}`,rules:[]});changed();renderNamed();});
    $('profile-dialog').addEventListener('cancel',e=>{e.preventDefault();close();});
    $('profile-name').addEventListener('input',()=>{draft.name=$('profile-name').value;changed();});
    $('profile-description').addEventListener('input',()=>{draft.description=$('profile-description').value;changed();});
    $('profile-rule-mode').addEventListener('change',()=>{draft.rules_override=$('profile-rule-mode').checked;changed();});
    $('profile-add-rule').addEventListener('click',()=>{if(ctx.busy()||!draft)return;(draft.rules ||= []).push('');changed();renderRules();});
    $('profile-add-rule-file').addEventListener('click',()=>{if(ctx.busy()||!draft)return;ruleFiles.push({id:'',name:`规则 ${ruleFiles.length+1}`,enabled:true,body:''});changed();renderFileRules();});
    $('profile-migrate-rules').addEventListener('click',()=>{
      if(ctx.busy()||!draft)return;
      const migrated=(draft.rules || []).map((body,i)=>({id:'',name:`迁移规则 ${i+1}`,enabled:true,body}));
      ruleFiles=[...migrated,...ruleFiles];
      draft.rules=[];changed();renderRules();renderFileRules();say('内联规则已移入文件草稿，原顺序与正文保留；预览确认后才会保存。');
    });
    $('profile-all-on').addEventListener('click',()=>batchSkills('enable',false));
    $('profile-all-off').addEventListener('click',()=>batchSkills('disable',false));
    for(const [id,value]of [['profile-filtered-on','enable'],['profile-filtered-off','disable'],['profile-filtered-inherit','inherit']])$(id).addEventListener('click',()=>batchSkills(value,true));
    $('profile-skill-search').addEventListener('input',()=>{if(!ctx.busy()&&draft)renderSkills();});
    for(const id of ['profile-skill-source','profile-skill-choice'])$(id).addEventListener('change',()=>{if(!ctx.busy()&&draft)renderSkills();});
    $('profile-skill-clear').addEventListener('click',()=>{if(ctx.busy()||!draft)return;$('profile-skill-search').value='';$('profile-skill-source').value='all';$('profile-skill-choice').value='all';renderSkills();});
    $('profile-import-mode').addEventListener('change',()=>{
      if (!importOrigin || ctx.busy()) return;
      const append=$('profile-import-mode').value === 'append', hasFiles=importOrigin.baseRuleFiles.length>0;
      draft.rules = append ? [...importOrigin.baseRules,...(hasFiles?[]:[importOrigin.body])] : [importOrigin.body];
      ruleFiles = append ? [...copy(importOrigin.baseRuleFiles),...(hasFiles?[{id:'',name:'导入手改规则',enabled:true,body:importOrigin.body}]:[])] : [];
      draft.rules_override = $('profile-import-mode').value === 'replace' ? true : importOrigin.baseRulesOverride;
      $('profile-rule-mode').checked = Boolean(draft.rules_override);
      changed();renderRules();renderFileRules();say('导入草稿已更新；可编辑合并后预览，尚未保存。');
    });
    $('profile-reload').addEventListener('click',()=>{
      if(ctx.busy())return;
      invalidate();
      if(dirty()&&!discardArmed){discardArmed=true;$('profile-reload').textContent='确认放弃 Profile 草稿并重读';say('再次点击将放弃当前 Profile 草稿；技能文件草稿保留。',true);return;}
      discardArmed=false;$('profile-reload').textContent='重读已保存版本';run(()=>load(true));
    });
    $('profile-form').addEventListener('submit',e=>{
      e.preventDefault();if(!loaded||!dirty()||ctx.pending())return;
      run(async()=>{
        if(activeLayer==='local'){await previewLocal({profile:copy(draft),library_root:libraryRoot});return;}
        invalidate();
        const out=(await ctx.api('/api/profile/plan',{expected_sha256:loaded.sha256,expected_layers_digest:loaded.layers?.digest,expected_mcp_sha256:loaded.mcp_sha256,profile:copy(draft),rule_files:copy(ruleFiles),expected_rules_sha256:loaded.rules_sha256,...(Array.isArray(loaded.named_profiles)?{named_profiles:copy(namedProfiles),expected_named_sha256:loaded.named_sha256}:{}),...(importOrigin ? {native_rule:{tool:importOrigin.tool,sha256:importOrigin.sha256,body:importOrigin.body,mode:$('profile-import-mode').value,merged_rules:copy(draft.rules || [])}} : {})})).items;
        plan=out.plan;
        $('profile-changes').replaceChildren();
        if(loaded.profile.name!==draft.name)$('profile-changes').append(element('p',`Profile 名称：${loaded.profile.name} → ${draft.name}`));
        if(loaded.profile.description!==draft.description)$('profile-changes').append(element('p',`说明：${draft.description || '（空）'}`));
        if(JSON.stringify(loaded.profile.rules||[])!==JSON.stringify(draft.rules||[]) || Boolean(loaded.profile.rules_override)!==Boolean(draft.rules_override))$('profile-changes').append(element('p',`规则：${(loaded.profile.rules||[]).length} → ${(draft.rules||[]).length} 条 · ${draft.rules_override?'整体替换上层列表':'按继承顺序追加'}`));
        if(JSON.stringify(loaded.rule_files||[])!==JSON.stringify(ruleFiles))$('profile-changes').append(element('p',`规则文件：${(loaded.rule_files||[]).length} → ${ruleFiles.length} 个 · ${ruleFiles.filter(x=>x.enabled).length} 个启用 · 按当前顺序编译`));
        appendSkillChanges(loaded.profile,draft);
        for(const item of loaded.mcp||[])if(mcpChoice(loaded.profile,item.name)!==mcpChoice(draft,item.name))$('profile-changes').append(element('p',`MCP · ${item.name}：${({inherit:'继承',enable:'明确启用',disable:'明确停用'})[mcpChoice(loaded.profile,item.name)]} → ${({inherit:'继承',enable:'明确启用',disable:'明确停用'})[mcpChoice(draft,item.name)]}`));
        for(const item of namedProfiles){const before=(loaded.named_profiles||[]).find(p=>p.name===item.name)||{},names=new Set([...(before.mcp?.enable||[]),...(before.mcp?.disable||[]),...(item.mcp?.enable||[]),...(item.mcp?.disable||[])]);for(const name of names)if(mcpChoice(before,name)!==mcpChoice(item,name))$('profile-changes').append(element('p',`可复用 Profile · ${item.name} · MCP ${name}：${({inherit:'继承',enable:'明确启用',disable:'明确停用'})[mcpChoice(before,name)]} → ${({inherit:'继承',enable:'明确启用',disable:'明确停用'})[mcpChoice(item,name)]}`));}
        for(const change of plan.changes)$('profile-changes').append(element('p',`${change.before ? (change.after ? '修改' : '移除') : '新增'} · ${change.path}`,'caption'));
        renderCandidateChecks($('profile-changes'),out.preflight?.profile_checks);
        renderReport($('profile-candidate'),out.profile_report||out.preflight?.profile_report);
        $('profile-plan-note').textContent='更新共享源并保留源回退点。工具应用另行预览。';
        $('profile-plan').hidden=false;say('请核对整批变化。确认只保存共享源，不应用到工具或其他副本。');sync();
      });
    });
    $('profile-cancel').addEventListener('click',()=>{if(ctx.busy())return;invalidate();say('已取消预览；Profile 草稿保留，源未修改。');});
    $('profile-confirm').addEventListener('click',()=>{
      if(!plan||ctx.pending())return;
      run(async()=>{
        const p=plan;invalidate();
        await ctx.api(activeLayer==='local'?'/api/profile/apply':'/api/source/apply',{plan_id:p.id,digest:p.digest,...(activeLayer==='local'?{target:'local'}:{})});
        draft=null;importOrigin=null;await ctx.refresh();await load(true);
        say(activeLayer==='local'?'本机配置已保存，有独立回退点；共享源未修改。工具应用需单独预览。':'Profile 已保存，整批变化共用一个回退点。工具应用与副本发布需单独确认。');
      });
    });
    $('profile-local-clear').addEventListener('click',()=>{if(ctx.busy()||ctx.pending())return;run(()=>previewLocal({clear:true}));});
    $('profile-local-reverse').addEventListener('click',()=>{if(ctx.busy()||ctx.pending())return;const latest=loaded?.layers?.history?.[0];if(latest?.state==='committed')run(()=>previewLocal({snapshot:latest.id}));});
    $('profile-local-recover').addEventListener('click',()=>{if(ctx.busy())return;run(async()=>{await ctx.api('/api/profile/recover-local',{});await ctx.refresh();await load(false);say('本机未完成事务已检查并恢复，当前草稿保留。');});});
    sync();
  }
  async function importRules(origin) {
    if (!ctx || ctx.busy() || ctx.pending()) return;
    if (dirty()) {ctx.message('当前 Profile 有未保存草稿，请先保存或处理；手改规则未导入，现有草稿保留。',true);return;}
    $('profile-dialog').hidden=false;if(!$('profile-dialog').open)$('profile-dialog').showModal?.();
    await run(async()=>{
      activeLayer='shared';
      await load(true);
      importOrigin={...origin,baseRules:copy(draft.rules||[]),baseRulesOverride:Boolean(draft.rules_override),baseRuleFiles:copy(ruleFiles)};
      draft.rules=[origin.body];ruleFiles=[];draft.rules_override=true;$('profile-import-mode').value='replace';
      changed();render();say('手改正文已放入导入草稿；请核对替换或追加方式、编辑合并，再预览保存。');
    });
  }
  return {init,sync,hasDraft:dirty,importRules};
})();
