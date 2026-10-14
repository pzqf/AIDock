'use strict';
const ReplicaView = (() => {
  const el=id=>document.getElementById(id);
  const node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
  const labels={SAME:'一致',BASELINE_UNKNOWN:'缺共同基线',SOURCE_CHANGED:'仅源变化',TARGET_CHANGED:'仅目标变化',CONFLICT:'双方冲突',SOURCE_DELETED:'源已移除或退役',LOCAL_EXCLUDED:'本机排除',EXTERNAL_REGENERATION_REQUIRED:'外部生成待接入'};
  const rehearsalLabels={APPLIED:'应用预演完成',ROLLED_BACK:'已回退预演',UNDONE:'已撤销预演回退',RECOVERED:'失败预演已恢复',PENDING:'预演中断，需恢复',READY:'预演已准备'};
  let context,copies=[],plan=null,report=null,generation=0,rehearsal=null;
  const choices=new Map();
  const ready=()=>context&&!context.busy()&&!context.pending?.();
  function sync(){
    for(const id of ['replica-load','replica-register','replica-compare','replica-baseline','replica-recover','replica-register-current','replica-history-load'])el(id).disabled=!ready();
    el('replica-confirm-run').disabled=!ready()||!plan;el('replica-cancel').disabled=Boolean(context?.busy());
    el('replica-resolve').disabled=!ready()||!report||Boolean(rehearsal?.pending);
    for(const button of document.querySelectorAll?.('.replica-session-open')||[])button.disabled=!ready();
    for(const select of document.querySelectorAll?.('.replica-choice')||[])select.disabled=!ready();
    el('replica-rehearsal-reverse').disabled=!ready()||!rehearsal||rehearsal.pending||!rehearsal.transactions?.some(t=>t.state==='committed');
    el('replica-rehearsal-recover').disabled=!ready()||!rehearsal?.pending;
    for(const copy of copies){const input=el(`replica-target-${copy.id}`);if(input){const source=copy.id===el('replica-from').value;input.disabled=!ready()||source;if(source)input.checked=false;}}
  }
  function close(){el('replica-confirm').close?.();el('replica-confirm').hidden=true;plan=null;sync();}
  function invalidate(){generation++;report=null;choices.clear();el('replica-result').hidden=true;close();}
  function selected(){const from=el('replica-from').value;return{from,targets:copies.filter(b=>b.id!==from&&el(`replica-target-${b.id}`)?.checked).map(b=>b.id)};}
  function renderRegistry(r){
    const previous=selected();copies=r.copies||[];
    el('replica-count').textContent=`${copies.length} 个已登记副本 · ${r.scope_id}`;
    el('replica-from').replaceChildren();el('replica-targets').replaceChildren();el('replica-copies').replaceChildren();
    for(const b of copies){
      const option=node('option',b.name);option.value=b.id;el('replica-from').append(option);
      const label=node('label');label.className='tool-check';const input=node('input');input.type='checkbox';input.id=`replica-target-${b.id}`;input.checked=previous.targets.includes(b.id);input.addEventListener('change',invalidate);label.append(input,node('span',b.name));el('replica-targets').append(label);
      const row=node('article');row.className='native-check-card';row.append(node('h3',b.name),node('code',b.root),node('small',`分支观察 ${b.branch} · ${b.id}`));
      if(b.local_excludes?.length)row.append(node('p',`本机排除 ${b.local_excludes.join('、')}`));el('replica-copies').append(row);
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
    el('replica-confirm').hidden=false;el('replica-confirm').showModal?.();sync();
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
    for(const t of r.targets){
      const card=node('article');card.className='native-check-card';card.append(node('h3',t.copy.name),node('small',t.copy.root));
      if(t.status!=='READY')card.append(node('p',`${t.status}：${t.reason}`));
      for(const d of t.entries){
        const row=node('details');row.className='replica-difference';row.append(node('summary',`${d.name} · ${labels[d.status]||d.status}`),node('p',d.reason));
        for(const [label,item]of[['源',d.source],['目标',d.target],['共同基线',d.base]])row.append(node('p',`${label}：${item?`${item.retired?'已退役':item.enabled?'已启用':'已停用'} · ${item.revision}`:'无记录'}`));
        if(d.changed_fields?.length)row.append(node('p',`配置字段变化：${d.changed_fields.join('、')}`));
        for(const c of d.package_changes||[])row.append(node('p',`${c.path} · ${c.before?.bytes||0} → ${c.after?.bytes||0} B`));
        card.append(row);
        if(d.status!=='SAME'){
          const label=node('label',`${d.name} 的处理`);label.className='replica-choice-label';const select=node('select');select.className='replica-choice';select.setAttribute?.('aria-label',`${t.copy.name} / ${d.name} 的处理`);
          const blocked=t.status!=='READY'||['LOCAL_EXCLUDED','EXTERNAL_REGENERATION_REQUIRED'].includes(d.status);
          for(const [value,text]of blocked?[['skip','跳过（当前边界不允许传播）']]:[['','请选择处理方式'],['source',d.source?.retired?'采用源：传播退役并保留完整包':'采用源：完整包与启停'],['target','保留目标：不修改发布者'],['skip','跳过本次']]){const option=node('option',text);option.value=value;select.append(option);}
          select.value=blocked?'skip':'';choices.set(`${t.copy.id}/${d.id}`,{target:t.copy.id,entry:d.id,choice:select.value});
          select.addEventListener('change',()=>{generation++;close();choices.set(`${t.copy.id}/${d.id}`,{target:t.copy.id,entry:d.id,choice:select.value});});label.append(select);card.append(label);
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
  async function confirm(){
    if(!ready()||!plan)return;const p=plan,g=++generation;close();context.setBusy(true);
    try{
      if(p.kind==='replica-resolution'){
        const data=await context.api('/api/replica/rehearsal',{plan_id:p.id,digest:p.digest});if(g===generation){renderRehearsal(data.items);context.message('独立副本预演完成，可回退和撤销；已登记项目与真实基线未修改。');}
      }else if(['native-targets','coordinated-roots'].includes(p.kind)){
        const data=await context.api('/api/replica/rehearsal/apply',{session:rehearsal.id,plan_id:p.id,digest:p.digest});if(g===generation){renderRehearsal(data.items);context.message('副本预演回退/撤销已完成。');}
      }else{
        await context.api('/api/replica/confirm',{plan_id:p.id,digest:p.digest});if(g===generation){context.message('本机登记/共同基线已保存，项目内容未修改。');const data=await context.api('/api/replica/list');if(g===generation)renderRegistry(data.items);}
      }
    }
    catch(e){if(g===generation){context.message(e.message,true);if(['replica-resolution','native-targets','coordinated-roots'].includes(p.kind)){try{const latest=await context.api('/api/replica/rehearsal/latest');if(g===generation)renderRehearsal(latest.items);}catch{}}}}finally{context.setBusy(false);sync();}
  }
  function showCandidate(p){
    plan=p;const reverse=['native-targets','coordinated-roots'].includes(p.kind);
    el('replica-confirm-scope').textContent='仅独立预演副本';el('replica-confirm-title').textContent=reverse?(p.action==='rollback'?'确认回退副本预演':'确认撤销副本预演回退'):'确认预演所选处理方案';
    el('replica-confirm-summary').textContent=reverse?'在独立副本恢复完整包与预演共同基线，保留已登记项目。':p.boundary;
    el('replica-confirm-run').textContent=reverse?'确认执行预演回退/撤销':'确认在独立副本预演';el('replica-confirm-changes').replaceChildren();
    for(const decision of p.request?.decisions||[]){const target=report?.targets.find(t=>t.copy.id===decision.target);const entry=target?.entries.find(e=>e.id===decision.entry);el('replica-confirm-changes').append(node('p',`${target?.copy.name||decision.target} / ${entry?.name||decision.entry}：${{source:'采用源',target:'保留目标',skip:'跳过'}[decision.choice]}`));}
    for(const target of p.targets||[]){
      const before=target.before?.entries||{},after=target.after?.entries||{};
      const changes=target.changes||Array.from(new Set([...Object.keys(before),...Object.keys(after)])).sort().filter(path=>JSON.stringify(before[path])!==JSON.stringify(after[path])).map(path=>({path,before:before[path],after:after[path]}));
      const name=target.copy?.name||(target.path==='control'?'预演共同基线':copies.find(c=>target.path===`copies/${c.id}/.aidock`)?.name||target.path);
      const card=node('details');card.append(node('summary',`${name} · ${changes.length} 项变化`));
      if(target.preflight)card.append(node('p',`候选静态检查 ${target.preflight.status}；会话生效另行检查`));
      for(const c of changes)card.append(node('p',`${c.after?c.before?'更新':'新增':'移除'} · ${c.path}`));el('replica-confirm-changes').append(card);
    }
    el('replica-confirm-changes').append(node('code',p.digest));el('replica-confirm').hidden=false;el('replica-confirm').showModal?.();sync();
  }
  async function resolve(){
    if(!ready()||!report)return;const decisions=Array.from(choices.values());if(decisions.some(d=>!d.choice)){context.message('每条差异都需明确选择采用源、保留目标或跳过。',true);return;}
    const req={...selected(),comparison_digest:report.digest,decisions},g=++generation;context.setBusy(true);
    try{const data=await context.api('/api/replica/resolve',req);if(g===generation)showCandidate(data.items);}
    catch(e){if(g===generation)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  function renderRehearsal(r){
    rehearsal=r;el('replica-rehearsal').hidden=!r;if(!r){sync();return;}
    el('replica-rehearsal-status').textContent=rehearsalLabels[r.status]||r.status;el('replica-rehearsal-boundary').textContent=[r.boundary,r.warning].filter(Boolean).join(' ');el('replica-rehearsal-root').textContent=r.root;
	  el('replica-rehearsal-bindings').hidden=!r.bound_roots?.length;el('replica-rehearsal-roots').replaceChildren();
	  for(const root of r.bound_roots||[]){const name=root.path==='control'?'共同基线':copies.find(c=>root.path===`copies/${c.id}/.aidock`)?.name||root.path;const row=node('p',`${name} · `);row.append(node('code',root.root));el('replica-rehearsal-roots').append(row);}
    const current=r.transactions?.find(t=>t.state==='committed');el('replica-rehearsal-reverse').textContent=current?.action==='rollback'?'预览撤销预演回退':'预览回退副本预演';
    el('replica-rehearsal-history').replaceChildren();for(const t of r.transactions||[])el('replica-rehearsal-history').append(node('p',`${{apply:'应用',rollback:'回退','undo-rollback':'撤销回退'}[t.action]||t.action} · ${{committed:'已完成',prepared:'待恢复',recovered:'已恢复'}[t.state]||t.state} · ${t.targets} 个副本 · ${t.changes} 项变化 · ${t.id}`));sync();
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
  el('replica-history-load').addEventListener('click',history);
  el('replica-from').addEventListener('change',()=>{invalidate();for(const b of copies){const input=el(`replica-target-${b.id}`);input.disabled=b.id===el('replica-from').value;if(input.disabled)input.checked=false;}});
  return{init(ctx,app){context=ctx;el('nav-replica').hidden=Boolean(app.global_root)||!ctx.sourceRoot();sync();},sync,invalidate,load};
})();
