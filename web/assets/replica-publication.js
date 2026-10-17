'use strict';
const ReplicaPublication = (() => {
  const el=id=>document.getElementById(id);
  const node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
  const labels={READY:'等待确认',APPLIED:'同步完成',ROLLED_BACK:'已回退',UNDONE:'已撤销回退',RECOVERED:'失败同步已恢复',PENDING:'中断，等待恢复'};
  let context,candidate=null,evidence=null,proposal=null,session=null,recoveryReport=null,epoch=0;
  let historyLoaded=false,historyRecords=[];
  const ready=()=>context&&!context.busy()&&!context.pending?.();
  const eligible=()=>candidate?.application_scope==='skills-rules'&&!candidate.blocked&&!candidate.no_changes&&evidence?.generation_id===candidate.id&&evidence.generation_digest===candidate.digest&&!evidence.stale&&evidence.probe&&evidence.candidate_status==='PASS'&&!evidence.gates?.some(g=>g.required&&!['PASS','REQUIRED'].includes(g.status));
  function sync(){
    el('replica-check-publish').disabled=!ready()||!eligible();
    el('replica-publication-confirm-run').disabled=!ready()||!proposal;
    el('replica-publication-cancel').disabled=Boolean(context?.busy());
    el('replica-publication-history-load').disabled=!ready();
    el('replica-publication-reverse').disabled=!ready()||!session||session.pending||!session.transactions?.some(t=>t.state==='committed');
    el('replica-publication-recover').disabled=!ready()||!session?.pending||recoveryReport?.status==='CONFLICT';
    el('replica-publication-inspect').disabled=!ready()||!session;
    el('replica-publication-select').disabled=!ready()||!session||session.pending||!session.target_copies?.length||typeof ReplayEntryView==='undefined';
    for(const button of document.querySelectorAll?.('.replica-replay-node')||[])button.disabled=!ready()||session?.pending;
    for(const button of document.querySelectorAll?.('.replica-publication-open')||[])button.disabled=!ready();
  }
  function close(){proposal=null;el('replica-publication-confirm').close?.();el('replica-publication-confirm').hidden=true;sync();}
  function invalidate(){epoch++;candidate=null;evidence=null;close();}
  function setEvidence(p,r,current=true){epoch++;candidate=current?p:null;evidence=r;close();sync();}
  function show(p,publication=null){
    proposal={plan:p,publication};
    const reverse=p.kind==='coordinated-roots';
    el('replica-publication-confirm-title').textContent=reverse?(p.action==='rollback'?'确认回退正式同步':'确认撤销正式同步回退'):'确认同步到已登记项目';
    el('replica-publication-confirm-summary').textContent=reverse?'整批恢复目标技能源、工具文件与共同基线。仅处理此事务修改的路径；遇到后续人工修改会阻断并保留原文。发布者不参与回退。':p.boundary;
    el('replica-publication-confirm-run').textContent=reverse?'确认执行正式回退 / 撤销':'确认同步所列项目';
    const box=el('replica-publication-confirm-changes');box.replaceChildren();
    const transaction=reverse?p:p.transaction;
    if(!reverse){box.append(node('p',`发布者（只读）：${p.resolution.source.name}`),node('code',p.resolution.source.root),node('p',`${p.resolution.targets.length} 个目标 · 仅技能与规则 · 检查 ${p.check.id}`));}
    for(const target of transaction.targets){
      const root=transaction.bound_roots.find(b=>b.path===target.path),card=node('article');card.className='native-check-card';
      const copy=(!reverse?p.resolution.targets:[]).find(t=>target.path.includes(t.copy.id))?.copy.name;
      const name=target.path==='control'?'共同基线':target.path.includes('/.aidock')?'技能源':target.file?'规则文件':'工具目录（只修改技能子目录）';
      card.append(node('h3',`${copy?copy+' / ':''}${name}`),node('code',root.root));
      const before=target.before.entries,after=target.after.entries;
      for(const path of new Set([...Object.keys(before),...Object.keys(after)])){if(JSON.stringify(before[path])!==JSON.stringify(after[path]))card.append(node('p',`${after[path]?before[path]?'更新':'新增':'移除'} · ${path}`));}
      box.append(card);
    }
    box.append(node('p',`确认摘要 ${p.digest}`));
    el('replica-publication-confirm').hidden=false;el('replica-publication-confirm').showModal?.();el('replica-publication-confirm').scrollTop=0;sync();
  }
  async function plan(){
    if(!ready()||!eligible())return;const p=candidate,r=evidence,g=++epoch;context.setBusy(true);
    try{const data=await context.api('/api/replica/publication/plan',{generation_id:p.id,digest:p.digest,check_id:r.id,check_digest:r.digest});if(g===epoch)show(data.items);}
    catch(e){if(g===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  function render(r){
    recoveryReport=null;
    el('replica-publication-recovery-report').hidden=true;
    session=r;el('replica-publication-detail').hidden=false;
    el('replica-publication-status').textContent=`${labels[r.status]||r.status} · ${r.id}`;
    el('replica-publication-boundary').textContent=[r.warning,r.boundary].filter(Boolean).join(' ');
    const roots=el('replica-publication-roots');roots.replaceChildren();for(const b of r.bound_roots||[])roots.append(node('p',b.root));
    const list=el('replica-publication-transactions');list.replaceChildren();for(const t of r.transactions||[]){const row=node('article');row.append(node('p',`${{apply:'正式同步',rollback:'回退','undo-rollback':'撤销回退'}[t.action]||t.action} · ${{committed:'完成',prepared:'待恢复',recovered:'已恢复'}[t.state]||t.state} · ${t.targets} 个目标 / ${t.generated_roots||0} 个工具目录 / ${t.rule_files||0} 个规则文件 · ${t.id}`));if(t.state==='committed'&&(t.action==='apply'||t.id===r.transactions.find(item=>item.state==='committed')?.id)&&r.target_copies?.length&&typeof ReplayEntryView!=='undefined'){const button=node('button',t.action==='rollback'?'选择副本，预览撤销此回退':'选择副本，预览恢复到此事务前');button.type='button';button.className='btn replica-replay-node';button.addEventListener('click',()=>ReplayEntryView.openPublication(r,t.id,render));row.append(button);}list.append(row);}
    const current=r.transactions?.find(t=>t.state==='committed');el('replica-publication-reverse').textContent=current?.action==='rollback'?'预览撤销正式回退':'预览回退正式同步';
    if(historyLoaded){historyRecords=[r,...historyRecords.filter(row=>row.id!==r.id)];renderHistory();}sync();
  }
  async function confirm(){
    if(!ready()||!proposal)return;const v=proposal,g=++epoch;close();context.setBusy(true);
    try{const reverse=v.plan.kind==='coordinated-roots',data=await context.api(reverse?'/api/replica/publication/apply-reverse':'/api/replica/publication/apply',reverse?{publication_id:v.publication,plan_id:v.plan.id,digest:v.plan.digest}:{publication_id:v.plan.id,digest:v.plan.digest});if(g===epoch){render(data.items);candidate=null;evidence=null;context.message(reverse?'正式同步回退 / 撤销已完成。':'所选目标的技能、规则与共同基线已同步。可在正式同步记录中回退。');}}
    catch(e){if(g===epoch){context.message(e.message,true);try{const data=await context.api('/api/replica/publication/show',{publication_id:v.publication||v.plan.id});if(g===epoch)render(data.items);}catch{}}}finally{context.setBusy(false);sync();}
  }
  async function open(id){if(!ready())return;const g=++epoch;close();context.setBusy(true);try{const data=await context.api('/api/replica/publication/show',{publication_id:id});if(g===epoch)render(data.items);}catch(e){if(g===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}}
  function renderHistory(){
    const box=el('replica-publication-history');box.replaceChildren();if(!historyRecords.length)box.append(node('p','还没有正式同步记录。'));
    for(const r of [...historyRecords].sort((a,b)=>(b.created||'').localeCompare(a.created||''))){const card=node('article');card.className='native-check-card';card.append(node('p',`${new Date(r.created).toLocaleString()} · ${labels[r.status]||r.status}`));const button=node('button','打开正式同步记录');button.type='button';button.className='btn replica-publication-open';button.addEventListener('click',()=>open(r.id));card.append(button);box.append(card);}
  }
  async function history(){
    if(!ready())return;const g=++epoch;context.setBusy(true);
    try{const data=await context.api('/api/replica/publication/history');if(g===epoch){historyLoaded=true;historyRecords=data.items;renderHistory();const pending=data.items.find(r=>r.pending);if(pending)render(pending);}}
    catch(e){if(g===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  async function reverse(){if(!ready()||!session||session.pending)return;const latest=session.transactions.find(t=>t.state==='committed');if(!latest)return;const id=session.id,g=++epoch;context.setBusy(true);try{const data=await context.api('/api/replica/publication/reverse',{publication_id:id,transaction:latest.id});if(g===epoch)show(data.items,id);}catch(e){if(g===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}}
  function renderRecovery(r){
    recoveryReport=r;
    const statuses={IDLE:'没有待恢复事务',READY:'恢复条件通过，执行时仍会重新检查',CONFLICT:'恢复被阻断，请先处理冲突',CLEANUP_ONLY:'事务已结束，仅需清理恢复标记'};
    el('replica-publication-recovery-report').hidden=false;
    el('replica-publication-recovery-status').textContent=statuses[r.status]||r.status;
    el('replica-publication-recovery-note').textContent=`${r.boundary} 检查时间：${new Date(r.observed).toLocaleString()}`;
    const issues=el('replica-publication-recovery-issues');issues.replaceChildren();
    const issue=(box,item)=>box.append(node('p',`${item.code} · ${item.path||''} · ${item.detail}`));
    for(const item of r.issues||[])issue(issues,item);
    const box=el('replica-publication-recovery-targets');box.replaceChildren();
    const entry=e=>!e?'不存在':e.directory?`目录 · 权限 ${e.mode?.toString(8).padStart(4,'0')||'未记录'}`:`SHA-256 ${e.sha256} · ${e.bytes} 字节 · 权限 ${e.mode?.toString(8).padStart(4,'0')||'未记录'}`;
    for(const tx of r.transactions||[]){
      const group=node('section');group.append(node('p',`事务 ${tx.id} · ${statuses[tx.status]||tx.status}`));for(const item of tx.issues||[])issue(group,item);
      for(const target of [...(tx.targets||[])].sort((a,b)=>Number(b.status==='CONFLICT')-Number(a.status==='CONFLICT'))){
        const card=node('details');card.className='native-check-card';card.open=target.status==='CONFLICT';card.append(node('summary',`${target.status==='CONFLICT'?'需处理冲突':'可恢复'} · ${target.root}`));
        for(const item of target.issues||[])issue(card,item);
        const conflicts=[],others=[];
        for(const change of target.changes||[]){const same=e=>JSON.stringify(change.current||null)===JSON.stringify(e||null);(target.current_available&&!same(change.before)&&!same(change.after)?conflicts:others).push(change);}
        const appendChanges=(parent,changes,conflict=false)=>{for(const change of changes){const detail=node('details');detail.open=conflict;detail.append(node('summary',`${conflict?'冲突 · ':''}${change.path}`));for(const [label,value]of [['事务前',entry(change.before)],['事务后',entry(change.after)],['当前',target.current_available?entry(change.current):'未能读取，不能判断是否存在']]){const row=node('p',`${label}：`);row.append(node('code',value));detail.append(row);}parent.append(detail);}};
        appendChanges(card,conflicts,true);
        if(others.length){const rest=node('details');rest.append(node('summary',`其它 ${others.length} 项事务变更`));appendChanges(rest,others);card.append(rest);}
        group.append(card);
      }
      box.append(group);
    }
  }
  async function inspect(){if(!ready()||!session)return;const id=session.id,g=++epoch;close();context.setBusy(true);try{const data=await context.api('/api/replica/publication/inspect-recovery',{publication_id:id});if(g===epoch){renderRecovery(data.items);context.message('恢复条件检查完成，项目文件与恢复标记保留。');}}catch(e){if(g===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}}
  async function recover(){if(!ready()||!session?.pending||recoveryReport?.status==='CONFLICT')return;const id=session.id,g=++epoch;context.setBusy(true);try{const data=await context.api('/api/replica/publication/recover',{publication_id:id});if(g===epoch){render(data.items);context.message('正式同步已整批恢复。');}}catch(e){if(g===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}}
  el('replica-check-publish').addEventListener('click',plan);
  el('replica-publication-confirm-run').addEventListener('click',confirm);
  el('replica-publication-cancel').addEventListener('click',()=>{if(ready())close();});
  el('replica-publication-confirm').addEventListener('cancel',e=>{e.preventDefault();if(ready())close();});
  el('replica-publication-history-load').addEventListener('click',history);
  el('replica-publication-reverse').addEventListener('click',reverse);
  el('replica-publication-recover').addEventListener('click',recover);
  el('replica-publication-inspect').addEventListener('click',inspect);
  el('replica-publication-select').addEventListener('click',()=>{if(ready()&&session&&!session.pending&&typeof ReplayEntryView!=='undefined')ReplayEntryView.openPublication(session,session.id,render);});
  return{init(ctx){epoch++;context=ctx;session=null;recoveryReport=null;historyLoaded=false;historyRecords=[];invalidate();el('replica-publication-detail').hidden=true;el('replica-publication-recovery-report').hidden=true;el('replica-publication-history').replaceChildren();},sync,invalidate,setEvidence,history};
})();
