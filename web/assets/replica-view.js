'use strict';
const ReplicaView = (() => {
  const el=id=>document.getElementById(id);
  const node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
  const labels={SAME:'一致',BASELINE_UNKNOWN:'缺共同基线',SOURCE_CHANGED:'仅源变化',TARGET_CHANGED:'仅目标变化',CONFLICT:'双方冲突',SOURCE_DELETED:'源已移除或退役',LOCAL_EXCLUDED:'本机排除',EXTERNAL_REGENERATION_REQUIRED:'外部生成待接入'};
  let context,copies=[],plan=null,report=null,generation=0;
  const ready=()=>context&&!context.busy();
  function sync(){
    for(const id of ['replica-load','replica-register','replica-compare','replica-baseline','replica-recover','replica-register-current'])el(id).disabled=!ready();
    el('replica-confirm-run').disabled=!ready()||!plan;el('replica-cancel').disabled=Boolean(context?.busy());
  }
  function close(){el('replica-confirm').close?.();el('replica-confirm').hidden=true;plan=null;sync();}
  function invalidate(){generation++;report=null;el('replica-result').hidden=true;close();}
  function selected(){return{from:el('replica-from').value,targets:copies.filter(b=>el(`replica-target-${b.id}`)?.checked).map(b=>b.id)};}
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
    for(const b of copies){const input=el(`replica-target-${b.id}`);if(b.id===el('replica-from').value)input.checked=false;input.disabled=b.id===el('replica-from').value;}
    el('replica-selection').hidden=copies.length<2;sync();
  }
  async function load(){
    if(!ready())return;const g=++generation;context.setBusy(true);
    try{const data=await context.api('/api/replica/list');if(g===generation){invalidate();renderRegistry(data.items);}}
    catch(e){if(g===generation)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  function show(p){
    plan=p;el('replica-confirm-title').textContent=p.action==='replica-register'?'确认登记项目副本':'确认建立共同基线';
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
    report=r;el('replica-result').hidden=false;el('replica-report-title').textContent=`${r.from.name} → ${r.targets.length} 个目标`;
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
    try{await context.api('/api/replica/confirm',{plan_id:p.id,digest:p.digest});if(g===generation){context.message('本机登记/共同基线已保存，项目内容未修改。');const data=await context.api('/api/replica/list');if(g===generation)renderRegistry(data.items);}}
    catch(e){if(g===generation)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  async function recover(){if(!ready())return;context.setBusy(true);try{const data=await context.api('/api/replica/recover',{});invalidate();context.message(`已恢复 ${data.items} 个本机副本管理事务。`);}catch(e){context.message(e.message,true);}finally{context.setBusy(false);sync();}}
  el('replica-load').addEventListener('click',load);el('replica-register').addEventListener('click',()=>register());el('replica-register-current').addEventListener('click',()=>register(true));
  el('replica-compare').addEventListener('click',()=>compare());el('replica-baseline').addEventListener('click',()=>compare(true));el('replica-confirm-run').addEventListener('click',confirm);el('replica-cancel').addEventListener('click',()=>{if(ready())close();});el('replica-recover').addEventListener('click',recover);
  el('replica-confirm').addEventListener('cancel',e=>{e.preventDefault();if(ready())close();});
  el('replica-from').addEventListener('change',()=>{invalidate();for(const b of copies){const input=el(`replica-target-${b.id}`);input.disabled=b.id===el('replica-from').value;if(input.disabled)input.checked=false;}});
  return{init(ctx,app){context=ctx;el('nav-replica').hidden=Boolean(app.global_root)||!ctx.sourceRoot();sync();},sync,invalidate,load};
})();
