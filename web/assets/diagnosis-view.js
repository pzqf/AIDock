'use strict';
const DiagnosisView = (() => {
  const el=id=>document.getElementById(id);
  const node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
  const labels={PASS:'已核对',FAIL:'需处理',WARN:'请留意',UNKNOWN:'未验证',NOT_APPLIED:'待安装',STALE:'已过期',SKIPPED:'无需检查',RECOVERY_REQUIRED:'待恢复'};
  const names={source:'技能源与配置',profile:'配置来源',tools:'工具文件与生效',replica:'副本登记',recovery:'恢复记录'};
  const destinations={source:['source','打开技能库'],profile:['profile','查看 Profile 与规则'],tools:['native','打开 AI 工具'],replica:['replica','打开跨副本'],recovery:['history','查看保存记录']};
  let context=null,epoch=0;
  function sync(){
    if(!context)return;
    el('nav-diagnosis').disabled=context.busy();
    el('diagnosis-refresh').disabled=context.busy();
    el('diagnosis-close').disabled=context.busy();
    for(const b of document.querySelectorAll?.('.diagnosis-go')||[])b.disabled=context.busy();
  }
  function close(){if(context?.busy())return;epoch++;el('diagnosis-dialog').close?.();el('diagnosis-dialog').hidden=true;}
  function invalidate(){epoch++;el('diagnosis-result').hidden=true;el('diagnosis-message').textContent='项目状态已变化，请刷新诊断。';}
  function status(text){const badge=node('span',labels[text]||text);badge.className=`diagnosis-status status-${text.toLowerCase()}`;return badge;}
  function go(destination,label){const b=node('button',label);b.className='btn diagnosis-go';b.type='button';b.addEventListener('click',()=>{if(context.busy())return;close();context.navigate(destination);});return b;}
  function render(r){
    el('diagnosis-result').hidden=false;
    el('diagnosis-summary').textContent=r.status==='ACTION_REQUIRED'?'有待处理项目，请查看下方原因':'文件与配置检查完成，尚有生效项待验证';
    el('diagnosis-binding').textContent=`${r.root} · ${r.scope_id}${r.copy?' · '+r.copy:''}`;
    const copies=Number.isInteger(r.registered_copies)?`${r.registered_copies} 个已登记副本`:'副本数量暂不可读取';
    el('diagnosis-time').textContent=`检查时间 ${new Date(r.observed).toLocaleString('zh-CN')} · ${copies}`;
    el('diagnosis-boundary').textContent=r.boundary;
    const sections=el('diagnosis-sections');sections.replaceChildren();
    for(const s of r.sections||[]){
      const card=node('article');card.className='diagnosis-card';
      const head=node('div');head.className='list-heading';head.append(node('h3',names[s.id]||s.id),status(s.status));card.append(head,node('p',s.summary));
      const findings=(s.findings||[]).filter(f=>f.status!=='PASS');
      if(findings.length){const list=node('details');list.open=s.status==='FAIL';list.append(node('summary',`${findings.length} 项诊断`));for(const f of findings)list.append(node('p',`${labels[f.status]||f.status} · ${f.code}：${f.detail}`));card.append(list);}
      if(destinations[s.id])card.append(go(...destinations[s.id]));
      sections.append(card);
    }
    const tools=el('diagnosis-tools');tools.replaceChildren();
    if(!r.tools?.length)tools.append(node('p','当前工具文件未能核对，原因见上方诊断。'));
    for(const t of r.tools||[]){
      const card=node('article');card.className='diagnosis-card';
      card.append(node('h3',{'claude-code':'Claude Code','codex-cli':'Codex CLI'}[t.tool]||t.tool),node('p',t.installed?'已找到本机程序':'未找到本机程序'),node('code',t.target));
      const levels=node('dl');levels.className='diagnosis-levels';
      for(const [label,key] of [['当前文件一致','files'],['隔离项目发现','discovery'],['实际会话加载','session'],['能力验证','capability'],['隔离规则来源','rules']]){const dd=node('dd');dd.append(status(t[key]));levels.append(node('dt',label),dd);}
      card.append(levels);
      if(t.evidence){const e=t.evidence,detail=node('details');detail.append(node('summary',e.stale?'历史检查已过期':'已有检查记录'),node('p',`${new Date(e.created).toLocaleString('zh-CN')} · ${t.version?'v'+t.version:'未核对版本'} · ${e.application_scope==='skills-rules'?'仅技能与规则':'完整 Profile 检查'}`));if(e.stale)detail.append(node('p',e.stale_reason),node('p',`历史：L1 ${labels[e.L1]||e.L1} / L2 ${labels[e.L2]||e.L2} / L3 ${labels[e.L3]||e.L3} / L4 ${labels[e.L4]||e.L4}`));detail.append(node('code',e.id));card.append(detail);}
      else card.append(node('p','尚无本项目的工具检查记录。'));
      for(const f of t.findings||[])if(f.status==='FAIL')card.append(node('p',`${f.code}：${f.detail}`));
      tools.append(card);
    }
    const origins=el('diagnosis-origins');origins.replaceChildren();
    origins.append(node('p',(r.layers||[]).map(l=>l.name||l.id).join(' → ')||'来源链尚未解析'));
    if(!r.local_origins?.length)origins.append(node('p','本次未发现已解析的本机配置项。'));
    for(const o of r.local_origins||[]){const line=node('p');line.append(node('code',o.path),node('span',o.previous_layer?` · 本机层覆盖 ${o.previous_layer}`:' · 本机层设置'));origins.append(line);}
    const recoveries=el('diagnosis-recoveries');recoveries.replaceChildren();
    for(const item of r.recoveries||[]){const row=node('article');row.className='diagnosis-recovery';const dest={source:['source','查看源恢复'],profile:['profile','查看本机 Profile 恢复'],native:['native','查看工具恢复'],mcp:['mcp','查看 MCP 连接恢复'],replica:['replica','查看正式同步恢复'],coordinated:['replica','查看副本入口']}[item.kind]||['history','查看恢复记录'];row.append(node('p',`${dest[1]}${item.id?' · '+item.id:''}`));if(item.kind==='coordinated')row.append(node('p','请切换到原发起副本，使用正式同步或预演的整批恢复；当前目标不能单独恢复。'));row.append(go(...dest));recoveries.append(row);}
    el('diagnosis-recovery-area').hidden=!r.recoveries?.length;
  }
  async function load(){
    if(!context||context.busy())return;
    const g=++epoch;el('diagnosis-result').hidden=true;el('diagnosis-message').textContent='正在读取当前项目状态…';context.setBusy(true);
    try{const data=await context.api('/api/diagnosis');if(g===epoch){render(data.items);el('diagnosis-message').textContent='诊断完成；选择对应入口继续处理。';}}
    catch(e){if(g===epoch)el('diagnosis-message').textContent=e.message;}
    finally{context.setBusy(false);sync();}
  }
  async function open(){if(!context||context.busy())return;el('diagnosis-dialog').hidden=false;el('diagnosis-dialog').showModal?.();await load();}
  el('nav-diagnosis').addEventListener('click',open);
  el('diagnosis-refresh').addEventListener('click',load);
  el('diagnosis-close').addEventListener('click',close);
  el('diagnosis-dialog').addEventListener('cancel',e=>{e.preventDefault();close();});
  return {init(ctx){context=ctx;invalidate();el('nav-diagnosis').hidden=false;sync();},sync,invalidate};
})();
