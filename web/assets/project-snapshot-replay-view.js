'use strict';
const ProjectSnapshotReplay=(()=>{
 const $=id=>document.getElementById(id),node=(tag,text)=>{const n=document.createElement(tag);n.textContent=text;return n;};
 const consent=['permissions','tools','unisolated','secrets'];
 const installationLabel=s=>({CURRENT:'安装匹配',STALE:'安装已过期',MISSING:'缺少有效安装'}[s]||s),authorizationLabel=s=>({CURRENT:'与候选匹配',STALE:'与候选不匹配',NONE:'尚未授权'}[s]||s);
 let ctx=null,epoch=0,snapshot='',review=null,plan=null,runtimeReview=null;const purposes=new Map();
 const ready=()=>ctx&&!ctx.busy(),selected=()=>review?.entries.find(e=>e.name===$('snapshot-replay-plugin').value),say=s=>{$('snapshot-replay-status').textContent=s;};
 function clearPlan(){plan=null;$('snapshot-replay-plan').hidden=true;$('snapshot-replay-apply').hidden=true;$('snapshot-replay-cancel').hidden=true;}
 function resetConsent(){for(const name of consent)$('snapshot-replay-confirm-'+name).checked=false;clearPlan();}
 function clearClients(){$('snapshot-replay-clients').replaceChildren();$('snapshot-replay-clients-status').textContent='';}
 function clearRuntime(){runtimeReview=null;$('snapshot-replay-runtime-consent').checked=false;$('snapshot-replay-runtime-entries').replaceChildren();$('snapshot-replay-runtime-status').textContent='';clearClients();}
 function runtimeMatches(){return runtimeReview&&review&&runtimeReview.candidate_digest===review.candidate_digest&&runtimeReview.permission_sha256===review.sha256&&runtimeReview.review_sha256===review.review_sha256&&!runtimeReview.runtime.recovery_required&&!runtimeReview.checks.some(c=>c.status==='FAIL');}
 function latestRuntime(name){return runtimeReview?.runtime.entries.find(e=>e.event.input.name===name);}
 function allVersions(){return runtimeMatches()&&review.entries.filter(e=>e.enabled).every(e=>{const v=latestRuntime(e.name);return e.authorization==='CURRENT'&&e.installation==='CURRENT'&&v?.current&&v.status==='PASS'&&v.event.code==='RUNTIME_VERSION_MATCH';});}
 function clearObservation(){clearRuntime();$('snapshot-replay-runtime-selected').textContent='';review=null;resetConsent();for(const id of ['snapshot-replay-binding','snapshot-replay-inputs','snapshot-replay-selection','snapshot-replay-boundary'])$(id).textContent='';$('snapshot-replay-plugin').replaceChildren();$('snapshot-replay-purpose').value='';}
 function close(){epoch++;clearRuntime();review=null;clearPlan();$('snapshot-replay-dialog').close?.();$('snapshot-replay-dialog').hidden=true;}
 function invalidate(){close();sync();}
 function sync(){
  const e=selected(),allowed=ready()&&!review?.recovery_required&&!review?.checks.some(c=>c.status==='FAIL')&&!ctx?.pending?.()&&e?.installation==='CURRENT';
  $('snapshot-replay-close').disabled=!ready();$('snapshot-replay-reload').disabled=!ready()||!snapshot;
  $('snapshot-replay-plugin').disabled=!ready()||!review;$('snapshot-replay-purpose').disabled=!allowed;
  for(const name of consent)$('snapshot-replay-confirm-'+name).disabled=!allowed;
  $('snapshot-replay-preview').disabled=!allowed||!$('snapshot-replay-purpose').value.trim()||!$('snapshot-replay-confirm-permissions').checked||Boolean(e?.tool_policy&&!$('snapshot-replay-confirm-tools').checked)||Boolean(e?.tier===0&&!$('snapshot-replay-confirm-unisolated').checked)||Boolean(e?.secrets?.length&&!$('snapshot-replay-confirm-secrets').checked);
  $('snapshot-replay-apply').disabled=!ready()||!plan;$('snapshot-replay-cancel').disabled=!ready();
  $('snapshot-replay-runtime-read').disabled=!ready()||!review;
  const canProbe=allowed&&runtimeMatches()&&e?.authorization==='CURRENT'&&e?.tier===0&&e?.unisolated?.kind==='node'&&e?.unisolated?.source_type==='local';
  $('snapshot-replay-runtime-consent').disabled=!canProbe;
  $('snapshot-replay-runtime-check').disabled=!canProbe||!$('snapshot-replay-runtime-consent').checked;
  $('snapshot-replay-clients-read').disabled=!ready()||!allVersions()||Boolean(ctx?.pending?.());
 }
 function renderEntry(){
  resetConsent();$('snapshot-replay-runtime-consent').checked=false;clearClients();const e=selected();$('snapshot-replay-purpose').value=purposes.get(snapshot+'|'+e?.name)||'';
  $('snapshot-replay-selection').textContent=e?`${e.enabled?'候选启用':'候选停用'} · ${installationLabel(e.installation)} · 授权${authorizationLabel(e.authorization)}${e.replaces_grant?' · 确认将替换同名现有授权':''}`:'请明确选择一个 MCP；缺少安装或安全证据的项需要先处理。';
  $('snapshot-replay-inputs').textContent=e?JSON.stringify({permissions:e.permissions,tier:e.tier,secret_references:e.secrets,tool_policy:e.tool_policy,unisolated_runtime:e.unisolated},null,2):'';
  $('snapshot-replay-runtime-selected').textContent=e?`本次选定：${e.name}${e.unisolated?.kind==='node'?' · 锁定 Node '+e.unisolated.version:''}`:'请先选择要核验的 MCP。';
  $('snapshot-replay-tools-row').hidden=!e?.tool_policy;$('snapshot-replay-unisolated-row').hidden=e?.tier!==0;$('snapshot-replay-secrets-row').hidden=!e?.secrets?.length;sync();
 }
 function render(){
  $('snapshot-replay-boundary').textContent=review.boundary;$('snapshot-replay-binding').textContent=`项目 ${review.project} / ${review.scope} / 副本 ${review.copy}\n项目源 ${review.source_root}\n快照 ${review.snapshot_id}\n候选 ${review.candidate_digest}`;
  const select=$('snapshot-replay-plugin'),previous=select.value;select.replaceChildren();const placeholder=node('option','请选择 MCP');placeholder.value='';select.append(placeholder);
  for(const e of review.entries){const option=node('option',`${e.name} · ${e.enabled?'候选启用':'候选停用'} · ${installationLabel(e.installation)}`);option.value=e.name;select.append(option);}
  select.value=review.entries.some(e=>e.name===previous)?previous:'';renderEntry();
  const failures=review.checks.filter(c=>c.status==='FAIL');say(review.recovery_required?'授权记录有待恢复事务，请先使用项目授权恢复入口。':failures.length?`依赖或安全检查存在阻断：${failures.map(c=>c.code).join('、')}`:review.entries.length?'请逐项核对后预览；此处不应用源或工具文件。':'候选没有 MCP 定义；不需要在此授权。');
 }
 async function fetchReview(id,g){const data=await ctx.api('/api/snapshots/replay/review',{snapshot_id:id});if(g!==epoch)return false;review=data.items;render();return true;}
 async function open(id){
  if(!ready())return;const g=++epoch;snapshot=id;clearObservation();$('snapshot-replay-dialog').hidden=false;$('snapshot-replay-dialog').showModal?.();say('正在重建冻结候选并核对当前安装与授权…');ctx.setBusy(true);
  try{await fetchReview(id,g);}catch(e){if(g===epoch){clearObservation();say(e.message);}}finally{ctx.setBusy(false);sync();}
 }
 async function preview(){
  if(!ready()||$('snapshot-replay-preview').disabled||!review)return;const e=selected(),g=epoch;
  const authorization={action:'grant',name:e.name,expected_sha256:review.sha256,expected_review_sha256:review.review_sha256,purpose:$('snapshot-replay-purpose').value.trim(),data_level:'Public',destination:'unknown-external-client'};
  for(const key of consent)authorization['confirm_'+key]=$('snapshot-replay-confirm-'+key).checked;
  clearPlan();ctx.setBusy(true);try{const data=await ctx.api('/api/snapshots/replay/permission/plan',{snapshot_id:snapshot,candidate_digest:review.candidate_digest,authorization});if(g!==epoch)return;plan=data.items;$('snapshot-replay-plan-boundary').textContent=plan.boundary;$('snapshot-replay-plan-digest').textContent=`计划 ${plan.id}\n确认摘要 ${plan.digest}`;$('snapshot-replay-plan').hidden=false;$('snapshot-replay-apply').hidden=false;$('snapshot-replay-apply').textContent=plan.replaces_grant?'确认替换项目授权':'确认新增候选授权';$('snapshot-replay-cancel').hidden=false;say(`预览未更改授权。确认将${plan.replaces_grant?'替换同名当前授权':'新增候选授权'}；可取消保留用途草稿。`);}catch(e){if(g===epoch){resetConsent();say(e.message);}}finally{ctx.setBusy(false);sync();}
 }
 async function apply(){
  if(!ready()||!plan)return;const p=plan,id=snapshot,g=epoch;let saved=false;clearPlan();ctx.setBusy(true);
  try{await ctx.api('/api/snapshots/replay/permission/apply',{plan_id:p.id,digest:p.digest});saved=true;if(g!==epoch)return;resetConsent();await ctx.refresh();snapshot=id;const next=++epoch;$('snapshot-replay-dialog').hidden=false;$('snapshot-replay-dialog').showModal?.();if(await fetchReview(id,next))say('候选授权及审计已更新。授权摘要已变化，现有连接需重新检查和生成；源、本机覆盖及工具文件未应用。');}
  catch(e){if(g===epoch||saved){clearObservation();if(saved){$('snapshot-replay-dialog').hidden=false;$('snapshot-replay-dialog').showModal?.();}say((saved?'授权已记录，但状态重读失败：':'')+e.message+'；请重新审查，保留用途草稿。');}}
  finally{ctx.setBusy(false);sync();}
 }

 function renderRuntime(){
  const list=$('snapshot-replay-runtime-entries');list.replaceChildren();
  for(const e of review.entries.filter(e=>e.enabled)){
   const v=latestRuntime(e.name),valid=v?.current&&v.status==='PASS'&&v.event.code==='RUNTIME_VERSION_MATCH';
   list.append(node('p',e.name+' · '+(valid?'候选版本匹配':v?(v.current?'最新记录未通过':'最新记录已过期或属于其他授权'):'尚无版本记录')+(v?' · '+(v.event.observed_version||v.event.code)+' · '+v.event.created_at:'')));
  }
  $('snapshot-replay-runtime-status').textContent=!runtimeMatches()?'授权、安装或版本记录有变化或待恢复；请重新审查候选。':allVersions()?'全部启用项的候选版本记录匹配，可以预览连接参数。':'请先完成全部启用项的候选授权与本次版本核验；历史成功不能替代当前记录。';
  if(!review.entries.some(e=>e.enabled)&&runtimeMatches())$('snapshot-replay-runtime-status').textContent='候选未启用 MCP；连接预览将返回空集合。';
 }
 async function loadRuntime(g){
  const data=await ctx.api('/api/snapshots/replay/runtime/state',{snapshot_id:snapshot,candidate_digest:review.candidate_digest});
  if(g!==epoch)return false;runtimeReview=data.items;renderRuntime();return true;
 }
 async function readRuntime(){
  if(!ready()||!review)return;const g=epoch;clearRuntime();ctx.setBusy(true);
  try{await loadRuntime(g);}catch(e){if(g===epoch){clearRuntime();$('snapshot-replay-runtime-status').textContent=e.message;}}finally{ctx.setBusy(false);sync();}
 }
 async function checkRuntime(){
  if(!ready()||$('snapshot-replay-runtime-check').disabled||!runtimeMatches())return;
  const g=epoch,name=selected().name,candidate=review.candidate_digest,sha=runtimeReview.permission_sha256,reviewSHA=runtimeReview.review_sha256;let outcome='';
  clearRuntime();ctx.setBusy(true);
  try{await ctx.api('/api/snapshots/replay/runtime/check',{snapshot_id:snapshot,candidate_digest:candidate,runtime:{name,probe:true,expected_sha256:sha,expected_review_sha256:reviewSHA}});outcome='固定版本核验已记录。';}
  catch(e){outcome='本次核验未通过：'+e.message+'。';}
  try{if(g===epoch&&await loadRuntime(g))$('snapshot-replay-runtime-status').textContent=outcome+' '+$('snapshot-replay-runtime-status').textContent;}
  catch(e){if(g===epoch){clearRuntime();$('snapshot-replay-runtime-status').textContent=outcome+' 状态重读失败：'+e.message+'；请重新读取记录。';}}
  finally{ctx.setBusy(false);sync();}
 }
 async function readClients(){
  if(!ready()||$('snapshot-replay-clients-read').disabled||!review)return;const g=epoch;clearClients();ctx.setBusy(true);
  try{const data=await ctx.api('/api/snapshots/replay/clients',{snapshot_id:snapshot,candidate_digest:review.candidate_digest});if(g!==epoch)return;const result=data.items;
   $('snapshot-replay-clients-status').textContent=result.boundary+(result.clients.length?' 共 '+result.clients.length+' 个连接。':' 候选连接为空。');
   for(let i=0;i<result.clients.length;i++){const c=result.clients[i],details=node('details','');details.append(node('summary',result.sessions[i].name+' · 候选连接参数'));details.append(node('pre',JSON.stringify({command:c.command,args:c.args,binding:c.binding},null,2)));$('snapshot-replay-clients').append(details);}
  }catch(e){if(g===epoch){clearRuntime();$('snapshot-replay-clients-status').textContent=e.message+'；请重新审查候选并读取记录。';}}
  finally{ctx.setBusy(false);sync();}
 }
 $('snapshot-replay-runtime-read').addEventListener('click',readRuntime);
 $('snapshot-replay-runtime-consent').addEventListener('change',sync);
 $('snapshot-replay-runtime-check').addEventListener('click',checkRuntime);
 $('snapshot-replay-clients-read').addEventListener('click',readClients);

 $('snapshot-replay-plugin').addEventListener('change',renderEntry);
 $('snapshot-replay-purpose').addEventListener('input',()=>{const e=selected();if(e)purposes.set(snapshot+'|'+e.name,$('snapshot-replay-purpose').value);clearPlan();sync();});
 for(const key of consent)$('snapshot-replay-confirm-'+key).addEventListener('change',()=>{clearPlan();sync();});
 $('snapshot-replay-preview').addEventListener('click',preview);$('snapshot-replay-apply').addEventListener('click',apply);
 $('snapshot-replay-reload').addEventListener('click',()=>open(snapshot));$('snapshot-replay-cancel').addEventListener('click',()=>{if(ready()){resetConsent();say('已取消预览，授权未修改，用途草稿保留。');sync();}});
 $('snapshot-replay-close').addEventListener('click',()=>{if(ready())close();});$('snapshot-replay-dialog').addEventListener('cancel',e=>{e.preventDefault();if(ready())close();});
 return {init(context,app){ctx=app.native_root?context:null;invalidate();},open,sync,invalidate};
})();
