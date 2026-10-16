'use strict';
const {readFileSync}=require('node:fs'),{join}=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element{constructor(){this.events={};this.children=[];this.hidden=false;this.disabled=false;}addEventListener(k,f){this.events[k]=f;}append(...v){this.children.push(...v);}replaceChildren(...v){this.children=v;}showModal(){this.hidden=false;}close(){this.hidden=true;}}
const ids=[...readFileSync(join(__dirname,'../assets/source.html'),'utf8').matchAll(/id="([^"]+)"/g)].map(x=>x[1]),dom=Object.fromEntries(ids.map(id=>[id,new Element()]));
const sandbox={document:{getElementById:id=>dom[id],createElement:()=>new Element(),querySelectorAll:()=>[]}};vm.createContext(sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/replica-publication.js'),'utf8')+'\nthis.view=ReplicaPublication;',sandbox);
const view=sandbox.view,calls=[],waiting=[];let busy=false,message='';
view.init({busy:()=>busy,setBusy:v=>{busy=v;view.sync();},message:m=>message=m,api:async(path,body)=>{calls.push({path,body});return new Promise((resolve,reject)=>waiting.push({resolve,reject}));}});
const candidate={id:'g',digest:'exact-g',application_scope:'skills-rules'},evidence={id:'check',digest:'exact-check',generation_id:'g',generation_digest:'exact-g',probe:true,candidate_status:'PASS'};
const tx={id:'tx',digest:'tx-digest',kind:'coordinated-roots',action:'apply',targets:[{path:'copies/two/.aidock',before:{entries:{}},after:{entries:{'skills/guide/SKILL.md':{sha256:'new'}}}}],bound_roots:[{path:'copies/two/.aidock',root:'/dedicated/copy2/.aidock'}]};
const proposal={id:'publication',kind:'replica-publication',digest:'formal-digest',boundary:'writes listed targets',transaction:tx,resolution:{source:{name:'publisher',root:'/dedicated/copy1/.aidock'},targets:[{copy:{id:'two',name:'copy2'}}]},check:{id:'check'}};
const session={id:'publication',status:'APPLIED',source:{name:'publisher'},bound_roots:tx.bound_roots,transactions:[{id:'tx',action:'apply',state:'committed',targets:1}],boundary:'source read only'};
(async()=>{
 assert(dom['replica-check-publish'].disabled);
 for(const [p,r,current]of [[{...candidate,application_scope:''},evidence,true],[candidate,{...evidence,probe:false},true],[candidate,{...evidence,stale:true},true],[candidate,{...evidence,generation_digest:'other'},true],[candidate,evidence,false]]){view.setEvidence(p,r,current);assert(dom['replica-check-publish'].disabled);}
 view.setEvidence(candidate,evidence);assert(!dom['replica-check-publish'].disabled);
 let task=dom['replica-check-publish'].events.click();assert.equal(calls.at(-1).path,'/api/replica/publication/plan');assert.equal(calls.at(-1).body.check_digest,'exact-check');waiting.shift().resolve({items:proposal});await task;
 assert(!dom['replica-publication-confirm'].hidden);assert.equal(calls.length,1);assert.match(dom['replica-publication-confirm-title'].textContent,/登记项目/);
 dom['replica-publication-cancel'].events.click();assert(dom['replica-publication-confirm'].hidden);assert.equal(calls.length,1);
 task=dom['replica-check-publish'].events.click();waiting.shift().resolve({items:proposal});await task;
 task=dom['replica-publication-confirm-run'].events.click();assert.equal(calls.at(-1).path,'/api/replica/publication/apply');assert.equal(calls.at(-1).body.digest,'formal-digest');assert(dom['replica-check-publish'].disabled);waiting.shift().resolve({items:session});await task;
 assert.match(dom['replica-publication-status'].textContent,/同步完成/);assert(dom['replica-check-publish'].disabled);assert(!dom['replica-publication-reverse'].disabled);
 task=dom['replica-publication-reverse'].events.click();waiting.shift().resolve({items:{...tx,id:'rollback',digest:'rollback-digest',action:'rollback'}});await task;assert.equal(calls.at(-1).path,'/api/replica/publication/reverse');assert.match(dom['replica-publication-confirm-title'].textContent,/回退正式同步/);
 task=dom['replica-publication-confirm-run'].events.click();assert.equal(calls.at(-1).path,'/api/replica/publication/apply-reverse');assert.equal(calls.at(-1).body.publication_id,'publication');waiting.shift().resolve({items:{...session,status:'ROLLED_BACK',transactions:[{id:'rollback',action:'rollback',state:'committed'}]}});await task;assert.match(dom['replica-publication-reverse'].textContent,/撤销/);
 view.setEvidence(candidate,evidence);task=dom['replica-check-publish'].events.click();view.invalidate();waiting.shift().resolve({items:proposal});await task;assert(dom['replica-publication-confirm'].hidden);
 task=dom['replica-publication-history-load'].events.click();waiting.shift().resolve({items:[{...session,status:'PENDING',pending:true}]});await task;assert(!dom['replica-publication-recover'].disabled);assert(dom['replica-publication-reverse'].disabled);
 task=dom['replica-publication-recover'].events.click();assert.equal(calls.at(-1).path,'/api/replica/publication/recover');waiting.shift().resolve({items:{...session,status:'RECOVERED',pending:false,transactions:[{id:'tx',state:'recovered'}]}});await task;assert.match(message,/整批恢复/);assert(dom['replica-publication-recover'].disabled);
 console.log('PASS formal replica publication: exact evidence, explicit confirmation/cancel, busy and stale response guards, rollback/undo and interrupted recovery');
})().catch(e=>{console.error(e);process.exitCode=1;});
