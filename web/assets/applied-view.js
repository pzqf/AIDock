'use strict';
const AppliedView=(()=>{
 const el=id=>document.getElementById(id),node=(tag,text)=>{const n=document.createElement(tag);n.textContent=text;return n;};
 let context,epoch=0;
 const ready=()=>context&&!context.busy();
 function sync(){el('applied-view-load').disabled=!ready();}
 function invalidate(){epoch++;el('applied-view-boundary').textContent='';el('applied-view-list').replaceChildren();el('applied-view-status').textContent='读取已应用版本，比较当前已保存草稿。';sync();}
 const diff=v=>({SAME:'字节相同',DIFFERENT:'有尚未应用的更改',UNKNOWN:'无法比较'}[v]||'未知');
 function render(report){
  const list=el('applied-view-list');list.replaceChildren();
  el('applied-view-status').textContent=report.recovery_required?'有待恢复的工具事务；先完成恢复，再核对启动状态。':report.views.length?`${report.views.length} 个安装视图 · 启动门槛以本次读取为准`:'未找到与当前安装匹配的联合应用视图。';
  if(report.draft_observation!=='OBSERVED')list.append(node('p','当前已保存草稿无法读取，不能据此判断一致。'));
  for(const item of report.views){
   const card=node('article');card.className='native-check-card';
   card.append(node('h4',({FROZEN:'已应用的固定配置',LEGACY_CURRENT_SOURCE:'旧安装：按当前源读取',UNPROVEN:'安装绑定缺少成功应用证据'}[item.mode]||'未知安装状态')));
   if(item.publication_id&&item.publication_members>0)card.append(node('p',`属于 ${item.publication_members} 个副本共同批次；成功提交由真实共同事务核对。`));
   card.append(node('p',`规范源：${diff(item.source_difference)} · 本机覆盖：${diff(item.local_difference)}`));
   if(item.mode==='LEGACY_CURRENT_SOURCE')card.append(node('p','此安装沿用原启动行为；保存草稿可能改变启动门槛。重新联合应用后才使用固定配置。'));
   if(item.mode==='UNPROVEN')card.append(node('p','当前绑定不能证明已经成功提交，请检查事务与恢复记录。'));
   for(const l of item.launchers||[])card.append(node('p',`${l.tool} · ${l.name} · ${l.status==='CURRENT'?'本次启动门槛通过':'启动被阻断'}${l.code?' · '+l.code:''}`));
   if(item.mode==='FROZEN'&&!(item.launchers||[]).length)card.append(node('p','此视图没有当前登记的 MCP 启动连接。'));
   const detail=node('details');detail.append(node('summary','应用身份与文件核对'));
   for(const [label,value]of [['快照',item.snapshot_id],['事务',item.transaction],['绑定',item.applied_binding]])if(value)detail.append(node('code',`${label} ${value}`));
   if(item.publication_id)detail.append(node('code',`共同批次 ${item.publication_id}`));
   for(const target of item.targets||[])detail.append(node('p',`${target.path} · ${target.status==='MATCH'?'与应用时字节相同':'当前文件有变化'}`));
   card.append(detail);list.append(card);
  }
  el('applied-view-boundary').textContent=report.boundary;
 }
 async function load(){
  if(!ready())return;const g=++epoch;el('applied-view-list').replaceChildren();el('applied-view-status').textContent='正在核对当前安装与已保存草稿…';context.setBusy(true);
  try{const data=await context.api('/api/native/applied');if(g===epoch)render(data.items);}
  catch(e){if(g===epoch){el('applied-view-list').replaceChildren();el('applied-view-status').textContent=e.message;}}
  finally{context.setBusy(false);sync();}
 }
 el('applied-view-load').addEventListener('click',load);
 return {init(ctx,app){context=app.native_root?ctx:null;el('applied-view-section').hidden=!app.native_root;invalidate();},sync,invalidate};
})();
