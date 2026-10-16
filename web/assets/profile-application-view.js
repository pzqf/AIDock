'use strict';
const ProfileApplicationView=(()=>{
 const $=id=>document.getElementById(id),node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
 let ctx,reader,changed,review=null,history=[],plan=null,reverse=false,epoch=0,ready=false;
 const pending=()=>history.some(j=>j.state==='prepared');
 const latest=()=>history.find(j=>j.state==='committed');
 const eligible=()=>{const p=reader?.();return p?.application_scope==='skills-rules'&&p.write_supported&&!p.blocked;};
 const say=(text,error=false)=>{$('profile-apply-message').textContent=text;$('profile-apply-message').classList.toggle('error',error);};
 function close(){plan=null;reverse=false;$('profile-apply-confirm').close?.();$('profile-apply-confirm').hidden=true;}
 function invalidate(){epoch++;ready=false;review=null;close();$('profile-apply-review').replaceChildren();say('项目输入已变化，请重新读取联合应用状态。');sync();}
 function sync(){if(!ctx)return;const busy=ctx.busy();
  for(const id of ['profile-apply-prepare','profile-apply-load','profile-apply-cancel','profile-apply-recover'])$(id).disabled=busy;
  $('profile-apply-plan').disabled=busy||ctx.pending()||!ready||pending()||!eligible()||review?.status!=='READY';
  $('profile-apply-reverse').disabled=busy||!ready||pending()||!latest();
  $('profile-apply-reverse').textContent=latest()?.plan.action==='rollback'?'预览撤销联合回退':'预览回退联合应用';
  $('profile-apply-confirm-run').disabled=busy||!plan||(!reverse&&!plan.changes);
 }
 function renderHistory(){const root=$('profile-apply-history');root.replaceChildren();
  for(const j of history){const item=node('article');item.className='native-check-card';item.append(node('p',({apply:'联合应用',rollback:'联合回退','undo-rollback':'撤销联合回退'}[j.plan.action]||j.plan.action)+' · '+({committed:'已完成',prepared:'中断待恢复',recovered:'失败后已恢复'}[j.state]||j.state)),node('small',new Date(j.updated).toLocaleString()+' · '+j.plan.id));const details=node('details');details.append(node('summary','本次文件范围'));for(const t of j.plan.targets||[])details.append(node('code',t.path));item.append(details);root.append(item);}
  $('profile-apply-recover').hidden=!pending();
 }
 async function fetchState(g){
  const [r,h]=await Promise.all([ctx.api('/api/native/profile/review'),ctx.api('/api/native/profile/history')]);if(g!==epoch)return false;
  review=r.items;history=h.items||[];ready=true;const target=$('profile-apply-review');target.replaceChildren();
  target.append(node('p',review.entries.length?'当前有效 MCP · '+review.entries.length+' 项':'当前无启用 MCP；预览仍会核对并移除所选工具中停用的旧托管连接。'));
  for(const entry of review.entries)target.append(node('p',entry.name+' · '+(entry.status==='READY'?'已有安装、授权与运行时记录可核对':'需处理 · '+entry.code)));
  const details=node('details');details.append(node('summary','检查范围'),node('p',review.boundary));target.append(details);renderHistory();sync();return true;
 }
 async function run(fn){if(!ctx||ctx.busy())return;ctx.setBusy(true);try{await fn();}catch(e){close();ready=false;say(e.message,true);}finally{ctx.setBusy(false);sync();}}
 async function load(){if(!ctx||ctx.busy())return;close();const g=++epoch;ready=false;await run(async()=>{if(await fetchState(g))say(pending()?'有未完成的联合事务，先恢复。':review.status==='READY'?'MCP 条件已读取。准备技能与规则后，可核对联合预览。':'先在 MCP 管理中处理上述条件，再重新读取。',pending());});}
 function show(p,isReverse){plan=p;reverse=isReverse;const tx=isReverse?p:p.transaction;const target=$('profile-apply-changes');target.replaceChildren();
  $('profile-apply-title').textContent=isReverse?(p.action==='undo-rollback'?'撤销联合回退':'回退联合应用'):'确认联合应用';
  $('profile-apply-boundary').textContent=isReverse?'恢复本次事务全部技能、规则与 MCP 配置原字节。恢复启动器前重新核对当前授权；文件手改会阻断。源与页面草稿保留。':p.boundary;
  if(!isReverse){target.append(node('p',p.request.sessions.length+' 个有效 MCP · '+p.changes+' 项路径变化'));for(const s of p.request.sessions)target.append(node('p',s.name+' · 已审查的当前绑定'));}
  for(const t of tx.targets||[]){const diff=JSON.stringify(t.before)!==JSON.stringify(t.after);target.append(node('p',t.path+' · '+(diff?(t.after.exists?'将更新':'将移除'):'已一致')));}
  $('profile-apply-confirm-run').textContent=isReverse?'确认恢复全部目标':'确认联合安装';$('profile-apply-confirm').hidden=false;$('profile-apply-confirm').showModal?.();sync();
 }
 function init(context,planReader,onChanged){ctx=context;reader=planReader;changed=onChanged;
  $('profile-apply-prepare').addEventListener('click',()=>{if(!ctx.busy())ctx.prepare();});
  $('profile-apply-load').addEventListener('click',load);
  $('profile-apply-plan').addEventListener('click',()=>{if(!ready||pending()||!eligible()||review?.status!=='READY')return;const p=reader(),sessions=review.entries.map(e=>e.session);if(sessions.some(s=>!s))return;const g=++epoch;run(async()=>{const out=await ctx.api('/api/native/profile/plan',{native_plan:p.id,native_digest:p.digest,sessions});if(g===epoch&&reader()?.digest===p.digest)show(out.items,false);});});
  $('profile-apply-reverse').addEventListener('click',()=>{if(!ready||pending()||!latest())return;const g=++epoch;run(async()=>{const out=await ctx.api('/api/native/profile/rollback/plan',{transaction:latest().plan.id});if(g===epoch)show(out.items,true);});});
  $('profile-apply-cancel').addEventListener('click',()=>{if(!ctx.busy()){close();say('已取消联合预览，没有写入文件。');sync();}});
  $('profile-apply-confirm').addEventListener('cancel',e=>{e.preventDefault();if(!ctx.busy()){close();sync();}});
  $('profile-apply-confirm-run').addEventListener('click',()=>{if(ctx.busy()||!plan||(!reverse&&!plan.changes))return;const p=plan,r=reverse;close();run(async()=>{try{await ctx.api(r?'/api/native/profile/rollback/apply':'/api/native/profile/apply',{plan_id:p.id,digest:p.digest});changed();await fetchState(++epoch);say(r?'联合恢复完成；可查看历史或撤销本次回退。':'技能、规则与有效 MCP 连接已共同保存。没有启动插件；请在新工具会话中验证实际加载。');}catch(e){changed();try{await fetchState(++epoch);}catch(_){}throw e;}});});
  $('profile-apply-recover').addEventListener('click',()=>run(async()=>{await ctx.api('/api/native/profile/recover',{});changed();await fetchState(++epoch);say('联合事务恢复完成，没有重放工具或插件启动。');}));sync();
 }
 return {init,sync,invalidate,load};
})();
