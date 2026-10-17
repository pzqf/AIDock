'use strict';
const ReplayPreparation=(()=>{
 const $=id=>document.getElementById('replay-preparation-'+id),node=(tag,text)=>{const n=document.createElement(tag);n.textContent=text;return n;};
 let ctx=null,candidate=null,report=null,epoch=0;const ids=new Map();
 const ready=()=>ctx&&!ctx.busy()&&candidate&&!ctx.pending?.();
 const evidence=value=>({PASS:'通过',FAIL:'失败',UNKNOWN:'未验证',NOT_APPLIED:'未应用',SKIPPED:'不适用'}[value]||value);
 function clearResults(){epoch++;report=null;$('results').replaceChildren();$('status').textContent='';$('probe').checked=false;$('rules').checked=false;sync();}
 function reset(){clearResults();candidate=null;$('id').value='';sync();}
 function sync(){
  const ok=ready();$('static').disabled=!ok;$('id').disabled=!ok;$('read').disabled=!ok||!$('id').value.trim();
  const canProbe=ok&&report?.fresh&&report.candidate_digest===candidate.candidate_digest&&report.status!=='BLOCKED'&&report.status!=='STALE';
  $('probe').disabled=!canProbe;$('rules').disabled=!canProbe||!$('probe').checked||!report?.check.tools.some(t=>t.rules);
  $('check').disabled=!canProbe||!$('probe').checked;
  $('plan').disabled=!ok||!report?.fresh||report.status!=='CHECKED'||typeof ProjectReplayView==='undefined';
 }
 function render(){
  const label={CHECKED:'候选检查已通过',NEEDS_CHECK:'静态编译完成，尚未进行工具发现',NEEDS_ATTENTION:'部分条件仍需处理',BLOCKED:'候选存在阻断',STALE:'检查已过期，需重新准备'};
  $('status').textContent=(label[report.status]||report.status)+(report.fresh&&report.status==='CHECKED'?'。可预览共同写入提案，确认后才会应用。':'。请先完成全部检查。');
  $('id').value=report.id;ids.set(JSON.stringify(candidate),report.id);$('results').replaceChildren();
  $('results').append(node('p','快照工具：'+report.tools.join('、')));
  for(const t of report.check.tools)$('results').append(node('p',report.fresh?`${t.tool} · 文件${evidence(t.L1)} · 隔离发现${evidence(t.L2)}${t.rules?' · 规则来源'+evidence(t.rules.status):''} · L3/L4 未验证`:`${t.tool} · 历史检查已过期，当前状态未验证`));
  for(const g of report.gates)$('results').append(node('p','待处理：'+g.code));
  if(!report.fresh){$('results').append(node('p','历史结果已失效，旧差异不再展示；重读不会自动重新运行进程。'));return;}
  const details=node('details','');details.append(node('summary','查看候选源、本机覆盖与工具文件差异摘要'));
  details.append(node('pre',JSON.stringify({source_changes:report.source_changes,local_changes:report.local_changes,native_changes:report.native_changes},null,2)));$('results').append(details);
  $('results').append(node('p',report.boundary));
 }
 async function prepare(probe){
  if(!ready()||probe&&$('check').disabled)return;const g=++epoch,body={...candidate,preparation:{probe,rule_probe:probe&&$('rules').checked}};
  report=null;$('results').replaceChildren();$('probe').checked=false;$('rules').checked=false;$('status').textContent=probe?'正在隔离项目中检查完整候选…':'正在重新编译冻结候选并核对完整输入…';ctx.setBusy(true);
  try{const data=await ctx.api('/api/snapshots/replay/prepare',body);if(g!==epoch)return;report=data.items;render();}
  catch(e){if(g===epoch){report=null;$('results').replaceChildren();$('status').textContent=e.message+'；请重新审查候选后重试。';}}
  finally{ctx.setBusy(false);sync();}
 }
 async function read(){
  if(!ready()||$('read').disabled)return;const id=$('id').value.trim(),g=++epoch;report=null;$('results').replaceChildren();$('probe').checked=false;$('rules').checked=false;$('status').textContent='正在复核这条历史检查的完整输入，不运行进程…';ctx.setBusy(true);
  try{const data=await ctx.api('/api/snapshots/replay/prepared',{id});if(g!==epoch)return;if(data.items.candidate_digest!==candidate.candidate_digest||JSON.stringify(data.items.restore||null)!==JSON.stringify(candidate.restore||null))throw Error('记录属于另一个候选或恢复目标；请先重新审查对应快照');report=data.items;render();}
  catch(e){if(g===epoch){report=null;$('results').replaceChildren();$('status').textContent=e.message;}}
  finally{ctx.setBusy(false);sync();}
 }
 $('static').addEventListener('click',()=>prepare(false));$('check').addEventListener('click',()=>prepare(true));$('read').addEventListener('click',read);
 $('plan').addEventListener('click',()=>{if(ready()&&!$('plan').disabled)return ProjectReplayView.prepare(report);});
 $('probe').addEventListener('change',()=>{if(!$('probe').checked)$('rules').checked=false;sync();});$('rules').addEventListener('change',sync);
 $('id').addEventListener('input',()=>{clearResults();sync();});
 return{init(context){ctx=context;reset();},setCandidate(value){if(JSON.stringify(candidate)!==JSON.stringify(value)){clearResults();candidate=value;$('id').value=ids.get(JSON.stringify(value))||'';}else candidate=value;sync();},clearResults,reset,sync};
})();
