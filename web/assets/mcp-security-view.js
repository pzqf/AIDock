'use strict';
const MCPSecurityView=(()=>{
  const $=id=>document.getElementById(id),el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
  const label=v=>({MISSING:'未提供快照',STALE:'绑定已变化',EXPIRED:'已过期或时间异常',PARTIAL:'覆盖不完整',FOUND:'有已记录风险',NO_KNOWN_MATCHES:'本快照未记录漏洞'})[v]||v;
  let ctx,loaded=null,plan=null;
  const dirty=()=>Boolean($('mcp-security-document').value.trim())||MCPSecurityAssessments.hasDraft();
  const blocked=()=>!loaded||ctx.pending()||loaded.recovery_required;
  function say(text,error=false){$('mcp-security-message').textContent=text;$('mcp-security-message').classList.toggle('error',error);}
  function invalidate(){plan=null;$('mcp-security-plan').hidden=true;}
  function sync(){if(!ctx)return;for(const n of $('mcp-security-dialog').querySelectorAll('button,select,textarea,input'))n.disabled=ctx.busy();$('mcp-security-open').disabled=ctx.busy();$('mcp-security-preview').disabled=ctx.busy()||blocked()||!$('mcp-security-document').value.trim();$('mcp-security-template').disabled=ctx.busy()||blocked()||!$('mcp-security-name').value;$('mcp-security-confirm').disabled=ctx.busy()||blocked()||!plan;for(const id of ['mcp-security-cache','mcp-security-history'])for(const n of $(id).querySelectorAll('button'))n.disabled=ctx.busy()||blocked();MCPSecurityAssessments.sync();}
  function detail(target,title,data){const d=el('details',undefined,'native-rule-preview');d.append(el('summary',title),el('pre',typeof data==='string'?data:JSON.stringify(data,null,2)));target.append(d);}
  function showResults(target,results){
    target.replaceChildren();for(const c of results||[]){const card=el('section',undefined,'native-check-card');card.append(el('h3',`${c.name} · ${label(c.cve_status)}`),el('p',`覆盖 ${c.covered}/${c.required} 项 · 已记录 ${c.advisory_count} 条风险 · 信任 ${c.trust_level}`,'caption'));
      for(const f of c.checks||[])card.append(el('p',`${({PASS:'已通过',UNKNOWN:'待验证',FAIL:'需处理'})[f.status]||f.status} · ${f.detail}`,f.status==='FAIL'?'notice error':'caption'));
      if(c.snapshot){const s=c.snapshot;card.append(el('p',`参考来源：${s.provider}\n采集：${s.collected_at}\n到期：${s.expires_at}`,'caption'));detail(card,'参考来源地址及内容摘要',{reference_url:s.reference_url,snapshot_sha256:c.snapshot_sha256,subject_sha256:c.subject_sha256});}
      if(c.assessment){card.append(el('p',`独立评估：${c.assessment.status} · ${c.assessment.authenticated?'登记密钥验签通过':'尚无已认证报告'} · ${c.assessment.current?'当前有效':'待核对或已过期'}`,'caption'));if(c.assessment.report)detail(card,'外部评估方法、逐项结论与签名',c.assessment);}
      for(const p of c.packages||[]){const row=el('section',undefined,'rehearsal-section');row.append(el('h4',`${p.ecosystem} · ${p.name}@${p.version}`),el('p',p.advisories===null||p.advisories===undefined?'尚未覆盖':p.advisories.length?`${p.advisories.length} 条已记录风险`:'本快照未记录漏洞','caption'));for(const a of p.advisories||[])detail(row,`${a.id} · ${a.severity} · ${a.summary}`,a);card.append(row);}target.append(card);
    }if(!results?.length)target.append(el('p','没有可展示的当前结果；请核对上方诊断及已保存的安装锁。','caption'));
  }
  function draft(document){if(dirty()){say('当前草稿保留。请先复制或清空，再载入另一个模板或快照。',true);return;}$('mcp-security-document').value=document;invalidate();sync();say('已放入草稿。逐项填写真实来源和核对结果；advisories:null 表示尚未检查，不能直接保存。');}
  function render(){
    $('mcp-security-boundary').textContent=loaded.boundary;const checks=$('mcp-security-checks');checks.replaceChildren();for(const f of loaded.checks||[])checks.append(el('p',f.detail,'notice error'));$('mcp-security-recover').hidden=!loaded.recovery_required;showResults($('mcp-security-results'),loaded.results);
    const selected=$('mcp-security-name').value;$('mcp-security-name').replaceChildren();for(const s of loaded.subjects||[]){const c=(loaded.results||[]).find(c=>c.subject_sha256===s.subject_sha256),option=el('option',c?.name||s.subject_sha256.slice(0,12));option.value=s.subject_sha256;$('mcp-security-name').append(option);}if(loaded.subjects?.some(s=>s.subject_sha256===selected))$('mcp-security-name').value=selected;
    const cache=$('mcp-security-cache');cache.replaceChildren();for(const [subject,document]of Object.entries(loaded.documents||{})){const card=el('section',undefined,'native-check-card');card.append(el('p',`快照绑定 ${subject}`,'caption'));detail(card,'已保存快照原文',document);const copy=el('button','载入快照到草稿','btn');copy.type='button';copy.addEventListener('click',()=>{if(!ctx.busy())draft(document);});const remove=el('button','预览移除此快照','btn');remove.type='button';remove.addEventListener('click',()=>{if(!ctx.busy()&&!blocked())run(()=>preview({action:'remove',subject_sha256:subject}));});card.append(copy,remove);cache.append(card);}if(!Object.keys(loaded.documents||{}).length)cache.append(el('p','尚无本机参考快照。','caption'));
    const history=$('mcp-security-history');history.replaceChildren();for(const h of loaded.history||[]){const card=el('section',undefined,'native-check-card');card.append(el('p',`${h.action} · ${h.state} · ${h.updated}\n${h.id}`,'caption'));if(h.request?.authority_reason)card.append(el('p',`本次评估者核对：${h.request.authority_reason}`,'caption'));if(h.state==='committed'){const b=el('button',h.action==='security-rollback'?'预览撤销快照回退':'预览回退快照变更','btn');b.type='button';b.addEventListener('click',()=>{if(!ctx.busy()&&!blocked())run(()=>preview({action:'rollback',snapshot:h.id,...MCPSecurityAssessments.confirmation()}));});card.append(b);}history.append(card);}MCPSecurityAssessments.render(loaded);
  }
  async function load(){invalidate();loaded=null;for(const id of ['mcp-security-results','mcp-security-cache','mcp-security-history','mcp-security-checks','mcp-security-authorities','mcp-security-assessment-cache'])$(id).replaceChildren();loaded=(await ctx.api('/api/mcp/security/state')).items;render();say('已核对当前产物、参考及评估有效期。草稿保留；信任等级按逐项证据显示，运行授权单独检查。');}
  async function run(fn){if(ctx.busy())return;ctx.setBusy(true);try{await fn();}catch(e){invalidate();say(e.message,true);}finally{ctx.setBusy(false);}}
  async function preview(req){invalidate();const out=(await ctx.api('/api/mcp/security/plan',{...req,expected_sha256:loaded.sha256})).items;plan=out.plan;const target=$('mcp-security-changes');target.replaceChildren();detail(target,'确认前的缓存原文',out.before.documents);detail(target,'确认后的缓存原文',out.candidate.documents);detail(target,'确认前评估者与签名报告',{authorities:out.before.authorities,reports:out.before.assessment_documents});detail(target,'确认后评估者与签名报告',{authorities:out.candidate.authorities,reports:out.candidate.assessment_documents});if(req.authority_reason)detail(target,'本次评估者确认',req.authority_reason);showResults($('mcp-security-candidate'),out.candidate.results);$('mcp-security-plan').hidden=false;say('请核对前后参考、评估者范围、逐项结论和风险变化，再确认保存本机缓存。');}
  function close(){if(ctx.busy())return;$('mcp-security-dialog').close?.();$('mcp-security-dialog').hidden=true;ctx.message(dirty()?'安全快照草稿保留在当前标签页。':'安全快照管理已关闭。');}
  function init(context){ctx=context;MCPSecurityAssessments.init({ctx,loaded:()=>loaded,blocked,run,preview,say,invalidate,sync,detail,el});$('mcp-security-open').addEventListener('click',()=>{if(ctx.busy())return;$('mcp-security-dialog').hidden=false;if(!$('mcp-security-dialog').open)$('mcp-security-dialog').showModal?.();$('mcp-security-dialog').scrollTop=0;run(load);});$('mcp-security-close').addEventListener('click',close);$('mcp-security-dialog').addEventListener('cancel',e=>{e.preventDefault();close();});$('mcp-security-reload').addEventListener('click',()=>run(load));$('mcp-security-name').addEventListener('change',()=>{if(!ctx.busy()){invalidate();sync();}});$('mcp-security-document').addEventListener('input',()=>{invalidate();sync();});$('mcp-security-template').addEventListener('click',()=>{if(ctx.busy()||blocked())return;const s=loaded.subjects?.find(s=>s.subject_sha256===$('mcp-security-name').value);if(s)draft(JSON.stringify(s,null,2));});$('mcp-security-preview').addEventListener('click',()=>{if(!ctx.busy()&&!blocked()&&dirty())run(()=>preview({action:'import',document:$('mcp-security-document').value}));});$('mcp-security-cancel').addEventListener('click',()=>{if(ctx.busy())return;invalidate();sync();say('预览已取消，草稿保留。');});$('mcp-security-confirm').addEventListener('click',()=>{if(ctx.busy()||blocked()||!plan)return;const p=plan;run(async()=>{await ctx.api('/api/mcp/security/apply',{plan_id:p.id,digest:p.digest});invalidate();if(!p.action||p.action==='security-import')$('mcp-security-document').value='';MCPSecurityAssessments.applied(p.action);await load();await ctx.refreshChecks();$('mcp-security-dialog').scrollTop=0;say('本机参考快照已保存，恢复点保留。安装状态已重读；未运行扫描器或插件。');});});$('mcp-security-recover').addEventListener('click',()=>run(async()=>{await ctx.api('/api/mcp/security/recover',{});await load();await ctx.refreshChecks();say('中断快照保存已恢复，草稿保留，请重新核对。');}));sync();}
  return {init,sync,hasDraft:dirty};
})();

