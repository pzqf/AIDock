'use strict';
const {readFileSync}=require('node:fs');const {join}=require('node:path');const vm=require('node:vm');const assert=require('node:assert/strict');
class Element{constructor(){this.events={};this.children=[];this.hidden=false;this.checked=false;this.value='';}addEventListener(k,f){this.events[k]=f;}append(...items){this.children.push(...items);if(!this.value&&items[0]?.value)this.value=items[0].value;}replaceChildren(...items){this.children=items;this.value='';}showModal(){this.hidden=false;}close(){this.hidden=true;}}
const ids=[...readFileSync(join(__dirname,'../assets/source.html'),'utf8').matchAll(/id="([^"]+)"/g)].map(x=>x[1]);const dom=Object.fromEntries(ids.map(id=>[id,new Element()]));
const find=(id,nodes=Object.values(dom))=>{for(const n of nodes){if(n.id===id)return n;const v=find(id,n.children);if(v)return v;}};
const sandbox={document:{getElementById:id=>dom[id]||find(id),createElement:()=>new Element()}};vm.createContext(sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/replica-view.js'),'utf8')+'\nthis.view=ReplicaView;',sandbox);
const view=sandbox.view,calls=[],waiting=[];let busy=false,message='';
view.init({busy:()=>busy,setBusy:v=>{busy=v;view.sync();},sourceRoot:()=>'/isolated/project/.aidock',message:m=>message=m,api:async(path,body)=>{calls.push({path,body});return new Promise((resolve,reject)=>waiting.push({resolve,reject}));}},{});
const registry={scope_id:'server',copies:[{id:'one',name:'copy1',root:'/isolated/one/.aidock',branch:'main'},{id:'two',name:'copy2',root:'/isolated/two/.aidock',branch:'main'}]};
(async()=>{
 assert.equal(dom['nav-replica'].hidden,false);
 const load=view.load();waiting.shift().resolve({items:registry});await load;assert.equal(dom['replica-selection'].hidden,false);assert.equal(find('replica-target-one').disabled,true);
 const before=calls.length;await dom['replica-compare'].events.click();assert.equal(calls.length,before);assert.match(message,/请选择/);
 find('replica-target-two').checked=true;
 const compare=dom['replica-compare'].events.click();assert.equal(calls.at(-1).path,'/api/replica/compare');assert.deepEqual(Array.from(calls.at(-1).body.targets),['two']);
 waiting.shift().resolve({items:{from:registry.copies[0],targets:[{copy:registry.copies[1],status:'READY',entries:[{id:'guide',name:'guide',status:'BASELINE_UNKNOWN',reason:'no baseline',package_changes:[]}]}],boundary:'read only'}});await compare;assert.equal(dom['replica-result'].hidden,false);
 const baseline=dom['replica-baseline'].events.click();waiting.shift().resolve({items:{id:'baseline',digest:'digest',action:'replica-baseline',changes:[{path:'baselines/one/two.json',after:{bytes:100}}]}});await baseline;assert.equal(dom['replica-confirm'].hidden,false);
 dom['replica-cancel'].events.click();assert(!calls.some(c=>c.path==='/api/replica/confirm'));
 const baseline2=dom['replica-baseline'].events.click();waiting.shift().resolve({items:{id:'baseline',digest:'digest',action:'replica-baseline',changes:[]}});await baseline2;
 const confirm=dom['replica-confirm-run'].events.click();assert.equal(calls.at(-1).body.digest,'digest');waiting.shift().reject(new Error('PLAN_STALE'));await confirm;assert.match(message,/PLAN_STALE/);
 const delayed=dom['replica-compare'].events.click();view.invalidate();waiting.shift().resolve({items:{}});await delayed;assert.equal(dom['replica-result'].hidden,true);
 const register=dom['replica-register-current'].events.click();assert.equal(calls.at(-1).body.root,'/isolated/project/.aidock');waiting.shift().resolve({items:{id:'registration',digest:'exact',action:'replica-register',inputs:{binding_root:'/isolated/project/.aidock'},changes:[]}});await register;
 assert.match(dom['replica-confirm-title'].textContent,/登记/);dom['replica-cancel'].events.click();assert(!calls.some(c=>c.path==='/api/replica/apply'));
 view.init({busy:()=>false,sourceRoot:()=>'/global'}, {global_root:'/global'});assert.equal(dom['nav-replica'].hidden,true);
 console.log('PASS replica UI: explicit registered targets, source exclusion, three-way display, baseline cancel/stale guard, late-response invalidation and local-only registration');
})().catch(e=>{console.error(e);process.exitCode=1;});
