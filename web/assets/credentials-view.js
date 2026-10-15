'use strict';
const CredentialsView = (() => {
  const $=id=>document.getElementById(id);
  const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
  let ctx,loaded=null,revision='';
  const refs=name=>(loaded?.references||[]).filter(r=>r.name===name);
  const referenceLabel=r=>`${r.project_name||r.project} · ${r.scope||'项目作用域'} · ${({current:'当前绑定',registered:'已登记',archived:'已归档',removed:'已解除登记'})[r.status]||r.status}`;
  const localTime=value=>{const date=new Date(value);return Number.isNaN(date.getTime())?value:date.toLocaleString('zh-CN',{hour12:false});};
  function say(text,error=false){$('credential-message').textContent=text;$('credential-message').classList.toggle('error',error);}
  function clearValue(){ $('credential-value').value='';$('credential-value').type='password';$('credential-visible').checked=false;$('credential-confirm').checked=false; }
  function sync(){
    if(!ctx)return;
    $('nav-credentials').disabled=ctx.busy();
    for(const n of $('credentials-dialog').querySelectorAll('button,input'))n.disabled=ctx.busy();
    $('credential-save').disabled=ctx.busy()||!loaded?.vault.available||Boolean(loaded?.vault.pending.length)||!$('credential-confirm').checked;
    $('credential-reconcile').hidden=!loaded?.vault.pending.length;
    $('credential-name').readOnly=$('credential-action').value!=='add';
    $('credential-value-field').hidden=$('credential-action').value==='delete';
  }
  function selection(action='add',entry=null){
    clearValue();revision=entry?.revision||'';$('credential-name').value=entry?.name||'';$('credential-action').value=action;
    $('credential-form-title').textContent=({add:'添加密钥',rotate:'轮换密钥',delete:'删除密钥'})[action];
    $('credential-save').textContent=({add:'确认添加',rotate:'确认轮换',delete:'确认删除'})[action];
    $('credential-impact').replaceChildren();
    if(action==='delete'){
      const root=$('credential-impact'),rows=refs(entry.name);
      root.append(el('p',rows.length?`已知 ${rows.length} 处声明会缺少此密钥：`:'当前登记范围内未发现此名称的引用。','notice'));
      for(const r of rows)root.append(el('p',`${r.plugin} · ${referenceLabel(r)}`,'caption'));
      for(const u of loaded.unknown)root.append(el('p',u,'notice'));
      root.append(el('p','未登记项目、外部客户端和其它数据根的引用不可知。删除后无法从 AIDock 回退恢复旧值。','notice'));
    }
    $('credential-consent').textContent=action==='delete'?'我已核对引用及未知范围，确认删除，旧值不进入恢复包。':action==='rotate'?'确认替换系统凭据值；下次启动取新值，旧值不进入恢复包。':'确认将此值添加到当前 Windows 用户的系统凭据库。';
    sync();
  }
  function render(){
    const root=$('credential-entries');root.replaceChildren();
    if(!loaded){$('credential-provider').textContent='正在读取系统凭据状态…';$('credential-missing').replaceChildren();$('credential-history').replaceChildren();$('credential-boundary').textContent='';return;}
    $('credential-provider').textContent=loaded.vault.available?`${loaded.vault.provider} · 当前用户 · 本数据根`:'此平台的系统凭据库尚不支持，不能添加或读取值。';
    for(const e of loaded.vault.entries){
      const card=el('section',undefined,'native-check-card');card.append(el('h3',e.name),el('p',`${refs(e.name).length} 处已知声明 · 更新于 ${localTime(e.updated)}`,'caption'));
      const actions=el('div',undefined,'source-actions');for(const [action,label] of [['rotate','轮换'],['delete','删除']]){const button=el('button',label,action==='delete'?'btn danger':'btn');button.type='button';button.addEventListener('click',()=>{if(!ctx.busy())selection(action,e);});actions.append(button);}card.append(actions);root.append(card);
    }
    if(!loaded.vault.entries.length)root.append(el('p','尚未添加密钥。只需输入一次；列表不会显示值或值摘要。','caption'));
    const missing=$('credential-missing');missing.replaceChildren();
    const groups=new Map();for(const r of loaded.references.filter(r=>!r.present)){if(!groups.has(r.name))groups.set(r.name,[]);groups.get(r.name).push(r);}
    for(const [name,rows] of groups){const detail=el('details');detail.append(el('summary',`${loaded.vault.available?'缺少':'尚未核验'} ${name} · ${rows.length} 处声明`));for(const r of rows)detail.append(el('p',`${r.plugin} · ${referenceLabel(r)}`,'caption'));missing.append(detail);}
    for(const u of loaded.unknown)missing.append(el('p',u,'notice'));
    if(loaded.vault.pending.length)missing.append(el('p',`有 ${loaded.vault.pending.length} 项中断操作需核对系统修订；新操作已暂停。`,'notice'));
    const history=$('credential-history');history.replaceChildren();for(const a of loaded.vault.history.slice(-30).reverse())history.append(el('p',`${localTime(a.at)} · ${a.name} · ${({add:'新增',rotate:'轮换',delete:'删除'})[a.action]||a.action} · ${({INTENT:'开始操作',APPLIED:'已完成',NOT_APPLIED:'未应用'})[a.outcome]||a.outcome}`,'caption'));
    if(!loaded.vault.history.length)history.append(el('p','暂无操作记录。','caption'));
    $('credential-boundary').textContent=loaded.vault.boundary+' '+loaded.boundary;
  }
  async function load(){loaded=null;render();loaded=(await ctx.api('/api/credentials/state')).items;render();}
  async function run(fn){if(ctx.busy())return;ctx.setBusy(true);try{await fn();}catch(e){say(e.message,true);}finally{ctx.setBusy(false);}}
  function close(){if(ctx.busy())return;clearValue();$('credentials-dialog').close?.();$('credentials-dialog').hidden=true;ctx.message('已关闭密钥管理，密钥输入已清空。');}
  function init(context){
    ctx=context;
    $('nav-credentials').addEventListener('click',()=>{if(ctx.busy())return;$('credentials-dialog').hidden=false;$('credentials-dialog').showModal?.();run(async()=>{loaded=null;render();selection();await load();say('只展示系统凭据名称和修订，密钥值保持隐藏。');});});
    $('credentials-close').addEventListener('click',close);$('credentials-dialog').addEventListener('cancel',e=>{e.preventDefault();close();});
    $('credential-new').addEventListener('click',()=>{if(!ctx.busy())selection();});
    $('credential-reload').addEventListener('click',()=>run(async()=>{await load();selection();say('已刷新，表单与密钥输入已重置。');}));
    $('credential-visible').addEventListener('change',()=>{$('credential-value').type=$('credential-visible').checked?'text':'password';});
    $('credential-confirm').addEventListener('change',sync);
    for(const id of ['credential-name','credential-value'])$(id).addEventListener('input',()=>{$('credential-confirm').checked=false;sync();});
    $('credential-save').addEventListener('click',()=>{
      if(ctx.busy()||!loaded||!$('credential-confirm').checked||loaded.vault.pending.length)return;
      const action=$('credential-action').value,name=$('credential-name').value;
      if(!/^[a-z][a-z0-9_]{0,63}$/.test(name)){say('名称须为小写字母开头及字母、数字、下划线，最多 64 字符。',true);return;}
      if(action!=='delete'&&!$('credential-value').value){say('请输入密钥值。',true);return;}
      run(async()=>{
        const body={action,name,confirm:true};if(action!=='add')body.expected_revision=revision;
        if(action==='delete'){body.references_reviewed=true;body.expected_references=loaded.review;}else body.value=$('credential-value').value;
        try{await ctx.api('/api/credentials/change',body);}finally{delete body.value;}
        clearValue();selection();say('系统凭据操作已完成，正在刷新状态。');
        try{await load();say('已完成并记录审计。密钥输入已清空。');}catch(e){throw new Error(`系统凭据操作已完成，状态刷新失败：${e.message}。请刷新后再操作。`);}
      });
    });
    $('credential-reconcile').addEventListener('click',()=>run(async()=>{await ctx.api('/api/credentials/reconcile',{});await load();selection();say('已核对系统修订并补记结果；没有恢复或重复写入密钥值。');}));
    window.addEventListener('pagehide',clearValue);sync();
  }
  return{init,sync};
})();
