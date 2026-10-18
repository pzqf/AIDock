'use strict';
const ReplicaView = (() => {
  const el=id=>document.getElementById(id);
  const node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
  const labels={SAME:'一致',BASELINE_UNKNOWN:'缺共同基线',SOURCE_CHANGED:'仅源变化',TARGET_CHANGED:'仅目标变化',CONFLICT:'双方冲突',SOURCE_DELETED:'源已移除或退役',LOCAL_EXCLUDED:'本机排除',EXTERNAL_REGENERATION_REQUIRED:'外部生成待接入',CONFIGURATION_CONTRACT_MISMATCH:'插件字段合同不匹配',CONFIGURATION_VALUE_BLOCKED:'插件共享值待协调',MCP_DEFINITION_BLOCKED:'MCP 定义待协调',MCP_DEFINITION_UNAVAILABLE:'缺少 MCP 共享定义'};
  const rehearsalLabels={APPLIED:'应用预演完成',ROLLED_BACK:'已回退预演',UNDONE:'已撤销预演回退',RECOVERED:'失败预演已恢复',PENDING:'预演中断，需恢复',READY:'预演已准备'};
  const toolLabels={'claude-code':'Claude Code','codex-cli':'Codex CLI'};
  let context,copies=[],plan=null,planCurrent=true,report=null,generation=0,rehearsal=null,externalReturns=[];
  const choices=new Map();
  const ready=()=>context&&!context.busy()&&!context.pending?.();
  function sync(){
    if(typeof ReplicaChecks!=='undefined')ReplicaChecks.sync();
    if(typeof ReplicaCandidate!=='undefined')ReplicaCandidate.sync();
    if(typeof ReplicaFullPublication!=='undefined')ReplicaFullPublication.sync();
    for(const id of ['replica-load','replica-register','replica-compare','replica-baseline','replica-recover','replica-register-current','replica-history-load'])el(id).disabled=!ready();
    el('replica-confirm-run').disabled=!ready()||!plan||!planCurrent;el('replica-cancel').disabled=Boolean(context?.busy());
    if(plan?.kind==='replica-generation'&&(plan.blocked||plan.no_changes))el('replica-confirm-run').disabled=true;
    for(const id of ['replica-generate-claude','replica-generate-codex','replica-application-scope'])el(id).disabled=!ready();
    el('replica-resolve').disabled=!ready()||!report||Boolean(rehearsal?.pending);
    el('replica-candidate-open').disabled=!ready()||!report||Boolean(rehearsal?.pending);
    el('replica-repair').disabled=!ready()||!report||Boolean(rehearsal?.pending)||!selectedTools().length;
    for(const button of document.querySelectorAll?.('.replica-session-open')||[])button.disabled=!ready();
    for(const button of document.querySelectorAll?.('.replica-member-remove')||[])button.disabled=!ready();
    for(const select of document.querySelectorAll?.('.replica-choice')||[])select.disabled=!ready();
    el('replica-rehearsal-reverse').disabled=!ready()||!rehearsal||rehearsal.pending||!rehearsal.transactions?.some(t=>t.state==='committed');
    el('replica-rehearsal-recover').disabled=!ready()||!rehearsal?.pending;
    el('replica-external-load').disabled=!ready()||!report;
    el('replica-external-output').disabled=!ready();
    el('replica-external-stage-run').disabled=!ready()||!report||Boolean(rehearsal?.pending)||!externalReturns.some(o=>o.stage.id===el('replica-external-output').value);
    for(const copy of copies){const input=el(`replica-target-${copy.id}`);if(input){const source=copy.id===el('replica-from').value;input.disabled=!ready()||source;if(source)input.checked=false;}}
  }
  function close(){el('replica-confirm').close?.();el('replica-confirm').hidden=true;plan=null;planCurrent=true;sync();}
  function invalidate(){generation++;report=null;choices.clear();el('replica-result').hidden=true;close();if(typeof ReplicaChecks!=='undefined')ReplicaChecks.invalidate();if(typeof ReplicaCandidate!=='undefined')ReplicaCandidate.invalidate();if(typeof ReplicaFullPublication!=='undefined')ReplicaFullPublication.invalidate();}
  function selected(){const from=el('replica-from').value;return{from,targets:copies.filter(b=>b.id!==from&&el(`replica-target-${b.id}`)?.checked).map(b=>b.id)};}
  function selectedTools(){return ['claude-code','codex-cli'].filter((tool,i)=>el(['replica-generate-claude','replica-generate-codex'][i]).checked);}
  const applicationScope=()=>el('replica-application-scope').value||'full';
  const scopeName=p=>p?.application_scope==='skills-rules'?'仅技能与规则':'完整 Profile 检查';
  function excludedSummary(card,target){
    if(!target.excluded_checks?.length)return;
    const detail=node('details');detail.append(node('summary',`范围外 MCP 诊断 ${target.excluded_checks.length} 项 · 保留，不代表已修复或生效`));
    for(const finding of target.excluded_checks)detail.append(node('p',`${finding.status} · ${finding.code} · ${finding.detail}`));card.append(detail);
  }
  function rootName(path){if(path==='control')return'共同基线';for(const copy of copies){if(path===`copies/${copy.id}/.aidock`)return`${copy.name} / 技能源`;for(const tool of Object.keys(toolLabels)){if(path===`copies/${copy.id}/generated/${tool}`)return`${copy.name} / ${toolLabels[tool]} 技能目录`;if(path===`copies/${copy.id}/rules/${tool}`)return`${copy.name} / ${toolLabels[tool]} 规则文件`;}}return path;}
  function ruleSummary(card,rule){
    if(!rule)return;
    card.append(node('p',`${rule.path} · ${rule.changes.length} 项规则变化 · 仅更新 AIDock 托管区块，保留手写正文与原字节格式`));
    for(const c of rule.changes)card.append(node('p',`${c.after?c.before?'更新':'新增':'移除'} · ${rule.path}`));
    const preview=node('details');preview.className='native-rule-preview';preview.append(node('summary',`${rule.path} · 查看规则正文`));
    if(rule.managed)preview.append(node('p','AIDock 托管内容'),node('pre',rule.managed));
    preview.append(node('p','手写内容 · 只读保留'),node('pre',rule.handwritten_before||'（前部无手写内容）'),node('pre',rule.handwritten_after||'（后部无手写内容）'));card.append(preview);
  }
  function renderRegistry(r){
    const previous=selected();copies=r.copies||[];
    el('replica-count').textContent=`${copies.length} 个已登记副本 · ${r.scope_id}`;
    el('replica-from').replaceChildren();el('replica-targets').replaceChildren();el('replica-copies').replaceChildren();
    for(const b of copies){
      const option=node('option',b.name);option.value=b.id;el('replica-from').append(option);
      const label=node('label');label.className='tool-check';const input=node('input');input.type='checkbox';input.id=`replica-target-${b.id}`;input.checked=previous.targets.includes(b.id);input.addEventListener('change',invalidate);label.append(input,node('span',b.name));el('replica-targets').append(label);
      const row=node('article');row.className='native-check-card';row.append(node('h3',b.name),node('code',b.root),node('small',`分支观察 ${b.branch} · ${b.id}`));
      if(b.local_excludes?.length)row.append(node('p',`本机排除 ${b.local_excludes.join('、')}`));
      const remove=node('button','移除登记');remove.type='button';remove.className='btn ghost replica-member-remove';remove.addEventListener('click',()=>{if(ready()&&typeof ProjectsView!=='undefined')ProjectsView.removeCopy(b);});row.append(remove);el('replica-copies').append(row);
    }
    if(copies.some(b=>b.id===previous.from))el('replica-from').value=previous.from;
    else{const bound=copies.find(b=>b.root.replace(/\\/g,'/').toLowerCase()===context.sourceRoot()?.replace(/\\/g,'/').toLowerCase());if(bound)el('replica-from').value=bound.id;}
    for(const b of copies){const input=el(`replica-target-${b.id}`);if(b.id===el('replica-from').value)input.checked=false;input.disabled=b.id===el('replica-from').value;}
    el('replica-selection').hidden=copies.length<2;sync();
  }
  async function load(){
    if(!ready())return;const g=++generation;context.setBusy(true);
    try{const data=await context.api('/api/replica/list');if(g===generation){invalidate();renderRegistry(data.items);const current=generation;const latest=await context.api('/api/replica/rehearsal/latest');if(current===generation)renderRehearsal(latest.items);}}
    catch(e){if(g===generation)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  function show(p){
    plan=p;el('replica-confirm-title').textContent=p.action==='replica-register'?'确认登记项目副本':'确认建立共同基线';
    el('replica-confirm-scope').textContent='仅本机副本管理数据';el('replica-confirm-run').textContent='确认保存本机记录';
    el('replica-confirm-summary').textContent=p.action==='replica-register'?'只在本机记录此副本的身份、路径与排除策略。不会修改所选项目。':'只记录当前两端已经一致且尚无基线的完整条目。差异条目不会自动选边或传播。';
    el('replica-confirm-changes').replaceChildren();for(const c of p.changes)el('replica-confirm-changes').append(node('p',`${c.after?c.before?'更新':'新增':'移除'} · ${c.path}`));
    if(p.inputs?.binding_root)el('replica-confirm-changes').append(node('code',p.inputs.binding_root));
    el('replica-confirm').hidden=false;el('replica-confirm').showModal?.();el('replica-confirm').scrollTop=0;sync();
  }
  async function register(current=false){
    if(!ready())return;const name=el('replica-name').value.trim()||(current?'当前项目':'');const root=current?context.sourceRoot():el('replica-root').value.trim();
    if(!name||!root){context.message('填写副本名称与明确的 .aidock 源目录。',true);return;}
    const g=++generation;context.setBusy(true);
    try{const data=await context.api('/api/replica/register',{name,root,local_excludes:el('replica-excludes').value.split(/[,，\n]/).map(x=>x.trim()).filter(Boolean)});if(g===generation)show(data.items);}
    catch(e){if(g===generation)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  function renderReport(r){
    report=r;choices.clear();el('replica-result').hidden=false;el('replica-report-title').textContent=`${r.from.name} → ${r.targets.length} 个目标`;
    el('replica-boundary').textContent=r.boundary;el('replica-comparisons').replaceChildren();
    for(const note of r.configuration_notes||[])el('replica-comparisons').append(node('p',`源端保留项 · ${note}`));
    for(const t of r.targets){
      const card=node('article');card.className='native-check-card';card.append(node('h3',t.copy.name),node('small',t.copy.root));
      if(t.status!=='READY')card.append(node('p',`${t.status}：${t.reason}`));
      for(const note of t.configuration_notes||[])card.append(node('p',`目标保留项 · ${note}`));
      for(const d of [...t.entries,...(t.configurations||[]).map(v=>({...v,configuration:true}))]){
        const row=node('details');row.className='replica-difference';row.append(node('summary',`${d.name} · ${labels[d.status]||d.status}`),node('p',d.reason));
        for(const [label,item]of[['源',d.source],['目标',d.target],['共同基线',d.base]]){
          if(d.configuration){
            const value=node('details');value.append(node('summary',`${label}：${item?item.digest:'无共同记录'}`));
            if(item){if(item.plugin)value.append(node('p',`共享字段：${item.plugin.fields.join('、')}；未设置的共享字段将移除本层覆盖。`),node('small',`清单 ${item.plugin.manifest_sha256} · 产物 ${item.plugin.artifact_sha256}`));if(item.blocked_code)value.append(node('p',`此组阻断：${item.blocked_code}，未展示配置正文。`));if(item.mcp){if(item.mcp.definition)value.append(node('pre',JSON.stringify(item.mcp.definition,null,2)));const lock=item.mcp.lock;if(lock){value.append(node('p',`精确版本 ${lock.version} · ${lock.runtime.kind} ${lock.runtime.version} · ${lock.artifact.entries.length} 项产物 · ${lock.artifact.sha256}`));const detail=node('details');detail.append(node('summary','查看精确锁定清单与依赖'),node('pre',JSON.stringify(lock,null,2)));value.append(detail);}}else value.append(node('pre',JSON.stringify(item.profile,null,2)));for(const [i,rule]of(item.rules||[]).entries())value.append(node('p',`${i+1}. ${rule.name} · ${rule.enabled?'启用':'停用'} · ${rule.id}`),node('pre',rule.body));}
            row.append(value);
          }else row.append(node('p',`${label}：${item?`${item.retired?'已退役':item.enabled?'已启用':'已停用'} · ${item.revision}`:'无记录'}`));
        }
        if(d.configuration)row.append(node('p',d.id==='rules/shared'?'采用源将同步此组内的内联规则、替换策略、已登记文件正文、顺序和启停；包含明确删除。未登记文件保留。':d.source?.mcp||d.target?.mcp?'采用明确的 MCP 定义与精确版本锁；目标保留自己的产物映射或准备独立版本目录，不复制源机器路径、安装、授权、凭据或会话。未来配置仍须目标独立审查，保存不代表已重新应用。':d.source?.plugin||d.target?.plugin?'只采用清单明确声明的共享字段及其移除。原始变量由每个目标独立展开；未声明字段、本机覆盖、安装、授权和已有 MCP 连接保留。保存共享配置不等于插件已重新应用。':'只同步名称、说明、继承引用及插件/MCP选择；技能决策和未登记配置保留。本机覆盖仍以目标自己的配置为准。'));
        if(d.changed_fields?.length)row.append(node('p',`配置字段变化：${d.changed_fields.join('、')}`));
        if(d.local_overrides?.length)row.append(node('p',`即使共享版本更新，这些字段的有效值仍由目标本机覆盖决定：${d.local_overrides.join('、')}。`));
        for(const c of d.package_changes||[])row.append(node('p',`${c.path} · ${c.before?.bytes||0} → ${c.after?.bytes||0} B`));
        card.append(row);
        if(d.status!=='SAME'){
          const label=node('label',`${d.name} 的处理`);label.className='replica-choice-label';const select=node('select');select.className='replica-choice';select.setAttribute?.('aria-label',`${t.copy.name} / ${d.name} 的处理`);
          const definitionAvailable=d.status==='CONFIGURATION_CONTRACT_MISMATCH'&&t.configurations?.some(v=>v.id==='mcp-definition/'+d.source?.plugin?.name&&v.source?.mcp&&!v.source.blocked_code);
          if(definitionAvailable)row.append(node('p','采用这些字段还需同批明确采用对应 MCP 定义，并在目标备齐精确产物；服务端会重新核对字段合同。'));
          const blocked=t.status!=='READY'||['LOCAL_EXCLUDED','EXTERNAL_REGENERATION_REQUIRED','CONFIGURATION_VALUE_BLOCKED','MCP_DEFINITION_BLOCKED','MCP_DEFINITION_UNAVAILABLE'].includes(d.status)||d.status==='CONFIGURATION_CONTRACT_MISMATCH'&&!definitionAvailable;
          for(const [value,text]of blocked?[['skip','跳过（当前边界不允许传播）']]:[['','请选择处理方式'],['source',d.configuration?(d.source?.mcp?'采用源：定义与精确版本锁':'采用源：共享配置组（含移除）'):d.source?.retired?'采用源：传播退役并保留完整包':'采用源：完整包与启停'],['target','保留目标：不修改发布者'],['skip','跳过本次']]){const option=node('option',text);option.value=value;select.append(option);}
          select.value=blocked?'skip':'';choices.set(`${t.copy.id}/${d.id}`,{target:t.copy.id,entry:d.id,choice:select.value});
          select.addEventListener('change',()=>{generation++;close();if(typeof ReplicaChecks!=='undefined')ReplicaChecks.invalidate();if(typeof ReplicaCandidate!=='undefined')ReplicaCandidate.invalidate();if(typeof ReplicaFullPublication!=='undefined')ReplicaFullPublication.invalidate();choices.set(`${t.copy.id}/${d.id}`,{target:t.copy.id,entry:d.id,choice:select.value});});label.append(select);card.append(label);
        }
      }
      el('replica-comparisons').append(card);
    }
    sync();
  }
  async function compare(baseline=false){
    if(!ready())return;const req=selected();if(!req.from||!req.targets.length){context.message('请选择源副本与至少一个目标副本。',true);return;}
    const g=++generation;context.setBusy(true);
    try{const data=await context.api(baseline?'/api/replica/baseline':'/api/replica/compare',req);if(g===generation){if(baseline)show(data.items);else{renderReport(data.items);context.message('三方比较完成；项目内容未修改，缺基线与冲突均需显式处理。');}}}
    catch(e){if(g===generation){el('replica-result').hidden=true;context.message(e.message,true);}}finally{context.setBusy(false);sync();}
  }
  async function prepareCandidate(){
    if(!ready()||!report||rehearsal?.pending||typeof ReplicaCandidate==='undefined')return;
    const decisions=Array.from(choices.values());if(decisions.some(d=>!d.choice)){context.message('每条差异都需明确选择采用源、保留目标或跳过。',true);return;}
    const req={...selected(),comparison_digest:report.digest,decisions},g=++generation;let prepared=null;close();context.setBusy(true);
    try{const data=await context.api('/api/replica/resolve',req);if(g===generation)prepared=data.items;}
    catch(e){if(g===generation)context.message(e.message,true);}finally{context.setBusy(false);sync();}
    if(prepared&&g===generation)await ReplicaCandidate.open(prepared);
  }
  async function confirm(){
    if(!ready()||!plan||!planCurrent||plan.blocked||plan.no_changes)return;const p=plan,g=++generation;close();context.setBusy(true);
    try{
      if(p.kind==='replica-resolution'){
        const data=await context.api('/api/replica/rehearsal',{plan_id:p.id,digest:p.digest});if(g===generation){renderRehearsal(data.items);context.message('独立副本预演完成，可回退和撤销；已登记项目与真实基线未修改。');}
      }else if(p.kind==='replica-generation'){
        const data=await context.api('/api/replica/rehearsal/generated',{plan_id:p.id,digest:p.digest});if(g===generation){renderRehearsal(data.items);context.message(p.repair_only?'纯生成物修复已在独立副本预演；各目标自己的源和共同基线保留，可回退。':'源、工具生成包与共同基线已在独立副本联合预演，可一起回退。');}
      }else if(['native-targets','coordinated-roots'].includes(p.kind)){
        const data=await context.api('/api/replica/rehearsal/apply',{session:rehearsal.id,plan_id:p.id,digest:p.digest});if(g===generation){renderRehearsal(data.items);context.message('副本预演回退/撤销已完成。');}
      }else{
        await context.api('/api/replica/confirm',{plan_id:p.id,digest:p.digest});if(g===generation){context.message('本机登记/共同基线已保存，项目内容未修改。');const data=await context.api('/api/replica/list');if(g===generation)renderRegistry(data.items);}
      }
    }
    catch(e){if(g===generation){context.message(e.message,true);if(['replica-resolution','replica-generation','native-targets','coordinated-roots'].includes(p.kind)){try{const latest=await context.api('/api/replica/rehearsal/latest');if(g===generation)renderRehearsal(latest.items);}catch{}}}}finally{context.setBusy(false);sync();}
  }
  function showCandidate(p,scope=null){
    if(typeof ReplicaChecks!=='undefined')ReplicaChecks.setCandidate(p,scope);
    plan=p;planCurrent=scope?.current!==false;const reverse=['native-targets','coordinated-roots'].includes(p.kind);
    el('replica-confirm-scope').textContent='仅独立预演副本';el('replica-confirm-title').textContent=reverse?(p.action==='rollback'?'确认回退副本预演':'确认撤销副本预演回退'):'确认预演所选处理方案';
    el('replica-confirm-summary').textContent=reverse?'在独立副本联合恢复技能源、工具生成物与共同基线，保留已登记项目。':p.boundary;
    el('replica-confirm-run').textContent=reverse?'确认执行预演回退/撤销':'确认在独立副本预演';el('replica-confirm-changes').replaceChildren();
    if(p.kind==='replica-generation'){
      el('replica-confirm-scope').textContent=`仅独立预演副本 · 工具生成范围：${scopeName(p)}`;
      el('replica-confirm-title').textContent=p.blocked?'生成检查有阻断项':p.no_changes?'所选生成物无需修复':p.repair_only?'确认纯生成物修复预演':'确认源、生成物与基线联合预演';
      el('replica-confirm-run').textContent=p.blocked?'解决阻断项后重新预览':p.no_changes?'无需建立预演事务':p.repair_only?'确认独立修复预演':'确认联合预演';
      const ruleCount=p.targets.reduce((n,t)=>n+t.outputs.filter(o=>o.rule).length,0),rulesPending=p.blocked&&p.targets.some(t=>t.outputs.some(o=>o.status==='FAIL'&&!o.rule));
      const ruleCountText=rulesPending?`${ruleCount} 个已编译规则文件，其余等待解决阻断后编译`:`${ruleCount} 个规则文件`;
      el('replica-confirm-changes').append(node('p',`${p.targets.length} 个目标副本 · ${p.targets.reduce((n,t)=>n+t.outputs.length,0)} 个工具生成目录 · ${ruleCountText} · ${p.repair_only?'按各目标自己已保存的源生成，不处理源差异':'按各目标候选中的启用技能生成'}；本机排除项保留`));
      if(p.blocked&&!p.application_scope)el('replica-confirm-changes').append(node('p','如仅处理技能与规则，可在工具生成范围选择“仅技能与规则”后重新预览；技能、规则和一般 Profile 阻断仍需解决，MCP 连接由独立流程处理。'));
      if(p.no_changes&&!p.blocked)el('replica-confirm-changes').append(node('p','生成目录已一致，或所选技能均被本机策略排除。查看下面的逐项诊断；不会建立空预演记录。'));
    }
    if(scope){
      el('replica-confirm-changes').append(node('p',`历史候选 ${p.id} · 摘要 ${p.digest}`),node('p',`原源副本：${scope.source.name} · ${scope.source.root}`));
      if(!planCurrent){el('replica-confirm-title').textContent='原候选已过期 · 只读查看';el('replica-confirm-run').textContent='请重新比较并生成候选';el('replica-confirm-changes').append(node('p',scope.reason));}
      if(scope.check.stale)el('replica-confirm-changes').append(node('p',`历史检查已过期：${scope.check.stale_reason}；原通过结论不能视为当前有效证据。`));
    }
    for(const decision of p.request?.decisions||[]){
      const target=scope?null:report?.targets.find(t=>t.copy.id===decision.target);
      const entry=[...(target?.entries||[]),...(target?.configurations||[])].find(e=>e.id===decision.entry);
      const name=scope?.targets.find(t=>t.id===decision.target)?.name||target?.copy.name||decision.target;
      const entryName=scope?({'profile/shared':'共享 Profile','rules/shared':'共享规则与有序规则库'}[decision.entry]||decision.entry):entry?.name||decision.entry;
      el('replica-confirm-changes').append(node('p',`${name} / ${entryName}：${{source:'采用源',target:'保留目标',skip:'跳过'}[decision.choice]}`));
    }
    for(const target of p.targets||[]){
      if(target.outputs){
        const name=(scope?.targets||copies).find(c=>c.id===target.copy_id)?.name||target.copy_id;
        const heading=node('p',`${name} · 候选源摘要 ${target.source_after}`);el('replica-confirm-changes').append(heading);
        excludedSummary(el('replica-confirm-changes'),target);
        if(target.preserved)el('replica-confirm-changes').append(node('p',`本机排除保护：${target.preserved.all?'全部技能':(target.preserved.names?.length?target.preserved.names:target.preserved.ids||[]).join('、')}；现有工具包和归属记录保留，不修复其人工改动。`));
        const source=node('details');source.append(node('summary',`${name} / 技能源 · ${target.source_changes?.length||0} 项源变化`));for(const c of target.source_changes||[])source.append(node('p',`${c.after?c.before?'更新':'新增':'移除'} · ${c.path}`));el('replica-confirm-changes').append(source);
        for(const output of target.outputs){const changes=output.changes.length+(output.rule?.changes.length||0);const card=node('details');card.append(node('summary',`${name} / ${output.adapter.id} · ${changes} 项生成变化 · ${!planCurrent?'历史候选，只读查看':output.status==='FAIL'?'阻断':changes?'可预演':'无生成变化'}`));
          card.append(node('p',`原始目录只读观察：${output.root}`));
          ruleSummary(card,output.rule);
          for(const pkg of output.packages||[])card.append(node('p',`${pkg.native_name} · ${pkg.revision} · ${Object.keys(pkg.tree.entries).length} 项文件/目录`));
          for(const finding of output.findings||[])card.append(node('p',`${finding.status} · ${finding.detail}`));
          for(const c of output.changes)card.append(node('p',`${c.after?c.before?'更新':'新增':'移除'} · ${c.path}`));el('replica-confirm-changes').append(card);
        }continue;
      }
      const before=target.before?.entries||{},after=target.after?.entries||{};
      const changes=target.changes||Array.from(new Set([...Object.keys(before),...Object.keys(after)])).sort().filter(path=>JSON.stringify(before[path])!==JSON.stringify(after[path])).map(path=>({path,before:before[path],after:after[path]}));
      const name=target.copy?.name||rootName(target.path);
      const card=node('details');card.append(node('summary',`${name} · ${changes.length} 项变化`));
      if(target.preflight)card.append(node('p',`候选静态检查 ${target.preflight.status}；会话生效另行检查`));
      for(const c of changes)card.append(node('p',`${c.after?c.before?'更新':'新增':'移除'} · ${c.path}`));el('replica-confirm-changes').append(card);
    }
    el('replica-confirm-changes').append(node('code',p.digest));el('replica-confirm').hidden=false;el('replica-confirm').showModal?.();el('replica-confirm').scrollTop=0;sync();
  }
  async function resolve(){
    if(!ready()||!report||rehearsal?.pending)return;const decisions=Array.from(choices.values());if(decisions.some(d=>!d.choice)){context.message('每条差异都需明确选择采用源、保留目标或跳过。',true);return;}
    const req={...selected(),comparison_digest:report.digest,decisions},g=++generation;context.setBusy(true);
    try{const data=await context.api('/api/replica/resolve',req);if(g===generation){const tools=selectedTools();if(tools.length){const staged=await context.api('/api/replica/generation',{plan_id:data.items.id,digest:data.items.digest,tools,application_scope:applicationScope()});if(g===generation)showCandidate(staged.items);}else showCandidate(data.items);}}
    catch(e){if(g===generation)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  async function repair(){
    if(!ready()||!report||rehearsal?.pending)return;const tools=selectedTools();if(!tools.length){context.message('先选择需要检查和修复的工具。',true);return;}
    const g=++generation,req={...selected(),comparison_digest:report.digest,tools,application_scope:applicationScope()};context.setBusy(true);
    try{const data=await context.api('/api/replica/generation/repair',req);if(g===generation)showCandidate(data.items);}
    catch(e){if(g===generation)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  async function loadExternal(){
    if(!ready()||!report)return;const g=++generation;close();externalReturns=[];context.setBusy(true);
    const select=el('replica-external-output');select.replaceChildren();const loading=node('option','正在读取可用返回包…');loading.value='';select.append(loading);select.value='';
    el('replica-external-stage-note').textContent='正在重新核对返回包，请稍候。';
    try{const data=await context.api('/api/generator/history');if(g!==generation)return;
      externalReturns=(data.items||[]).filter(o=>o.state==='STAGED'&&!o.stale&&o.cleanup_safe&&o.validation?.status==='PASS'&&!o.validation.stale);
      select.replaceChildren();const empty=node('option',externalReturns.length?'选择已校验返回包':'当前没有可用返回包');empty.value='';select.append(empty);select.value='';
      for(const o of externalReturns){const p=o.stage.preview;const option=node('option',`${p.contract.id} · ${p.source.packages.length} 个技能 · ${p.contract.tools.map(t=>toolLabels[t]||t).join('、')} · ${new Date(o.stage.created).toLocaleString()}`);option.value=o.stage.id;select.append(option);}
      el('replica-external-stage-note').textContent=externalReturns.length?`${externalReturns.length} 个当前可用返回包。先逐项选择差异处理；预览时再次核对完整输入和返回内容。`:'请先到 AI 工具校验当前暂存的返回包，再读取记录。历史和已过期的返回包不可用于新预演。';
      context.message('可用返回包已更新，项目内容未修改。');
    }catch(e){if(g===generation){select.replaceChildren();const failed=node('option','读取失败，请重试');failed.value='';select.append(failed);select.value='';el('replica-external-stage-note').textContent='未能读取返回包，请重试。';context.message(e.message,true);}}finally{context.setBusy(false);sync();}
  }
  async function externalStage(){
    if(!ready()||!report||rehearsal?.pending)return;
    const returned=externalReturns.find(o=>o.stage.id===el('replica-external-output').value);if(!returned)return;
    const decisions=Array.from(choices.values());if(decisions.some(d=>!d.choice)){context.message('每条差异都需明确选择采用源、保留目标或跳过。',true);return;}
    const req={...selected(),comparison_digest:report.digest,decisions},g=++generation;close();context.setBusy(true);
    try{const resolved=await context.api('/api/replica/resolve',req);if(g!==generation)return;
      const data=await context.api('/api/replica/generation/external',{plan_id:resolved.items.id,digest:resolved.items.digest,stage_id:returned.stage.id,stage_digest:returned.stage.digest});
      if(g===generation){showCandidate(data.items);context.message('外部返回包联合方案已准备；确认后仅在独立副本预演。');}
    }catch(e){if(g===generation)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  function renderRehearsal(r){
    rehearsal=r;el('replica-rehearsal').hidden=!r;if(!r){sync();return;}
    el('replica-rehearsal-status').textContent=(r.generation?.repair_only?'纯生成物修复 · ':'')+(rehearsalLabels[r.status]||r.status);el('replica-rehearsal-boundary').textContent=[r.generation?`工具生成范围：${scopeName(r.generation)}。`:'',r.boundary,r.warning].filter(Boolean).join(' ');el('replica-rehearsal-root').textContent=r.root;
	  el('replica-rehearsal-bindings').hidden=!r.bound_roots?.length;el('replica-rehearsal-roots').replaceChildren();
	  for(const root of r.bound_roots||[]){const row=node('p',`${rootName(root.path)} · `);row.append(node('code',root.root));el('replica-rehearsal-roots').append(row);}
    el('replica-generation-manifest').hidden=!r.generation;el('replica-generation-packages').replaceChildren();
    for(const target of r.generation?.targets||[]){
      const name=copies.find(c=>c.id===target.copy_id)?.name||target.copy_id,card=node('article');card.className='native-check-card';card.append(node('h4',name));
      excludedSummary(card,target);
      if(target.preserved)card.append(node('p',`本机排除保留：${target.preserved.all?'全部技能':(target.preserved.names?.length?target.preserved.names:target.preserved.ids||[]).join('、')}`));
      for(const output of target.outputs){
        const root=r.bound_roots?.find(b=>b.path===`copies/${target.copy_id}/generated/${output.adapter.id}`);
        card.append(node('p',`${toolLabels[output.adapter.id]||output.adapter.id} · 计划生成 ${output.packages.length} 个包`));
        for(const pkg of output.packages||[]){const entries=Object.values(pkg.tree.entries);card.append(node('p',`${pkg.native_name} · ${entries.filter(e=>!e.directory).length} 个文件 / ${entries.filter(e=>e.directory).length} 个目录`));}
        ruleSummary(card,output.rule);
        const detail=node('details');detail.append(node('summary',`${toolLabels[output.adapter.id]||output.adapter.id} 版本与目录映射`),node('p',`候选源摘要 ${target.source_after}`),node('p',`原始目录（只读） ${output.root}`),node('p',`独立生成目录 ${root?.root||'见恢复范围'}`));
        if(output.rule){const ruleRoot=r.bound_roots?.find(b=>b.path===`copies/${target.copy_id}/rules/${output.adapter.id}`);detail.append(node('p',`原始规则（只读） ${output.rule.path}`),node('p',`独立规则文件 ${ruleRoot?.root||'见恢复范围'}`));}
        for(const pkg of output.packages||[])detail.append(node('p',`${pkg.native_name} · ${pkg.revision}`));card.append(detail);
      }
      el('replica-generation-packages').append(card);
    }
    const current=r.transactions?.find(t=>t.state==='committed');el('replica-rehearsal-reverse').textContent=current?.action==='rollback'?'预览撤销预演回退':'预览回退副本预演';
    el('replica-rehearsal-history').replaceChildren();for(const t of r.transactions||[])el('replica-rehearsal-history').append(node('p',`${{apply:'应用',rollback:'回退','undo-rollback':'撤销回退'}[t.action]||t.action} · ${{committed:'已完成',prepared:'待恢复',recovered:'已恢复'}[t.state]||t.state} · ${t.targets} 个副本${t.generated_roots?`、${t.generated_roots} 个生成目录`:''}${t.rule_files?`、${t.rule_files} 个规则文件`:''} · ${t.changes} 项变化 · ${t.id}`));sync();
  }
  async function reverse(){
    if(!ready()||!rehearsal||rehearsal.pending)return;const transaction=rehearsal.transactions.find(t=>t.state==='committed');if(!transaction)return;
    const g=++generation;context.setBusy(true);try{const data=await context.api('/api/replica/rehearsal/plan',{session:rehearsal.id,transaction:transaction.id});if(g===generation)showCandidate(data.items);}catch(e){if(g===generation)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  async function recoverRehearsal(){if(!ready()||!rehearsal)return;context.setBusy(true);try{const data=await context.api('/api/replica/rehearsal/recover',{session:rehearsal.id});renderRehearsal(data.items);context.message('中断的副本预演已恢复。');}catch(e){context.message(e.message,true);}finally{context.setBusy(false);sync();}}
  async function history(){
    if(!ready())return;const g=++generation;context.setBusy(true);close();
    try{const data=await context.api('/api/replica/rehearsal/history');if(g===generation){el('replica-sessions').replaceChildren();
      if(!data.items.length)el('replica-sessions').append(node('p','还没有副本预演记录。'));
      for(const session of data.items){const row=node('article');row.className='native-check-card';row.append(node('p',`${new Date(session.created).toLocaleString()} · ${session.pending?'待恢复':rehearsalLabels[session.status]||session.status} · ${session.transactions.length} 条操作`),node('code',session.id));const open=node('button','打开此预演');open.className='btn replica-session-open';open.type='button';open.addEventListener('click',()=>openSession(session.id));row.append(open);el('replica-sessions').append(row);}
    }}catch(e){if(g===generation)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  async function openSession(session){
    if(!ready())return;const g=++generation;context.setBusy(true);close();
    try{const data=await context.api('/api/replica/rehearsal/show',{session});if(g===generation){renderRehearsal(data.items);context.message('已打开所选独立副本预演记录。');}}
    catch(e){if(g===generation)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  async function recover(){if(!ready())return;context.setBusy(true);try{const data=await context.api('/api/replica/recover',{});invalidate();context.message(`已恢复 ${data.items} 个本机副本管理事务。`);}catch(e){context.message(e.message,true);}finally{context.setBusy(false);sync();}}
  el('replica-load').addEventListener('click',load);el('replica-register').addEventListener('click',()=>register());el('replica-register-current').addEventListener('click',()=>register(true));
  el('replica-compare').addEventListener('click',()=>compare());el('replica-baseline').addEventListener('click',()=>compare(true));el('replica-confirm-run').addEventListener('click',confirm);el('replica-cancel').addEventListener('click',()=>{if(ready())close();});el('replica-recover').addEventListener('click',recover);
  el('replica-confirm').addEventListener('cancel',e=>{e.preventDefault();if(ready())close();});
  el('replica-resolve').addEventListener('click',resolve);el('replica-rehearsal-reverse').addEventListener('click',reverse);el('replica-rehearsal-recover').addEventListener('click',recoverRehearsal);
  el('replica-candidate-open').addEventListener('click',prepareCandidate);
  el('replica-repair').addEventListener('click',repair);
  el('replica-external-stage-run').addEventListener('click',externalStage);
  el('replica-external-load').addEventListener('click',loadExternal);
  el('replica-external-output').addEventListener('change',()=>{generation++;close();if(typeof ReplicaChecks!=='undefined')ReplicaChecks.invalidate();sync();});
  el('replica-history-load').addEventListener('click',history);
  for(const id of ['replica-generate-claude','replica-generate-codex','replica-application-scope'])el(id).addEventListener('change',()=>{generation++;close();if(typeof ReplicaChecks!=='undefined')ReplicaChecks.invalidate();});
  el('replica-from').addEventListener('change',()=>{invalidate();for(const b of copies){const input=el(`replica-target-${b.id}`);input.disabled=b.id===el('replica-from').value;if(input.disabled)input.checked=false;}});
  return{init(ctx,app){context=ctx;if(typeof ReplicaFullPublication!=='undefined')ReplicaFullPublication.init(ctx);if(typeof ReplicaCandidate!=='undefined')ReplicaCandidate.init(ctx);if(typeof ReplicaChecks!=='undefined')ReplicaChecks.init(ctx,{preview:showCandidate,previewAllowed:()=>!rehearsal?.pending});el('nav-replica').hidden=Boolean(app.global_root)||!(ctx.sourceRoot()||app.source_root);sync();},sync,invalidate,load};
})();
