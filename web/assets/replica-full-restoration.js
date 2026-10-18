'use strict';
const ReplicaFullRestoration=(()=>{
 const $=id=>document.getElementById('full-restore-'+id),node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
 const names={'claude-code':'Claude Code','codex-cli':'Codex CLI'},actionName=a=>a==='undo-rollback'?'撤销回退':'回退';
 const statusLabel=s=>({MATCHED:'已匹配',CURRENT:'有效',MISSING:'缺少',EMPTY:'尚未导入',STALE:'已过期',NONE:'尚未授权',INVALID:'无效',CHECKED:'检查完成',NEEDS_CHECK:'还需工具发现',NEEDS_ATTENTION:'需要处理',BLOCKED:'被阻断',PASS:'通过',FAIL:'未通过',UNKNOWN:'未验证',NOT_APPLIED:'尚未写入',NOT_CHECKED:'尚未检查'}[s]||s||'未读取');
 const restoreReason=code=>({PLAN_STALE:'原条件已变化，请审查下方原应用',MCP_GRANT_STALE:'授权已变化，需要重新审查',MCP_GRANT_REQUIRED:'需要本次授权',PLAN_CONSUMED:'此提案已经执行，请读取最新事务'}[code]||code||'需要独立审查');
 let ctx=null,changed=null,batch=null,seed=null,proposal=null,review=null,operation=null,epoch=0,probing=false;
 const boxes=new Map(),checks=new Map(),drafts=new Map();
 const idle=()=>ctx&&!ctx.busy(),ready=()=>idle()&&!ctx.pending?.(),say=t=>{$('status').textContent=t;};
 const chosen=()=>[...boxes].filter(([,b])=>b.checked).map(([id])=>id);
 const views=()=>seed?.members.flatMap(m=>(m.views||[]).map(v=>({copy:m.copy,binding:v.applied_binding,snapshot:v.snapshot})))||[];
 const viewKey=v=>v.copy.id+'|'+v.binding,selectedView=()=>views().find(v=>viewKey(v)===$('view').value);
 const entry=()=>review?.permission.entries.find(e=>e.name===$('plugin').value);
 const artifact=()=>review?.artifacts.artifacts.find(e=>e.name===entry()?.name);
 const installed=()=>review?.installation.installations.find(e=>e.record.plugin.name===entry()?.name);
 const installCandidate=()=>review?.installation.candidates.find(e=>e.plugin.name===entry()?.name);
 const bad=list=>Boolean(list?.some(f=>f.status==='FAIL'));
 const base=()=>{const v=selectedView();return {publication_id:batch.id,plan_id:seed.id,digest:seed.digest,copy_id:v.copy.id,applied_binding:v.binding};};
 const reviewed=()=>({...base(),candidate_digest:review.candidate_digest});
 const draftKey=()=>review?.candidate_digest+'|'+$('plugin').value;
 const call=async(action,request)=>(await ctx.api('/api/replica/publication/full-restore-'+action,request)).items;
 function detail(parent,title,value){const d=node('details');d.append(node('summary',title),node('pre',JSON.stringify(value,null,2)));parent.append(d);}
 function clearOperation(){operation=null;$('operation').hidden=true;$('operation-details').replaceChildren();}
 function consent(){for(const id of ['risk','permissions','tools','unisolated','secrets','runtime-consent','rule-probe'])$(id).checked=false;probing=false;$('probe-confirm').hidden=true;clearOperation();}
 function clearReview(){review=null;consent();$('review-content').hidden=true;$('check-result').replaceChildren();$('review-summary').replaceChildren();$('entry-summary').replaceChildren();$('review-details').replaceChildren();$('plugin').replaceChildren();}
 function clearProposal(){proposal=null;$('proposal').hidden=true;clearOperation();}
 function selectionChanged(){epoch++;seed=null;checks.clear();clearReview();clearProposal();$('review-section').hidden=true;$('ready-references').replaceChildren();sync();}
 function close(){epoch++;batch=seed=null;checks.clear();clearReview();clearProposal();$('dialog').close?.();$('dialog').hidden=true;sync();}
 function sync(){
  if(!ctx)return;const disabled=!ready(),e=entry(),a=artifact(),current=selectedView(),ref=current?checks.get(viewKey(current)):null;
  for(const n of $('dialog').querySelectorAll?.('button,input,select,textarea')||[])n.disabled=disabled;
  $('close').disabled=!idle();$('plan').disabled=disabled||!batch||!$('node').value||!chosen().length;$('all').disabled=disabled||!batch;
  $('read').disabled=disabled||!proposal;$('confirm-plan').disabled=disabled||!proposal?.can_apply;
  $('review').disabled=disabled||!current;$('plugin').disabled=disabled||!review;
  const installReady=review&&e&&a?.status==='MATCHED'&&!review.installation.recovery_required&&!bad(review.installation.checks);
  $('artifact-plan').disabled=disabled||!review||!a||a.recovery_required||!['MISSING','EMPTY'].includes(a.status)||!$('archive').value.trim();
  const risk=installCandidate()?.check.trust_level!=='L2';
  $('install-plan').disabled=disabled||!installReady||installed()?.status==='CURRENT'||risk&&(!$('risk').checked||!$('reason').value.trim());
  const grant=ready()&&review&&e&&e.installation==='CURRENT'&&!review.permission.recovery_required&&!bad(review.permission.checks);
  $('permission-plan').disabled=!grant||!$('purpose').value.trim()||!$('permissions').checked||e.tool_policy&&!$('tools').checked||e.tier===0&&!$('unisolated').checked||e.secrets.length&&!$('secrets').checked;
  const runtime=grant&&e.authorization==='CURRENT'&&e.tier===0&&e.unisolated?.kind==='node'&&e.unisolated.source_type==='local'&&!review.runtime.runtime.recovery_required;
  $('runtime-consent').disabled=!runtime;$('runtime').disabled=!runtime||!$('runtime-consent').checked;
  for(const id of ['static','probe'])$(id).disabled=disabled||!review;
  $('prepared').disabled=disabled||!review||!ref||ref.candidate!==review.candidate_digest;
  $('probe-run').disabled=disabled||!probing||!review;$('probe-cancel').disabled=disabled;
  const all=views();$('regenerate').disabled=disabled||!seed||!all.length||all.some(v=>!checks.get(viewKey(v))?.good);
  $('operation-confirm').disabled=disabled||!operation;$('operation-cancel').disabled=!idle();
 }
 function open(r){
  if(!ready()||!r.execution||r.execution.pending)return;close();batch=r;$('binding').textContent=`只读发布者：${r.source.name} · 批次 ${r.id}`;
  $('node').replaceChildren();const empty=node('option','请选择恢复节点');empty.value='';$('node').append(empty);
  const committed=r.execution.transactions.filter(t=>t.state==='committed'),latest=committed[0];
  for(const t of committed){if(t.action!=='apply'&&t!==latest)continue;const option=node('option',`${t.action==='apply'?'恢复到完整同步之前':t.action==='rollback'?'撤销最近一次回退':'撤销最近一次撤销回退'} · ${t.id}`);option.value=t.id;$('node').append(option);}
  $('node').value='';boxes.clear();$('copies').replaceChildren();
  for(const m of r.members){const label=node('label'),box=node('input');box.type='checkbox';box.checked=false;box.addEventListener('change',selectionChanged);label.append(box,node('span',m.copy.name));boxes.set(m.copy.id,box);$('copies').append(label);}
  $('dialog').hidden=false;$('dialog').showModal?.();say('明确选择恢复节点与成员后预览；此时不修改项目。');sync();
 }
 function verifyProposal(p,expected=null){
  if(!p?.id||!p.digest||p.publication_id!==batch.id||!['rollback','undo-rollback'].includes(p.action)||!Array.isArray(p.members)||typeof p.can_apply!=='boolean')throw Error('恢复提案的批次或动作不一致。');
  if(expected&&(p.id!==expected.id||p.digest!==expected.digest))throw Error('恢复提案身份变化。');
  const wanted=[...(seed?seed.members.map(m=>m.copy.id):chosen())].sort(),actual=p.members.map(m=>m.copy.id).sort();
  if(JSON.stringify(wanted)!==JSON.stringify(actual))throw Error('恢复返回了未明确选择的成员。');
 }
 function renderProposal(p){
  proposal=p;$('proposal').hidden=false;$('proposal-title').textContent=(p.regenerated?'参数已重新生成 · ':'')+actionName(p.action)+'提案';
  $('proposal-note').textContent=(p.can_apply?'当前条件通过，仍须单独确认。':'当前不能确认：'+restoreReason(p.code)+'。')+' '+p.boundary;
  const members=$('proposal-members');members.replaceChildren(node('p',`明确选择：${p.members.map(m=>m.copy.name).join('、')}`),node('p',`保留未选成员：${p.retained_members.map(m=>m.name).join('、')||'无'}；本机覆盖与发布者保留。`));
  for(const m of p.members)if(m.code)members.append(node('p',`${m.copy.name} · ${restoreReason(m.code)}`));
  $('proposal-identity').textContent=`提案 ${p.id} · 摘要 ${p.digest} · 原节点 ${p.original_transaction}`;
  $('proposal-changes').replaceChildren();for(const c of p.changes||[]){const d=node('details');d.append(node('summary',`${c.path} · ${c.changes.length} 项`));for(const change of c.changes)d.append(node('p',`${change.before?(change.after?'修改':'移除'):'新增'} · ${change.path}`));$('proposal-changes').append(d);}
  sync();
 }
 function renderViews(){
  const previous=$('view').value;$('view').replaceChildren();for(const v of views()){const option=node('option',`${v.copy.name} · 原应用 ${v.binding.slice(0,12)}`);option.value=viewKey(v);$('view').append(option);}
  $('view').value=views().some(v=>viewKey(v)===previous)?previous:views().length===1?viewKey(views()[0]):'';
  $('review-section').hidden=!views().length;renderReferences();
 }
 function renderReferences(){const box=$('ready-references');box.replaceChildren();for(const v of views()){const ref=checks.get(viewKey(v));box.append(node('p',`${v.copy.name} / ${v.binding.slice(0,12)} · ${ref?.good?'有当前完整检查，生成时再次核对':'还需完成完整候选检查'}`));}sync();}
 async function plan(){
  if(!ready()||$('plan').disabled)return;const req={publication_id:batch.id,transaction:$('node').value,copies:chosen()},g=++epoch;clearReview();clearProposal();checks.clear();seed=null;ctx.setBusy(true);say('正在按明确成员核对原事务变化…');
  try{const p=await call('plan',req);if(g!==epoch)return;verifyProposal(p);if(p.original_transaction!==req.transaction)throw Error('恢复节点与本次选择不一致。');seed=p;renderProposal(p);renderViews();say('恢复预览已保存。尚未写入项目；条件不足的原应用可在下方独立准备。');}
  catch(e){if(g===epoch){selectionChanged();say(e.message+'；未修改项目。');}}finally{ctx.setBusy(false);sync();}
 }
 async function readProposal(){
  if(!ready()||!proposal)return;const p=proposal,g=++epoch;clearOperation();ctx.setBusy(true);
  try{const r=await call('show',{publication_id:batch.id,plan_id:p.id,digest:p.digest});if(g!==epoch)return;verifyProposal(r,p);if(r.regenerated&&!r.can_apply){checks.clear();renderReferences();}renderProposal(r);say('提案已重新核对；确认时仍会检查当前条件。');}
  catch(e){if(g===epoch){clearProposal();checks.clear();clearReview();say(e.message+'；旧结果已清除。');}}finally{ctx.setBusy(false);sync();}
 }
 function renderEntry(){
  consent();const e=entry();$('entry-summary').replaceChildren();$('review-details').replaceChildren();if(!e){sync();return;}
  const a=artifact(),i=installed(),runtime=review.runtime.runtime.entries?.find(r=>r.current&&r.event.input.name===e.name&&r.event.code==='RUNTIME_VERSION_MATCH');
  $('entry-summary').append(node('p',`${e.name} · 产物 ${statusLabel(a?.status)} · 安装 ${statusLabel(e.installation)} · 授权 ${statusLabel(e.authorization)} · 版本 ${runtime?'已核验':'待核验'}`));
  if(i&&i.status!=='CURRENT')$('entry-summary').append(node('p',`当前安装为 ${i.record.plugin.version}。登记原合同版本会替换同名安装，旧授权随后失效；产物保留。`));
  if(e.replaces_grant)$('entry-summary').append(node('p','本次授权确认会替换同名现有授权，旧参数不能继续使用。'));
  detail($('review-details'),'原合同权限、工具策略与凭据引用',{permissions:e.permissions,tool_policy:e.tool_policy,tier:e.tier,unisolated_runtime:e.unisolated,secret_references:e.secrets});
  if(installCandidate())detail($('review-details'),'精确来源、版本、入口与运行时',installCandidate());
  $('tools-row').hidden=!e.tool_policy;$('unisolated-row').hidden=e.tier!==0;$('secrets-row').hidden=!e.secrets.length;
  const d=drafts.get(draftKey());$('purpose').value=d?.purpose||'';$('archive').value=d?.archive||'';$('reason').value=d?.reason||'';
  $('package-section').open=a?.status!=='MATCHED';$('install-section').open=i?.status!=='CURRENT';$('permission-section').open=e.installation==='CURRENT'&&e.authorization!=='CURRENT';$('runtime-section').open=e.authorization==='CURRENT'&&!runtime;
  sync();
 }
 async function loadReview(g){
  const wanted=base(),r=await call('review',wanted);if(g!==epoch)return false;
  if(r.publication_id!==wanted.publication_id||r.plan_id!==wanted.plan_id||r.copy_id!==wanted.copy_id||r.applied_binding!==wanted.applied_binding||!r.candidate_digest||!Array.isArray(r.tools)||!Array.isArray(r.permission?.entries))throw Error('原合同审查返回了其它成员或应用。');
  review=r;$('review-content').hidden=false;$('review-summary').replaceChildren(node('p',`原应用 ${r.applied_binding} · 候选 ${r.candidate_digest}`),node('p',r.boundary));
  $('plugin').replaceChildren();const enabled=r.permission.entries.filter(e=>e.enabled);for(const e of enabled){const option=node('option',e.name);option.value=e.name;$('plugin').append(option);}$('plugin').value=enabled.length===1?enabled[0].name:'';
  $('check-tools').textContent='完整接收工具：'+r.tools.map(t=>names[t]||t).join('、')+'；不会通过删减工具或 MCP 缩小检查范围。';
  renderEntry();return true;
 }
 async function reviewRun(){if(!ready()||!selectedView())return;const g=++epoch;clearReview();ctx.setBusy(true);say('正在读取原应用的产物、安装、授权与运行时条件…');try{if(await loadReview(g))say('已读取原应用精确合同。各项准备均须独立预览确认。');}catch(e){if(g===epoch){clearReview();say(e.message);}}finally{ctx.setBusy(false);sync();}}
 function saveDraft(){if(review&&entry())drafts.set(draftKey(),{purpose:$('purpose').value,archive:$('archive').value,reason:$('reason').value});clearOperation();sync();}
 function clearCopyChecks(copy){for(const key of [...checks.keys()])if(key.startsWith(copy+'|'))checks.delete(key);renderReferences();if(proposal?.regenerated)clearProposal();}
 async function setupPlan(kind){
  if(!ready()||!review||$(''+kind+'-plan').disabled)return;const e=entry(),req=reviewed(),g=++epoch;let input;
  if(kind==='artifact')input={action:'import',name:e.name,archive_path:$('archive').value.trim(),expected_sha256:artifact().sha256,expected_lock_sha256:review.artifacts.lock_sha256};
  if(kind==='install')input={action:'install',name:e.name,expected_sha256:review.installation.sha256,allow_unverified:$('risk').checked,risk_reason:$('reason').value.trim()};
  if(kind==='permission'){input={action:'grant',name:e.name,expected_sha256:review.permission.sha256,expected_review_sha256:review.permission.review_sha256,purpose:$('purpose').value.trim(),data_level:'Public',destination:'unknown-external-client'};for(const id of ['permissions','tools','unisolated','secrets'])input['confirm_'+id]=$(id).checked;}
  clearOperation();ctx.setBusy(true);say('正在核对原合同并生成独立准备预览…');
  try{const p=await call(kind+'-plan',{...req,[kind==='permission'?'authorization':'edit']:input});if(g!==epoch)return;if(p.candidate_digest!==review.candidate_digest||p.name!==e.name||!p.id||!p.digest)throw Error('准备提案与本次原合同不一致。');operation={kind,p,req,name:e.name};$('operation-title').textContent='确认'+({artifact:'导入精确版本',install:'登记原合同版本',permission:'本次新授权'}[kind]);$('operation-note').textContent=p.boundary;$('operation-details').replaceChildren(node('p',`${selectedView().copy.name} / ${e.name}`));if(kind==='install')$('operation-details').append(node('p',`安装登记：${p.before||'无'} → ${p.after}`));detail($('operation-details'),'本次明确输入',input);detail($('operation-details'),'确认身份',{plan_id:p.id,digest:p.digest});$('operation-confirm').textContent='确认此独立准备';$('operation').hidden=false;$('operation').scrollIntoView?.({block:'nearest'});say('预览尚未执行。核对原合同与替换影响后确认，或取消。');}
  catch(e){if(g===epoch){clearOperation();say(e.message);}}finally{ctx.setBusy(false);sync();}
 }
 function prepareFinal(){if(!ready()||!proposal?.can_apply)return;clearOperation();operation={kind:'restore',p:{...proposal}};$('operation-title').textContent='确认'+actionName(proposal.action)+'所选成员';$('operation-note').textContent=proposal.boundary;$('operation-details').replaceChildren(node('p',`明确成员：${proposal.members.map(m=>m.copy.name).join('、')}`),node('p',`保留成员：${proposal.retained_members.map(m=>m.name).join('、')||'无'}；发布者和本机覆盖保留。`),node('p',`提案 ${proposal.id} · 摘要 ${proposal.digest}`));$('operation-confirm').textContent='确认'+actionName(proposal.action);$('operation').hidden=false;$('operation').scrollIntoView?.({block:'nearest'});sync();}
 async function confirm(){
  if(!ready()||!operation)return;const op=operation,g=++epoch;clearOperation();ctx.setBusy(true);say(op.kind==='restore'?'正在确认共同恢复，请等待真实事务结果…':'正在记录本次独立准备并重新核对条件…');let saved=false,completed=false,reconcile=false;
  try{if(op.kind==='restore'){const r=await call('confirm',{publication_id:batch.id,plan_id:op.p.id,digest:op.p.digest});if(r.publication_id!==batch.id||r.pending||r.latest_transaction!==op.p.id||!['ROLLED_BACK','UNDONE'].includes(r.status))throw Error('未取得本次共同恢复的完成证明。');saved=completed=true;}
   else{await call(op.kind+'-confirm',{...op.req,name:op.name,operation_plan_id:op.p.id,operation_digest:op.p.digest});saved=true;if(g!==epoch)return;clearCopyChecks(op.req.copy_id);clearReview();if(await loadReview(g)){const fresh=await call('show',{publication_id:batch.id,plan_id:seed.id,digest:seed.digest});if(g===epoch){verifyProposal(fresh,seed);seed=fresh;renderProposal(fresh);say('独立准备已记录，项目尚未恢复；当前授权和连接须继续检查。');}}}
  }catch(e){if(g===epoch){clearOperation();say((saved?'操作已记录，状态重读失败：':'未取得完成证明：')+e.message);if(op.kind!=='restore')clearReview();else{reconcile=true;clearProposal();checks.clear();clearReview();ctx.message?.('恢复未取得完成证明，请读取真实事务并检查中断恢复：'+e.message,true);}}}
  finally{ctx.setBusy(false);sync();}
  if((completed||reconcile)&&g===epoch){close();try{await ctx.refresh?.();}catch(e){ctx.message?.((completed?'共同恢复已提交，项目状态暂不可读：':'项目状态暂不可读：')+e.message,true);}try{await changed?.();}catch(e){ctx.message?.('请重新读取共同事务状态：'+e.message,true);}}
 }
 async function runtime(){if(!ready()||!review||$('runtime').disabled)return;const req=reviewed(),e=entry(),g=++epoch;clearOperation();ctx.setBusy(true);say('正在核验精确固定运行时的版本…');let outcome='';try{const r=await call('runtime-check',{...req,runtime:{name:e.name,probe:true,expected_sha256:review.runtime.permission_sha256,expected_review_sha256:review.runtime.review_sha256}});outcome=r.code==='RUNTIME_VERSION_MATCH'?'固定运行时核验通过。':'运行时核验未通过：'+r.code;}catch(e){outcome=e.message;}try{if(g===epoch){clearCopyChecks(req.copy_id);clearReview();if(await loadReview(g))say(outcome);}}catch(e){if(g===epoch){clearReview();say(outcome+' 状态重读失败：'+e.message);}}finally{ctx.setBusy(false);sync();}}
 function showCheck(r){
  if(r.candidate_digest!==review.candidate_digest||r.copy!==review.copy_id||r.can_apply!==false||!r.id||!r.digest)throw Error('完整检查与此原应用不一致。');
  checks.set(viewKey(selectedView()),{id:r.id,digest:r.digest,candidate:review.candidate_digest,good:r.status==='CHECKED'&&r.fresh&&r.mcp_ready});
  const box=$('check-result');box.replaceChildren(node('p',`${statusLabel(r.status)} · ${r.fresh?'本次输入匹配':'检查已过期'} · ${r.mcp_ready?'MCP 条件匹配':'MCP 仍需处理'}`));
  for(const t of r.check.tools||[])box.append(node('p',`${names[t.tool]||t.tool} · 文件 ${statusLabel(t.L1)} · 工具发现 ${statusLabel(t.L2)} · 规则 ${statusLabel(t.rules?.status)}；实际会话与能力未验证`));
  for(const f of r.gates||[])box.append(node('p',`${f.status} · ${f.code}`));renderReferences();
 }
 async function check(probe=false,read=false){
  if(!ready()||!review)return;const req=reviewed(),ref=checks.get(viewKey(selectedView())),g=++epoch;const rule=$('rule-probe').checked;probing=false;$('probe-confirm').hidden=true;clearOperation();ctx.setBusy(true);say(read?'正在重新核对此原应用的完整检查…':probe?'正在验证全部原工具的发现与所选规则来源…':'正在静态检查全部原应用配置…');
  try{const r=await call(read?'prepared':'prepare',read?{...req,preparation_id:ref.id}:{...req,preparation:{probe,rule_probe:probe&&rule}});if(g!==epoch)return;showCheck(r);say(r.status==='CHECKED'&&r.fresh&&r.mcp_ready?'此原应用检查通过；完成全部原应用后可重新生成共同恢复提案。':'检查尚未满足条件，请按门槛继续处理；未应用文件。');}
  catch(e){if(g===epoch){checks.delete(viewKey(selectedView()));$('check-result').replaceChildren();renderReferences();say(e.message);}}finally{ctx.setBusy(false);sync();}
 }
 async function regenerate(){
  if(!ready()||$('regenerate').disabled)return;const grouped=new Map();for(const v of views()){const list=grouped.get(v.copy.id)||[],ref=checks.get(viewKey(v));list.push({applied_binding:v.binding,check_id:ref.id,check_digest:ref.digest});grouped.set(v.copy.id,list);}
  const req={publication_id:batch.id,plan_id:seed.id,digest:seed.digest,checks:[...grouped].map(([copy_id,checks])=>({copy_id,checks}))},g=++epoch;clearOperation();ctx.setBusy(true);say('正在将全部原应用检查汇总为新的共同恢复提案…');
  try{const p=await call('regenerate',req);if(g!==epoch)return;verifyProposal(p);if(!p.regenerated)throw Error('未取得重新生成的共同恢复提案。');renderProposal(p);say('新参数与原恢复范围已组成共同提案，尚未恢复项目；请另行核对并确认。');}
  catch(e){if(g===epoch){clearProposal();checks.clear();renderReferences();say(e.message+'；需要重新读取并检查。');}}finally{ctx.setBusy(false);sync();}
 }
 $('node').addEventListener('change',selectionChanged);$('all').addEventListener('click',()=>{if(ready()){for(const b of boxes.values())b.checked=true;selectionChanged();}});$('plan').addEventListener('click',plan);$('read').addEventListener('click',readProposal);$('confirm-plan').addEventListener('click',prepareFinal);
 $('view').addEventListener('change',()=>{epoch++;clearReview();sync();});$('review').addEventListener('click',reviewRun);$('plugin').addEventListener('change',renderEntry);
 for(const id of ['purpose','archive','reason'])$(id).addEventListener('input',saveDraft);
 for(const id of ['risk','permissions','tools','unisolated','secrets','runtime-consent'])$(id).addEventListener('change',()=>{clearOperation();sync();});
 for(const kind of ['artifact','install','permission'])$(kind+'-plan').addEventListener('click',()=>setupPlan(kind));
 $('runtime').addEventListener('click',runtime);$('static').addEventListener('click',()=>check());$('prepared').addEventListener('click',()=>{if(ready()&&!$('prepared').disabled)return check(false,true);});
 $('probe').addEventListener('click',()=>{if(ready()&&review){probing=true;$('rule-probe').checked=false;$('probe-confirm').hidden=false;sync();}});$('probe-run').addEventListener('click',()=>{if(probing&&ready())return check(true);});$('probe-cancel').addEventListener('click',()=>{if(idle()){probing=false;$('probe-confirm').hidden=true;sync();}});
 $('regenerate').addEventListener('click',regenerate);$('operation-confirm').addEventListener('click',confirm);$('operation-cancel').addEventListener('click',()=>{if(idle()){consent();say('已取消，独立准备和项目文件未因本次预览改变。');sync();}});
 $('close').addEventListener('click',()=>{if(idle())close();});$('dialog').addEventListener('cancel',e=>{e.preventDefault();if(idle())close();});
 return{init(context,onChanged){ctx=context;changed=onChanged;close();},open,invalidate:close,sync};
})();
