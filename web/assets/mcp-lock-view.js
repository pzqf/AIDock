'use strict';
const MCPLockView = (() => {
  const $ = id => document.getElementById(id);
  const element = (tag,text,cls) => { const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n; };
  let ctx,loaded=null,plan=null,discardArmed=false;
  const dirty = () => Boolean(loaded && $('mcp-lock-document').value !== loaded.document);
  function say(text,error=false){$('mcp-lock-message').textContent=text;$('mcp-lock-message').classList.toggle('error',error);}
  function invalidate(){plan=null;$('mcp-lock-plan').hidden=true;$('mcp-lock-inspected').hidden=true;}
  function changed(){invalidate();discardArmed=false;$('mcp-lock-reload').textContent='重读并核验已保存锁';sync();}
  function sync(){
    if(!ctx)return;
    for(const n of $('mcp-lock-dialog').querySelectorAll('button,input,textarea,select'))n.disabled=ctx.busy();
    $('mcp-lock-open').disabled=ctx.busy();
    $('mcp-lock-inspect').disabled=ctx.busy()||ctx.pending()||!loaded||!loaded.definitions?.length||dirty();
    $('mcp-lock-preview').disabled=ctx.busy()||ctx.pending()||!dirty();
    $('mcp-lock-confirm').disabled=ctx.busy()||ctx.pending()||!plan;
    $('mcp-lock-draft').textContent=dirty()?'有未保存的安装锁清单；请预览保存或放弃后再生成候选。':'与已读取清单一致。';
  }
  function findings(target,state){
    target.replaceChildren();
    for(const c of state.checks||[])target.append(element('p',`${c.code}：${c.detail}`,'caption'));
    for(const p of state.plugins||[]){
      const card=element('section',undefined,'native-check-card');
      const status=v=>({PASS:'匹配',FAIL:'需处理',UNKNOWN:'待验证'})[v]||v;
      card.append(element('h4',`${p.name} · 文件 ${status(p.artifact_status)} · 依赖 ${status(p.dependency_status)} · 来源 ${p.trust_level}`));
      for(const c of p.checks||[])card.append(element('p',`${status(c.status)} · ${c.code}：${c.detail}`,'caption'));
      target.append(card);
    }
    if(state.present&&!(state.plugins||[]).length&&!(state.checks||[]).length)target.append(element('p','锁清单为空，没有本地产物可核验。','caption'));
  }
  async function run(fn){if(ctx.busy())return;ctx.setBusy(true);try{await fn();}catch(e){invalidate();say(e.message,true);}finally{ctx.setBusy(false);}}
  async function load(replace=false){
    const next=(await ctx.api('/api/mcp/lock/state')).items;
    await ctx.refreshDefinitionChecks?.();
    $('mcp-lock-root').textContent=`本地产物根：${next.artifact_root}`;
    findings($('mcp-lock-checks'),next);
    if(!replace&&dirty()){
      if(next.sha256!==loaded.sha256||next.verification_sha256!==loaded.verification_sha256){invalidate();say('安装锁或本地产物已变化；清单草稿保留，请重新核对和预览。',true);}
      return;
    }
    const selected=$('mcp-lock-name').value;
    $('mcp-lock-name').replaceChildren();
    for(const name of next.definitions||[]){const option=element('option',name);option.value=name;$('mcp-lock-name').append(option);}
    if((next.definitions||[]).includes(selected))$('mcp-lock-name').value=selected;
    loaded=next;$('mcp-lock-document').value=next.document;discardArmed=false;invalidate();
    $('mcp-lock-reload').textContent='重读并核验已保存锁';
    say('已重新核验。先核对本地文件与依赖，再确认保存清单；来源信任和运行资格分别显示。');sync();
  }
  function close(){if(ctx.busy())return;$('mcp-lock-dialog').close?.();$('mcp-lock-dialog').hidden=true;ctx.message(dirty()?'安装锁草稿保留在当前标签页。':'安装锁管理已关闭。');}
  function showDiff(candidate){
    $('mcp-lock-changes').replaceChildren();
    for(const c of plan.changes)$('mcp-lock-changes').append(element('p',`${c.before?(c.after?'修改':'移除'):'新增'} · ${c.path}`,'caption'));
    const section=element('details',undefined,'native-rule-preview');section.append(element('summary','查看完整清单前后对照'));
    for(const [label,value]of [['已保存',loaded.present?loaded.document:'未保存安装锁'],['确认后',candidate.document]]){
      section.append(element('p',label,'caption'),element('pre',value.slice(0,49152)));
      if(value.length>49152)section.append(element('p','清单较长，对照显示前 48K 字符；可在完整锁清单编辑框核对候选全文。','caption'));
    }
    $('mcp-lock-changes').append(section);
  }
  function init(context){
    ctx=context;
    $('mcp-lock-open').addEventListener('click',()=>{if(ctx.busy())return;$('mcp-lock-dialog').hidden=false;if(!$('mcp-lock-dialog').open)$('mcp-lock-dialog').showModal?.();$('mcp-lock-dialog').scrollTop=0;run(()=>load());});
    $('mcp-lock-close').addEventListener('click',close);
    $('mcp-lock-dialog').addEventListener('cancel',e=>{e.preventDefault();close();});
    $('mcp-lock-document').addEventListener('input',()=>{if(!ctx.busy())changed();});
    $('mcp-lock-file').addEventListener('change',()=>{
      const file=$('mcp-lock-file').files?.[0];if(!file||ctx.busy())return;
      run(async()=>{if(file.size>10*1024*1024)throw new Error('锁清单文件超过 10 MB');$('mcp-lock-document').value=await file.text();changed();say('已载入清单草稿。预览核验后再确认保存。');});
    });
    $('mcp-lock-inspect-form').addEventListener('submit',e=>{
      e.preventDefault();if(ctx.busy()||ctx.pending()||!loaded||dirty())return;
      run(async()=>{
        const version=$('mcp-lock-runtime-version').value.trim();
        const out=(await ctx.api('/api/mcp/lock/inspect',{name:$('mcp-lock-name').value,artifact_path:'mcp-artifacts/'+$('mcp-lock-path').value.trim(),version:$('mcp-lock-version').value.trim(),runtime:{kind:$('mcp-lock-runtime-kind').value,version,constraint:'='+version,executable:$('mcp-lock-runtime-path').value.trim()}})).items;
        if(out.expected_sha256!==loaded.sha256){invalidate();say('安装锁在读取期间已变化；请重读后再生成候选。',true);return;}
        $('mcp-lock-document').value=out.document;changed();findings($('mcp-lock-inspected-checks'),out.candidate);$('mcp-lock-inspected').hidden=false;
        say('候选清单已生成，尚未保存。文件哈希来自本地观察，来源信任保持 L0；请预览保存。');
      });
    });
    $('mcp-lock-reload').addEventListener('click',()=>{
      if(ctx.busy())return;
      if(dirty()&&!discardArmed){discardArmed=true;$('mcp-lock-reload').textContent='确认放弃安装锁草稿并重读';say('再次点击将放弃本页安装锁草稿。',true);return;}
      run(()=>load(true));
    });
    $('mcp-lock-preview').addEventListener('click',()=>{
      if(ctx.busy()||ctx.pending()||!dirty())return;
      run(async()=>{invalidate();const out=(await ctx.api('/api/mcp/lock/plan',{expected_sha256:loaded.sha256,document:$('mcp-lock-document').value})).items;plan=out.plan;showDiff(out.candidate);findings($('mcp-lock-candidate'),out.candidate);$('mcp-lock-plan').hidden=false;say('核对清单变化与本地产物结果，再确认保存。');});
    });
    $('mcp-lock-cancel').addEventListener('click',()=>{if(ctx.busy())return;invalidate();say('已取消安装锁预览，清单草稿保留。');sync();});
    $('mcp-lock-confirm').addEventListener('click',()=>{
      if(ctx.busy()||ctx.pending()||!plan)return;
      run(async()=>{await ctx.api('/api/mcp/lock/apply',{plan_id:plan.id,digest:plan.digest});invalidate();loaded=null;await ctx.refresh();await load(true);say('安装锁已保存，源回退点已保留。可通过时间线回退和撤销；来源信任与运行资格继续单独检查。');});
    });
    $('mcp-lock-history').addEventListener('click',()=>{if(ctx.busy())return;close();$('mcp-close').click();$('nav-history').click();});
    sync();
  }
  return {init,sync,hasDraft:dirty};
})();
