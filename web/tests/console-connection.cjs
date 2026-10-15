'use strict';
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element {
 constructor(tag='div'){this.tag=tag;this.children=[];this.events={};this.dataset={};this.hidden=false;this.disabled=false;this.value='draft retained';}
 setAttribute(){}append(...nodes){for(const n of nodes){n.parent=this;this.children.push(n);}}prepend(n){n.parent=this;this.children.unshift(n);}remove(){this.parent.children=this.parent.children.filter(n=>n!==this);}addEventListener(name,fn){this.events[name]=fn;}
}
const dom=Object.fromEntries(['console-connection','console-connection-text','console-reconnect'].map(id=>[id,new Element()])),dialog=new Element('dialog'),field=new Element('input'),listeners={};dialog.append(field);
let now=100000,pending=null,mode='pass',reads=0,csrf='',notices=[],changes=0;
const sandbox={Date:{now:()=>now},document:{getElementById:id=>dom[id],createElement:tag=>new Element(tag),addEventListener:(event,fn)=>{listeners[event]=fn;},querySelectorAll:selector=>selector==='dialog[open]'?[dialog]:dialog.children.filter(n=>n.dataset.consoleConnectionNotice)},fetch:async(url,options)=>{assert.equal(url,'/api/session');assert.equal(options.credentials,'same-origin');assert.equal(options.method,undefined);reads++;if(mode==='network')throw new Error('offline');if(mode==='pending')await new Promise(resolve=>{pending=resolve;});return {ok:mode!=='expired',status:mode==='expired'?401:200,json:async()=>({csrf:'fresh-session'})};}};
vm.createContext(sandbox);vm.runInContext(fs.readFileSync(path.join(__dirname,'../assets/console-connection.js'),'utf8')+'\nthis.view=ConsoleConnection;',sandbox);
const tick=()=>new Promise(r=>setImmediate(r));
(async()=>{
 sandbox.view.init({changed(){changes++;dom['console-reconnect'].disabled=false;},session:value=>{csrf=value;},message:value=>notices.push(value)});
 assert.equal(sandbox.view.blocked(),false);assert.equal(dom['console-connection'].hidden,true);listeners.input();await tick();assert.equal(reads,0);
 sandbox.view.lost();sandbox.view.lost();assert.equal(sandbox.view.blocked(),true);assert.equal(dialog.children.filter(n=>n.dataset.consoleConnectionNotice).length,1);assert.match(dom['console-connection-text'].textContent,/草稿/);assert.equal(field.value,'draft retained');
 mode='pending';const retry=sandbox.view.reconnect();assert.equal(dom['console-reconnect'].disabled,true);await sandbox.view.reconnect();assert.equal(reads,1);mode='pass';pending();await retry;assert.equal(sandbox.view.blocked(),false);assert.equal(csrf,'fresh-session');assert.equal(dialog.children.length,1);assert.match(notices[0],/草稿保留/);
 sandbox.view.arm();now+=60001;mode='network';listeners.input();listeners.pointerdown();listeners.keydown();await tick();assert.equal(reads,2);assert.equal(sandbox.view.blocked(),true);assert.equal(field.value,'draft retained');
 mode='expired';await sandbox.view.reconnect();assert.match(dom['console-connection-text'].textContent,/会话已过期/);sandbox.view.lost();assert.match(dom['console-connection-text'].textContent,/会话已过期/);
 mode='pass';await sandbox.view.reconnect();sandbox.view.arm();now+=60001;listeners.keydown();await tick();assert.equal(sandbox.view.blocked(),false);const n=reads;listeners.input();await tick();assert.equal(reads,n,'activity heartbeat was not throttled');
 sandbox.view.stop();sandbox.view.lost();now+=60001;listeners.input();await sandbox.view.reconnect();assert.equal(reads,n);assert.equal(dom['console-connection'].hidden,true);assert(changes>0);
 console.log('PASS console connection: SSE/network/expiry notice, modal notice, no lost drafts, one reconnect, CSRF refresh, activity-only throttled session check and intentional close');
})().catch(e=>{console.error(e);process.exitCode=1;});
