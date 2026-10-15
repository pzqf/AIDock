'use strict';
const MCPOriginView = (() => {
  const $ = id => document.getElementById(id), copy = v => JSON.parse(JSON.stringify(v));
  const el = (tag,text,cls) => {const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
  let ctx,loaded=null,keyDraft=null,baseEvidence='',plan=null,action='',discardArmed=false;
  const keysDirty = () => Boolean(loaded&&keyDraft&&JSON.stringify(keyDraft)!==JSON.stringify(loaded.keys));
  const evidenceDirty = () => Boolean(loaded&&$('mcp-origin-evidence').value!==baseEvidence);
  const dirty = () => keysDirty()||evidenceDirty();
  const blocked = () => ctx.pending()||loaded?.recovery_required;
  const validity = s => JSON.stringify([s.results,s.checks]);
  const status = s => ({PASS:'已验证',FAIL:'需处理',UNKNOWN:'待验证'})[s]||s;
  function say(text,error=false){$('mcp-origin-message').textContent=text;$('mcp-origin-message').classList.toggle('error',error);}
  function invalidate(){plan=null;action='';$('mcp-origin-plan').hidden=true;}
  function changed(){invalidate();discardArmed=false;$('mcp-origin-reload').textContent='重读来源证据';sync();}
  function sync(){
    if(!ctx)return;
    for(const n of $('mcp-origin-dialog').querySelectorAll('button,input,textarea,select'))n.disabled=ctx.busy();
    $('mcp-origin-open').disabled=ctx.busy();
    $('mcp-origin-add-key').disabled=ctx.busy()||blocked()||!loaded;
    $('mcp-origin-preview-keys').disabled=ctx.busy()||blocked()||!keysDirty();
    $('mcp-origin-preview-evidence').disabled=ctx.busy()||blocked()||!evidenceDirty()||!$('mcp-origin-evidence').value.trim();
    $('mcp-origin-confirm').disabled=ctx.busy()||blocked()||!plan;
    $('mcp-origin-key-draft').textContent=keysDirty()?'有未保存的公钥变更；先预览核对指纹、范围及有效期。':'与已读取的公钥表一致。';
  }
  async function run(fn){if(ctx.busy())return;ctx.setBusy(true);try{await fn();}catch(e){invalidate();say(e.message,true);}finally{ctx.setBusy(false);}}
  function field(parent,label,value,update,options){
    const wrap=el('label',label),input=el(options?'select':'input');input.setAttribute('aria-label',label);input.maxLength=2048;
    if(options)for(const [value,text]of options){const option=el('option',text);option.value=value;input.append(option);}
    input.value=value;input.addEventListener(options?'change':'input',()=>{if(ctx.busy())return;update(input.value);changed();});wrap.append(input);parent.append(wrap);
  }
  function renderKeys(){
    const target=$('mcp-origin-keys');target.replaceChildren();
    for(const [i,k]of keyDraft.keys.entries()){
      const card=el('section',undefined,'native-check-card'),grid=el('div',undefined,'mcp-fields'),n=i+1;
      const fingerprint=el('p',k.id?`SHA-256 指纹：${k.id}`:'新增公钥：预览时计算完整 SHA-256 指纹。','caption');card.append(el('h4',`公钥 ${n}`),fingerprint);
      field(grid,`公钥名称 ${n}`,k.label,v=>k.label=v);
      field(grid,`Ed25519 公钥 Base64 ${n}`,k.public_key,v=>{k.public_key=v;delete k.id;fingerprint.textContent='公钥已修改：预览时重新计算完整 SHA-256 指纹。';});
      field(grid,`公钥来源类型 ${n}`,k.source_type,v=>k.source_type=v,[['npm','npm'],['github','GitHub'],['registry','Registry'],['local','项目本地目录']]);
      field(grid,`公钥来源标识 ${n}`,k.source_id,v=>k.source_id=v);
      field(grid,`公钥到期时间 RFC3339 ${n}`,k.expires_at,v=>k.expires_at=v);card.append(grid);
      const toggle=el('label',undefined,'mcp-toggle'),check=el('input');check.type='checkbox';check.checked=k.enabled;check.setAttribute('aria-label',`启用公钥 ${n}`);
      check.addEventListener('change',()=>{if(ctx.busy())return;k.enabled=check.checked;changed();});toggle.append(check,el('span',`启用公钥 ${n}（停用立即使旧声明失效）`));card.append(toggle);
      const remove=el('button',`移除公钥 ${n}`,'btn');remove.type='button';remove.addEventListener('click',()=>{if(ctx.busy())return;keyDraft.keys.splice(i,1);changed();renderKeys();});card.append(remove);target.append(card);
    }
    if(!keyDraft.keys.length)target.append(el('p','尚未登记公钥。先核对来源与指纹，再登记。','caption'));sync();
  }
  function checks(target,s){
    target.replaceChildren();
    for(const f of s.checks||[])target.append(el('p',`${status(f.status)} · ${f.code}：${f.detail}`,'caption'));
    for(const c of s.results||[]){const card=el('section',undefined,'native-check-card');card.append(el('h4',`${c.name} · ${status(c.status)}`));for(const f of c.checks||[])card.append(el('p',f.detail,'caption'));if(c.publisher)card.append(el('p',`声明发布者：${c.publisher} · 公钥：${c.key_label||c.key_id} · 到期：${c.expires_at}`,'caption'));const codes=el('details');codes.append(el('summary','查看检查代码'));for(const f of c.checks||[])codes.append(el('p',f.code,'caption'));card.append(codes);target.append(card);}
  }
  function renderState(s){
    $('mcp-origin-binding').textContent=`本机证据目录：${s.root}。${s.boundary}`;
    $('mcp-origin-audit').textContent=`已追加 ${s.audit_count} 条记录 · 链头 ${s.audit_head||'尚无记录'}`;
    $('mcp-origin-recover').hidden=!s.recovery_required;checks($('mcp-origin-checks'),s);
    $('mcp-origin-subjects').replaceChildren();
    for(const subject of s.subjects||[]){const d=el('details',undefined,'native-rule-preview');d.append(el('summary',`${subject.name} · 签名对象摘要`),el('pre',JSON.stringify(subject,null,2)));$('mcp-origin-subjects').append(d);}
    if(!s.subjects?.length)$('mcp-origin-subjects').append(el('p','先在安装锁中保存产物清单，才能查看签名对象与检查结果。','caption'));
    $('mcp-origin-reports').replaceChildren();
    for(const report of s.reports||[]){
      const card=el('section',undefined,'native-check-card');card.append(el('p',`${report.publisher} · 到期 ${report.expires_at}`),el('p',`对象 ${report.subject_sha256} · 公钥 ${report.key_id}`,'caption'));
      const view=el('button','查看声明原文','btn');view.type='button';view.addEventListener('click',()=>{if(ctx.busy())return;if(evidenceDirty()){say('声明草稿保留；请保存或重读放弃后再查看另一份声明。',true);return;}run(async()=>{const out=(await ctx.api('/api/mcp/origin/show?subject='+encodeURIComponent(report.subject_sha256))).items;$('mcp-origin-evidence').value=out.document;baseEvidence=out.document;$('mcp-origin-evidence').closest('details').open=true;changed();say('已读取声明原字节；修改后可预览保存。');});});
      const remove=el('button','预览移除声明','btn');remove.type='button';remove.addEventListener('click',()=>{if(ctx.busy()||blocked())return;if(dirty()){say('请先保存或重读放弃草稿，再移除已缓存声明。',true);return;}run(()=>preview({action:'remove',subject_sha256:report.subject_sha256}));});card.append(view,remove);$('mcp-origin-reports').append(card);
    }
    if(!s.reports?.length)$('mcp-origin-reports').append(el('p','尚无缓存声明。','caption'));
    $('mcp-origin-history').replaceChildren();
    for(const h of s.history||[]){
      const row=el('div',undefined,'native-check-card');row.append(el('p',`${h.action} · ${h.state} · ${h.updated}`,'caption'),el('p',h.id,'caption'));
      if(h.state==='committed'){const button=el('button',h.action==='origin-rollback'?'预览撤销此回退':'预览回退此保存','btn');button.type='button';button.addEventListener('click',()=>{if(ctx.busy()||blocked())return;if(dirty()){say('请先保存或重读放弃草稿，再预览历史回退。',true);return;}run(()=>preview({action:'rollback',snapshot:h.id}));});row.append(button);}$('mcp-origin-history').append(row);
    }
  }
  async function load(replace=false){
    const next=(await ctx.api('/api/mcp/origin/state')).items;renderState(next);
    if(!replace&&dirty()){
      if(next.sha256!==loaded.sha256||validity(next)!==validity(loaded)){invalidate();say('来源证据或有效性已变化；草稿保留，请核对后重读并重新预览。',true);}return;
    }
    loaded=next;keyDraft=copy(next.keys);if(replace){$('mcp-origin-evidence').value='';baseEvidence='';}
    discardArmed=false;$('mcp-origin-reload').textContent='重读来源证据';invalidate();renderKeys();say('已读取当前项目本机证据。签名、来源范围与有效期分别核对，运行资格仍单独判断。');
  }
  function comparison(target,label,value){const d=el('details',undefined,'native-rule-preview');d.append(el('summary',label),el('pre',value));target.append(d);}
  async function preview(req){
    invalidate();const out=(await ctx.api('/api/mcp/origin/plan',{...req,expected_sha256:loaded.sha256})).items;plan=out.plan;action=req.action;
    const target=$('mcp-origin-changes');target.replaceChildren();
    for(const c of plan.changes)target.append(el('p',`${c.before?(c.after?'修改':'移除'):'新增'} · ${c.path}`,'caption'));
    comparison(target,'已保存的公钥与声明清单',JSON.stringify({keys:out.before.keys,reports:out.before.reports},null,2));
    comparison(target,'确认后的公钥与声明清单',JSON.stringify({keys:out.candidate.keys,reports:out.candidate.reports},null,2));
    if(out.evidence_before)comparison(target,'已保存声明原文',out.evidence_before);
    if(out.evidence_after)comparison(target,'确认后声明原文',out.evidence_after);
    checks($('mcp-origin-candidate'),out.candidate);$('mcp-origin-plan').hidden=false;say('请核对公钥完整指纹、来源范围、有效期及声明变化，再确认本机保存。');
  }
  function close(){if(ctx.busy())return;$('mcp-origin-dialog').close?.();$('mcp-origin-dialog').hidden=true;ctx.message(dirty()?'来源管理草稿保留在当前标签页。':'来源管理已关闭。');}
  function init(context){
    ctx=context;
    $('mcp-origin-open').addEventListener('click',()=>{if(ctx.busy())return;$('mcp-origin-dialog').hidden=false;if(!$('mcp-origin-dialog').open)$('mcp-origin-dialog').showModal?.();$('mcp-origin-dialog').scrollTop=0;run(()=>load());});
    $('mcp-origin-close').addEventListener('click',close);$('mcp-origin-dialog').addEventListener('cancel',e=>{e.preventDefault();close();});
    $('mcp-origin-add-key').addEventListener('click',()=>{if(ctx.busy()||blocked()||!keyDraft)return;keyDraft.keys.push({label:'',public_key:'',source_type:'npm',source_id:'',expires_at:new Date(Date.now()+30*24*3600*1000).toISOString(),enabled:true});changed();renderKeys();});
    $('mcp-origin-evidence').addEventListener('input',()=>{if(!ctx.busy())changed();});
    $('mcp-origin-file').addEventListener('change',()=>{const file=$('mcp-origin-file').files?.[0];if(!file||ctx.busy())return;run(async()=>{if(file.size>1024*1024)throw new Error('来源声明超过 1 MB');$('mcp-origin-evidence').value=await file.text();changed();say('已载入声明草稿，请预览核验。');});});
    $('mcp-origin-preview-keys').addEventListener('click',()=>{if(ctx.busy()||blocked()||!keysDirty())return;run(()=>preview({action:'keys',keys:keyDraft}));});
    $('mcp-origin-preview-evidence').addEventListener('click',()=>{if(ctx.busy()||blocked()||!evidenceDirty())return;run(()=>preview({action:'evidence',evidence:$('mcp-origin-evidence').value}));});
    $('mcp-origin-cancel').addEventListener('click',()=>{if(ctx.busy())return;invalidate();say('已取消来源预览，草稿保留。');sync();});
    $('mcp-origin-reload').addEventListener('click',()=>{if(ctx.busy())return;if(dirty()&&!discardArmed){discardArmed=true;$('mcp-origin-reload').textContent='确认放弃来源草稿并重读';say('再次点击将放弃当前公钥和声明草稿。',true);return;}run(async()=>{await load(true);await ctx.refreshChecks();});});
    $('mcp-origin-recover').addEventListener('click',()=>{if(ctx.busy())return;run(async()=>{await ctx.api('/api/mcp/origin/recover',{});await load();await ctx.refreshChecks();say('已恢复中断来源事务；草稿保留，请重新核对和预览。');});});
    $('mcp-origin-confirm').addEventListener('click',()=>{
      if(ctx.busy()||blocked()||!plan)return;
      run(async()=>{
        const savedAction=action,keepKeys=keysDirty()&&savedAction!=='keys',keys=copy(keyDraft),keepEvidence=evidenceDirty()&&savedAction!=='evidence',evidence=$('mcp-origin-evidence').value,base=baseEvidence;
        await ctx.api('/api/mcp/origin/apply',{plan_id:plan.id,digest:plan.digest});invalidate();loaded=null;await load(true);
        if(keepKeys){keyDraft=keys;renderKeys();}if(keepEvidence){$('mcp-origin-evidence').value=evidence;baseEvidence=base;}
        await ctx.refreshChecks();say('来源证据已保存，恢复点与审计已保留。安全评估、项目授权和隔离继续独立检查。');
      });
    });sync();
  }
  return {init,sync,hasDraft:dirty};
})();
