'use strict';
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element{constructor(){this.children=[];this.events={};this.hidden=true;this.disabled=false;this.classList={toggle(){}};}append(...n){this.children.push(...n);}replaceChildren(...n){this.children=n;}addEventListener(n,f){this.events[n]=f;}click(){return this.events.click?.();}showModal(){this.hidden=false;}close(){this.hidden=true;}}
const html=fs.readFileSync(path.join(__dirname,'../assets/source.html'),'utf8'),dom=Object.fromEntries([...html.matchAll(/id="(profile-apply-[^"]+)"/g)].map(m=>[m[1],new Element()]));
const requests=[],clone=x=>JSON.parse(JSON.stringify(x)),tick=()=>new Promise(r=>setImmediate(r)),flush=async()=>{await tick();await tick();};
let busy=false,sourcePending=false,prepared=0,writes=0,recovered=0,changed=0,history=[],fail='',hold=false,held=[];
let native={id:'native',digest:'native-digest',application_scope:'skills-rules',write_supported:true,blocked:false};
let review={status:'READY',entries:[{name:'<literal service>',status:'READY',session:{name:'service'}}],boundary:'only existing authority'};
const makePlan=()=>({id:'joint',digest:'joint-digest',changes:3,request:{sessions:[{name:'service'}]},boundary:'不启动插件',transaction:{targets:[{path:'AGENTS.md',before:{exists:true},after:{exists:true,entries:{a:1}}},{path:'.mcp.json',before:{exists:false},after:{exists:true}}]}});
const sandbox={document:{getElementById:id=>dom[id],createElement:()=>new Element()}};vm.createContext(sandbox);vm.runInContext(fs.readFileSync(path.join(__dirname,'../assets/profile-application-view.js'),'utf8')+'\nthis.view=ProfileApplicationView;',sandbox);
const ctx={prepare:()=>prepared++,busy:()=>busy,pending:()=>sourcePending,setBusy:v=>{busy=v;sandbox.view.sync();},api:async(url,body)=>{
 requests.push({url,body:body&&clone(body)});if(fail)throw Error(fail);if(hold)return new Promise(resolve=>held.push(()=>resolve({items:url.endsWith('review')?clone(review):[]})));
 if(url.endsWith('/review'))return {items:clone(review)};if(url.endsWith('/history'))return {items:clone(history)};
 if(url.endsWith('/rollback/plan'))return {items:{id:'reverse',digest:'reverse-digest',action:history[0].plan.action==='rollback'?'undo-rollback':'rollback',targets:makePlan().transaction.targets}};
 if(url.endsWith('/plan'))return {items:makePlan()};if(url.endsWith('/apply')){writes++;history=[{plan:{id:body.plan_id,action:body.plan_id==='reverse'?'rollback':'apply',targets:makePlan().transaction.targets},state:'committed',updated:'2026-10-16T00:00:00Z'}];return {items:{}};}
 if(url.endsWith('/recover')){recovered++;history=[];return {items:1};}throw Error(url);
}};
const click=id=>dom[id].click(),text=id=>{const walk=n=>[n.textContent||'',...n.children.flatMap(c=>walk(c))];return walk(dom[id]).join('\n');};
(async()=>{
 sandbox.view.init(ctx,()=>native,()=>{changed++;sandbox.view.invalidate();});assert(dom['profile-apply-plan'].disabled);click('profile-apply-prepare');assert.equal(prepared,1);
 await sandbox.view.load();assert(!dom['profile-apply-plan'].disabled);assert.match(text('profile-apply-review'),/<literal service>/);
 click('profile-apply-plan');await flush();assert.equal(writes,0);assert(!dom['profile-apply-confirm'].hidden);assert.deepEqual(requests.at(-1).body,{native_plan:'native',native_digest:'native-digest',sessions:[{name:'service'}]});
 click('profile-apply-cancel');assert.equal(writes,0);assert(dom['profile-apply-confirm'].hidden);
 click('profile-apply-plan');await flush();sandbox.view.invalidate();click('profile-apply-confirm-run');await flush();assert.equal(writes,0);assert(dom['profile-apply-plan'].disabled);
 await sandbox.view.load();native.write_supported=false;sandbox.view.sync();assert(dom['profile-apply-plan'].disabled);native.write_supported=true;sourcePending=true;sandbox.view.sync();assert(dom['profile-apply-plan'].disabled);sourcePending=false;sandbox.view.sync();
 click('profile-apply-plan');await flush();click('profile-apply-confirm-run');click('profile-apply-confirm-run');await flush();assert.equal(writes,1);assert.equal(changed,1);assert.match(text('profile-apply-message'),/没有启动插件/);
 click('profile-apply-reverse');await flush();assert.match(text('profile-apply-title'),/回退联合应用/);click('profile-apply-confirm-run');await flush();assert.equal(writes,2);
 click('profile-apply-reverse');await flush();assert.match(text('profile-apply-title'),/撤销联合回退/);click('profile-apply-cancel');assert.equal(writes,2);
 review.status='NEEDS_ATTENTION';await sandbox.view.load();assert(dom['profile-apply-plan'].disabled);review.status='READY';await sandbox.view.load();click('profile-apply-plan');await flush();fail='PLAN_STALE';click('profile-apply-confirm-run');await flush();assert.equal(writes,2);assert(dom['profile-apply-confirm'].hidden);assert.match(text('profile-apply-message'),/PLAN_STALE/);fail='';
 history=[{plan:{id:'pending',action:'apply',targets:[]},state:'prepared',updated:'2026-10-16T00:00:00Z'}];await sandbox.view.load();assert(dom['profile-apply-plan'].disabled);assert(!dom['profile-apply-recover'].hidden);click('profile-apply-recover');await flush();assert.equal(recovered,1);
 hold=true;const older=sandbox.view.load();sandbox.view.invalidate();held.splice(0).forEach(f=>f());await older;assert(dom['profile-apply-plan'].disabled,'late state resurrected an invalidated preview');
 console.log('PASS joint profile application: complete review, literal text, preview/cancel, exact confirmation, duplicate guard, qualification and source gates, stale/late response, rollback/undo and recovery');
})().catch(e=>{console.error(e);process.exitCode=1;});
