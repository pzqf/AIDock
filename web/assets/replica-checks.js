'use strict';
const ReplicaChecks = (() => {
  const el=id=>document.getElementById(id);
  const node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
  const toolNames={'claude-code':'Claude Code','codex-cli':'Codex CLI'};
  const scopeName=p=>p?.application_scope==='skills-rules'?'仅技能与规则':'完整 Profile 检查';
  const states={PASS:'通过',FAIL:'阻断',UNKNOWN:'未验证',UNAVAILABLE:'尚未开放',SKIPPED:'无需变化',NOT_APPLIED:'待应用'};
  let context,candidate=null,candidateScope=null,evidence=null,epoch=0,preview=null,previewAllowed=()=>true;
  const ready=()=>context&&!context.busy()&&!context.pending?.();
  const current=()=>candidate&&candidateScope?.current!==false;
  function sync(){
    if(typeof ReplicaPublication!=='undefined')ReplicaPublication.sync();else el('replica-check-publish').disabled=true;
    el('replica-check-preview').disabled=!ready()||!candidate||!preview||!previewAllowed();
    el('replica-check-run').disabled=!ready()||!current();
    el('replica-check-probe').disabled=!ready()||!current()||candidate.blocked;
    el('replica-check-reopen').disabled=!ready()||!evidence||!previewAllowed();
    el('replica-check-history-load').disabled=!ready();
    el('replica-check-probe-run').disabled=!ready()||!current()||candidate.blocked;
    el('replica-check-probe-cancel').disabled=Boolean(context?.busy());
    el('replica-check-rules').disabled=!ready()||!current()||candidate.blocked;
    for(const button of document.querySelectorAll?.('.replica-check-open')||[])button.disabled=!ready();
  }
  function cancelProbe(){el('replica-check-probe-dialog').close?.();el('replica-check-probe-dialog').hidden=true;}
  function invalidate(){if(typeof ReplicaPublication!=='undefined')ReplicaPublication.invalidate();epoch++;candidate=null;candidateScope=null;evidence=null;cancelProbe();el('replica-check-candidate').textContent='先选择工具并预览处理方案或生成物修复，或从预检历史恢复原候选。';el('replica-check-results').hidden=true;sync();}
  function setCandidate(p,scope=null){
    if(p?.kind!=='replica-generation')return;
    const same=candidate?.id===p.id&&candidate.digest===p.digest;
    if(!same){epoch++;evidence=null;cancelProbe();el('replica-check-results').hidden=true;}
    candidate=p;candidateScope=scope;if(typeof ReplicaPublication!=='undefined')ReplicaPublication.setEvidence(p,evidence,scope?.current!==false);
    el('replica-check-candidate').textContent=`工具生成范围：${scopeName(p)} · ${scope?.current===false?'已过期 · 只读查看':'已绑定'} ${p.targets.length} 个目标的${p.repair_only?'生成物修复':'源与生成物'}候选 · ${(p.tools||[]).map(t=>toolNames[t]||t).join('、')} · ${p.id} · 摘要 ${p.digest}；${scope?.current===false?'重新比较并生成后才能检查或预演。':'检查不应用此候选。'}`;
    sync();
  }
  function render(r){
    evidence=r;if(typeof ReplicaPublication!=='undefined')ReplicaPublication.setEvidence(candidate,r,candidateScope?.current!==false);
    el('replica-check-results').hidden=false;
    el('replica-check-summary').textContent=`${scopeName(r)} · ${r.stale?'已过期 · 历史 · ':''}${r.rule_probe?'显式发现与规则来源验证':r.probe?'显式发现验证':'静态检查'} · 候选${states[r.candidate_status]||r.candidate_status} · ${r.application_scope==='skills-rules'?'可另行预览正式同步':'完整 Profile 正式发布尚未开放'}`;
    el('replica-check-boundary').textContent=[r.stale_reason,r.boundary].filter(Boolean).join(' ');
    const bound=candidate?.id===r.generation_id&&candidate.digest===r.generation_digest;
    if(bound&&candidateScope)candidateScope={...candidateScope,check:r};
    el('replica-check-identity').textContent=`检查 ${r.id} · 有效期至 ${new Date(r.expires).toLocaleString()} · 候选 ${r.generation_id} · 摘要 ${r.generation_digest} · ${bound?'对应当前候选':'仅查看历史；恢复后才能继续检查此候选'}`;
    el('replica-check-matrix').replaceChildren();
    for(const t of r.targets||[]){
      const card=node('article');card.className='native-check-card';card.append(node('h4',t.copy.name),node('code',t.copy.root));
      if(t.inspection.excluded_checks?.length){const excluded=node('details');excluded.append(node('summary',`范围外 MCP 诊断 ${t.inspection.excluded_checks.length} 项 · 保留，不代表已修复或生效`));for(const f of t.inspection.excluded_checks)excluded.append(node('p',`${f.status} · ${f.code} · ${f.detail}`));card.append(excluded);}
      if(t.preserved)card.append(node('p',`本机排除项保留，不在发现检查范围：${t.preserved.all?'全部技能':(t.preserved.names?.length?t.preserved.names:t.preserved.ids||[]).join('、')}`));
      for(const tool of t.inspection.tools||[]){
        card.append(node('p',`${toolNames[tool.tool]||tool.tool} · ${tool.version||'版本未验证'} · 文件 ${states[tool.L1]||tool.L1} · 隔离发现 ${r.stale?'历史 ':''}${tool.L2==='SKIPPED'?'无候选包，未运行发现':states[tool.L2]||tool.L2} · 新会话/能力未验证`));
        for(const skill of tool.skills||[])card.append(node('p',`${skill.native_name} · ${r.stale?'历史 ':''}${states[skill.status]||skill.status} · ${skill.detail}`));
        if(tool.rules)card.append(node('p',`${tool.rules.path} · 规则来源 ${r.stale?'历史 ':''}${states[tool.rules.status]||tool.rules.status} · ${tool.rules.detail}`));
        const detail=node('details');detail.append(node('summary',`${toolNames[tool.tool]||tool.tool} 检查详情`),node('p',`原始目标 ${tool.target}`),node('p',`候选源摘要 ${t.candidate_source}`));
        for(const f of tool.findings||[])detail.append(node('p',`${states[f.status]||f.status} · ${f.detail}`));
        card.append(detail);
      }
      el('replica-check-matrix').append(card);
    }
    el('replica-check-gates').replaceChildren();
    for(const gate of r.gates||[]){
      const copy=r.targets?.find(t=>t.copy.id===gate.copy_id)?.copy.name;
      el('replica-check-gates').append(node('p',`${copy?copy+' / ':''}${gate.tool?(toolNames[gate.tool]||gate.tool)+' · ':''}${r.stale?'历史 ':''}${states[gate.status]||gate.status} · ${gate.detail}`));
    }
    sync();
  }
  async function run(probe=false,ruleProbe=false){
    if(!ready()||!current()||probe&&candidate.blocked)return;
    const p=candidate,g=++epoch;cancelProbe();context.setBusy(true);
    context.message(probe?`正在逐目标验证候选发现${ruleProbe?'与规则来源':''}（${p.targets.length} 个目标），各工具有超时限制。`:'正在检查候选与发布门槛。');
    try{const data=await context.api('/api/replica/generation/check',{plan_id:p.id,digest:p.digest,probe,...(probe&&ruleProbe?{rule_probe:true}:{})});if(g===epoch){render(data.items);context.message('候选检查已保存；查看逐副本结果与发布门槛。登记项目未修改。');}}
    catch(e){if(g===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  function beginProbe(){
    if(!ready()||!current()||candidate.blocked)return;
    el('replica-check-probe-scope').textContent=`工具生成范围：${scopeName(candidate)} · ${candidate.targets.length} 个目标副本 · ${(candidate.tools||[]).map(t=>toolNames[t]||t).join('、')}`;
    el('replica-check-rules').checked=false;
    el('replica-check-probe-dialog').hidden=false;el('replica-check-probe-dialog').showModal?.();sync();
  }
  async function open(id){
    if(!ready())return;const g=++epoch;context.setBusy(true);
    try{const data=await context.api('/api/replica/generation/check/show',{check_id:id});if(g===epoch)render(data.items);}
    catch(e){if(g===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  async function reopen(){
    if(!ready()||!evidence||!previewAllowed())return;
    const id=evidence.id,g=++epoch;context.setBusy(true);cancelProbe();
    try{const data=await context.api('/api/replica/generation/reopen',{check_id:id});if(g!==epoch)return;
      const v=data.items,p=v.generation,r=v.check;
      if(r.id!==id||p.id!==r.generation_id||p.digest!==r.generation_digest||typeof v.current!=='boolean')throw new Error('历史记录与候选绑定不一致，请重新读取。');
      setCandidate(p,v);render(r);
      context.message(v.current?(r.stale?'原候选仍有效；历史检查已过期，请重新检查。':'已恢复原候选及其检查，可查看并单独确认预演。'):`原候选已过期，只能查看：${v.reason}`);
    }catch(e){if(g===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  async function history(){
    if(!ready())return;const g=++epoch;context.setBusy(true);
    try{const data=await context.api('/api/replica/generation/check/history');if(g===epoch){el('replica-check-history').replaceChildren();
      if(!data.items.length)el('replica-check-history').append(node('p','还没有发布预检记录。'));
      for(const r of data.items){const card=node('article');card.className='native-check-card';card.append(node('p',`${new Date(r.created).toLocaleString()} · ${scopeName(r)} · ${r.rule_probe?'显式发现与规则来源':r.probe?'显式发现':'静态检查'} · ${r.targets.length} 个目标 · 历史候选${states[r.candidate_status]||r.candidate_status}（有效性待核对）`));const button=node('button','打开并核对有效性');button.type='button';button.className='btn replica-check-open';button.addEventListener('click',()=>open(r.id));card.append(button);el('replica-check-history').append(card);}
    }}catch(e){if(g===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  el('replica-check-run').addEventListener('click',()=>run());el('replica-check-probe').addEventListener('click',beginProbe);
  el('replica-check-probe-run').addEventListener('click',()=>run(true,el('replica-check-rules').checked));el('replica-check-probe-cancel').addEventListener('click',()=>{if(ready())cancelProbe();});
  el('replica-check-probe-dialog').addEventListener('cancel',e=>{e.preventDefault();if(ready())cancelProbe();});
  el('replica-check-history-load').addEventListener('click',history);
  el('replica-check-reopen').addEventListener('click',reopen);
  el('replica-check-preview').addEventListener('click',()=>{if(ready()&&candidate&&preview&&previewAllowed())preview(candidate,candidateScope);});
  return{init(ctx,actions={}){context=ctx;if(typeof ReplicaPublication!=='undefined')ReplicaPublication.init(ctx);preview=actions.preview;previewAllowed=actions.previewAllowed||(()=>true);invalidate();},sync,invalidate,setCandidate};
})();
