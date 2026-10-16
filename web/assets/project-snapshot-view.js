'use strict';
const ProjectSnapshotView=(()=>{
 const el=id=>document.getElementById(id),node=(tag,text)=>{const n=document.createElement(tag);n.textContent=text;return n;};
 let context,epoch=0,rows=[];
 const ready=()=>context&&!context.busy();
 function sync(){el('project-snapshot-load').disabled=!ready();for(const b of el('project-snapshot-list').querySelectorAll('button'))b.disabled=!ready();}
 function invalidate(){epoch++;rows=[];el('project-snapshot-list').replaceChildren();el('project-snapshot-status').textContent='请读取当前绑定的应用快照。';sync();}
 function render(){
  const list=el('project-snapshot-list');list.replaceChildren();
  for(const r of rows){
   const card=node('article');card.className='native-check-card';
   card.append(node('h4',`${new Date(r.at).toLocaleString('zh-CN')} · ${r.packages} 个有效技能包`),node('p',`${{committed:'当次应用已提交',prepared:'原事务待恢复',recovered:'当次应用已恢复',unknown:'未找到原事务记录，提交状态未知'}[r.transaction_state]||'原事务状态未知'} · ${r.targets} 个目标`));
   card.append(node('p',`新增 ${r.summary.added} · 修改 ${r.summary.changed} · 移除 ${r.summary.removed}`));
   card.append(node('p',r.integrity==='PASS_ARCHIVED_BYTES'?'归档正文与原生备份字节已核对；当前依赖和安全策略未验证。':'归档正文与原生备份尚未逐项核对。'));
   const detail=node('details');detail.append(node('summary','快照身份与保留范围'),node('code',r.id),node('code',`Git ${r.commit}`),node('code',`原事务 ${r.transaction}`),node('p',`${r.source_files} 个规范源文件 · 保留当前已保存草稿及有效技能包正文`),node('p',r.previous_applied?`已识别事务前应用源：${r.previous_applied}`:'未识别与事务前全部目标一致的已应用源，不能据此恢复旧配置。'));
   for(const [tool,version]of Object.entries(r.adapter_versions))detail.append(node('p',`${tool} · 适配器 ${version}`));
   card.append(detail);const check=node('button','核验归档完整性');check.className='btn';check.type='button';check.addEventListener('click',()=>verify(r.id));card.append(check);list.append(card);
  }
  sync();
 }
 async function load(){
  if(!ready())return;const g=++epoch;rows=[];render();context.setBusy(true);el('project-snapshot-status').textContent='正在读取应用快照…';
  try{const data=await context.api('/api/snapshots');if(g!==epoch)return;rows=data.items.snapshots;el('project-snapshot-boundary').textContent=data.items.boundary;el('project-snapshot-status').textContent=rows.length?`${rows.length} 个联合应用快照 · 当次提交与当前生效分别核对`:'当前绑定暂无联合应用快照。旧事务仍使用原恢复入口。';render();}
  catch(e){if(g===epoch){rows=[];render();el('project-snapshot-status').textContent=e.message;}}
  finally{context.setBusy(false);sync();}
 }
 async function verify(id){
  if(!ready())return;const g=epoch;context.setBusy(true);
  try{const data=await context.api('/api/snapshots/check',{snapshot_id:id});if(g!==epoch)return;rows=rows.map(r=>r.id===id?data.items:r);render();el('project-snapshot-status').textContent='归档完整性已核对；完整项目回退与当前安全策略仍未验证。';}
  catch(e){if(g===epoch){rows=rows.map(r=>r.id===id?{...r,integrity:'NOT_CHECKED'}:r);render();el('project-snapshot-status').textContent=e.message;}}
  finally{context.setBusy(false);sync();}
 }
 el('project-snapshot-load').addEventListener('click',load);
 return {init(ctx,app){context=app.native_root?ctx:null;el('project-snapshot-section').hidden=!app.native_root;invalidate();},sync,invalidate};
})();
