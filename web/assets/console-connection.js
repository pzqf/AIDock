'use strict';
const ConsoleConnection = (() => {
  let ctx,reason='',closed=false,armed=false,checking=false,lastActivityCheck=0;
  const $=id=>document.getElementById(id);
  function text(){return reason==='expired'?'本地会话已过期。当前标签页的草稿保留。请重新运行启动命令，在新标签页打开新链接；需要的草稿可从本页复制。':'本地服务连接已断开，保存操作已暂停。草稿仍在当前标签页。若断开前正在保存，请先重读状态核对结果。服务已退出时，请重新运行启动命令，在新标签页打开新链接。';}
  function render(){
    if(!ctx)return;
    ctx.changed?.();
    $('console-connection').hidden=!reason||closed;$('console-connection-text').textContent=reason?text():'';
    $('console-reconnect').disabled=checking||closed;$('console-reconnect').textContent=checking?'检查连接…':'检查连接';
    for(const n of document.querySelectorAll('[data-console-connection-notice]'))n.remove();
    if(reason&&!closed){for(const dialog of document.querySelectorAll('dialog[open]')){
      const notice=document.createElement('section');notice.className='console-connection-notice';notice.dataset.consoleConnectionNotice='true';notice.setAttribute('role','alert');
      const p=document.createElement('p');p.textContent=text();const button=document.createElement('button');button.type='button';button.className='btn';button.textContent=checking?'检查连接…':'检查连接';button.disabled=checking;button.dataset.connectionSafe='true';button.addEventListener('click',()=>reconnect());notice.append(p,button);dialog.prepend(notice);
    }}
  }
  function lost(why='network') {if(closed)return;if(reason!=='expired'||why==='expired')reason=why;render();}
  function connected(){if(closed)return;const restored=Boolean(reason);reason='';lastActivityCheck=Date.now();render();if(restored){ctx.restored?.();ctx.message?.('本地连接已恢复，草稿保留；保存时会重新核对当前源和确认摘要。');}}
  async function reconnect(){
    if(closed||checking)return;checking=true;render();
    try{
      const res=await fetch('/api/session',{credentials:'same-origin',cache:'no-store'});
      if(!res.ok){lost(res.status===401?'expired':'network');return;}
      const data=await res.json();if(typeof data.csrf!=='string'||!data.csrf)throw new Error('invalid session');
      ctx.session?.(data.csrf);connected();
    }catch{lost();}finally{checking=false;render();}
  }
  function activity(){if(!armed||closed||reason||checking||Date.now()-lastActivityCheck<60000)return;lastActivityCheck=Date.now();void reconnect();}
  function init(context){ctx=context;$('console-reconnect').addEventListener('click',()=>reconnect());document.addEventListener('input',activity,{passive:true});document.addEventListener('pointerdown',activity,{passive:true});document.addEventListener('keydown',activity,{passive:true});render();}
  return {init,lost,connected,reconnect,blocked:()=>Boolean(reason)||closed,arm:()=>{armed=true;lastActivityCheck=Date.now();},stop:()=>{closed=true;render();}};
})();
