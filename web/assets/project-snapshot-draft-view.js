'use strict';
const ProjectSnapshotDraft=(()=>{
 const $=id=>document.getElementById(id),node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
 let ctx=null,epoch=0,plan=null,history=[],inspection=null,historyReady=false;
 const blocked=()=>!ctx||Boolean(ctx.pending?.())||Boolean(inspection?.pending);
 const canRun=()=>ctx&&!ctx.busy();
 const say=text=>{$('snapshot-draft-status').textContent=text;};
 function close(){plan=null;$('snapshot-draft-confirm').close?.();$('snapshot-draft-confirm').hidden=true;}
 function sync(){
  for(const id of ['snapshot-draft-load','snapshot-draft-inspect','snapshot-draft-cancel'])$(id).disabled=!canRun();
  $('snapshot-draft-confirm-run').disabled=!canRun()||!plan;
  $('snapshot-draft-recover').hidden=!inspection?.pending;
  $('snapshot-draft-recover').disabled=!canRun()||!inspection?.pending||!['READY','CLEANUP_ONLY'].includes(inspection.status);
  for(const b of $('snapshot-draft-history').querySelectorAll('button'))b.disabled=!canRun()||!historyReady||blocked();
 }
 function invalidate(){epoch++;close();history=[];inspection=null;historyReady=false;$('snapshot-draft-history').replaceChildren();$('snapshot-draft-inspection').replaceChildren();say('可将快照恢复为已保存草稿，工具应用另行确认。');sync();}
 function renderHistory(){
  const root=$('snapshot-draft-history');root.replaceChildren();
  for(const item of history){
   const row=node('article');row.className='native-check-card';
   row.append(node('p',`${{apply:'恢复为草稿',rollback:'回退草稿恢复','undo-rollback':'撤销草稿回退'}[item.action]||'草稿操作'} · ${{committed:'已保存',prepared:'中断待恢复',recovered:'失败后已恢复'}[item.state]||'状态未知'} · ${new Date(item.at).toLocaleString('zh-CN')}`));
   const detail=node('details');detail.append(node('summary','草稿事务身份'),node('code',item.id),node('code',`来源快照 ${item.snapshot}`));row.append(detail);
   if(item.restorable){const b=node('button',item.action==='rollback'?'预览撤销草稿回退':'预览回退草稿恢复');b.type='button';b.className='btn';b.addEventListener('click',()=>reverse(item.id));row.append(b);}
   root.append(row);
  }
 }
 function renderInspection(){
  const root=$('snapshot-draft-inspection');root.replaceChildren();
  if(!inspection)return;
  root.append(node('p',inspection.pending?`中断恢复条件：${{READY:'可恢复',CLEANUP_ONLY:'提交完成，需收口标记',CONFLICT:'存在冲突，先保存并核对人工修改'}[inspection.status]||'需检查'}`:'当前没有待恢复的草稿共同事务。'));
  for(const tx of inspection.transactions||[])for(const target of tx.targets||[]){
   root.append(node('p',`${target.path==='source'?'项目源':'本机覆盖'} · ${target.status}`));
   for(const issue of target.issues||[])root.append(node('p',`${issue.path||target.path} · ${issue.code} · ${issue.detail}`));
  }
  for(const issue of inspection.issues||[])root.append(node('p',`${issue.code} · ${issue.detail}`));
 }
 async function fetchHistory(g){
  historyReady=false;history=[];inspection=null;renderHistory();renderInspection();
  const [h,i]=await Promise.allSettled([ctx.api('/api/snapshots/draft/history'),ctx.api('/api/snapshots/draft/inspect')]);
  if(g!==epoch)return false;
  if(h.status==='fulfilled'){history=h.value.items;historyReady=true;}else say(h.reason.message);
  if(i.status==='fulfilled')inspection=i.value.items;else say(i.reason.message);
  renderHistory();renderInspection();sync();return h.status==='fulfilled'&&i.status==='fulfilled';
 }
 async function run(fn){if(!canRun())return;ctx.setBusy(true);try{await fn();}catch(e){close();say(e.message);}finally{ctx.setBusy(false);sync();}}
 async function load(){if(!canRun())return;const g=++epoch;close();await run(async()=>{if(await fetchHistory(g))say(inspection?.pending?'有中断事务；先检查并共同恢复两层。':history.length?`${history.length} 条草稿恢复记录。工具应用记录单独核对。`:'暂无草稿恢复记录。选择应用快照可预览恢复。');});}
 function show(value){
  plan=value;$('snapshot-draft-title').textContent=value.action==='apply'?'将快照恢复为已保存草稿':value.action==='rollback'?'回退此次草稿恢复':'撤销草稿回退';
  $('snapshot-draft-boundary').textContent=value.boundary;
  const root=$('snapshot-draft-changes');root.replaceChildren();
  if(value.source_root){const binding=node('details');binding.append(node('summary',`恢复绑定 · ${value.scope} · 当前副本`),node('code',`项目 ${value.project} / 副本 ${value.copy}`),node('code',`项目源 ${value.source_root}`),node('code',`本机覆盖 ${value.local_root}`));root.append(binding);}
  for(const [label,changes]of [['项目共享源',value.source_changes],['本机覆盖',value.local_changes]]){
   const detail=node('details');detail.append(node('summary',`${label} · ${changes.length} 项变化`));
   for(const c of changes)detail.append(node('p',`${c.before?(c.after?'修改':'移除'):'新增'} · ${c.path} · ${c.before?.bytes||0} → ${c.after?.bytes||0} B`));root.append(detail);
  }
  $('snapshot-draft-confirm-run').textContent=value.action==='apply'?'确认保存恢复的草稿':'确认回退草稿';$('snapshot-draft-confirm').hidden=false;$('snapshot-draft-confirm').showModal?.();sync();
 }
 async function prepare(id){if(!canRun()||blocked())return;const g=++epoch;close();await run(async()=>{const data=await ctx.api('/api/snapshots/draft/plan',{snapshot_id:id});if(g===epoch)show(data.items);});}
 async function reverse(id){if(!canRun()||blocked()||!historyReady||!history.some(h=>h.id===id&&h.restorable))return;const g=++epoch;close();await run(async()=>{const data=await ctx.api('/api/snapshots/draft/reverse',{transaction:id});if(g===epoch)show(data.items);});}
 async function refreshAfterWrite(text){
  let readError='';try{await ctx.refresh();}catch(e){readError=` 当前源仍不能读取：${e.message}；草稿恢复入口继续可用。`;}
  const g=++epoch;const loaded=await fetchHistory(g);say(text+(loaded?'':' 恢复记录或恢复条件暂不可读，请重新读取。')+readError);
 }
 $('snapshot-draft-load').addEventListener('click',load);
 $('snapshot-draft-inspect').addEventListener('click',()=>run(async()=>{const g=++epoch;close();inspection=null;renderInspection();const data=await ctx.api('/api/snapshots/draft/inspect');if(g===epoch){inspection=data.items;renderInspection();say('恢复条件已重新读取；执行时会再次核对原字节。');}}));
 $('snapshot-draft-cancel').addEventListener('click',()=>{if(canRun()){close();say('已取消预览，没有修改已保存源或本机覆盖。');sync();}});
 $('snapshot-draft-confirm').addEventListener('cancel',e=>{e.preventDefault();if(canRun()){close();sync();}});
 $('snapshot-draft-confirm-run').addEventListener('click',()=>{
  if(!canRun()||!plan)return;const p=plan;close();say('正在共同保存两层草稿…');return run(async()=>{
   try{await ctx.api('/api/snapshots/draft/apply',{plan_id:p.id,digest:p.digest});await refreshAfterWrite('两层草稿事务已保存；工具文件未应用。请另行检查与预览工具应用。');}
   catch(e){try{await refreshAfterWrite('读取草稿恢复状态。');}catch(_){}throw e;}
  });
 });
 $('snapshot-draft-recover').addEventListener('click',()=>{
  if(!canRun()||!inspection?.pending||!['READY','CLEANUP_ONLY'].includes(inspection.status))return;
  return run(async()=>{try{await ctx.api('/api/snapshots/draft/recover',{});await refreshAfterWrite('中断的草稿事务已共同恢复，没有重放工具或插件。');}catch(e){await fetchHistory(++epoch);throw e;}});
 });
 return {init(context,app){ctx=app.native_root?context:null;$('snapshot-draft-section').hidden=!app.native_root;invalidate();},prepare,load,sync,invalidate,blocked};
})();
