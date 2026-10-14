'use strict';
const {readFileSync}=require('node:fs');
const {join}=require('node:path');
const vm=require('node:vm');
const assert=require('node:assert/strict');
class Element {
 constructor(){this.events={};this.children=[];this.hidden=false;this.disabled=false;this.textContent='';}
 addEventListener(name,fn){this.events[name]=fn;} append(...items){this.children.push(...items);} replaceChildren(...items){this.children=items;}
 showModal(){this.hidden=false;} close(){this.hidden=true;}
}
const ids=[...readFileSync(join(__dirname,'../assets/source.html'),'utf8').matchAll(/id="([^"]+)"/g)].map(x=>x[1]);
const dom=Object.fromEntries(ids.map(id=>[id,new Element()]));
const sandbox={document:{getElementById:id=>dom[id],createElement:()=>new Element()}};
vm.createContext(sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/native-write.js'),'utf8')+'\nthis.writer=NativeWrite;',sandbox);
const view=sandbox.writer,pending=[],calls=[];
let busy=false,sourceRecovery=false,message='',invalidations=0;
let plan={id:'approved',digest:'checked-digest',write_supported:true,blocked:false,targets:[{root:'test/.agents/skills',changes:[{path:'guide/SKILL.md',after:{bytes:20}}]}]};
const tx={state:'committed',updated:'2026-10-14T01:00:00Z',plan:{id:'installed',action:'apply',root:'test',targets:[{path:'.agents/skills'}]}};
view.init({busy:()=>busy,pending:()=>sourceRecovery,setBusy:v=>{busy=v;view.sync();},message:m=>message=m,api:async(path,body)=>{calls.push({path,body});return new Promise((resolve,reject)=>pending.push({resolve,reject}));}},()=>plan,()=>{invalidations++;plan=null;view.invalidate();});
const tick=()=>new Promise(resolve=>setImmediate(resolve));
(async()=>{
 assert.equal(dom['native-apply'].disabled,false);
 dom['native-apply'].events.click();assert.equal(dom['native-write-confirm'].hidden,false);assert.equal(calls.length,0);
 dom['native-write-cancel'].events.click();assert.equal(dom['native-write-confirm'].hidden,true);assert.equal(calls.length,0);
 dom['native-apply'].events.click();const applying=dom['native-write-confirm-run'].events.click();assert.equal(calls[0].path,'/api/native/apply');assert.equal(calls[0].body.digest,'checked-digest');assert.equal(busy,true);
 await dom['native-write-confirm-run'].events.click();assert.equal(calls.length,1);
 pending.shift().resolve({items:[tx]});await tick();assert.equal(calls.at(-1).path,'/api/native/transactions');pending.shift().resolve({items:[tx]});await applying;
 assert.equal(invalidations,1);assert.equal(dom['native-apply'].disabled,true);assert.equal(dom['native-tx-reverse'].disabled,false);assert.match(message,/会话生效待验证/);
 const reverse=dom['native-tx-reverse'].events.click();assert.equal(calls.at(-1).body.transaction_id,'installed');
 const q={id:'rollback',digest:'rollback-digest',root:'test',action:'rollback',targets:[{path:'.agents/skills',before:{entries:{'guide/SKILL.md':{bytes:20}}},after:{entries:{}}}]};
 pending.shift().resolve({items:q});await reverse;assert.match(dom['native-write-title'].textContent,/回退/);
 dom['native-write-cancel'].events.click();assert(!calls.some(c=>c.path==='/api/native/rollback/apply'));
 const again=dom['native-tx-reverse'].events.click();pending.shift().resolve({items:q});await again;
 const rollback=dom['native-write-confirm-run'].events.click();assert.equal(calls.at(-1).path,'/api/native/rollback/apply');
 pending.shift().resolve({items:{}});await tick();pending.shift().resolve({items:[{...tx,plan:{...tx.plan,id:'rolled-back',action:'rollback'}},tx]});await rollback;
 assert.match(dom['native-tx-reverse'].textContent,/撤销/);
 const loading=view.load();pending.shift().resolve({items:[{...tx,state:'prepared'},tx]});await loading;assert.equal(dom['native-tx-reverse'].disabled,true);assert.match(dom['native-tx-status'].textContent,/先恢复/);
 sourceRecovery=true;const recovery=dom['native-tx-recover'].events.click();assert.equal(calls.at(-1).path,'/api/native/recover');pending.shift().reject(new Error('RECOVERY_CONFLICT'));await recovery;assert.match(message,/RECOVERY_CONFLICT/);
 sourceRecovery=false;plan={...q,write_supported:false};view.sync();assert.equal(dom['native-apply'].disabled,true);dom['native-apply'].events.click();assert.equal(dom['native-write-confirm'].hidden,true);
 console.log('PASS native installation UI: cancel without writes, exact digest confirmation, duplicate-submit guard, persisted history, rollback/undo and recovery conflict');
})().catch(e=>{console.error(e);process.exitCode=1;});
