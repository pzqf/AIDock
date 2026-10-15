'use strict';
const MCPInstallView = (() => {
  const $=id=>document.getElementById(id), el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
  let ctx,loaded=null,plan=null;
  const dirty=()=>Boolean($('mcp-install-allow').checked||$('mcp-install-reason').value);
  const blocked=()=>!loaded||ctx.pending()||loaded.recovery_required||loaded.checks?.some(f=>f.code==='MCP_INSTALL_STORE_INVALID');
  const status=s=>({CURRENT:'与当前检查一致',STALE:'需重新审查',RECOVERY_REQUIRED:'需恢复',PASS:'已通过',FAIL:'需处理',UNKNOWN:'待验证'})[s]||s;
  function say(text,error=false){$('mcp-install-message').textContent=text;$('mcp-install-message').classList.toggle('error',error);}
  function invalidate(){plan=null;$('mcp-install-plan').hidden=true;}
  function sync(){
    if(!ctx)return;for(const n of $('mcp-install-dialog').querySelectorAll('button,input,textarea,select'))n.disabled=ctx.busy();$('mcp-install-open').disabled=ctx.busy();
    $('mcp-install-preview').disabled=ctx.busy()||blocked()||!$('mcp-install-name').value||!$('mcp-install-allow').checked||!$('mcp-install-reason').value.trim();
    $('mcp-install-confirm').disabled=ctx.busy()||blocked()||!plan;
    for(const n of $('mcp-install-current').querySelectorAll('button'))n.disabled=ctx.busy()||blocked();
    for(const n of $('mcp-install-history').querySelectorAll('button'))n.disabled=ctx.busy()||blocked();
  }
  function detail(target,title,value){const d=el('details',undefined,'native-rule-preview');d.append(el('summary',title),el('pre',typeof value==='string'?value:JSON.stringify(value,null,2)));target.append(d);}
  function list(target,entries,official=true){
    target.replaceChildren();for(const entry of entries||[]){const r=entry.record,card=el('section',undefined,'native-check-card');card.append(el('h4',`${r.plugin.name} · ${r.plugin.version} · ${status(entry.status)}`),el('p',`来源 ${r.trust_level} · 默认门槛 ${r.minimum_trust} · ${r.installed_at}`,'caption'),el('p',`登记理由：${r.risk_reason}`,'caption'));
      if(official&&entry.sbom){card.append(el('p',`轻量 SBOM：${entry.sbom.dependencies.length} 项直接声明`,'caption'));if(entry.sbom.dependencies.length){const table=el('table',undefined,'manifest-dependencies'),head=el('thead'),row=el('tr');for(const name of ['生态','依赖','固定版本'])row.append(el('th',name));head.append(row);table.append(head);const body=el('tbody');for(const d of entry.sbom.dependencies){const row=el('tr');for(const value of [d.ecosystem,d.name,d.version])row.append(el('td',value));body.append(row);}table.append(body);card.append(table);}detail(card,'查看本机安装的 SBOM',entry.sbom);detail(card,'SBOM 校验摘要',entry.sbom_sha256);if(entry.status!=='CURRENT')card.append(el('p','这是登记时的历史清单，当前产物或证据已变化，不能用它证明当前安全或可运行。','notice'));}
      else card.append(el('p','确认登记后才生成正式轻量 SBOM。','caption'));
      detail(card,'完整产物、运行时与直接依赖绑定',r.plugin);
      if(official){const b=el('button','预览移除登记','btn');b.type='button';b.addEventListener('click',()=>{if(!ctx.busy()&&!blocked())run(()=>preview({action:'remove',name:r.plugin.name}));});card.append(b);}target.append(card);
    }if(!entries?.length)target.append(el('p','尚无本机安装登记。','caption'));
  }
  function candidate(){
    const target=$('mcp-install-review');target.replaceChildren();const c=loaded?.candidates?.find(c=>c.plugin.name===$('mcp-install-name').value);if(!c)return;
    target.append(el('h4',`${c.manifest.manifest?.name||c.plugin.name} · ${c.plugin.version} · L0`));if(c.manifest.manifest?.description)target.append(el('p',c.manifest.manifest.description,'caption'));
    for(const f of c.check.checks||[])target.append(el('p',`${status(f.status)} · ${f.detail}`,'caption'));
    detail(target,'请求的文件、网络和子进程权限',c.manifest.manifest?.permissions||c.manifest.definition_permissions||{});detail(target,'来源、入口与运行时', {source:c.plugin.source,entrypoint:c.plugin.entrypoint,runtime:c.plugin.runtime,artifact_sha256:c.plugin.artifact.sha256});
    detail(target,'将记录的直接依赖声明',c.manifest.manifest?.dependencies??'尚未声明，不能登记');
  }
  function render(){
    $('mcp-install-binding').textContent=loaded.root;list($('mcp-install-current'),loaded.installations);$('mcp-install-recover').hidden=!loaded.recovery_required;
    $('mcp-install-audit').textContent=`已追加 ${loaded.audit_count} 条记录 · 链头 ${loaded.audit_head||'尚无记录'}`;
    const checks=$('mcp-install-checks');checks.replaceChildren();for(const f of loaded.checks||[])checks.append(el('p',`${status(f.status)} · ${f.detail}`,'notice'));
    const selected=$('mcp-install-name').value;$('mcp-install-name').replaceChildren();for(const c of loaded.candidates||[]){const option=el('option',c.plugin.name);option.value=c.plugin.name;$('mcp-install-name').append(option);}if(loaded.candidates?.some(c=>c.plugin.name===selected))$('mcp-install-name').value=selected;candidate();
    const history=$('mcp-install-history');history.replaceChildren();for(const h of loaded.history||[]){const row=el('section',undefined,'native-check-card');row.append(el('p',`${h.action} · ${h.state} · ${h.updated}`,'caption'),el('p',h.id,'caption'));if(h.request){if(h.request.risk_reason)row.append(el('p',`本次放行理由：${h.request.risk_reason}`,'caption'));detail(row,'查看本次动作与风险确认',h.request);}if(h.state==='committed'){const b=el('button',h.action==='install-rollback'?'预览撤销此回退':'预览回退此登记变更','btn');b.type='button';b.addEventListener('click',()=>{if(!ctx.busy()&&!blocked())run(()=>preview({action:'rollback',snapshot:h.id,allow_unverified:$('mcp-install-allow').checked,risk_reason:$('mcp-install-reason').value}));});row.append(b);}history.append(row);}
  }
  async function load(){invalidate();loaded=null;for(const id of ['mcp-install-current','mcp-install-checks','mcp-install-review','mcp-install-history'])$(id).replaceChildren();const next=(await ctx.api('/api/mcp/install/state')).items;loaded=next;render();say('已核对本机登记与当前产物。草稿保留；登记、信任等级和运行权限分别显示。');}
  async function run(fn){if(ctx.busy())return;ctx.setBusy(true);try{await fn();}catch(e){invalidate();say(e.message,true);}finally{ctx.setBusy(false);}}
  async function preview(req){
    invalidate();const out=(await ctx.api('/api/mcp/install/plan',{...req,expected_sha256:loaded.sha256})).items;plan=out.plan;
    $('mcp-install-changes').replaceChildren();detail($('mcp-install-changes'),'确认前登记',out.before.installations.map(e=>e.record));detail($('mcp-install-changes'),'确认后登记',out.candidate.installations.map(e=>e.record));
    detail($('mcp-install-changes'),'本次动作与低信任放行',req);for(const change of plan.changes)$('mcp-install-changes').append(el('p',`${change.before?(change.after?'修改':'移除'):'新增'} · ${change.path}`,'caption'));
    $('mcp-install-plan').hidden=false;say('请核对前后登记、来源、权限与风险理由，再确认。产物保留，运行权限单独授权。');
  }
  function changed(){invalidate();sync();}
  function close(){if(ctx.busy())return;$('mcp-install-dialog').close?.();$('mcp-install-dialog').hidden=true;ctx.message(dirty()?'安装登记草稿保留在当前标签页。':'已关闭安装登记。');}
  function init(context){
    ctx=context;$('mcp-install-open').addEventListener('click',()=>{if(ctx.busy())return;$('mcp-install-dialog').hidden=false;if(!$('mcp-install-dialog').open)$('mcp-install-dialog').showModal?.();$('mcp-install-dialog').scrollTop=0;run(async()=>{try{await load();}catch(e){loaded=null;for(const id of ['mcp-install-current','mcp-install-checks','mcp-install-review','mcp-install-history'])$(id).replaceChildren();throw e;}});});
    $('mcp-install-close').addEventListener('click',close);$('mcp-install-dialog').addEventListener('cancel',e=>{e.preventDefault();close();});
    $('mcp-install-name').addEventListener('change',()=>{if(ctx.busy())return;changed();candidate();});$('mcp-install-allow').addEventListener('change',()=>{if(!ctx.busy())changed();});$('mcp-install-reason').addEventListener('input',()=>{if(!ctx.busy())changed();});
    $('mcp-install-reload').addEventListener('click',()=>run(async()=>{try{await load();}catch(e){loaded=null;for(const id of ['mcp-install-current','mcp-install-checks','mcp-install-review','mcp-install-history'])$(id).replaceChildren();throw e;}}));
    $('mcp-install-preview').addEventListener('click',()=>{if(!ctx.busy()&&!blocked()&&$('mcp-install-allow').checked&&$('mcp-install-reason').value.trim())run(()=>preview({action:'install',name:$('mcp-install-name').value,allow_unverified:true,risk_reason:$('mcp-install-reason').value}));});
    $('mcp-install-cancel').addEventListener('click',()=>{if(ctx.busy())return;invalidate();say('已取消预览，登记草稿保留。');sync();});
    $('mcp-install-confirm').addEventListener('click',()=>{if(ctx.busy()||blocked()||!plan)return;const confirmed=plan;run(async()=>{await ctx.api('/api/mcp/install/apply',{plan_id:confirmed.id,digest:confirmed.digest});invalidate();$('mcp-install-allow').checked=false;$('mcp-install-reason').value='';await load();$('mcp-install-dialog').scrollTop=0;await ctx.refreshChecks();say('登记变更已保存，恢复点与追加审计已保留；未下载或启动程序。');});});
    $('mcp-install-recover').addEventListener('click',()=>run(async()=>{await ctx.api('/api/mcp/install/recover',{});await load();await ctx.refreshChecks();say('中断登记事务已恢复，草稿保留；请重新核对后预览。');}));sync();
  }
  return {init,sync,hasDraft:dirty};
})();
