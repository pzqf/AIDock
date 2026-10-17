'use strict';
const ReplayEntryView=(()=>{
 const $=id=>document.getElementById('replay-entry-'+id),node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
 let ctx=null,session=null,subject='',plan=null,epoch=0,completed=null;
 const ready=()=>ctx&&!ctx.busy(),selected=()=>[...$('copies').querySelectorAll('input')].filter(n=>n.checked).map(n=>n.value),say=s=>{$('status').textContent=s;};
 function sync(){const ok=ready()&&!ctx.pending?.();$('preview').disabled=!ok||!session||selected().length===0;$('reload').disabled=!ok||!plan;$('consent').disabled=!ok||!plan?.can_apply;$('commit').disabled=!ok||!plan?.can_apply||!$('consent').checked;$('cancel').disabled=!ready();for(const input of $('copies').querySelectorAll('input'))input.disabled=!ok;}
 function clearPlan(){plan=null;$('consent').checked=false;$('changes').replaceChildren();$('boundary').textContent='';sync();}
 function targetLabel(path,copies){
  if(path==='control')return '所选共同基线';
  const parts=path.split('/'),copy=parts[0]==='copies'&&copies.find(c=>c.id===parts[1]);if(!copy)return path;
  const rest=parts.slice(2).join('/'),area={'.aidock':'项目源','generated/claude-code':'Claude Code 技能包','generated/codex-cli':'Codex CLI 技能包','rules/claude-code':'Claude Code 规则','rules/codex-cli':'Codex CLI 规则'}[rest]||rest;
  return copy.name+' · '+area;
 }
 function close(){epoch++;clearPlan();$('dialog').close?.();$('dialog').hidden=true;session=null;completed=null;sync();}
 function invalidate(){close();}
 function openPublication(value,snapshot=value.id,onComplete=null){
  if(!ready()||ctx.pending?.())return;if(value.pending){ctx.message('原批次存在中断事务，请先检查并整批恢复。',true);return;}close();session=value;subject=snapshot;completed=onComplete;
  $('title').textContent=snapshot===value.id?'选择副本，预览原批次回退':'选择副本，预览该事务的回退或撤销';
  $('subject').textContent='正式批次 '+value.id+'\n恢复节点 '+snapshot+'\n发布者（只读） '+value.source.name+' · '+value.source.root;
  $('copies').replaceChildren();for(const copy of value.target_copies||[]){if(copy.id===value.source.id)continue;const label=node('label'),input=node('input');input.type='checkbox';input.value=copy.id;input.checked=true;const text=node('span',copy.name);text.append(node('small',copy.root));label.append(input,text);input.addEventListener('change',()=>{epoch++;clearPlan();say('副本选择已改变，请重新预览；当前文件保留。');});$('copies').append(label);}
  $('dialog').hidden=false;$('dialog').showModal?.();say(selected().length?'默认恢复全部原批次目标。取消勾选可局部回退，并只恢复所选副本对应的共同基线。':'此记录缺少明确成员信息，请重新读取正式批次；不能按路径猜测。');sync();
 }
 function show(value){
  if(value.kind!=='replica'||value.snapshot_id!==subject||value.publication_plan?.publication_id!==session.id)throw Error('返回提案与本次恢复节点或正式批次不一致');
  const p=value.publication_plan,expected=[...selected()].sort(),actual=(p.copies||[]).map(c=>c.id).sort();if(JSON.stringify(expected)!==JSON.stringify(actual))throw Error('返回提案改变了本次副本选择');
  plan=p;$('consent').checked=false;$('boundary').textContent=p.boundary;$('changes').replaceChildren();
  $('changes').append(node('p','恢复 '+p.copies.length+' 个副本 · 保留 '+p.retained_copies.length+' 个未选副本'));
  if(!p.can_apply){say('当前提案不能执行（'+(p.code||'需要重新预览')+'）；旧差异已隐藏。');sync();return;}
  for(const target of p.changes){const details=node('details');details.append(node('summary',targetLabel(target.path,p.copies)+' · '+target.changes.length+' 项变化'));for(const c of target.changes)details.append(node('p',(c.before?(c.after?'修改':'移除'):'新增')+' · '+c.path));details.append(node('code',target.path));$('changes').append(details);}
  const binding=node('details');binding.append(node('summary','提案身份与确认摘要'),node('code',p.id),node('code',p.digest));$('changes').append(binding);say(p.action==='undo-rollback'?'已预览撤销回退；确认只恢复本次列出的副本和基线。':'预览完成，当前文件尚未修改。请核对范围后另行确认。');sync();
 }
 async function preview(){if(!ready()||$('preview').disabled)return;const g=++epoch,ids=selected();clearPlan();ctx.setBusy(true);say('正在核对原批次、所选副本及共同基线…');try{const out=await ctx.api('/api/replay',{kind:'replica',snapshot_id:subject,action:'plan',copies:ids});if(g===epoch)show(out.items);}catch(e){if(g===epoch){clearPlan();say(e.message);}}finally{ctx.setBusy(false);sync();}}
 async function reload(){if(!ready()||!plan)return;const p=plan,g=++epoch;clearPlan();ctx.setBusy(true);try{const out=await ctx.api('/api/replay',{kind:'replica',snapshot_id:subject,action:'planned',plan_id:p.id,digest:p.digest});if(g===epoch)show(out.items);}catch(e){if(g===epoch){clearPlan();say(e.message);}}finally{ctx.setBusy(false);sync();}}
 async function commit(){if(!ready()||$('commit').disabled||!plan)return;const p=plan,callback=completed,g=epoch;clearPlan();ctx.setBusy(true);try{const out=await ctx.api('/api/replay',{kind:'replica',snapshot_id:subject,action:'apply',plan_id:p.id,digest:p.digest,confirm:true});if(g!==epoch)return;if(out.items.action!=='committed')throw Error(out.items.code||'恢复事务没有提交');close();let readError='';try{await ctx.refresh();}catch(e){readError=' 当前项目重读失败：'+e.message+'；请检查原批次恢复条件。';}callback?.(out.items.result);ctx.message('所选副本与共同基线已恢复；未选副本和发布者保留。'+readError,Boolean(readError));}catch(e){if(g===epoch){clearPlan();say(e.message+'；请重新读取提案或原批次恢复条件。');}}finally{ctx.setBusy(false);sync();}}
 $('preview').addEventListener('click',preview);$('reload').addEventListener('click',reload);$('commit').addEventListener('click',commit);$('consent').addEventListener('change',sync);$('cancel').addEventListener('click',()=>{if(ready())close();});$('dialog').addEventListener('cancel',e=>{e.preventDefault();if(ready())close();});
 return{init(context,app){ctx=app.source_root?context:null;invalidate();},openPublication,sync,invalidate};
})();
