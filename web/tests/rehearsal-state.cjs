'use strict';
const {readFileSync} = require('node:fs');
const {join} = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
class Element {
  constructor(){this.children=[];this.events={};this.disabled=false;this.hidden=false;this.textContent='';}
  addEventListener(name,fn){this.events[name]=fn;}
  append(...children){this.children.push(...children);}
  replaceChildren(...children){this.children=children;}
}
const ids=[...readFileSync(join(__dirname,'../assets/source.html'),'utf8').matchAll(/id="([^"]+)"/g)].map(x=>x[1]);
const dom=Object.fromEntries(ids.map(id=>[id,new Element()]));
const sandbox={document:{getElementById:id=>dom[id],createElement:()=>new Element()}};
vm.createContext(sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/native-rehearsal.js'),'utf8')+'\nthis.view = NativeRehearsal;',sandbox);
const view=sandbox.view;let busy=false,pendingSource=false,plan=null,message='';const pending=[],calls=[];
view.init({busy:()=>busy,pending:()=>pendingSource,setBusy:v=>{busy=v;view.sync();},message:v=>message=v,api:(path,body)=>{calls.push({path,body});return new Promise((resolve,reject)=>pending.push({resolve,reject}));}},()=>plan);
const report={id:'session',native_plan:'native-plan',root:'independent/project',status:'APPLIED',pending:false,boundary:'independent copy',transactions:[{id:'tx-apply',action:'apply',state:'committed',targets:2,changes:8}]};
const reversal={id:'reverse-plan',digest:'reverse-digest',action:'rollback',targets:[{path:'.agents/skills',before:{entries:{'guide/SKILL.md':{sha256:'body'}}},after:{entries:{}}}]};
(async()=>{
  assert.equal(dom['rehearsal-run'].disabled,true);
  plan={id:'native-plan',digest:'native-digest',targets:[{},{}],blocked:false};view.sync();assert.equal(dom['rehearsal-run'].disabled,false);
  dom['rehearsal-run'].events.click();assert.equal(dom['rehearsal-confirm'].hidden,false);assert.equal(calls.length,0);
  dom['rehearsal-cancel'].events.click();assert.equal(dom['rehearsal-confirm'].hidden,true);assert.equal(calls.length,0);
  dom['rehearsal-run'].events.click();plan={...plan,digest:'new-digest'};await dom['rehearsal-confirm-run'].events.click();assert.equal(calls.length,0);assert.match(message,/预览已变化/);
  dom['rehearsal-run'].events.click();const run=dom['rehearsal-confirm-run'].events.click();assert.equal(calls.at(-1).path,'/api/native/rehearsal/run');assert.equal(busy,true);
  await dom['rehearsal-confirm-run'].events.click();assert.equal(calls.length,1);pending.shift().resolve({items:report});await run;
  assert.equal(dom['rehearsal-result'].hidden,false);assert.match(dom['rehearsal-state'].textContent,/已应用到预演项目/);assert.equal(dom['rehearsal-reverse'].disabled,false);
  const rev=dom['rehearsal-reverse'].events.click();assert.equal(calls.at(-1).body.transaction_id,'tx-apply');pending.shift().resolve({items:reversal});await rev;
  assert.equal(dom['rehearsal-confirm'].hidden,false);assert.match(dom['rehearsal-title'].textContent,/回退/);assert.equal(dom['rehearsal-changes'].children.length,1);
  dom['rehearsal-cancel'].events.click();assert.equal(calls.length,2);
  const rev2=dom['rehearsal-reverse'].events.click();pending.shift().resolve({items:reversal});await rev2;
  const confirm=dom['rehearsal-confirm-run'].events.click();assert.equal(calls.at(-1).path,'/api/native/rehearsal/apply');assert.equal(calls.at(-1).body.digest,'reverse-digest');
  pending.shift().resolve({items:{...report,status:'ROLLED_BACK',transactions:[{...report.transactions[0],id:'tx-rollback',action:'rollback'}]}});await confirm;
  assert.equal(dom['rehearsal-reverse'].textContent,'撤销本次回退');assert.equal(dom['rehearsal-state'].textContent,'已回退');
  const undo=dom['rehearsal-reverse'].events.click();pending.shift().resolve({items:{...reversal,action:'undo-rollback'}});await undo;
  const confirmingUndo=dom['rehearsal-confirm-run'].events.click();pending.shift().resolve({items:{...report,status:'UNDONE',transactions:[{...report.transactions[0],id:'tx-undo',action:'undo-rollback'}]}});await confirmingUndo;
  assert.equal(dom['rehearsal-state'].textContent,'已撤销回退');
  const restore=view.restore();pending.shift().resolve({items:{...report,pending:true,status:'PENDING'}});await restore;
  assert.equal(dom['rehearsal-reverse'].disabled,true);assert.equal(dom['rehearsal-recover'].hidden,false);
  const recovering=dom['rehearsal-recover'].events.click();pending.shift().reject(new Error('RECOVERY_CONFLICT: external edit'));await recovering;assert.match(message,/RECOVERY_CONFLICT/);assert.equal(dom['rehearsal-recover'].hidden,false);
  const recovered=dom['rehearsal-recover'].events.click();pending.shift().resolve({items:{...report,pending:false,status:'RECOVERED',transactions:[{...report.transactions[0],state:'recovered'}]}});await recovered;assert.equal(dom['rehearsal-recover'].hidden,true);assert.equal(dom['rehearsal-reverse'].disabled,true);
  const late=view.restore();view.invalidate();pending.shift().resolve({items:report});await late;assert.match(dom['rehearsal-state'].textContent,/已恢复/);
  const history=dom['native-tx-load'].events.click();pending.shift().resolve({items:[]});await history;assert.match(dom['native-tx-status'].textContent,/没有项目工具事务/);
  const actualRecover=dom['native-tx-recover'].events.click();assert.equal(calls.at(-1).path,'/api/native/recover');pending.shift().resolve({items:0});await new Promise(resolve=>setImmediate(resolve));pending.shift().resolve({items:[]});await actualRecover;assert.match(message,/已恢复 0/);
  pendingSource=true;view.sync();assert.equal(dom['rehearsal-run'].disabled,true);await dom['rehearsal-run'].events.click();assert.equal(dom['rehearsal-confirm'].hidden,true);
  assert(!calls.some(c=>c.path==='/api/native/apply'));
  console.log('PASS rehearsal state: cancel, stale preview, apply, rollback/undo confirmation, recovery conflict, restart report, late responses and source/busy gates');
})().catch(e=>{console.error(e);process.exitCode=1;});
