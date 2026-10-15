'use strict';
const MCPArtifactView = (() => {
  const $=id=>document.getElementById(id);
  const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
  let ctx,loaded=null,plan=null;
  const selected=()=>loaded?.artifacts?.find(a=>a.name===$('mcp-artifact-name').value);
  const dirty=()=>Boolean($('mcp-artifact-archive').value.trim());
  function say(text,error=false){$('mcp-artifact-message').textContent=text;$('mcp-artifact-message').classList.toggle('error',error);}
  function invalidate(){plan=null;$('mcp-artifact-plan').hidden=true;}
  function sync(){
    if(!ctx)return;
    for(const n of $('mcp-artifact-dialog').querySelectorAll('button,input,select'))n.disabled=ctx.busy();
    $('mcp-artifact-open').disabled=ctx.busy();
    const a=selected();
    $('mcp-artifact-preview').disabled=ctx.busy()||ctx.pending()||!a||a.recovery_required||!['MISSING','EMPTY'].includes(a.status)||!dirty();
    $('mcp-artifact-confirm').disabled=ctx.busy()||ctx.pending()||!plan;
    $('mcp-artifact-recover').disabled=ctx.busy()||!$('mcp-artifact-recovery-path').value.trim();
    for(const n of $('mcp-artifact-history').querySelectorAll('button'))n.disabled=ctx.busy()||ctx.pending()||!a||a.recovery_required;
  }
  async function run(fn){if(ctx.busy())return;ctx.setBusy(true);try{await fn();}catch(e){invalidate();say(e.message,true);}finally{ctx.setBusy(false);}}
  function render(){
    const a=selected();$('mcp-artifact-current').replaceChildren();$('mcp-artifact-history').replaceChildren();
    if(!a){$('mcp-artifact-current').append(el('p','先保存包含完整文件、运行时和依赖的安装锁，再选择对应完整包。','caption'));sync();return;}
    const label={MISSING:'产物目录尚不存在',EMPTY:'产物目录为空',MATCHED:'文件与锁匹配',OCCUPIED:'已有内容，保留并阻断覆盖'}[a.status]||a.status;
    $('mcp-artifact-current').append(el('h3',`${a.name} · ${label}`),el('p',a.root,'caption'));
    if(a.recovery_required)$('mcp-artifact-current').append(el('p','有未完成的完整包事务；先恢复，再核验安装。','notice'));
    $('mcp-artifact-recovery-path').value=a.artifact_path;
    let latest=true;
    for(const j of a.history||[]){
      const row=el('section',undefined,'native-check-card');
      row.append(el('p',`${new Date(j.updated||j.plan.created).toLocaleString()} · ${j.plan.action==='artifact-import'?'导入完整包':'回退 / 撤销'} · ${{committed:'已完成',prepared:'待恢复',recovered:'失败后已恢复'}[j.state]||j.state}`,'caption'));
      if(j.state==='committed'&&latest){latest=false;const button=el('button','预览回退 / 撤销最近完整包操作','btn');button.type='button';button.addEventListener('click',()=>{if(ctx.busy()||ctx.pending()||a.recovery_required)return;run(()=>preview({action:'rollback',snapshot:j.plan.id}));});row.append(button);}
      $('mcp-artifact-history').append(row);
    }
    if(!(a.history||[]).length)$('mcp-artifact-history').append(el('p','此产物目录尚无导入记录。','caption'));
    sync();
  }
  async function load(){
    try{
      const next=(await ctx.api('/api/mcp/artifact/state')).items;
      const previous=$('mcp-artifact-name').value,old=selected();
      loaded=next;$('mcp-artifact-name').replaceChildren();
      for(const a of next.artifacts||[]){const option=el('option',a.name);option.value=a.name;$('mcp-artifact-name').append(option);}
      if((next.artifacts||[]).some(a=>a.name===previous))$('mcp-artifact-name').value=previous;
      $('mcp-artifact-checks').replaceChildren();for(const f of next.checks||[])$('mcp-artifact-checks').append(el('p',`${f.code}：${f.detail}`,'notice'));
      if(old&&(!selected()||old.sha256!==selected().sha256))invalidate();
      render();
    }catch(e){loaded=null;invalidate();$('mcp-artifact-name').replaceChildren();$('mcp-artifact-checks').replaceChildren();render();throw e;}
  }
  async function preview(edit){
    const a=selected();if(!a)return;invalidate();
    const out=(await ctx.api('/api/mcp/artifact/plan',{...edit,name:a.name,expected_sha256:a.sha256,expected_lock_sha256:loaded.lock_sha256})).items;
    plan=out.plan;$('mcp-artifact-changes').replaceChildren();
    const files=plan.changes.filter(c=>c.after&&!c.after.directory),bytes=files.reduce((sum,c)=>sum+(c.after.bytes||0),0),removed=plan.changes.filter(c=>c.before&&!c.before.directory&&!c.after).length; 
    $('mcp-artifact-changes').append(el('p',`目标：${out.candidate.root}`,'caption'),el('p',`写入 ${files.length} 个文件（${bytes.toLocaleString()} 字节），移除 ${removed} 个文件；共 ${plan.changes.length} 项目录/文件变化。`,'caption'));
    if(out.archive_sha256)$('mcp-artifact-changes').append(el('p',`完整 ZIP 摘要：${out.archive_sha256}`,'caption'));
    const details=el('details');details.append(el('summary','查看文件变化'));
    for(const c of plan.changes.slice(0,200))details.append(el('p',`${c.before?(c.after?'修改':'移除'):'新增'} · ${c.path}`,'caption'));
    if(plan.changes.length>200)details.append(el('p','显示前 200 项；所有文件均已按安装锁核对。','caption'));
    $('mcp-artifact-changes').append(details);$('mcp-artifact-plan').hidden=false;
    say('核对目录、文件变化和完整包摘要后再确认。可回退和撤销；安装登记与运行权限另行处理。');
  }
  function close(){if(ctx.busy())return;$('mcp-artifact-dialog').close?.();$('mcp-artifact-dialog').hidden=true;ctx.message(dirty()?'完整包路径草稿保留在当前标签页。':'完整包导入已关闭。');}
  function init(context){
    ctx=context;
    $('mcp-artifact-open').addEventListener('click',()=>{if(ctx.busy())return;$('mcp-artifact-dialog').hidden=false;if(!$('mcp-artifact-dialog').open)$('mcp-artifact-dialog').showModal?.();run(async()=>{invalidate();await load();say('先保存完整安装锁，再选本机 ZIP。包内直接放置锁中列出的文件与目录。');});});
    $('mcp-artifact-close').addEventListener('click',close);$('mcp-artifact-dialog').addEventListener('cancel',e=>{e.preventDefault();close();});
    $('mcp-artifact-name').addEventListener('change',()=>{if(ctx.busy())return;invalidate();render();});
    $('mcp-artifact-archive').addEventListener('input',()=>{if(ctx.busy())return;invalidate();sync();});
    $('mcp-artifact-recovery-path').addEventListener('input',sync);
    $('mcp-artifact-reload').addEventListener('click',()=>run(async()=>{invalidate();await load();say('已重读安装锁和产物观察；完整包路径草稿保留。');}));
    $('mcp-artifact-preview').addEventListener('click',()=>{if(ctx.busy()||ctx.pending()||$('mcp-artifact-preview').disabled)return;run(()=>preview({action:'import',archive_path:$('mcp-artifact-archive').value.trim()}));});
    $('mcp-artifact-cancel').addEventListener('click',()=>{if(ctx.busy())return;invalidate();say('已取消导入预览，路径草稿保留；产物目录未改变。');sync();});
    $('mcp-artifact-confirm').addEventListener('click',()=>{if(ctx.busy()||ctx.pending()||!plan)return;run(async()=>{await ctx.api('/api/mcp/artifact/apply',{name:selected().name,plan_id:plan.id,digest:plan.digest});invalidate();$('mcp-artifact-archive').value='';await load();await ctx.refreshChecks?.();say('完整包变化已保存，并保留回退点。请到本机安装与 SBOM 审查登记；当前未启动程序。');});});
    $('mcp-artifact-recover').addEventListener('click',()=>{if(ctx.busy())return;const path=$('mcp-artifact-recovery-path').value.trim();if(!path)return;run(async()=>{await ctx.api('/api/mcp/artifact/recover',{artifact_path:path});invalidate();await load();await ctx.refreshChecks?.();say('完整包中断事务已恢复；路径草稿保留，请重新预览。');});});
    sync();
  }
  return {init,sync,hasDraft:dirty};
})();
