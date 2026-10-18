'use strict';
const ReplicaFullPublication=(()=>{
 const $=id=>document.getElementById(id),node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
 const states={CHECKED:'完整批次检查通过，等待独立确认',SAVED:'已保存，尚未重新核对',STALE:'原批次已过期，需要重新检查',APPLIED:'完整同步已提交',ROLLED_BACK:'最近一次所选成员回退已提交',UNDONE:'最近一次所选成员撤销回退已提交',RECOVERED:'最近一次未完成事务已恢复',PENDING:'完整批次中断，需要恢复',UNKNOWN:'真实事务状态需要检查'};
 let ctx=null,resolution=null,epoch=0,report=null,confirmation=null,recovery=null;
 const refs=new Map(),idle=()=>ctx&&!ctx.busy(),writable=()=>idle()&&!ctx.pending?.();
 const say=t=>{$('replica-full-status').textContent=t;};
 function cancel(){confirmation=null;$('replica-full-confirm').hidden=true;sync();}
 function clearReport(){report=null;recovery=null;cancel();$('replica-full-result').hidden=true;$('replica-full-recovery').hidden=true;for(const id of ['identity','members','execution'])$('replica-full-'+id).replaceChildren();$('replica-full-execution').hidden=true;if(typeof ReplicaFullRestoration!=='undefined')ReplicaFullRestoration.invalidate();}
 function close(){epoch++;cancel();if(typeof ReplicaFullRestoration!=='undefined')ReplicaFullRestoration.invalidate();$('replica-full-dialog').close?.();$('replica-full-dialog').hidden=true;}
 function invalidate(){close();resolution=null;refs.clear();clearReport();$('replica-full-references').replaceChildren();$('replica-full-history').replaceChildren();say('从已保存批次继续，或完成各目标检查后汇总。');sync();}
 function sync(){
  if(!ctx)return;const disabled=!idle();
  for(const id of ['open','prepare-open','close','history-load'])$('replica-full-'+id).disabled=disabled;
  $('replica-full-plan').disabled=!writable()||!resolution||resolution.targets.some(t=>!refs.has(t.copy.id));
  $('replica-full-read').disabled=disabled||!report;
  $('replica-full-apply').disabled=!writable()||!report?.can_apply||Boolean(report.execution);
  $('replica-full-restore-open').disabled=!writable()||!report?.execution||report.execution.pending||!report.execution.transactions.some(t=>t.state==='committed');
  $('replica-full-inspect').disabled=disabled||!report?.execution;
  $('replica-full-recover-plan').disabled=disabled||!recovery?.pending||!['READY','CLEANUP_ONLY'].includes(recovery.status);
  $('replica-full-confirm-run').disabled=disabled||!confirmation||(confirmation.kind==='apply'&&!writable());
  $('replica-full-confirm-cancel').disabled=disabled;
  for(const b of $('replica-full-history').querySelectorAll?.('button')||[])b.disabled=disabled;
  if(typeof ReplicaFullRestoration!=='undefined')ReplicaFullRestoration.sync();
 }
 function renderReferences(){
  const list=$('replica-full-references');list.replaceChildren();
  if(!resolution){list.append(node('p','当前没有打开的处理方案，可从下方继续已保存批次。'));return;}
  list.append(node('p',`处理方案：${resolution.id} · ${resolution.targets.length} 个明确目标`));
  for(const t of resolution.targets)list.append(node('p',`${t.copy.name} · ${refs.has(t.copy.id)?'已有完整检查引用，汇总时重新核对':'还需完整工具发现检查'}`));
 }
 function bind(p){if(!resolution||p.id!==resolution.id||p.digest!==resolution.digest){epoch++;refs.clear();clearReport();resolution=p;}renderReferences();sync();}
 function remember(req,r){
  if(!resolution||req.plan_id!==resolution.id||req.digest!==resolution.digest||!resolution.targets.some(t=>t.copy.id===req.copy_id))return;
  epoch++;clearReport();
  if(r?.status==='CHECKED'&&r.fresh&&r.mcp_ready&&r.can_apply===false&&r.copy===req.copy_id&&r.id&&r.digest)refs.set(req.copy_id,{copy_id:req.copy_id,preparation_id:r.id,preparation_digest:r.digest});else refs.delete(req.copy_id);
  renderReferences();sync();
 }
 function verify(r,wanted=null){
  const checked=r?.status==='CHECKED'&&r.fresh&&!r.execution;
  if(!r?.id||!r.digest||!Array.isArray(r.members)||!Object.hasOwn(states,r.status)||r.fresh!==(r.status==='CHECKED')||r.can_apply!==Boolean(checked))throw Error('完整批次的确认边界不一致。');
  if(!['CHECKED','SAVED','STALE'].includes(r.status)&&(!r.execution||r.execution.publication_id!==r.id||r.execution.status!==r.status||!Array.isArray(r.execution.transactions)||!Array.isArray(r.execution.latest_members)||!Array.isArray(r.execution.unchanged_members)))throw Error('缺少实际共同事务记录。');
  if(wanted&&(r.id!==wanted.id||r.digest!==wanted.digest))throw Error('返回的批次或完整摘要不一致。');
  const root=ctx.sourceRoot?.();
  if(root&&r.initiator_root.replaceAll('\\','/').toLowerCase()!==root.replaceAll('\\','/').toLowerCase())throw Error('批次不属于当前发起项目。');
 }
 function render(r){
  report=r;recovery=null;cancel();$('replica-full-recovery').hidden=true;$('replica-full-result').hidden=false;say(states[r.status]+(r.code?' · '+r.code:''));
  $('replica-full-boundary').textContent=r.boundary;
  $('replica-full-identity').replaceChildren(node('p',`批次 ${r.id}`),node('p',`完整摘要 ${r.digest}`),node('p',`只读源：${r.source.name} · ${r.members.length} 个原目标`));
  const summary=$('replica-full-execution');summary.replaceChildren();summary.hidden=!r.execution;
  if(r.execution){const e=r.execution,planned=['PENDING','RECOVERED','UNKNOWN'].includes(r.status);summary.append(node('h4',`最近事务${planned?'计划':'已提交'}变化：${e.latest_members.length} / ${r.members.length} 个成员`));
   if(e.latest_selection_known)summary.append(node('p',`明确选择：${e.latest_selected_members.map(m=>m.name).join('、')||'无'}；选择成员可能已与目标状态一致。`));
   else summary.append(node('p','此历史节点未记录独立成员选择，不能从变化数量推断选择范围。'));
   summary.append(node('p',`${planned?'计划变化成员':'变更成员'}：${e.latest_members.map(m=>m.name).join('、')||'无'}`),node('p',`此事务无变化：${e.unchanged_members.map(m=>m.name).join('、')||'无'}；发布者只读`));
   $('replica-full-identity').append(node('p',`最近事务 ${e.latest_transaction}`));
  }
  const list=$('replica-full-members');list.replaceChildren();
  for(const m of r.members){const card=node('article');card.className='native-check-card';card.append(node('h4',m.copy.name),node('p',m.copy.root));
   if(r.fresh){card.append(node('p',`工具：${m.tools.join('、')} · 启用 MCP：${m.mcp_names.length?m.mcp_names.join('、'):'无'}`));const changes=node('details');changes.append(node('summary','源与工具文件变化'));for(const t of m.changes){const d=node('details');d.append(node('summary',`${t.path} · ${t.changes.length} 项`));for(const c of t.changes)d.append(node('p',`${c.before?(c.after?'修改':'移除'):'新增'} · ${c.path}`));changes.append(d);}card.append(changes);}
   else card.append(node('p','原检查属于历史记录；文件提交不代表实际会话或能力已验证。'));list.append(card);
  }sync();
 }
 async function plan(){
  if(!writable()||$('replica-full-plan').disabled)return;const p=resolution,req={plan_id:p.id,digest:p.digest,checks:p.targets.map(t=>({...refs.get(t.copy.id)}))},g=++epoch;clearReport();ctx.setBusy(true);say('正在核对全部目标并保存完整批次提案…');
  try{const data=await ctx.api('/api/replica/publication/full-plan',req);if(g!==epoch)return;verify(data.items);if(data.items.resolution_id!==p.id||data.items.members.length!==p.targets.length||data.items.members.some((m,i)=>m.copy.id!==p.targets[i].copy.id))throw Error('批次目标与原方案不一致。');render(data.items);}
  catch(e){if(g===epoch){clearReport();say(e.message+'；未执行同步。');}}finally{ctx.setBusy(false);sync();}
 }
 async function loadReport(wanted,g){const data=await ctx.api('/api/replica/publication/full-show',{publication_id:wanted.id,digest:wanted.digest});if(g!==epoch)return false;verify(data.items,wanted);render(data.items);return true;}
 async function read(saved=report){
  if(!idle()||!saved)return;const wanted={id:saved.id,digest:saved.digest},g=++epoch;clearReport();$('replica-full-history').replaceChildren();ctx.setBusy(true);say('正在核对原提案或读取实际共同事务…');
  try{await loadReport(wanted,g);}catch(e){if(g===epoch){clearReport();say(e.message+'；旧结果已清除。');}}finally{ctx.setBusy(false);sync();}
 }
 async function history(){
  if(!idle())return;const g=++epoch;clearReport();$('replica-full-history').replaceChildren();ctx.setBusy(true);say('正在读取原发起绑定的完整批次…');
  try{const data=await ctx.api('/api/replica/publication/full-history');if(g!==epoch)return;for(const r of data.items){verify(r);if(r.fresh||r.can_apply||r.status==='STALE')throw Error('历史列表不能代替当前确认检查。');const row=node('article');row.className='native-check-card';row.append(node('p',`${r.created} · ${r.members.length} 个原目标 · ${states[r.status]}`));const b=node('button','重读此完整批次');b.className='btn';b.addEventListener('click',()=>read(r));row.append(b);$('replica-full-history').append(row);}say(data.items.length?'选择批次查看原提案、真实事务或恢复入口。':'当前发起绑定没有已保存的完整批次。');}
  catch(e){if(g===epoch){$('replica-full-history').replaceChildren();say(e.message+'；历史结果已清除。');}}finally{ctx.setBusy(false);sync();}
 }
 function confirmPanel(kind){
  if(!idle()||!report)return;
  if(kind==='apply'&&(!writable()||!report.can_apply))return;
  if(kind==='recover'&&(!recovery?.pending||!['READY','CLEANUP_ONLY'].includes(recovery.status)))return;
  confirmation={kind,id:report.id,digest:report.digest};$('replica-full-confirm-title').textContent=kind==='apply'?'确认完整同步到所列副本':'确认恢复此完整批次的未完成事务';
  $('replica-full-confirm-note').textContent=kind==='apply'?'一次共同事务处理完整技能、规则、已启用 MCP 和共同基线。各目标本机覆盖与发布者保留；不启动 MCP，不复制旧授权。':'使用此批次原备份恢复未完成写入；遇到事务范围内的额外修改会阻断并保留现场。不要删除恢复标记。';
  const box=$('replica-full-confirm-members');box.replaceChildren(node('p',`发布者只读：${report.source.name}`));for(const m of report.members)box.append(node('p',`${m.copy.name} · ${m.copy.root}`));
  $('replica-full-confirm-identity').textContent=`批次 ${report.id}${kind==='apply'?' · 确认摘要 '+report.digest:''}`;
  $('replica-full-confirm-run').textContent=kind==='apply'?`确认同步 ${report.members.length} 个副本`:'确认恢复未完成事务';$('replica-full-confirm').hidden=false;$('replica-full-confirm').scrollIntoView?.({block:'nearest'});sync();
 }
 async function confirm(){
  if(!idle()||!confirmation||$('replica-full-confirm-run').disabled)return;const p=confirmation,g=++epoch;cancel();$('replica-full-history').replaceChildren();ctx.setBusy(true);let saved=false;
  try{const response=await ctx.api('/api/replica/publication/full-'+(p.kind==='apply'?'apply':'recover'),p.kind==='apply'?{publication_id:p.id,digest:p.digest}:{publication_id:p.id});const e=response.items;
   if(e.publication_id!==p.id||e.pending||!['APPLIED','RECOVERED'].includes(e.status))throw Error('未取得此批次的预期完成记录，请检查实际状态。');saved=true;
   if(g!==epoch)return;await ctx.refresh?.();if(await loadReport(p,g))say(p.kind==='apply'?'完整同步已提交。实际会话和能力仍须单独验证。':'未完成事务已恢复，可重新比较并检查。');
  }catch(e){if(g===epoch){cancel();say((saved?'操作已记录，后续状态核对失败：':'操作未取得完成证明：')+e.message);try{await loadReport(p,g);say((saved?'操作已记录：':'请核对真实事务状态：')+e.message);}catch{clearReport();}}}finally{ctx.setBusy(false);sync();}
 }
 async function inspect(){
  if(!idle()||!report?.execution)return;const wanted=report,g=++epoch;cancel();recovery=null;$('replica-full-recovery').hidden=true;ctx.setBusy(true);
  try{const data=await ctx.api('/api/replica/publication/full-inspect-recovery',{publication_id:wanted.id});if(g!==epoch)return;const r=data.items;if(r.schema_version!==1||!r.identity?.endsWith('/'+wanted.id)||!['IDLE','READY','CLEANUP_ONLY','CONFLICT'].includes(r.status))throw Error('恢复检查的批次或状态不一致。');recovery=r;
   $('replica-full-recovery-status').textContent=({IDLE:'没有待恢复事务',READY:'可恢复，执行时仍会重新检查',CLEANUP_ONLY:'事务已结束，尚需清理恢复标记',CONFLICT:'存在冲突，保留现场并先处理冲突'}[r.status]);
   const issues=$('replica-full-recovery-issues');issues.replaceChildren();for(const i of r.issues||[])issues.append(node('p',`${i.code} · ${i.path||''} · ${i.detail}`));
   const box=$('replica-full-recovery-targets');box.replaceChildren();for(const tx of r.transactions||[]){const d=node('details');d.open=tx.status==='CONFLICT';d.append(node('summary',`${tx.id} · ${tx.status}`));const targets=tx.targets||[],ready=targets.filter(t=>t.status==='READY');const appendTarget=(parent,target)=>{parent.append(node('p',`${target.status} · ${target.root}`));for(const i of target.issues||[])parent.append(node('p',`${i.code} · ${i.path||''} · ${i.detail}`));};for(const target of targets.filter(t=>t.status!=='READY'))appendTarget(d,target);if(ready.length){const good=node('details');good.append(node('summary',`${ready.length} 个路径检查通过`));for(const target of ready)appendTarget(good,target);d.append(good);}box.append(d);}
   $('replica-full-recovery').hidden=false;say('恢复检查已完成，尚未恢复任何文件。');
  }catch(e){if(g===epoch){recovery=null;say(e.message);}}finally{ctx.setBusy(false);sync();}
 }
 function open(){if(!idle())return;$('replica-full-dialog').hidden=false;$('replica-full-dialog').showModal?.();renderReferences();sync();}
 for(const id of ['open','prepare-open'])$('replica-full-'+id).addEventListener('click',open);
 $('replica-full-close').addEventListener('click',()=>{if(idle())close();});$('replica-full-dialog').addEventListener('cancel',e=>{e.preventDefault();if(idle())close();});
 $('replica-full-plan').addEventListener('click',plan);$('replica-full-read').addEventListener('click',()=>read());$('replica-full-history-load').addEventListener('click',history);
 $('replica-full-apply').addEventListener('click',()=>confirmPanel('apply'));$('replica-full-confirm-run').addEventListener('click',confirm);$('replica-full-confirm-cancel').addEventListener('click',()=>{if(idle())cancel();});
 $('replica-full-inspect').addEventListener('click',inspect);$('replica-full-recover-plan').addEventListener('click',()=>confirmPanel('recover'));
 $('replica-full-restore-open').addEventListener('click',()=>{if(writable()&&!$('replica-full-restore-open').disabled&&typeof ReplicaFullRestoration!=='undefined')ReplicaFullRestoration.open(report);});
 return{init(context){ctx=context;if(typeof ReplicaFullRestoration!=='undefined')ReplicaFullRestoration.init(context,()=>read());invalidate();},bind,remember,invalidate,sync};
})();
