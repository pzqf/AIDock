'use strict';
const ReplicaChecks = (() => {
  const el=id=>document.getElementById(id);
  const node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
  const toolNames={'claude-code':'Claude Code','codex-cli':'Codex CLI'};
  const states={PASS:'通过',FAIL:'阻断',UNKNOWN:'未验证',UNAVAILABLE:'尚未开放',SKIPPED:'无需变化',NOT_APPLIED:'待应用'};
  let context,candidate=null,epoch=0;
  const ready=()=>context&&!context.busy()&&!context.pending?.();
  function sync(){
    el('replica-check-publish').disabled=true;
    el('replica-check-run').disabled=!ready()||!candidate;
    el('replica-check-probe').disabled=!ready()||!candidate||candidate.blocked;
    el('replica-check-history-load').disabled=!ready();
    el('replica-check-probe-run').disabled=!ready()||!candidate||candidate.blocked;
    el('replica-check-probe-cancel').disabled=Boolean(context?.busy());
    for(const button of document.querySelectorAll?.('.replica-check-open')||[])button.disabled=!ready();
  }
  function cancelProbe(){el('replica-check-probe-dialog').close?.();el('replica-check-probe-dialog').hidden=true;}
  function invalidate(){epoch++;candidate=null;cancelProbe();el('replica-check-candidate').textContent='先选择工具并预览处理方案或生成物修复，再检查该候选。';el('replica-check-results').hidden=true;sync();}
  function setCandidate(p){
    if(p?.kind!=='replica-generation')return;
    epoch++;candidate=p;cancelProbe();el('replica-check-results').hidden=true;
    el('replica-check-candidate').textContent=`已暂存 ${p.targets.length} 个目标的${p.repair_only?'生成物修复':'源与生成物'}候选 · ${(p.tools||[]).map(t=>toolNames[t]||t).join('、')}；检查不应用此候选。`;
    sync();
  }
  function render(r){
    el('replica-check-results').hidden=false;
    el('replica-check-summary').textContent=`${r.stale?'已过期 · 历史':r.probe?'显式发现验证':'静态检查'} · 候选${states[r.candidate_status]||r.candidate_status} · 正式发布尚未开放`;
    el('replica-check-boundary').textContent=[r.stale_reason,r.boundary].filter(Boolean).join(' ');
    el('replica-check-identity').textContent=`检查 ${r.id} · 有效期至 ${new Date(r.expires).toLocaleString()}`;
    el('replica-check-matrix').replaceChildren();
    for(const t of r.targets||[]){
      const card=node('article');card.className='native-check-card';card.append(node('h4',t.copy.name),node('code',t.copy.root));
      if(t.preserved)card.append(node('p',`本机排除项保留，不在发现检查范围：${t.preserved.all?'全部技能':(t.preserved.names?.length?t.preserved.names:t.preserved.ids||[]).join('、')}`));
      for(const tool of t.inspection.tools||[]){
        card.append(node('p',`${toolNames[tool.tool]||tool.tool} · ${tool.version||'版本未验证'} · 文件 ${states[tool.L1]||tool.L1} · 隔离发现 ${r.stale?'历史 ':''}${tool.L2==='SKIPPED'?'无候选包，未运行发现':states[tool.L2]||tool.L2} · 新会话/能力未验证`));
        for(const skill of tool.skills||[])card.append(node('p',`${skill.native_name} · ${r.stale?'历史 ':''}${states[skill.status]||skill.status} · ${skill.detail}`));
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
  async function run(probe=false){
    if(!ready()||!candidate||probe&&candidate.blocked)return;
    const p=candidate,g=++epoch;cancelProbe();context.setBusy(true);
    context.message(probe?`正在逐目标验证候选发现（${p.targets.length} 个目标），各工具有超时限制。`:'正在检查候选与发布门槛。');
    try{const data=await context.api('/api/replica/generation/check',{plan_id:p.id,digest:p.digest,probe});if(g===epoch){render(data.items);context.message('候选检查已保存；查看逐副本结果与发布门槛。登记项目未修改。');}}
    catch(e){if(g===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  function beginProbe(){
    if(!ready()||!candidate||candidate.blocked)return;
    el('replica-check-probe-scope').textContent=`${candidate.targets.length} 个目标副本 · ${(candidate.tools||[]).map(t=>toolNames[t]||t).join('、')}`;
    el('replica-check-probe-dialog').hidden=false;el('replica-check-probe-dialog').showModal?.();sync();
  }
  async function open(id){
    if(!ready())return;const g=++epoch;context.setBusy(true);
    try{const data=await context.api('/api/replica/generation/check/show',{check_id:id});if(g===epoch)render(data.items);}
    catch(e){if(g===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  async function history(){
    if(!ready())return;const g=++epoch;context.setBusy(true);
    try{const data=await context.api('/api/replica/generation/check/history');if(g===epoch){el('replica-check-history').replaceChildren();
      if(!data.items.length)el('replica-check-history').append(node('p','还没有发布预检记录。'));
      for(const r of data.items){const card=node('article');card.className='native-check-card';card.append(node('p',`${new Date(r.created).toLocaleString()} · ${r.probe?'显式发现':'静态检查'} · ${r.targets.length} 个目标 · 历史候选${states[r.candidate_status]||r.candidate_status}（有效性待核对）`));const button=node('button','打开并核对有效性');button.type='button';button.className='btn replica-check-open';button.addEventListener('click',()=>open(r.id));card.append(button);el('replica-check-history').append(card);}
    }}catch(e){if(g===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  el('replica-check-run').addEventListener('click',()=>run());el('replica-check-probe').addEventListener('click',beginProbe);
  el('replica-check-probe-run').addEventListener('click',()=>run(true));el('replica-check-probe-cancel').addEventListener('click',()=>{if(ready())cancelProbe();});
  el('replica-check-probe-dialog').addEventListener('cancel',e=>{e.preventDefault();if(ready())cancelProbe();});
  el('replica-check-history-load').addEventListener('click',history);
  return{init(ctx){context=ctx;invalidate();},sync,invalidate,setCandidate};
})();
