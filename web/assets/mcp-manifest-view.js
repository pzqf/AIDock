'use strict';
const MCPManifestView = (() => {
  const $=id=>document.getElementById(id);
  const element=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
  const status=v=>({PASS:'已通过',FAIL:'需处理',UNKNOWN:'待验证'})[v]||v;
  let ctx,loaded=null;
  function say(text,error=false){$('mcp-manifest-message').textContent=text;$('mcp-manifest-message').classList.toggle('error',error);}
  function sync(){if(!ctx)return;for(const n of $('mcp-manifest-dialog').querySelectorAll('button,select'))n.disabled=ctx.busy();$('mcp-manifest-open').disabled=ctx.busy();}
  function detail(parent,title,value){const d=element('details',undefined,'native-rule-preview');d.append(element('summary',title),element('pre',value));parent.append(d);}
  function render(){
    const root=$('mcp-manifest-results');root.replaceChildren();if(!loaded)return;
    for(const f of loaded.checks||[])root.append(element('p',f.detail,'notice'));
    const selected=$('mcp-manifest-filter').value;
    for(const p of loaded.plugins||[]){
      if(selected&&p.name!==selected)continue;
      const card=element('section',undefined,'native-check-card');
      const installed=p.installation_state==='INSTALLED'?'已登记本机安装 · 轻量 SBOM 已生成':p.installation_state==='STALE'?'已有安装登记 · 当前检查已变化 · 历史 SBOM 需重新审查':'预备产物 · 尚未登记安装 · 正式 SBOM 待生成';
      card.append(element('h3',`${p.name} · ${status(p.status)}`),element('p',installed,'caption'));
      const m=p.manifest;
      if(m){
        card.append(element('h4',`${m.name} · ${m.version}`));
        if(m.description)card.append(element('p',m.description,'caption'));
      }
      for(const f of p.checks||[])card.append(element('p',`${status(f.status)} · ${f.detail}`,'caption'));
      if(m){
        const section=element('details',undefined,'native-rule-preview');section.append(element('summary','核对文件、网络与子进程权限'));
        section.append(element('p','产物清单声明','caption'),element('pre',JSON.stringify(m.permissions,null,2)),element('p','当前已保存 MCP 定义','caption'),element('pre',JSON.stringify(p.definition_permissions||{},null,2)));card.append(section);
        const deps=element('section',undefined,'rehearsal-section');deps.append(element('h4','直接依赖声明'));
        if(!Object.hasOwn(m,'dependencies'))deps.append(element('p','未声明。不能按“没有依赖”处理。','caption'));
        else if(!m.dependencies.length)deps.append(element('p','清单明确声明空的直接依赖列表；是否完整见上方核验结果。','caption'));
        else{
          deps.append(element('p',`${m.dependencies.length} 项；仅列清单直接声明，不包含传递依赖。`,'caption'));
          const table=element('table',undefined,'manifest-dependencies'),head=element('thead'),row=element('tr');
          for(const name of ['生态','依赖','固定版本'])row.append(element('th',name));head.append(row);table.append(head);
          const body=element('tbody');for(const d of m.dependencies){const row=element('tr');for(const value of [d.ecosystem,d.name,d.version])row.append(element('td',value));body.append(row);}table.append(body);deps.append(table);
        }
        card.append(deps);
      }
      if(p.document)detail(card,'查看 plugin.yaml 原文',p.document);
      detail(card,'摘要与检查代码',JSON.stringify({artifact_sha256:p.artifact_sha256,artifact_status:p.artifact_status,manifest_sha256:p.manifest_sha256,manifest_status:p.manifest_status,permission_status:p.permission_status,dependency_status:p.dependency_status,installation_state:p.installation_state,sbom_status:p.sbom_status,checks:p.checks},null,2));
      root.append(card);
    }
    if(!(loaded.plugins||[]).length&&!(loaded.checks||[]).length)root.append(element('p','没有已保存的安装锁条目。先保存 MCP 定义，在安装锁页读取本地产物并确认保存。','caption'));
    $('mcp-manifest-boundary').textContent=loaded.boundary;
  }
  async function load(){
    loaded=null;$('mcp-manifest-results').replaceChildren();$('mcp-manifest-boundary').textContent='';say('正在按当前安装锁重新核验产物…');
    loaded=(await ctx.api('/api/mcp/manifest/state')).items;
    const selected=$('mcp-manifest-filter').value;$('mcp-manifest-filter').replaceChildren();const all=element('option','全部 MCP');all.value='';$('mcp-manifest-filter').append(all);
    for(const p of loaded.plugins||[]){const n=element('option',p.name);n.value=p.name;$('mcp-manifest-filter').append(n);}
    $('mcp-manifest-filter').value=(loaded.plugins||[]).some(p=>p.name===selected)?selected:'';render();say('已读取当前产物。清单核验结果与来源信任、安装登记和运行资格分别显示。');
  }
  async function run(fn){if(ctx.busy())return;ctx.setBusy(true);try{await fn();}catch(e){loaded=null;$('mcp-manifest-results').replaceChildren();say(e.message,true);}finally{ctx.setBusy(false);}}
  function close(){if(ctx.busy())return;$('mcp-manifest-dialog').close?.();$('mcp-manifest-dialog').hidden=true;ctx.message('已关闭清单检查。MCP 定义和安装锁草稿保留。');}
  function init(context){
    ctx=context;
    $('mcp-manifest-open').addEventListener('click',()=>{if(ctx.busy())return;$('mcp-manifest-dialog').hidden=false;if(!$('mcp-manifest-dialog').open)$('mcp-manifest-dialog').showModal?.();$('mcp-manifest-dialog').scrollTop=0;run(load);});
    $('mcp-manifest-close').addEventListener('click',close);$('mcp-manifest-dialog').addEventListener('cancel',e=>{e.preventDefault();close();});
    $('mcp-manifest-reload').addEventListener('click',()=>run(load));$('mcp-manifest-filter').addEventListener('change',()=>{if(!ctx.busy())render();});sync();
  }
  return {init,sync};
})();
