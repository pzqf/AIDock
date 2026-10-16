'use strict';
const {readFileSync}=require('node:fs'),{join}=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element {
 constructor(){this.events={};this.children=[];this.hidden=false;this.disabled=false;this.checked=true;this.textContent='';}
 addEventListener(k,fn){this.events[k]=fn;} append(...v){this.children.push(...v);} replaceChildren(...v){this.children=v;}
 showModal(){this.hidden=false;} close(){this.hidden=true;}
}
const html=readFileSync(join(__dirname,'../assets/source.html'),'utf8');
const dom=Object.fromEntries([...html.matchAll(/id="([^"]+)"/g)].map(m=>[m[1],new Element()]));
const sandbox={document:{getElementById:id=>dom[id],createElement:()=>new Element()}};
vm.createContext(sandbox);
for(const file of ['native-write.js','native-view.js'])vm.runInContext(readFileSync(join(__dirname,'../assets',file),'utf8'),sandbox);
vm.runInContext('this.view=NativePreview;',sandbox);
const pending=[],calls=[];let busy=false,message='';
sandbox.view.init({busy:()=>busy,pending:()=>false,setBusy:v=>{busy=v;sandbox.view.sync();},message:m=>message=m,api:(route,body)=>{calls.push({route,body});return new Promise((resolve,reject)=>pending.push({resolve,reject}));}},{native_root:'fixture/project'});
const native={id:'skills-id',digest:'skills-digest',root:'fixture/project',blocked:false,write_supported:true,evidence:{L1:'PASS',L2:'PASS',L3:'UNKNOWN',L4:'UNKNOWN'},targets:[{root:'fixture/project/.agents/skills',adapter:{id:'codex-cli',version:'test'},status:'PASS',changes:[],packages:[],findings:[]}]};
const profile={id:'joint-id',digest:'joint-digest',changes:1,request:{sessions:[{name:'service'}]},boundary:'one complete project transaction',transaction:{targets:[{path:'.codex/config.toml',before:{exists:false,entries:{}},after:{exists:true,entries:{content:{sha256:'new'}}}}]}};
const check={id:'qualified',created:'2026-10-16T00:00:00Z',tools:[]};
const report={status:'READY',native_plan:native,check,mcp:{status:'READY',entries:[{name:'service',status:'READY'}]},plan:profile,gates:[]};
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const tx={state:'committed',updated:'2026-10-16T00:01:00Z',application_kind:'profile',plan:{id:'joint-id',action:'apply',root:'fixture/project',targets:profile.transaction.targets}};
async function prepare(r){dom['native-prepare'].events.click();const p=dom['native-probe-confirm-run'].events.click();assert.equal(calls.at(-1).route,'/api/native/profile/prepare');pending.shift().resolve({items:r});await p;}
(async()=>{
 await prepare(report);
 assert.equal(dom['native-apply'].disabled,false,'MCP-only changes must enable the main install action');
 assert.match(dom['native-prepare-title'].textContent,/已准备/);
 const recheck=dom['native-recheck'].events.click();assert.equal(calls.at(-1).body.plan_id,'joint-id');pending.shift().resolve({items:profile});await recheck;
 dom['native-apply'].events.click();assert.equal(dom['native-write-title'].textContent,'确认联合应用');assert.equal(dom['native-write-confirm-run'].textContent,'确认联合安装');
 assert(dom['native-write-changes'].children.some(n=>n.textContent==='fixture/project/.codex/config.toml'));
 dom['native-write-cancel'].events.click();assert(!calls.some(c=>c.route==='/api/native/apply'));
 dom['native-apply'].events.click();const apply=dom['native-write-confirm-run'].events.click();assert.equal(calls.at(-1).route,'/api/native/apply');assert.equal(calls.at(-1).body.plan_id,'joint-id');assert.equal(calls.at(-1).body.digest,'joint-digest');
 pending.shift().resolve({items:[tx]});await tick();assert.equal(calls.at(-1).route,'/api/native/transactions');pending.shift().resolve({items:[tx]});await apply;
 assert.equal(dom['native-apply'].disabled,true);assert.equal(dom['native-tx-reverse'].disabled,false);assert.match(dom['native-tx-history'].children[0].children[0].textContent,/联合应用/);
 const reverse=dom['native-tx-reverse'].events.click();assert.equal(calls.at(-1).body.transaction_id,'joint-id');const q={...profile.transaction,id:'reverse-id',digest:'reverse-digest',root:'fixture/project',action:'rollback',application_kind:'profile'};pending.shift().resolve({items:q});await reverse;
 const rollback=dom['native-write-confirm-run'].events.click();assert.equal(calls.at(-1).route,'/api/native/rollback/apply');pending.shift().resolve({items:{}});await tick();pending.shift().resolve({items:[{...tx,plan:{...tx.plan,id:'reverse-id',action:'rollback'}},tx]});await rollback;assert.match(dom['native-tx-reverse'].textContent,/撤销/);
 await prepare({...report,status:'NEEDS_ATTENTION',plan:undefined,mcp:{status:'NEEDS_ATTENTION',entries:[]}});
 assert.equal(dom['native-apply'].disabled,true);assert.equal(dom['native-recheck'].disabled,true);assert.match(message,/待处理/);
 await prepare({...report,plan:{...profile,changes:0}});assert.equal(dom['native-apply'].disabled,true);assert.match(dom['native-prepare-title'].textContent,/已与保存版本一致/);
 console.log('PASS combined main Profile UI: MCP-only diff, exact joint identity, cancel, main apply/history/rollback, incomplete MCP blocking and no-op');
})().catch(e=>{console.error(e);process.exitCode=1;});
