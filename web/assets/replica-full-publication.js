'use strict';
const ReplicaFullPublication=(()=>{
 const $=id=>document.getElementById(id),node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
 let ctx=null,resolution=null,epoch=0,report=null;
 const refs=new Map(),ready=()=>ctx&&!ctx.busy()&&!ctx.pending?.();
 const say=t=>{$('replica-full-status').textContent=t;};
 function clearReport(){report=null;$('replica-full-result').hidden=true;for(const id of ['identity','members'])$('replica-full-'+id).replaceChildren();}
 function close(){epoch++;$('replica-full-dialog').close?.();$('replica-full-dialog').hidden=true;}
 function invalidate(){close();resolution=null;refs.clear();clearReport();$('replica-full-references').replaceChildren();$('replica-full-history').replaceChildren();say('先读取已保存批次，或完成各目标完整检查后汇总。');sync();}
 function sync(){
  if(!ctx)return;const disabled=!ready();
  for(const id of ['open','prepare-open','close','history-load'])$('replica-full-'+id).disabled=disabled;
  $('replica-full-plan').disabled=disabled||!resolution||resolution.targets.some(t=>!refs.has(t.copy.id));
  $('replica-full-read').disabled=disabled||!report;
  for(const b of $('replica-full-history').querySelectorAll?.('button')||[])b.disabled=disabled;
 }
 function renderReferences(){
  const list=$('replica-full-references');list.replaceChildren();
  if(!resolution){list.append(node('p','当前没有打开的处理方案，可从下方读取已保存批次。'));return;}
  list.append(node('p',`当前处理方案：${resolution.id} · ${resolution.targets.length} 个明确目标`));
  for(const t of resolution.targets){const ref=refs.get(t.copy.id);list.append(node('p',`${t.copy.name} · ${ref?'已有完整检查引用，汇总时重新核对':'还需完整工具发现检查'}`));}
 }
 function bind(p){if(!resolution||p.id!==resolution.id||p.digest!==resolution.digest){epoch++;refs.clear();clearReport();resolution=p;}renderReferences();sync();}
 function remember(req,r){
  if(!resolution||req.plan_id!==resolution.id||req.digest!==resolution.digest||!resolution.targets.some(t=>t.copy.id===req.copy_id))return;
  epoch++;clearReport();
  if(r?.status==='CHECKED'&&r.fresh&&r.mcp_ready&&r.can_apply===false&&r.copy===req.copy_id&&r.id&&r.digest)refs.set(req.copy_id,{copy_id:req.copy_id,preparation_id:r.id,preparation_digest:r.digest});else refs.delete(req.copy_id);
  renderReferences();sync();
 }
 function verify(r,expected=null){
  if(!r?.id||!r.digest||r.can_apply!==false||!Array.isArray(r.members)||!['CHECKED','SAVED','STALE'].includes(r.status)||r.fresh!==(r.status==='CHECKED'))throw Error('完整批次返回边界不一致。');
  if(expected&&(r.id!==expected.id||r.digest!==expected.digest))throw Error('返回的批次或完整摘要不一致。');
  if(ctx.sourceRoot&&r.initiator_root.replaceAll('\\','/').toLowerCase()!==ctx.sourceRoot().replaceAll('\\','/').toLowerCase())throw Error('批次不属于当前发起项目。');
 }
 function render(r){
  report=r;$('replica-full-result').hidden=false;
  say(({CHECKED:'本次完整批次观察通过，尚未同步',SAVED:'已保存批次；尚未重新核对当前有效性',STALE:'原批次已过期，旧通过结果已清除'}[r.status])+(r.code?' · '+r.code:''));
  $('replica-full-boundary').textContent=r.boundary;
  $('replica-full-identity').replaceChildren(node('p',`批次 ${r.id}`),node('p',`完整摘要 ${r.digest}`),node('p',`只读源：${r.source.name} · ${r.members.length} 个目标`));
  const list=$('replica-full-members');list.replaceChildren();
  for(const m of r.members){const card=node('article');card.className='native-check-card';card.append(node('h4',m.copy.name),node('p',m.copy.root));if(r.fresh){card.append(node('p',`工具：${m.tools.join('、')} · 启用 MCP：${m.mcp_names.length?m.mcp_names.join('、'):'无'}`));const changes=node('details');changes.append(node('summary','源与原生文件变化（无正文）'));for(const t of m.changes){const d=node('details');d.append(node('summary',`${t.path} · ${t.changes.length} 项变化`));for(const c of t.changes)d.append(node('p',`${c.before?(c.after?'修改':'移除'):'新增'} · ${c.path}`));changes.append(d);}card.append(changes);}else card.append(node('p','此处仅保留原检查引用，不显示历史变化为当前可用结果。'));list.append(card);}
  sync();
 }
 async function plan(){
  if(!ready()||$('replica-full-plan').disabled)return;const p=resolution,req={plan_id:p.id,digest:p.digest,checks:p.targets.map(t=>({...refs.get(t.copy.id)}))},g=++epoch;clearReport();ctx.setBusy(true);say('正在逐目标重核完整检查并组装私有固定批次…');
  try{const data=await ctx.api('/api/replica/publication/full-plan',req);if(g!==epoch)return;verify(data.items);if(data.items.resolution_id!==p.id||data.items.members.length!==p.targets.length||data.items.members.some((m,i)=>m.copy.id!==p.targets[i].copy.id))throw Error('批次目标与原方案不一致。');render(data.items);}
  catch(e){if(g===epoch){clearReport();say(e.message+'；未执行同步，重新处理目标检查后再汇总。');}}finally{ctx.setBusy(false);sync();}
 }
 async function read(saved=report){
  if(!ready()||!saved)return;const wanted={id:saved.id,digest:saved.digest},g=++epoch;clearReport();ctx.setBusy(true);say('正在重读原批次，重新核对所有目标与现行授权…');
  try{const data=await ctx.api('/api/replica/publication/full-show',{publication_id:wanted.id,digest:wanted.digest});if(g!==epoch)return;verify(data.items,wanted);render(data.items);}
  catch(e){if(g===epoch){clearReport();say(e.message+'；旧结果已清除。');}}finally{ctx.setBusy(false);sync();}
 }
 async function history(){
  if(!ready())return;const g=++epoch;clearReport();$('replica-full-history').replaceChildren();ctx.setBusy(true);say('正在读取当前发起绑定保存的完整批次…');
  try{const data=await ctx.api('/api/replica/publication/full-history');if(g!==epoch)return;for(const r of data.items){verify(r);if(r.fresh||r.status!=='SAVED')throw Error('历史列表不能代替当前有效性核对。');const row=node('article');row.className='native-check-card';row.append(node('p',`${r.created} · ${r.members.length} 个目标 · 尚未核对当前有效性`));const b=node('button','重读此完整批次');b.className='btn';b.addEventListener('click',()=>read(r));row.append(b);$('replica-full-history').append(row);}say(data.items.length?'已读取保存记录；选择原批次重新核对。':'当前发起绑定没有已保存的完整批次。');}
  catch(e){if(g===epoch){$('replica-full-history').replaceChildren();say(e.message+'；历史结果已清除。');}}finally{ctx.setBusy(false);sync();}
 }
 function open(){if(!ready())return;$('replica-full-dialog').hidden=false;$('replica-full-dialog').showModal?.();renderReferences();sync();}
 for(const id of ['open','prepare-open'])$('replica-full-'+id).addEventListener('click',open);
 $('replica-full-close').addEventListener('click',()=>{if(ready())close();});$('replica-full-dialog').addEventListener('cancel',e=>{e.preventDefault();if(ready())close();});
 $('replica-full-plan').addEventListener('click',plan);$('replica-full-read').addEventListener('click',()=>read());$('replica-full-history-load').addEventListener('click',history);
 return{init(context){ctx=context;invalidate();},bind,remember,invalidate,sync};
})();
