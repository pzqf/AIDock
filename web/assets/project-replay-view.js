'use strict';
const ProjectReplayView=(()=>{
 const $=id=>document.getElementById('project-replay-'+id),node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
 let ctx=null,epoch=0,plan=null,history=[],inspection=null,historyReady=false;
 const ready=()=>ctx&&!ctx.busy(),blocked=()=>!ctx||Boolean(ctx.pending?.())||Boolean(inspection?.pending),say=s=>{$('status').textContent=s;};
 const action=p=>p.action==='apply'?'共同回放':p.action==='rollback'?'回退共同回放':'撤销共同回退';
 function close(){plan=null;$('consent').checked=false;$('confirm').close?.();$('confirm').hidden=true;sync();}
 function sync(){
  for(const id of ['load','inspect','cancel','reload'])$(id).disabled=!ready()||(id==='reload'&&!plan);
  $('commit').disabled=!ready()||blocked()||!plan?.can_apply||!$('consent').checked;
  $('consent').disabled=!ready()||!plan?.can_apply;
  $('recover').hidden=!inspection?.pending;$('recover').disabled=!ready()||!inspection?.pending||!['READY','CLEANUP_ONLY'].includes(inspection.status);
  for(const b of $('history').querySelectorAll('button'))b.disabled=!ready()||!historyReady||blocked();
  for(const b of $('restore-targets').querySelectorAll('button'))b.disabled=!ready()||blocked();
 }
 function invalidate(){epoch++;close();history=[];inspection=null;historyReady=false;$('history').replaceChildren();$('inspection').replaceChildren();say('共同回放会同时处理已保存源、本机覆盖和工具文件；确认前可查看差异。');sync();}
 function show(value){
  plan=value;$('consent').checked=false;$('title').textContent=action(value);$('proposal-status').textContent=value.can_apply?(value.regenerated_restore?'原应用已重新授权和核验；将生成新启动参数，并恢复原来的已保存草稿。':'请核对以下范围；确认后共同写入全部目标。'):'当前授权、程序或输入已变化，请重新核对或审查原应用（'+(value.code||'需要重新准备')+'）。过期差异已隐藏。';
  $('boundary').textContent=value.boundary;$('binding').textContent='项目 '+value.project+' / '+value.scope+' / 副本 '+value.copy+'\n项目源 '+value.source_root+'\n本机覆盖 '+value.local_root+'\n工具项目 '+value.root+'\n提案 '+value.id+'\n确认摘要 '+value.digest;
  const root=$('changes');root.replaceChildren();
  for(const [label,changes]of [['已保存项目源',value.source_changes],['已保存本机覆盖',value.local_changes]]){const details=node('details');details.append(node('summary',label+' · '+changes.length+' 项变化'));for(const c of changes)details.append(node('p',(c.before?(c.after?'修改':'移除'):'新增')+' · '+c.path+' · '+(c.before?.bytes||0)+' → '+(c.after?.bytes||0)+' B'));root.append(details);}
  const native=node('details');native.append(node('summary','工具应用文件 · '+value.native_changes.reduce((n,t)=>n+t.changes.length,0)+' 项变化'));
  for(const t of value.native_changes){native.append(node('h4',t.path));for(const c of t.changes)native.append(node('p',(c.before?(c.after?'修改':'移除'):'新增')+' · '+c.path));}root.append(native);
  $('restore-targets').replaceChildren();for(const t of value.restore_targets||[]){const b=node('button','重新审查原应用的授权与版本');b.className='btn';b.type='button';b.addEventListener('click',()=>{if(!ready()||blocked())return;close();ProjectSnapshotReplay.open(t.snapshot_id,t.restore);});$('restore-targets').append(b);}
  $('commit').textContent='确认'+action(value);$('confirm').hidden=false;$('confirm').showModal?.();sync();
 }
 async function run(fn){if(!ready())return;ctx.setBusy(true);try{await fn();}catch(e){close();say(e.message);}finally{ctx.setBusy(false);sync();}}
 function renderHistory(){const root=$('history');root.replaceChildren();for(const item of history){const row=node('article');row.className='native-check-card';row.append(node('p',action(item)+' · '+({committed:'已提交',prepared:'中断待恢复',recovered:'失败后已恢复'}[item.state]||'状态未知')+' · '+new Date(item.at).toLocaleString('zh-CN')));const details=node('details');details.append(node('summary','共同事务身份'),node('code',item.id));row.append(details);if(item.can_reverse){const b=node('button',item.action==='rollback'?'预览撤销共同回退':'预览共同回退');b.type='button';b.className='btn';b.addEventListener('click',()=>reverse(item.id));row.append(b);}root.append(row);}}
 function renderInspection(){const root=$('inspection');root.replaceChildren();if(!inspection)return;root.append(node('p',inspection.pending?'中断恢复条件：'+({READY:'可整批恢复',CLEANUP_ONLY:'提交已完成，需收口标记',CONFLICT:'存在冲突，先保留并核对人工修改'}[inspection.status]||inspection.status):'当前没有待恢复的共同回放事务。'));for(const tx of inspection.transactions||[])for(const t of tx.targets||[]){root.append(node('p',t.path+' · '+t.status));for(const issue of t.issues||[])root.append(node('p',(issue.path||t.path)+' · '+issue.code+' · '+issue.detail));}for(const issue of inspection.issues||[])root.append(node('p',issue.code+' · '+issue.detail));}
 async function fetchHistory(g){history=[];inspection=null;historyReady=false;renderHistory();renderInspection();const [h,i]=await Promise.allSettled([ctx.api('/api/snapshots/replay/history'),ctx.api('/api/snapshots/replay/inspect')]);if(g!==epoch)return false;if(h.status==='fulfilled'){history=h.value.items;historyReady=true;}else say(h.reason.message);if(i.status==='fulfilled')inspection=i.value.items;else say(i.reason.message);renderHistory();renderInspection();sync();return h.status==='fulfilled'&&i.status==='fulfilled';}
 async function load(){if(!ready())return;const g=++epoch;close();await run(async()=>{if(await fetchHistory(g))say(inspection?.pending?'有中断事务，请先检查全部目标并整批恢复。':history.length?history.length+' 条共同回放记录。仅最新成功事务可生成回退预览。':'暂无共同回放记录；从应用快照审查完整候选后可创建提案。');});}
 function projectPlan(data,snapshot){if(data.items.kind!=='project'||data.items.snapshot_id!==snapshot||data.items.project_plan?.snapshot_id!==snapshot)throw Error('返回提案与本次项目恢复节点不一致');return data.items.project_plan;}
 async function prepare(report){if(!ready()||blocked()||!report?.fresh||report.status!=='CHECKED')return;const g=++epoch;close();await run(async()=>{const data=await ctx.api('/api/replay',{kind:'project',snapshot_id:report.snapshot_id,action:'plan',prepared_id:report.id,digest:report.digest});if(g===epoch)show(projectPlan(data,report.snapshot_id));});}
 async function reverse(id){if(!ready()||blocked()||!historyReady||!history.some(h=>h.id===id&&h.can_reverse))return;const g=++epoch;close();await run(async()=>{const data=await ctx.api('/api/snapshots/replay/reverse',{transaction:id});if(g===epoch)show(data.items);});}
 async function refreshAfterWrite(message){let problem='';try{await ctx.refresh();}catch(e){problem=' 当前源读取失败：'+e.message+'；仍可检查共同事务恢复条件。';}const loaded=await fetchHistory(++epoch);say(message+(loaded?'':' 历史或恢复条件暂不可读，请重新读取。')+problem);}
 $('load').addEventListener('click',load);
 $('inspect').addEventListener('click',()=>run(async()=>{const g=++epoch;close();inspection=null;renderInspection();const data=await ctx.api('/api/snapshots/replay/inspect');if(g===epoch){inspection=data.items;renderInspection();say('已检查各共同根的恢复条件；执行恢复时仍会重新核对原字节和项目身份。');}}));
 $('reload').addEventListener('click',()=>{if(!ready()||!plan)return;const p=plan,g=++epoch;close();return run(async()=>{const data=await ctx.api(p.snapshot_id?'/api/replay':'/api/snapshots/replay/planned',p.snapshot_id?{kind:'project',snapshot_id:p.snapshot_id,action:'planned',plan_id:p.id,digest:p.digest}:{plan_id:p.id,digest:p.digest});if(g===epoch)show(p.snapshot_id?projectPlan(data,p.snapshot_id):data.items);});});
 $('cancel').addEventListener('click',()=>{if(ready()){close();say('已取消共同写入预览，当前文件保留。');}});
 $('confirm').addEventListener('cancel',e=>{e.preventDefault();if(ready())close();});$('consent').addEventListener('change',sync);
 $('commit').addEventListener('click',()=>{if(!ready()||$('commit').disabled||!plan)return;const p=plan;close();return run(async()=>{try{const data=await ctx.api(p.snapshot_id?'/api/replay':'/api/snapshots/replay/apply',p.snapshot_id?{kind:'project',snapshot_id:p.snapshot_id,action:'apply',plan_id:p.id,digest:p.digest,confirm:true}:{plan_id:p.id,digest:p.digest});if(p.snapshot_id&&data.items.action!=='committed')throw Error(data.items.code||'恢复事务没有提交');await refreshAfterWrite(action(p)+'已完成。请核对已应用配置；新会话行为仍需单独验证。');}catch(e){try{await refreshAfterWrite('已重新读取共同事务状态。');}catch(_){}throw e;}});});
 $('recover').addEventListener('click',()=>{if(!ready()||$('recover').disabled)return;return run(async()=>{try{await ctx.api('/api/snapshots/replay/recover',{});await refreshAfterWrite('中断共同事务已整批恢复，没有启动插件或回放历史授权。');}catch(e){await fetchHistory(++epoch);throw e;}});});
 return{init(context,app){ctx=app.native_root?context:null;$('section').hidden=!ctx;invalidate();},prepare,load,sync,invalidate};
})();
