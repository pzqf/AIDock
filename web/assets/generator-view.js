const GeneratorView = (() => {
  const el=id=>document.getElementById(id);
  const node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
  const states={PREPARING:'准备中断 · 需清理',STAGED:'暂存已准备',CLEANING:'清理中断 · 可重试',CLEANED:'已清理'};
  let context,epoch=0,preview=null,current=null,cleanup=null,wired=false;
  const ready=()=>context&&!context.busy()&&!context.pending?.();
  function sync(){
    for(const id of ['generator-adapter','generator-inspect','generator-history'])el(id).disabled=!ready();
    el('generator-stage').disabled=!ready()||!preview;
    el('generator-stage-confirm').disabled=!ready()||!preview;
    el('generator-validate').disabled=!ready()||!current||current.state!=='STAGED'||current.stale||!current.cleanup_safe;
    el('generator-clean').disabled=!ready()||!current||current.state==='CLEANED'||!current.cleanup_safe;
    el('generator-clean-confirm').disabled=!ready()||!cleanup;
    for(const id of ['generator-stage-cancel','generator-clean-cancel'])el(id).disabled=Boolean(context?.busy());
    el('generator-run').disabled=true;
  }
  function close(id){el(id).close?.();el(id).hidden=true;}
  function show(id){el(id).hidden=false;el(id).showModal?.();sync();}
  function counts(tree){const entries=Object.values(tree?.entries||{});return entries.filter(e=>!e.directory).length+' 个文件 / '+entries.filter(e=>e.directory).length+' 个目录';}
  function facts(p,target){
    target.replaceChildren();
    target.append(node('p',p.contract.id+' · 声明版本 '+p.contract.declared_version+' · '+p.source.scope_id));
    target.append(node('p','完整运行依赖：'+counts(p.runtime)+' · '+p.contract.runtime_root));
    const status={PASS:'已通过',WARN:'有提醒',UNKNOWN:'部分未验证',FAIL:'有阻断'}[p.source.preflight.status]||p.source.preflight.status;
    target.append(node('p','输入：'+p.source.packages.length+' 个启用技能 · '+counts(p.input)+' · 源静态检查 '+status));
    target.append(node('p','目标：'+p.contract.tools.map(t=>t==='codex-cli'?'.agents/skills':'.claude/skills').join('、')));
    for(const pkg of p.source.packages)target.append(node('p',pkg.skill.native_name+' · '+pkg.skill.revision));
  }
  function render(o){
    current=o;el('generator-stage-result').hidden=!o;
    if(!o){sync();return;}
    const s=o.stage,p=s.preview;
    el('generator-stage-summary').textContent=(states[o.state]||o.state)+(o.stale?' · 已过期':'');
    el('generator-stage-path').textContent=s.root;
    el('generator-stage-identity').textContent='暂存 '+s.id+' · '+new Date(s.created).toLocaleString()+' · '+o.files+' 个文件 / '+o.directories+' 个目录';
    el('generator-stage-boundary').textContent=[o.stale_reason,o.cleanup_reason,p.boundary].filter(Boolean).join(' ');
    facts(p,el('generator-stage-facts'));
    el('generator-validation').replaceChildren();
    el('generator-validation').className='generator-validation';
    if(o.validation){
      const v=o.validation;
      el('generator-validation').className+=' status-'+(v.stale?'unknown':v.status.toLowerCase());
      el('generator-validation').append(node('strong',(v.stale?'已过期 · 历史 ':'')+(v.status==='PASS'?'返回包完整性校验通过':'返回包校验有阻断')));
      if(v.issues?.length){
        const issues=node('details');issues.append(node('summary','查看 '+v.issues.length+' 项校验问题'));
        for(const issue of v.issues)issues.append(node('p',issue));
        el('generator-validation').append(issues);
      }
    }else el('generator-validation').append(node('p',o.state==='CLEANED'?'暂存已清理，历史记录和字节快照已保留。可从暂存历史重新查看。':'尚未校验返回包。外部流程的返回内容只能放在此暂存的 output 目录；当前版本不启动生成器。'));
    const tree=el('generator-output-files');tree.replaceChildren();
    for(const [path,e] of Object.entries(p.expected_output.entries||{}).sort(([a],[b])=>a.localeCompare(b))){
      tree.append(node('p',path+(e.directory?'/':' · '+e.bytes+' 字节')));
    }
    sync();
  }
  async function task(message,fn){
    if(!ready())return;const version=++epoch;context.setBusy(true);context.message(message);
    try{await fn(version);}catch(e){if(version===epoch){if(current?.validation){current.validation.stale=true;render(current);}context.message(e.message,true);}}
    finally{context.setBusy(false);sync();}
  }
  async function inspect(){
    if(!ready())return;preview=null;sync();
    await task('正在锁定当前技能源、适配文件和完整运行依赖；不启动程序。',async version=>{
      const data=await context.api('/api/generator/inspect',{adapter_path:el('generator-adapter').value.trim()});
      if(version!==epoch)return;preview=data.items;el('generator-preview').hidden=false;facts(preview,el('generator-preview-facts'));
      el('generator-executable-hash').textContent='程序 SHA256：'+preview.executable_sha256;
      context.message('接入预览已准备。确认后只在本机复制暂存包。');
    });
  }
  async function stage(){
    if(!ready()||!preview)return;const reviewed=preview;close('generator-stage-dialog');
    await task('正在复制已锁定的输入和完整运行依赖。',async version=>{
      const data=await context.api('/api/generator/stage',{adapter_path:reviewed.adapter_path,digest:reviewed.digest});
      if(version!==epoch)return;render(data.items);preview=null;el('generator-preview').hidden=true;el('generator-history-result').hidden=true;
      context.message('暂存已准备。未启动生成器；可校验返回包或清理暂存。');
    });
  }
  async function view(id){
    await task('正在读取暂存状态和当前有效性。',async version=>{
      const data=await context.api('/api/generator/show',{stage_id:id});if(version===epoch){render(data.items);context.message('暂存状态已更新。');}
    });
  }
  async function history(){
    await task('正在读取生成器暂存历史。',async version=>{
      const data=await context.api('/api/generator/history');if(version!==epoch)return;
      const target=el('generator-history-list');target.replaceChildren();el('generator-history-result').hidden=false;
      if(!data.items.length)target.append(node('p','还没有生成器暂存。'));
      for(const o of data.items){
        const row=node('div');row.className='generator-history-row';
        row.append(node('span',o.stage.preview.contract.id+' · '+(states[o.state]||o.state)+(o.stale?' · 已过期':'')+' · '+new Date(o.stage.created).toLocaleString()));
        const button=node('button','查看暂存');button.type='button';button.className='btn sm';
        button.addEventListener('click',()=>view(o.stage.id));row.append(button);target.append(row);
      }
      context.message('已读取 '+data.items.length+' 条暂存历史。');
    });
  }
  async function validate(){
    if(!ready()||!current||current.stale||current.state!=='STAGED')return;const s=current.stage;
    if(current.validation)current.validation.stale=true;render(current);
    await task('正在核对输入、依赖和返回目录中的完整技能包。',async version=>{
      const result=await context.api('/api/generator/validate',{stage_id:s.id,digest:s.digest});
      const shown=await context.api('/api/generator/show',{stage_id:s.id});
      if(version===epoch){render(shown.items);context.message(result.items.status==='PASS'?'返回包校验通过，记录已保存。执行与发布仍未开放。':'返回包校验有阻断，请查看缺失、额外或变动文件。',result.items.status!=='PASS');}
    });
  }
  async function prepareCleanup(){
    if(!ready()||!current)return;const id=current.stage.id;cleanup=null;
    await task('正在读取将要清理的具体目录与内容。',async version=>{
      const data=await context.api('/api/generator/show',{stage_id:id});if(version!==epoch)return;
      render(data.items);if(!current.cleanup_safe||current.state==='CLEANED')return;
      cleanup=current;el('generator-clean-path').textContent=current.stage.root;
      el('generator-clean-count').textContent=current.files+' 个文件 / '+current.directories+' 个目录，包括暂存输入、依赖、返回内容与临时文件。';
      show('generator-clean-dialog');
      context.message('清理预览已准备。确认后只删除此专用暂存。');
    });
  }
  async function clean(){
    if(!ready()||!cleanup)return;const reviewed=cleanup;close('generator-clean-dialog');
    await task('正在清理已确认的专用暂存目录；历史与字节快照保留。',async version=>{
      const data=await context.api('/api/generator/clean',{stage_id:reviewed.stage.id,digest:reviewed.observation_digest});
      if(version===epoch){cleanup=null;render(data.items);el('generator-history-result').hidden=true;context.message('暂存已清理，历史记录保留。');}
    });
  }
  function init(c){
    context=c;epoch++;preview=null;current=null;cleanup=null;
    for(const id of ['generator-preview','generator-stage-result','generator-history-result'])el(id).hidden=true;
    if(wired){sync();return;}wired=true;
    el('generator-adapter').addEventListener('input',()=>{preview=null;el('generator-preview').hidden=true;sync();});
    el('generator-inspect').addEventListener('click',inspect);
    el('generator-history').addEventListener('click',history);
    el('generator-stage').addEventListener('click',()=>{if(!ready()||!preview)return;facts(preview,el('generator-stage-review'));show('generator-stage-dialog');});
    el('generator-stage-confirm').addEventListener('click',stage);
    el('generator-validate').addEventListener('click',validate);
    el('generator-clean').addEventListener('click',prepareCleanup);
    el('generator-clean-confirm').addEventListener('click',clean);
    for(const kind of ['stage','clean']){
      el('generator-'+kind+'-cancel').addEventListener('click',()=>{if(!context.busy()){close('generator-'+kind+'-dialog');if(kind==='clean'){cleanup=null;context.message('已取消清理，暂存内容保留。');}}});
      el('generator-'+kind+'-dialog').addEventListener('cancel',e=>{e.preventDefault();if(!context.busy()){close('generator-'+kind+'-dialog');if(kind==='clean')cleanup=null;}});
    }
    sync();
  }
  return {init,sync};
})();
