'use strict';
const ReplicaCandidate=(()=>{
 const $=id=>document.getElementById(id),node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
 let ctx=null,resolution=null,review=null,plan=null,epoch=0;
 const drafts=new Map(),ready=()=>ctx&&!ctx.busy()&&!ctx.pending?.();
 const request=()=>({plan_id:resolution.id,digest:resolution.digest,copy_id:$('replica-candidate-copy').value});
 const entry=()=>review?.permission.entries.find(e=>e.name===$('replica-candidate-plugin').value);
 const artifact=()=>review?.artifacts.artifacts.find(e=>e.name===entry()?.name);
 const installed=()=>review?.installation.installations.find(e=>e.record.plugin.name===entry()?.name);
 const installCandidate=()=>review?.installation.candidates.find(e=>e.plugin.name===entry()?.name);
 const key=()=>review?.candidate_digest+'|'+$('replica-candidate-copy').value+'|'+$('replica-candidate-plugin').value;
 const say=text=>{$('replica-candidate-status').textContent=text;};
 const label=s=>({CURRENT:'与候选匹配',STALE:'与候选不匹配',MISSING:'尚未准备',NONE:'尚未授权',MATCHED:'完整包匹配',OCCUPIED:'目标目录有其他内容',RECOVERY_REQUIRED:'需先恢复'}[s]||s);
 const fails=checks=>Boolean(checks?.some(c=>c.status==='FAIL'));
 function clearPlan(){plan=null;$('replica-candidate-plan').hidden=true;$('replica-candidate-plan-details').replaceChildren();sync();}
 function clearConsent(){for(const n of ['allow','permissions','tools','unisolated','secrets','runtime-consent'])$('replica-candidate-'+n).checked=false;clearPlan();}
 function clearReview(){review=null;clearConsent();if(typeof ReplicaCandidatePreparation!=='undefined')ReplicaCandidatePreparation.clear();for(const id of ['binding','summary','checks','inputs'])$('replica-candidate-'+id).replaceChildren();$('replica-candidate-plugin').replaceChildren();$('replica-candidate-actions').hidden=true;}
 function close(){epoch++;clearReview();resolution=null;$('replica-candidate-dialog').close?.();$('replica-candidate-dialog').hidden=true;}
 function invalidate(){close();}
 function sync(){
  const e=entry(),a=artifact(),busy=!ready(),risk=installCandidate()?.check.trust_level!=='L2';
  for(const n of $('replica-candidate-dialog').querySelectorAll?.('button,input,select,textarea')||[])n.disabled=busy;
  $('replica-candidate-reload').disabled=busy||!resolution;
  $('replica-candidate-artifact-plan').disabled=busy||!review||!a||a.recovery_required||!['MISSING','EMPTY'].includes(a.status)||!$('replica-candidate-archive').value.trim();
  $('replica-candidate-install-plan').disabled=busy||!review||!e||a?.status!=='MATCHED'||review.installation.recovery_required||fails(review.installation.checks)||installed()?.status==='CURRENT'||risk&&(!$('replica-candidate-allow').checked||!$('replica-candidate-reason').value.trim());
  const grant=ready()&&review&&e&&e.installation==='CURRENT'&&!review.permission.recovery_required&&!fails(review.permission.checks);
  $('replica-candidate-permission-plan').disabled=!grant||!$('replica-candidate-purpose').value.trim()||!$('replica-candidate-permissions').checked||e.tool_policy&&!$('replica-candidate-tools').checked||e.tier===0&&!$('replica-candidate-unisolated').checked||e.secrets.length&&!$('replica-candidate-secrets').checked;
  const runtime=grant&&e.authorization==='CURRENT'&&e.tier===0&&e.unisolated?.kind==='node'&&e.unisolated.source_type==='local'&&!review.runtime.runtime.recovery_required;
  $('replica-candidate-runtime-consent').disabled=!runtime;$('replica-candidate-runtime-check').disabled=!runtime||!$('replica-candidate-runtime-consent').checked;
  $('replica-candidate-confirm').disabled=busy||!plan;$('replica-candidate-cancel').disabled=busy;
  if(typeof ReplicaCandidatePreparation!=='undefined')ReplicaCandidatePreparation.sync();
 }
 function detail(parent,title,value){const d=node('details');d.className='native-rule-preview';d.append(node('summary',title),node('pre',typeof value==='string'?value:JSON.stringify(value,null,2)));parent.append(d);}
 function saveDraft(){if(!entry())return;drafts.set(key(),{archive:$('replica-candidate-archive').value,reason:$('replica-candidate-reason').value,purpose:$('replica-candidate-purpose').value});clearPlan();sync();}
 function renderEntry(){
  clearConsent();const e=entry(),a=artifact(),i=installed(),c=installCandidate(),draft=drafts.get(key())||{};
  for(const name of ['archive','reason','purpose'])$('replica-candidate-'+name).value=draft[name]||'';
  $('replica-candidate-actions').hidden=!e;$('replica-candidate-summary').replaceChildren();$('replica-candidate-inputs').replaceChildren();
  if(!e){$('replica-preparation-section').open=true;sync();return;}
  $('replica-candidate-summary').append(node('p',`${e.name} · ${e.enabled?'候选启用':'候选停用'} · 完整包：${label(a?.status||'MISSING')} · 安装：${label(i?.status||'MISSING')} · 授权：${label(e.authorization)}`));
  const runtime=review.runtime.runtime.entries.find(v=>v.event.input.name===e.name);
  const runtimeReady=runtime?.current&&runtime.status==='PASS'&&runtime.event.code==='RUNTIME_VERSION_MATCH';
  $('replica-candidate-summary').append(node('p',runtimeReady?'运行时版本与本次候选授权匹配':runtime?'运行时记录未通过或已过期':'运行时尚未核验'));
  $('replica-candidate-package-section').open=a?.status!=='MATCHED';$('replica-candidate-install-section').open=i?.status!=='CURRENT';$('replica-candidate-permission-section').open=e.installation==='CURRENT'&&e.authorization!=='CURRENT';$('replica-candidate-runtime-section').open=e.authorization==='CURRENT'&&!runtimeReady;
  $('replica-preparation-section').open=a?.status==='MATCHED'&&i?.status==='CURRENT'&&e.authorization==='CURRENT'&&Boolean(runtimeReady);
  if(c)$('replica-candidate-summary').append(node('p',`精确版本 ${c.plugin.version} · 来源 ${c.check.trust_level||'L0'}`));
  if(i&&i.status!=='CURRENT')$('replica-candidate-summary').append(node('p',`目标现有安装登记为 ${i.record.plugin.version}。确认安装候选将替换同名登记，旧配置的安装与授权随后失效；旧包保留，可在项目安装历史中重新审查并回退。`));
  if(e.replaces_grant)$('replica-candidate-summary').append(node('p','候选授权确认会替换此目标的同名旧授权；旧配置随后无法再用旧授权运行。'));
  detail($('replica-candidate-inputs'),'权限、工具策略与凭据引用',{permissions:e.permissions,tier:e.tier,tool_policy:e.tool_policy,secret_references:e.secrets,unisolated_runtime:e.unisolated});
  if(a)detail($('replica-candidate-inputs'),'目标产物目录与完整摘要',{root:a.root,artifact_sha256:c?.plugin.artifact.sha256,current_tree_sha256:a.sha256});
  if(c){detail($('replica-candidate-inputs'),'精确来源、入口、运行时与锁',c.plugin);detail($('replica-candidate-inputs'),'包清单核验',c.manifest);}
  $('replica-candidate-tools-row').hidden=!e.tool_policy;$('replica-candidate-unisolated-row').hidden=e.tier!==0;$('replica-candidate-secrets-row').hidden=!e.secrets.length;
  $('replica-candidate-risk-note').textContent=c?.check.trust_level==='L2'?'当前证据达到默认 L2，可正常预览登记。':'当前证据低于默认 L2；安装需明确接受风险并填写理由，不授予运行权限。';
  $('replica-candidate-runtime-note').textContent='明确确认后只运行此目标已授权、锁定的 Node 运行时版本检查；不启动插件。其它运行时或隔离模式保持未验证。';sync();
 }
 function render(){
  $('replica-candidate-binding').replaceChildren(node('p',`目标 ${review.copy.name} · ${review.copy.id}`),node('code',review.copy.root),node('p',`候选 ${review.candidate_digest}`));
  const select=$('replica-candidate-plugin'),previous=select.value;select.replaceChildren();const empty=node('option','请选择 MCP');empty.value='';select.append(empty);
  for(const e of review.permission.entries){const option=node('option',e.name);option.value=e.name;select.append(option);}
  select.value=review.permission.entries.some(e=>e.name===previous)?previous:review.permission.entries.length===1?review.permission.entries[0].name:'';
  $('replica-candidate-checks').replaceChildren();const findings=[...(review.installation.checks||[]),...(review.permission.checks||[]),...(review.runtime.checks||[])],seen=new Set();
  for(const f of findings){const k=f.status+'|'+f.code+'|'+f.subject;if(seen.has(k))continue;seen.add(k);$('replica-candidate-checks').append(node('p',`${f.status} · ${f.code}${f.subject?' · '+f.subject:''}`));}
  $('replica-candidate-boundary').textContent=review.boundary;
  if(typeof ReplicaCandidatePreparation!=='undefined')ReplicaCandidatePreparation.bind(request(),review.candidate_digest,JSON.stringify({permission:review.permission,installation:review.installation.sha256,checks:review.installation.checks,runtime:review.runtime}),review.permission.entries.filter(e=>e.enabled).map(e=>e.name));renderEntry();
 }
 async function read(g){const result=await ctx.api('/api/replica/candidate/show',request());if(g!==epoch)return false;review=result.items;render();return true;}
 async function reload(){if(!ready()||!resolution)return;const g=++epoch;clearReview();ctx.setBusy(true);say('正在重新核对原候选、目标配置层与独立准备记录…');try{if(await read(g))say('已核对目标未来配置。逐项准备不会应用源或工具文件。');}catch(e){if(g===epoch){clearReview();say(e.message);}}finally{ctx.setBusy(false);sync();}}
 function open(p){
  if(!ready()||!p?.targets?.length)return;close();resolution=p;const select=$('replica-candidate-copy');select.replaceChildren();for(const t of p.targets){const option=node('option',t.copy.name);option.value=t.copy.id;select.append(option);}select.value=p.targets[0].copy.id;
  $('replica-candidate-dialog').hidden=false;$('replica-candidate-dialog').showModal?.();$('replica-candidate-dialog').scrollTop=0;return reload();
 }
 async function preview(kind){
  if(!ready()||!review||$('replica-candidate-'+kind+'-plan').disabled)return;const e=entry(),req=request(),g=epoch;let edit;
  if(kind==='artifact')edit={action:'import',name:e.name,archive_path:$('replica-candidate-archive').value.trim(),expected_sha256:artifact().sha256,expected_lock_sha256:review.artifacts.lock_sha256};
  if(kind==='install')edit={action:'install',name:e.name,expected_sha256:review.installation.sha256,allow_unverified:$('replica-candidate-allow').checked,risk_reason:$('replica-candidate-reason').value.trim()};
  if(kind==='permission'){edit={action:'grant',name:e.name,expected_sha256:review.permission.sha256,expected_review_sha256:review.permission.review_sha256,purpose:$('replica-candidate-purpose').value.trim(),data_level:'Public',destination:'unknown-external-client'};for(const n of ['permissions','tools','unisolated','secrets'])edit['confirm_'+n]=$('replica-candidate-'+n).checked;}
  clearPlan();ctx.setBusy(true);try{const response=await ctx.api('/api/replica/candidate/'+kind+'-plan',{...req,edit});if(g!==epoch)return;const p=response.items.plan;plan={kind,name:e.name,p,request:req};const list=$('replica-candidate-plan-details');list.append(node('p',`${review.copy.name} / ${e.name} · ${{artifact:'导入精确版本 ZIP',install:'登记目标未来版本',permission:'更新目标未来配置授权'}[kind]}`),node('p',`计划 ${p.id} · 摘要 ${p.digest}`));
   for(const c of p.changes||[])list.append(node('p',`${c.before?(c.after?'更新':'移除'):'新增'} · ${c.path}`));
   if(kind==='install'){const before=response.items.before?.installations.find(i=>i.record.plugin.name===e.name),after=response.items.candidate?.installations.find(i=>i.record.plugin.name===e.name);list.append(node('p',`目标安装登记：${before?before.record.plugin.version:'尚未登记'} → ${after?.record.plugin.version||installCandidate()?.plugin.version}。确认后将替换同名现有安装；旧包保留，当前旧配置可能需要恢复安装及重新授权。`));detail(list,'本次风险确认',{allow_unverified:edit.allow_unverified,risk_reason:edit.risk_reason});}if(kind==='permission')detail(list,'本次授权',edit);
   $('replica-candidate-plan').hidden=false;$('replica-candidate-plan').scrollIntoView?.({block:'nearest'});say('预览未执行。核对目标、动作与内容后确认，或取消保留草稿。');
  }catch(e){if(g===epoch){clearReview();say(e.message+'；请重新核对候选，输入草稿保留。');}}finally{ctx.setBusy(false);sync();}
 }
 async function confirm(){
  if(!ready()||!plan)return;const p=plan,g=epoch;clearPlan();ctx.setBusy(true);let saved=false;
  try{await ctx.api('/api/replica/candidate/'+p.kind+'-confirm',{...p.request,name:p.name,operation_plan_id:p.p.id,operation_digest:p.p.digest});saved=true;if(g!==epoch)return;clearReview();if(await read(g))say('此目标的独立准备已记录，源和工具文件尚未应用；需要时可从项目产物、安装或授权入口恢复。');}
  catch(e){if(g===epoch){clearReview();say((saved?'操作已记录，但状态重读失败：':'')+e.message+'；请重新核对候选。');}}finally{ctx.setBusy(false);sync();}
 }
 async function runtime(){
  if(!ready()||!review||$('replica-candidate-runtime-check').disabled)return;const req=request(),g=epoch,e=entry(),check={name:e.name,probe:true,expected_sha256:review.runtime.permission_sha256,expected_review_sha256:review.runtime.review_sha256};clearConsent();ctx.setBusy(true);let outcome='';
  try{await ctx.api('/api/replica/candidate/runtime-check',{...req,check});outcome='运行时版本核验已记录。';}catch(e){outcome='本次版本核验未通过：'+e.message+'。';}
  try{if(g===epoch){clearReview();if(await read(g))say(outcome);}}catch(e){if(g===epoch){clearReview();say(outcome+' 状态重读失败：'+e.message);}}finally{ctx.setBusy(false);sync();}
 }
 $('replica-candidate-copy').addEventListener('change',reload);$('replica-candidate-plugin').addEventListener('change',renderEntry);
 for(const name of ['archive','reason','purpose'])$('replica-candidate-'+name).addEventListener('input',saveDraft);
 for(const name of ['allow','permissions','tools','unisolated','secrets','runtime-consent'])$('replica-candidate-'+name).addEventListener('change',()=>{clearPlan();sync();});
 for(const kind of ['artifact','install','permission'])$('replica-candidate-'+kind+'-plan').addEventListener('click',()=>preview(kind));
 $('replica-candidate-reload').addEventListener('click',reload);$('replica-candidate-confirm').addEventListener('click',confirm);$('replica-candidate-runtime-check').addEventListener('click',runtime);
 $('replica-candidate-cancel').addEventListener('click',()=>{if(ready()){clearConsent();say('已取消，独立准备记录未修改，输入草稿保留。');}});
 $('replica-candidate-close').addEventListener('click',()=>{if(ready())close();});$('replica-candidate-dialog').addEventListener('cancel',e=>{e.preventDefault();if(ready())close();});
 return{init(context){ctx=context;if(typeof ReplicaCandidatePreparation!=='undefined')ReplicaCandidatePreparation.init(context);invalidate();},open,invalidate,sync};
})();
