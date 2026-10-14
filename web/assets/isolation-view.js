'use strict';
const IsolationView = (() => {
  const el=id=>document.getElementById(id);
  const node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;};
  const states={PASS:'已验证',FAIL:'阻断',UNKNOWN:'证据不足',UNAVAILABLE:'当前平台不可用'};
  const labels={APPCONTAINER_TOKEN:'OS 隔离令牌',DECLARED_INPUT_READ:'声明输入读取',DECLARED_OUTPUT_WRITE:'声明输出生成',SCRATCH_WRITE:'临时目录写入',READ_ONLY_INPUT:'输入只读',READ_ONLY_BINARY:'执行依赖只读',PROJECT_READ_DENIED:'测试项目越界读取',PROJECT_WRITE_DENIED:'测试项目越界写入',LOCAL_READ_DENIED:'测试私有目录读取',LOCAL_WRITE_DENIED:'测试私有目录写入',JUNCTION_READ_DENIED:'junction 越界读取',JUNCTION_WRITE_DENIED:'junction 越界写入',LOOPBACK_TCP_DENIED:'回环 TCP',OUTBOUND_TCP_DENIED:'外出 TCP',OUTBOUND_UDP_DENIED:'外出 UDP',CHILD_PROCESS_DENIED:'禁止创建子进程',JOB_EXIT_CLEANUP:'正常退出清理',TIMEOUT_JOB_CLEANUP:'超时进程清理',HOST_SENTINELS_UNCHANGED:'原输入与哨兵保留',HOST_OUTPUT_VERIFIED:'完整测试包校验',HOST_NO_CHILD_ESCAPE:'子进程逃逸检查',OUTPUT_PATHS_SAFE:'输出路径检查',FIXTURE_CLEANUP:'临时目录与 profile 清理'};
  let context,epoch=0,pendingRows=[];
  labels.EXECUTABLE_LOCK='执行程序摘要锁';
  const ready=()=>context&&!context.busy()&&!context.pending?.();
  function sync(){
    for(const id of ['isolation-static','isolation-probe','isolation-last','isolation-pending','isolation-probe-confirm'])el(id).disabled=!ready();
    for(const id of ['isolation-recover','isolation-recover-confirm'])el(id).disabled=!ready()||!pendingRows.length;
    el('isolation-recover-cancel').disabled=Boolean(context?.busy());
    el('isolation-generator-run').disabled=true;
    el('isolation-probe-cancel').disabled=Boolean(context?.busy());
  }
  function cancel(){el('isolation-probe-dialog').close?.();el('isolation-probe-dialog').hidden=true;}
  function render(r){
    el('isolation-result').hidden=false;el('isolation-findings').replaceChildren();
    if(!r){el('isolation-summary').textContent='还没有执行隔离检查记录';el('isolation-platform').textContent='点击“验证隔离边界”运行专用测试；读取记录不会重新启动进程。';el('isolation-boundary').textContent='外部生成器执行与正式发布尚未开放。';el('isolation-identity').textContent='';return;}
    const historic=r.stale?'已过期 · 历史 ':'';
    el('isolation-summary').textContent=`${historic}${r.probe?'隔离原型':'静态能力'} · ${states[r.status]||r.status} · 外部执行尚未开放`;
    el('isolation-platform').textContent=`${r.platform.os} / ${r.platform.arch} · 系统 ${r.platform.kernel} · ${r.platform.apis_available?'检测到平台 API':'缺少平台 API'}`;
    el('isolation-boundary').textContent=[r.stale_reason,r.boundary].filter(Boolean).join(' ');
    el('isolation-identity').textContent=r.id?`检查 ${r.id} · ${new Date(r.created).toLocaleString()} · 有效期至 ${new Date(r.expires).toLocaleString()}`:'静态读取不启动进程，也不保存执行证据。';
    const groups=[['目录与完整包',f=>!(/TCP|UDP|PROCESS|CLEANUP/.test(f.code))],['网络边界',f=>/TCP|UDP/.test(f.code)],['进程与清理',f=>/PROCESS|CLEANUP/.test(f.code)]];
    for(const [title,predicate] of groups){
      const rows=(r.findings||[]).filter(predicate);if(!rows.length)continue;
      const passed=rows.filter(f=>f.status==='PASS').length;
      const card=node('details');
      const status=rows.some(f=>f.status==='FAIL')?'FAIL':rows.every(f=>f.status==='PASS')?'PASS':'UNKNOWN';
      card.className=`isolation-card status-${status.toLowerCase()}`;
      card.append(node('summary',`${title} · ${historic}${states[status]} · ${passed}/${rows.length} 项通过`));
      for(const f of rows){const item=node('div');item.className='isolation-finding';item.append(node('strong',`${labels[f.code]||f.code} · ${historic}${states[f.status]||f.status}`),node('p',f.detail));card.append(item);}
      el('isolation-findings').append(card);
    }
  }
  async function run(probe=false){
    if(!ready())return;const current=++epoch;cancel();context.setBusy(true);
    context.message(probe?'正在验证 OS 隔离边界；只运行固定测试程序，最多等待十余秒。':'正在读取平台能力；不启动进程。');
    try{const data=await context.api('/api/platform/check',{probe});if(current===epoch){render(data.items);context.message(probe?'隔离检查已保存。请查看具体通过项与缺失证据。':'平台静态能力已读取。');}}
    catch(e){if(current===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  async function last(){
    if(!ready())return;const current=++epoch;context.setBusy(true);
    try{const data=await context.api('/api/platform/check/latest');if(current===epoch)render(data.items);}
    catch(e){if(current===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  async function pending(){
    if(!ready())return;const current=++epoch;context.setBusy(true);
    try{const data=await context.api('/api/platform/check/pending');if(current===epoch){pendingRows=data.items||[];el('isolation-pending-result').hidden=false;el('isolation-pending-summary').textContent=pendingRows.length?`${pendingRows.length} 个隔离测试未清理；若其他进程仍在验证，目录锁会拒绝清理。`:'没有未清理的隔离测试。';}}
    catch(e){if(current===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  function closeRecovery(){el('isolation-recover-dialog').close?.();el('isolation-recover-dialog').hidden=true;}
  async function recover(){
    if(!ready()||!pendingRows.length)return;const current=++epoch;closeRecovery();context.setBusy(true);
    try{const data=await context.api('/api/platform/check/recover',{});if(current===epoch){pendingRows=[];el('isolation-pending-summary').textContent=`已清理 ${data.items.cleaned} 个专用测试；项目和检查历史保留。`;context.message('隔离测试残留已清理，可重新验证。');}}
    catch(e){if(current===epoch)context.message(e.message,true);}finally{context.setBusy(false);sync();}
  }
  el('isolation-static').addEventListener('click',()=>run());el('isolation-last').addEventListener('click',last);
  el('isolation-probe').addEventListener('click',()=>{if(!ready())return;el('isolation-probe-dialog').hidden=false;el('isolation-probe-dialog').showModal?.();sync();});
  el('isolation-probe-confirm').addEventListener('click',()=>run(true));el('isolation-probe-cancel').addEventListener('click',()=>{if(ready())cancel();});
  el('isolation-probe-dialog').addEventListener('cancel',e=>{e.preventDefault();if(ready())cancel();});
  el('isolation-pending').addEventListener('click',pending);
  el('isolation-recover').addEventListener('click',()=>{if(!ready()||!pendingRows.length)return;el('isolation-recover-paths').replaceChildren();for(const row of pendingRows)el('isolation-recover-paths').append(node('p',row.root));el('isolation-recover-dialog').hidden=false;el('isolation-recover-dialog').showModal?.();sync();});
  el('isolation-recover-confirm').addEventListener('click',recover);
  el('isolation-recover-cancel').addEventListener('click',()=>{if(ready())closeRecovery();});
  el('isolation-recover-dialog').addEventListener('cancel',e=>{e.preventDefault();if(ready())closeRecovery();});
  return{init(ctx){context=ctx;epoch++;pendingRows=[];cancel();closeRecovery();sync();},sync};
})();
