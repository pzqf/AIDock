'use strict';
const ProjectSnapshotView=(()=>{
 const el=id=>document.getElementById(id),node=(tag,text)=>{const n=document.createElement(tag);n.textContent=text;return n;};
 let context,epoch=0,rows=[],assessments=new Map();
 const checkLabels={MCP_PROJECT_AUTHORIZATION_REQUIRED:'需要当前项目重新授权',MCP_RUNTIME_CHECK_STALE:'已有运行时核验不再适用',MCP_RUNTIME_VERSION_UNVERIFIED:'运行时版本尚需核验',MCP_DEPENDENCIES_DECLARED:'依赖声明不等于运行验证',MCP_EXECUTION_PENDING:'受管执行和隔离能力尚待完成',MCP_MANIFEST_DEPENDENCIES_UNVERIFIED:'包内依赖尚未完成运行核验',MCP_ORIGIN_EVIDENCE_MISSING:'缺少来源核验证据',MCP_SECURITY_ASSESSMENT_MISSING:'缺少完整安全评估',MCP_SECURITY_REFERENCE_MISSING:'缺少安全评估引用',MCP_UNISOLATED_REQUEST:'当前声明使用非隔离运行',PROFILE_RULES_PENDING:'规则的工具发现和会话加载需单独验证'};
 const ready=()=>context&&!context.busy();
 function sync(){el('project-snapshot-load').disabled=!ready();for(const b of el('project-snapshot-list').querySelectorAll('button'))b.disabled=!ready()||b.dataset?.unavailable==='true'||b.dataset?.draft==='true'&&ProjectSnapshotDraft.blocked();}
 function invalidate(){epoch++;rows=[];assessments.clear();el('project-snapshot-list').replaceChildren();el('project-snapshot-status').textContent='请读取当前绑定的应用快照。';sync();}
 function render(){
  const list=el('project-snapshot-list');list.replaceChildren();
  for(const r of rows){
   const card=node('article');card.className='native-check-card';
   card.append(node('h4',`${new Date(r.at).toLocaleString('zh-CN')} · ${r.packages} 个有效技能包`),node('p',`${{committed:'当次应用已提交',prepared:'原事务待恢复',recovered:'当次应用已恢复',unknown:'未找到原事务记录，提交状态未知'}[r.transaction_state]||'原事务状态未知'} · ${r.targets} 个目标`));
   card.append(node('p',`新增 ${r.summary.added} · 修改 ${r.summary.changed} · 移除 ${r.summary.removed}`));
   if(r.reference_packages>0)card.append(node('p',`另保留 ${r.reference_packages} 个未采纳引用包，仅作恢复材料；启停状态仍按历史配置。`));
   card.append(node('p',r.integrity==='PASS_ARCHIVED_BYTES'?'归档正文与原生备份字节已核对；依赖结果以单独检查为准。':'归档正文与原生备份尚未逐项核对。'));
   const captured=r.configuration_inputs==='CAPTURED_ORIGINAL_LAYERS';
   card.append(node('p',captured?'已保存原始配置层和本机覆盖，可检查历史配置重建。':'旧快照缺少原始分层输入；仍可核验归档字节，不能推测历史配置。'));
   const detail=node('details');detail.append(node('summary','快照身份与保留范围'),node('code',r.id),node('code',`Git ${r.commit}`),node('code',`原事务 ${r.transaction}`),node('p',`${r.source_files} 个规范源文件 · 保留当前已保存草稿及有效技能包正文`),node('p',r.previous_applied?`已识别事务前应用源：${r.previous_applied}`:'未识别与事务前全部目标一致的已应用源，不能据此恢复旧配置。'));
   for(const [tool,version]of Object.entries(r.adapter_versions))detail.append(node('p',`${tool} · 适配器 ${version}`));
   card.append(detail);const check=node('button','核验归档完整性');check.className='btn';check.type='button';check.addEventListener('click',()=>verify(r.id));card.append(check);
   const assess=node('button','检查历史配置与当前依赖');assess.className='btn';assess.type='button';assess.dataset.unavailable=String(!captured);assess.addEventListener('click',()=>review(r.id));card.append(assess);
   const observation=assessments.get(r.id);
   const missingMaterial=observation?.skill_revision_material?.status==='MISSING';
   if(typeof ProjectSnapshotDraft!=='undefined'){const draft=node('button','恢复为已保存草稿');draft.type='button';draft.className='btn';draft.dataset.draft='true';draft.dataset.unavailable=String(!captured||missingMaterial);draft.addEventListener('click',()=>{if(!missingMaterial&&captured)ProjectSnapshotDraft.prepare(r.id);});card.append(draft);}
   if(observation){
    card.append(node('p',`历史配置重建通过 · 当前静态依赖：${{PASS:'已通过本次检查',WARN:'有提醒',UNKNOWN:'证据待补齐',FAIL:'存在阻断'}[observation.dependency_status]||'未知'} · ${new Date(observation.at).toLocaleString('zh-CN')}`));
    const material=observation.skill_revision_material;
    const materialNote=node('p',material?.status==='COMPLETE'?'技能精确修订材料齐全；当前权限和原生应用仍需分别检查。':missingMaterial?`缺少 ${material.missing_pins} 个精确技能修订的完整可核验正文，不能恢复为草稿；已有同 ID 技能不能代替。`:'技能精确修订材料尚未核对。');
    if(missingMaterial)materialNote.className='notice err';card.append(materialNote);
    const checks=node('details');checks.append(node('summary','本次检查结果与恢复边界'));
    const counts={PASS:0,WARN:0,UNKNOWN:0,FAIL:0};for(const c of observation.checks)counts[c.status]=(counts[c.status]||0)+1;
    checks.append(node('p',`通过 ${counts.PASS} · 提醒 ${counts.WARN} · 待补证据 ${counts.UNKNOWN} · 阻断 ${counts.FAIL}`));
    for(const c of observation.checks.filter(c=>c.status!=='PASS'))checks.append(node('p',`${c.component==='skill'?'技能 '+c.subject:c.subject||'配置'} · ${{WARN:'提醒',UNKNOWN:'待补证据',FAIL:'阻断'}[c.status]||c.status} · ${checkLabels[c.code]||c.code}`));
    checks.append(node('p',observation.boundary));card.append(checks);
   }
   list.append(card);
  }
  sync();
 }
 async function load(){
  if(!ready())return;const g=++epoch;rows=[];assessments.clear();render();context.setBusy(true);el('project-snapshot-status').textContent='正在读取应用快照…';
  try{const data=await context.api('/api/snapshots');if(g!==epoch)return;rows=data.items.snapshots;el('project-snapshot-boundary').textContent=data.items.boundary;el('project-snapshot-status').textContent=rows.length?`${rows.length} 个联合应用快照 · 当次提交与当前生效分别核对`:'当前绑定暂无联合应用快照。旧事务仍使用原恢复入口。';render();}
  catch(e){if(g===epoch){rows=[];render();el('project-snapshot-status').textContent=e.message;}}
  finally{context.setBusy(false);sync();}
 }
 async function verify(id){
  if(!ready())return;const g=epoch;assessments.delete(id);render();context.setBusy(true);
  try{const data=await context.api('/api/snapshots/check',{snapshot_id:id});if(g!==epoch)return;rows=rows.map(r=>r.id===id?data.items:r);render();el('project-snapshot-status').textContent='归档完整性已核对；完整项目回退与当前安全策略仍未验证。';}
  catch(e){if(g===epoch){rows=rows.map(r=>r.id===id?{...r,integrity:'NOT_CHECKED'}:r);render();el('project-snapshot-status').textContent=e.message;}}
  finally{context.setBusy(false);sync();}
 }
 async function review(id){
  if(!ready()||!rows.some(r=>r.id===id&&r.configuration_inputs==='CAPTURED_ORIGINAL_LAYERS'))return;
  const g=epoch;assessments.delete(id);render();context.setBusy(true);el('project-snapshot-status').textContent='正在重建历史配置并检查当前依赖…';
  try{const data=await context.api('/api/snapshots/assess',{snapshot_id:id});if(g!==epoch)return;rows=rows.map(r=>r.id===id?data.items.snapshot:r);assessments.set(id,data.items);render();el('project-snapshot-status').textContent='历史配置已重建，当前依赖结果见快照卡片。完整项目回退尚未开放。';}
  catch(e){if(g===epoch){assessments.delete(id);rows=rows.map(r=>r.id===id?{...r,integrity:'NOT_CHECKED'}:r);render();el('project-snapshot-status').textContent=e.message;}}
  finally{context.setBusy(false);sync();}
 }
 el('project-snapshot-load').addEventListener('click',load);
 return {init(ctx,app){context=app.native_root?ctx:null;el('project-snapshot-section').hidden=!app.native_root;invalidate();},sync,invalidate};
})();
