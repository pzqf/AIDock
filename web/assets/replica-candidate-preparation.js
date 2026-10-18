'use strict';
const ReplicaCandidatePreparation=(()=>{
 const $=id=>document.getElementById(id),node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
 const names={'claude-code':'Claude Code','codex-cli':'Codex CLI'},labels={CHECKED:'完整候选检查通过',NEEDS_CHECK:'还需明确验证工具发现',NEEDS_ATTENTION:'需要处理检查门槛',BLOCKED:'存在阻断项',STALE:'原检查已过期'};
 let ctx=null,binding=null,candidate='',fingerprint='',epoch=0,report=null,last=null,probe=null;
 const saved=new Map(),ready=()=>ctx&&!ctx.busy()&&!ctx.pending?.(),key=()=>candidate+'|'+binding?.copy_id;
 const tools=()=>['claude-code','codex-cli'].filter((t,i)=>$('replica-preparation-'+['claude','codex'][i]).checked);
 const say=s=>{$('replica-preparation-status').textContent=s;};
 function closeProbe(){probe=null;$('replica-preparation-probe-dialog').close?.();$('replica-preparation-probe-dialog').hidden=true;}
 function clearResult(){report=null;$('replica-preparation-result').hidden=true;for(const id of ['binding','tools','gates','changes'])$('replica-preparation-'+id).replaceChildren();}
 function clear(){epoch++;binding=null;candidate='';fingerprint='';last=null;clearResult();closeProbe();$('replica-preparation-enabled').textContent='';say('选择并读取目标未来配置后，可编译完整候选。');sync();}
 function bind(req,digest,stamp,enabled=[]){
  const changed=candidate!==digest||binding?.copy_id!==req.copy_id||fingerprint!==stamp;
  binding={...req};candidate=digest;fingerprint=stamp;last=saved.get(key())||null;
  if(changed){epoch++;clearResult();closeProbe();$('replica-preparation-enabled').textContent=enabled.length?'本目标启用 MCP：'+enabled.join('、')+'；完整检查不会省略未准备的项。':'本目标没有启用 MCP；仍核对旧托管连接的处理。';say(last?'有此目标的原检查；请明确重读核对当前有效性。':'默认只编译与静态核对。工具发现需另行明确确认。');}
  sync();
 }
 function sync(){
  if(!ctx)return;const disabled=!ready();
  for(const id of ['claude','codex'])$('replica-preparation-'+id).disabled=disabled||!binding;
  $('replica-preparation-run').disabled=disabled||!binding||!tools().length;
  $('replica-preparation-probe').disabled=disabled||!binding||!tools().length;
  $('replica-preparation-read').disabled=disabled||!binding||!last;
  $('replica-preparation-probe-run').disabled=disabled||!probe;
  $('replica-preparation-probe-cancel').disabled=disabled;$('replica-preparation-rules').disabled=disabled||!probe;
 }
 function render(r){
  if(typeof ReplicaFullPublication!=='undefined')ReplicaFullPublication.remember(binding,r);report=r;last={id:r.id,digest:r.digest};saved.set(key(),last);
  $('replica-preparation-result').hidden=false;
  say((labels[r.status]||r.status)+' · '+(r.fresh?'本次输入仍匹配':'需重新比较或检查')+' · 尚未正式同步');
  $('replica-preparation-binding').replaceChildren(node('p',`检查 ${r.id} · 摘要 ${r.digest}`),node('p',`副本 ${r.copy} · 候选 ${r.candidate_digest}`),node('p',r.boundary));
  $('replica-preparation-tools').replaceChildren();
  const evidence=s=>({PASS:'通过',UNKNOWN:'未验证',NOT_APPLIED:'尚未安装',FAIL:'未通过',STALE:'已过期'}[s]||s||'未验证');
  for(const t of r.check.tools||[]){const card=node('article');card.className='native-check-card';card.append(node('h4',(names[t.tool]||t.tool)+' · '+(t.version||'版本未验证')),node('p',`候选文件：${evidence(t.L1)} · ${r.fresh?'':'历史'}工具发现：${evidence(t.L2)}`),node('p',`规则来源：${evidence(t.rules?.status)} · 实际会话与能力：未验证`));const d=node('details');d.append(node('summary','检查说明'));if(t.rules)d.append(node('p',`${t.rules.path} · ${t.rules.detail}`));for(const f of t.findings||[])d.append(node('p',`${evidence(f.status)} · ${f.detail}`));card.append(d);$('replica-preparation-tools').append(card);}
  $('replica-preparation-gates').replaceChildren(node('p',!r.fresh?'原检查已过期，需要重新核对完整候选。':r.mcp_ready?((r.mcp_names||[]).length?'全部启用 MCP 的当前安装、独立授权、固定运行时与连接编译匹配。':'没有启用 MCP，已核对旧托管连接的处理。'):'部分 MCP 安装、独立授权、运行时或连接编译仍需处理；不能省略后视为完整候选通过。'));
  for(const g of r.gates||[])$('replica-preparation-gates').append(node('p',`${g.status} · ${g.code}`));
  $('replica-preparation-changes').replaceChildren();for(const item of r.native_changes||[]){const d=node('details');d.append(node('summary',`${item.path} · ${item.changes.length} 项候选变化`));for(const c of item.changes)d.append(node('p',`${c.before?(c.after?'修改':'移除'):'新增'} · ${c.path}`));$('replica-preparation-changes').append(d);}sync();
 }
 function verify(r){if(!binding||r.candidate_digest!==candidate||r.copy!==binding.copy_id||r.can_apply!==false||!r.id||!r.digest)throw Error('完整检查返回的候选、目标或应用边界不一致；请重新读取。');}
 async function run(probeRequest=null){
  if(!ready()||!binding)return;const req={...binding},selected=probeRequest?.tools||tools();if(!selected.length)return;const g=++epoch;
  const preparation={tools:selected,probe:Boolean(probeRequest),...(probeRequest?.rules?{rule_probe:true}:{})};clearResult();closeProbe();ctx.setBusy(true);say(preparation.probe?'正在明确验证临时项目中的候选工具发现…':'正在编译未来配置并静态核对全部启用项…');
  try{const data=await ctx.api('/api/replica/candidate/prepare',{...req,preparation});if(g!==epoch)return;verify(data.items);render(data.items);}
  catch(e){if(g===epoch){if(typeof ReplicaFullPublication!=='undefined')ReplicaFullPublication.remember(binding,null);clearResult();say(e.message+'；未应用源或工具文件。');}}finally{ctx.setBusy(false);sync();}
 }
 function previewProbe(){if(!ready()||!binding||!tools().length)return;probe={tools:tools(),epoch};$('replica-preparation-rules').checked=false;$('replica-preparation-probe-scope').textContent=`目标 ${binding.copy_id} · ${probe.tools.map(t=>names[t]).join('、')} · 候选 ${candidate}`;$('replica-preparation-probe-dialog').hidden=false;$('replica-preparation-probe-dialog').showModal?.();sync();}
 async function confirmProbe(){if(!ready()||!probe||probe.epoch!==epoch)return;const p={...probe,rules:$('replica-preparation-rules').checked};return run(p);}
 async function read(){
  if(!ready()||!binding||!last)return;const req={...binding,preparation_id:last.id},g=++epoch;clearResult();closeProbe();ctx.setBusy(true);say('正在重读原检查并核对当前候选、授权、版本及原生文件…');
  try{const data=await ctx.api('/api/replica/candidate/prepared',req);if(g!==epoch)return;verify(data.items);render(data.items);}
  catch(e){if(g===epoch){if(typeof ReplicaFullPublication!=='undefined')ReplicaFullPublication.remember(binding,null);clearResult();say(e.message+'；旧通过结果已清除，未重跑工具。');}}finally{ctx.setBusy(false);sync();}
 }
 for(const id of ['claude','codex'])$('replica-preparation-'+id).addEventListener('change',()=>{if(typeof ReplicaFullPublication!=='undefined')ReplicaFullPublication.remember(binding,null);epoch++;clearResult();closeProbe();say('工具选择已变化；重新运行检查，或明确重读原检查。');sync();});
 $('replica-preparation-run').addEventListener('click',()=>run());$('replica-preparation-read').addEventListener('click',read);$('replica-preparation-probe').addEventListener('click',previewProbe);$('replica-preparation-probe-run').addEventListener('click',confirmProbe);
 $('replica-preparation-probe-cancel').addEventListener('click',()=>{if(ready()){closeProbe();sync();}});$('replica-preparation-probe-dialog').addEventListener('cancel',e=>{e.preventDefault();if(ready()){closeProbe();sync();}});
 return{init(context){ctx=context;clear();},bind,clear,sync};
})();