const MCPSecurityAssessments=(()=>{
  const $=id=>document.getElementById(id);
  let host;
  const text=id=>$(id).value.trim();
  const hasDraft=()=>Boolean(text('mcp-security-authorities-document')||text('mcp-security-assessment-document')||$('mcp-security-authority-confirmation').checked||text('mcp-security-authority-reason'));
  const confirmation=()=>$('mcp-security-authority-confirmation').checked?{authority_confirmation:true,authority_reason:$('mcp-security-authority-reason').value}:{};
  function sync(){
    if(!host)return;
    const disabled=host.ctx.busy()||host.blocked();
    $('mcp-security-authorities-preview').disabled=disabled||!text('mcp-security-authorities-document')||!$('mcp-security-authority-confirmation').checked||!text('mcp-security-authority-reason');
    $('mcp-security-assessment-preview').disabled=disabled||!text('mcp-security-assessment-document');
    $('mcp-security-assessment-template').disabled=disabled||!$('mcp-security-name').value;
    $('mcp-security-authorities-load').disabled=disabled;
    for(const n of $('mcp-security-assessment-cache').querySelectorAll('button'))n.disabled=disabled;
  }
  function put(id,value){
    if(text(id)){host.say('当前草稿保留，请先复制或清空，再载入另一份内容。',true);return;}
    $(id).value=typeof value==='string'?value:JSON.stringify(value,null,2);
    host.invalidate();host.sync();host.say('已载入草稿，尚未保存。外部报告须由已独立核对的评估者签名。');
  }
  function render(s){
    const target=$('mcp-security-authorities');target.replaceChildren();
    for(const a of s.authorities?.authorities||[]){const card=host.el('section',undefined,'native-check-card');card.append(host.el('h4',`${a.label} · ${a.enabled?'已登记':'已停用'}`),host.el('p',`${a.provider} · ${a.source_type} / ${a.source_id}\n到期：${a.expires_at}`,'caption'));host.detail(card,'公钥指纹与独立核对来源',a);target.append(card);}
    if(!target.children.length)target.append(host.el('p','尚未独立登记评估者，来源签名公钥不会自动授权评估。','caption'));
    const cache=$('mcp-security-assessment-cache');cache.replaceChildren();
    for(const [subject,raw]of Object.entries(s.assessment_documents||{})){const card=host.el('section',undefined,'native-check-card');host.detail(card,`签名报告原文 · ${subject.slice(0,12)}`,raw);const copy=host.el('button','载入签名报告到草稿','btn');copy.type='button';copy.addEventListener('click',()=>{if(!host.ctx.busy())put('mcp-security-assessment-document',raw);});const remove=host.el('button','预览移除签名报告','btn');remove.type='button';remove.addEventListener('click',()=>{if(!host.ctx.busy()&&!host.blocked())host.run(()=>host.preview({action:'assessment-remove',subject_sha256:subject}));});card.append(copy,remove);cache.append(card);}
    if(!cache.children.length)cache.append(host.el('p','尚无当前项目本机的签名评估缓存。','caption'));
  }
  function applied(action){
    if(action==='security-assessment-import')$('mcp-security-assessment-document').value='';
    if(action==='security-authorities')$('mcp-security-authorities-document').value='';
    if(action==='security-authorities'||action==='security-rollback'){$('mcp-security-authority-confirmation').checked=false;$('mcp-security-authority-reason').value='';}
  }
  function init(context){
    host=context;
    for(const id of ['mcp-security-authorities-document','mcp-security-assessment-document','mcp-security-authority-reason'])$(id).addEventListener('input',()=>{host.invalidate();host.sync();});
    $('mcp-security-authority-confirmation').addEventListener('change',()=>{host.invalidate();host.sync();});
    $('mcp-security-authorities-load').addEventListener('click',()=>{if(!host.ctx.busy()&&!host.blocked())put('mcp-security-authorities-document',host.loaded().authorities||{schema_version:1,authorities:[]});});
    $('mcp-security-assessment-template').addEventListener('click',()=>{if(host.ctx.busy()||host.blocked())return;const subject=$('mcp-security-name').value;const p=host.loaded().assessment_subjects?.find(p=>p.reference.subject_sha256===subject);if(p)put('mcp-security-assessment-document',{schema_version:1,key_id:'',payload:p,signature:''});});
    $('mcp-security-authorities-preview').addEventListener('click',()=>{if(host.ctx.busy()||host.blocked())return;host.run(async()=>{let authorities;try{authorities=JSON.parse($('mcp-security-authorities-document').value);}catch{throw new Error('评估者公钥清单需要完整 JSON。草稿保留。');}await host.preview({action:'authorities',authorities,...confirmation()});});});
    $('mcp-security-assessment-preview').addEventListener('click',()=>{if(!host.ctx.busy()&&!host.blocked()&&text('mcp-security-assessment-document'))host.run(()=>host.preview({action:'assessment-import',document:$('mcp-security-assessment-document').value}));});
  }
  return {init,sync,render,hasDraft,confirmation,applied};
})();
