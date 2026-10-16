'use strict';
const AuditView=(()=>{
  const el=id=>document.getElementById(id),node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
  const categories={origin:'来源证据',install:'安装登记',permission:'项目权限',runtime:'运行时核验',session:'MCP 会话'};
  const statuses={PASS:'本机核对通过',EMPTY:'尚无记录',FAIL:'完整性未通过',RECOVERY_REQUIRED:'先恢复事务',NOT_APPLICABLE:'当前未绑定副本'};
  const actions={install:'登记安装',remove:'移除登记',rollback:'回退',keys:'更新公钥',evidence:'保存来源声明',grant:'授权',revoke:'撤销授权',INTENT:'记录意图',RELAY:'中继事件',COMPLETE:'记录结果'};
  let context=null,epoch=0,page=1,pages=0;
  function sync(){if(!context)return;const busy=context.busy();for(const id of ['nav-audit','audit-close','audit-refresh','audit-category','audit-search'])el(id).disabled=busy;el('audit-previous').disabled=busy||page<=1;el('audit-next').disabled=busy||page>=pages;}
  function invalidate(){epoch++;pages=0;el('audit-result').hidden=true;el('audit-message').textContent='项目状态已变化，请刷新审计。';sync();}
  function close(){if(context?.busy())return;epoch++;el('audit-dialog').close?.();el('audit-dialog').hidden=true;}
  function render(r){
    el('audit-result').hidden=false;el('audit-chain-area').open=r.status!=='PASS';page=r.query.page;pages=r.pages;
    el('audit-binding').textContent=`${r.root} · ${r.scope}${r.copy?' · '+r.copy:''} · ${new Date(r.observed).toLocaleString('zh-CN')}`;
    const chains=el('audit-chains');chains.replaceChildren();
    for(const c of r.chains||[]){const card=node('article');card.className='diagnosis-card';card.append(node('h3',categories[c.category]||c.category),node('p',statuses[c.status]||c.status),node('p',c.scope==='source'?'当前项目、作用域与源目录':'当前副本'),node('p',Number.isInteger(c.count)?`${c.count} 条事件`:'事件数量未核对'));if(c.code)card.append(node('code',c.code));if(c.head){const detail=node('details');detail.append(node('summary','本机链尾摘要'),node('code',c.head));card.append(detail);}card.append(node('p',c.anchor==='LOCAL_JOURNALS_ONLY'?'仅本机链与事务核对；无外部可信锚点':'锚点未核对'));chains.append(card);}
    el('audit-boundary').textContent=r.boundary;
    el('audit-page').textContent=`匹配 ${r.total} 条 · ${r.pages?`第 ${page} / ${r.pages} 页`:'暂无匹配事件'}`;
    const events=el('audit-events');events.replaceChildren();
    for(const e of r.events||[]){const card=node('article');card.className='diagnosis-card audit-event';card.append(node('h3',`${categories[e.category]||e.category} · ${actions[e.action]||e.action}`),node('p',`${new Date(e.at).toLocaleString('zh-CN')} · 链内序号 ${e.sequence}${e.subject?' · '+e.subject:''}${e.actor?' · '+e.actor:''}`));if(e.method)card.append(node('p',`${e.method}${e.tool?' · '+e.tool:''}`));if(e.code)card.append(node('p',e.code==='RISK_ACCEPTED'?'已记录安装风险放行确认':e.code==='UNISOLATED_ACCEPTED'?'已记录非隔离选择确认':e.code));card.append(node('code',e.id));const detail=node('details');detail.append(node('summary','事件摘要与关联'),node('p',`摘要 ${e.sha256}`),node('p',`前序 ${e.previous||'首条事件'}`));if(e.attempt)detail.append(node('p',`关联尝试 ${e.attempt}`));if(e.request_id)detail.append(node('p',`请求标识 ${e.request_id}`));card.append(detail);events.append(card);}
  }
  async function load(next=1){if(!context||context.busy())return;const g=++epoch;pages=0;el('audit-result').hidden=true;el('audit-message').textContent='正在核对审计链…';context.setBusy(true);try{const q=new URLSearchParams({category:el('audit-category').value,search:el('audit-search').value,page:String(next)}),v=await context.api('/api/audit?'+q);if(g===epoch){render(v.items);el('audit-message').textContent=v.items.status==='PASS'?'读取完成；事件结果与链完整性分别判断。':'部分链未通过核对，请到对应管理页处理；其它已核对记录仍可查看。';}}catch(e){if(g===epoch)el('audit-message').textContent=e.message;}finally{context.setBusy(false);sync();}}
  el('nav-audit').addEventListener('click',async()=>{if(!context||context.busy())return;el('audit-dialog').hidden=false;el('audit-dialog').showModal?.();await load();});
  el('audit-close').addEventListener('click',close);el('audit-dialog').addEventListener('cancel',e=>{e.preventDefault();close();});
  el('audit-filter').addEventListener('submit',e=>{e.preventDefault();return load();});
  el('audit-category').addEventListener('change',()=>load());el('audit-search').addEventListener('input',()=>{epoch++;pages=0;page=1;el('audit-result').hidden=true;el('audit-message').textContent='查询条件已变化，请点击查询 / 刷新。';sync();});
  el('audit-previous').addEventListener('click',()=>load(page-1));el('audit-next').addEventListener('click',()=>load(page+1));
  return{init(ctx){context=ctx;invalidate();el('nav-audit').hidden=false;sync();},sync,invalidate};
})();
