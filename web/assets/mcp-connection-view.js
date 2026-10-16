'use strict';
const MCPConnectionView=(()=>{
 const $=id=>document.getElementById(id),node=(tag,text,cls)=>{const e=document.createElement(tag);if(text!==undefined)e.textContent=text;if(cls)e.className=cls;return e;};
 let ctx,versions=null,plan=null,reverse=false,pending=false,generation=0;
 const selection=()=>['claude-code','codex-cli'].filter(id=>$('mcp-connect-'+id).checked);
 const say=(text,error=false)=>{$('mcp-connect-message').textContent=text;$('mcp-connect-message').classList.toggle('error',error);};
 function invalidate(){generation++;plan=null;reverse=false;$('mcp-connect-preview').hidden=true;}
 function sync(){if(!ctx)return;$('mcp-connect-plugin-state').textContent='当前 MCP：'+(ctx.plugin()||'尚未选择');for(const e of $('mcp-native-connect').querySelectorAll('button,input'))e.disabled=ctx.busy();const empty=!selection().length;
  $('mcp-connect-plan').disabled=ctx.busy()||pending||empty||!ctx.session()||!versions;
  $('mcp-connect-remove').disabled=ctx.busy()||pending||empty||!ctx.plugin();
  $('mcp-connect-versions').disabled=ctx.busy()||empty;
  $('mcp-connect-confirm').disabled=ctx.busy()||pending||!plan||(!reverse&&!plan.changes);
  for(const e of $('mcp-connect-history').querySelectorAll('button'))e.disabled=ctx.busy()||pending;
 }
 async function run(fn){if(ctx.busy())return;ctx.setBusy(true);try{await fn();}catch(e){invalidate();say(e.message,true);}finally{ctx.setBusy(false);sync();}}
 function show(p,isReverse=false){plan=p;reverse=isReverse;const tx=isReverse?p:p.transaction,target=$('mcp-connect-changes');target.replaceChildren();
  target.append(node('h4',isReverse?(p.action==='undo-rollback'?'撤销连接回退':'回退连接变化'):p.request.action==='remove'?'移除所选工具的连接':'安装所选工具的连接'));
  for(const item of tx.targets||[]){const name=item.path==='.aidock-mcp-ownership.json'?'字段归属记录':item.path;target.append(node('p',name+' · '+(JSON.stringify(item.before)===JSON.stringify(item.after)?'已一致':'将更新'),'caption'));}
  if(!isReverse)target.append(node('p',p.boundary,'notice'));
  else target.append(node('p','恢复此事务涉及的原生文件与字段归属。恢复启动器前重新核对原授权，失效授权须重新审查并安装。','notice'));
  $('mcp-connect-confirm').textContent=isReverse?'确认本次恢复':p.request.action==='remove'?'确认移除连接':'确认安装连接';
  $('mcp-connect-preview').hidden=false;say(!isReverse&&!p.changes?'所选连接已一致，无需重复安装。':'请核对文件范围后确认；取消只关闭预览。');sync();
 }
 async function load(){if(!ctx)return;invalidate();const revision=generation;versions=null;pending=true;sync();
  try{
   const [v,h,c]=await Promise.all([ctx.api('/api/native/mcp/versions'),ctx.api('/api/native/mcp/history'),ctx.api('/api/native/config',{tools:['claude-code','codex-cli']})]);
   if(revision!==generation)return;
   versions=v.items;pending=(h.items||[]).some(j=>j.state==='prepared');
   renderVersions();const fields=$('mcp-connect-state');fields.replaceChildren();for(const file of c.items.files||[]){const managed=(file.fields||[]).filter(f=>f.ownership==='MANAGED').length,drift=(file.fields||[]).some(f=>f.ownership==='DRIFT');fields.append(node('p',file.contract.path+' · '+(drift?'托管字段有手改，先协调':managed?managed+' 个受管连接':'暂无受管连接')+' · 保留其它字段','caption'));for(const check of file.checks||[])if(check.status==='FAIL')fields.append(node('p',check.detail,'error'));}
   const history=$('mcp-connect-history');history.replaceChildren();let canReverse=true;
   for(const entry of h.items||[]){const card=node('section',undefined,'native-check-card');card.append(node('p',({apply:'连接变更',rollback:'连接回退','undo-rollback':'撤销连接回退'}[entry.plan.action]||entry.plan.action)+' · '+new Date(entry.updated).toLocaleString()+' · '+({committed:'已完成',recovered:'失败后已恢复',prepared:'中断待恢复'}[entry.state]||entry.state),'caption'));
    if(entry.state==='committed'&&canReverse){canReverse=false;const b=node('button','预览回退 / 撤销此连接操作','btn');b.type='button';b.addEventListener('click',()=>run(async()=>show((await ctx.api('/api/native/mcp/rollback/plan',{transaction:entry.plan.id})).items,true)));card.append(b);}history.append(card);
   }
   $('mcp-connect-recover').hidden=!pending;if(pending)say('存在未完成的连接事务，请先恢复；不会重放插件启动。',true);
  }catch(e){if(revision!==generation)return;versions=null;pending=true;$('mcp-connect-recover').hidden=false;say(e.message,true);}
  sync();
 }
 function renderVersions(){const target=$('mcp-connect-version-state');target.replaceChildren();if(!versions){target.append(node('p','安装前先核对所选工具的精确版本。检查仅执行 --version，不连接 MCP。','caption'));return;}target.append(node('p','版本记录：'+new Date(versions.created).toLocaleString()+' · 24 小时有效，程序变化后重新检查','caption'));for(const check of versions.checks||[])target.append(node('p',check.detail,check.status==='PASS'?'caption':'notice'));}
 function init(context){ctx=context;$('mcp-connect-review').addEventListener('click',()=>{if(!ctx.busy())ctx.review?.();});
  for(const id of ['claude-code','codex-cli'])$('mcp-connect-'+id).addEventListener('change',()=>{invalidate();sync();});
  $('mcp-connect-refresh').addEventListener('click',()=>run(load));
  $('mcp-connect-versions').addEventListener('click',()=>run(async()=>{invalidate();versions=(await ctx.api('/api/native/mcp/versions/check',{tools:selection()})).items;renderVersions();say('版本检查已结束；未加载项目配置或启动 MCP。');}));
  $('mcp-connect-plan').addEventListener('click',()=>{if(!ctx.session()||!versions||pending)return;run(async()=>{invalidate();show((await ctx.api('/api/native/mcp/plan',{action:'install',tools:selection(),check_id:versions.id,session:ctx.session()})).items);});});
  $('mcp-connect-remove').addEventListener('click',()=>{if(!ctx.plugin()||pending)return;run(async()=>{invalidate();show((await ctx.api('/api/native/mcp/plan',{action:'remove',tools:selection(),session:{name:ctx.plugin()}})).items);});});
  $('mcp-connect-cancel').addEventListener('click',()=>{if(!ctx.busy()){invalidate();say('已取消连接预览，文件未写入。');sync();}});
  $('mcp-connect-confirm').addEventListener('click',()=>{if(!plan||ctx.busy()||pending)return;const p=plan,isReverse=reverse;run(async()=>{await ctx.api(isReverse?'/api/native/mcp/rollback/apply':'/api/native/mcp/apply',{plan_id:p.id,digest:p.digest});await load();say('连接文件与字段归属已共同保存，可预览回退或撤销。没有启动插件；请由新的 AI 客户端会话加载。');});});
  $('mcp-connect-recover').addEventListener('click',()=>run(async()=>{await ctx.api('/api/native/mcp/recover',{});await load();say('连接事务恢复已完成，没有重放客户端或插件启动。');}));sync();
 }
 return {init,sync,load,invalidate};
})();
