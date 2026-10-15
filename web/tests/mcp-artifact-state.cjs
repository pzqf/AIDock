'use strict';
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element{
 constructor(tag='div'){this.tag=tag;this.children=[];this.events={};this.value='';this.disabled=false;this.hidden=false;this.classList={toggle(){}};}
 append(...nodes){this.children.push(...nodes);if(!this.value&&this.tag==='select'&&nodes[0])this.value=nodes[0].value;}replaceChildren(...nodes){this.children=nodes;if(this.tag==='select')this.value='';}
 addEventListener(name,fn){this.events[name]=fn;}querySelectorAll(selector){const all=this.children.flatMap(n=>[n,...n.querySelectorAll()]);return selector==='button'?all.filter(n=>n.tag==='button'):all;}showModal(){this.open=true;}close(){this.open=false;}click(){return this.events.click?.();}
}
const html=fs.readFileSync(path.join(__dirname,'../assets/source.html'),'utf8'),dom=Object.fromEntries([...html.matchAll(/id="([^"]+)"/g)].map(x=>[x[1],new Element(x[1]==='mcp-artifact-name'?'select':'div')]));
let busy=false,pending=false,fail='',writes=0,plans=0,refreshes=0,recoveries=0;
let current={lock_sha256:'lock',checks:[],artifacts:[{name:'fixture',artifact_path:'mcp-artifacts/fixture',root:'D:/test/fixture',sha256:'empty',status:'MISSING',history:[]}]};
const clone=v=>JSON.parse(JSON.stringify(v)),requests=[],sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element(tag)}};vm.createContext(sandbox);vm.runInContext(fs.readFileSync(path.join(__dirname,'../assets/mcp-artifact-view.js'),'utf8')+'\nthis.view=MCPArtifactView;',sandbox);
const ctx={busy:()=>busy,pending:()=>pending,setBusy:v=>{busy=v;sandbox.view.sync();},message(){},refreshChecks:async()=>{refreshes++;},api:async(url,body)=>{
 requests.push({url,body});if(fail)throw new Error(fail);
 if(url.endsWith('/state'))return {items:clone(current)};
 if(url.endsWith('/plan')){plans++;return {items:{plan:{id:'plan',digest:'digest',changes:[{path:'<script>fixture</script>',after:{bytes:12}}]},candidate:{root:'D:/test/fixture'},archive_sha256:'zip-sha256'}};}
 if(url.endsWith('/apply')){assert.equal(body.plan_id,'plan');assert.equal(body.digest,'digest');assert.equal(body.name,'fixture');writes++;current.artifacts[0].status='MATCHED';current.artifacts[0].sha256='imported';current.artifacts[0].history=[{plan:{id:'latest',action:'artifact-import',created:'time'},state:'committed',updated:'time'}];return {items:{state:'committed'}};}
 if(url.endsWith('/recover')){recoveries++;current.artifacts[0].recovery_required=false;return {items:{recovered:1}};}
 throw new Error(url);
}};
const tick=()=>new Promise(r=>setImmediate(r)),click=id=>dom[id].click(),text=id=>dom[id].querySelectorAll().map(n=>n.textContent||'').join('\n');
(async()=>{
 sandbox.view.init(ctx);click('mcp-artifact-open');await tick();assert.equal(dom['mcp-artifact-dialog'].open,true);assert.equal(dom['mcp-artifact-preview'].disabled,true);
 dom['mcp-artifact-archive'].value='D:/fixtures/complete.zip';dom['mcp-artifact-archive'].events.input();assert.equal(dom['mcp-artifact-preview'].disabled,false);assert.equal(sandbox.view.hasDraft(),true);
 click('mcp-artifact-preview');await tick();assert.equal(plans,1);assert.equal(writes,0);assert.equal(requests.at(-1).body.expected_lock_sha256,'lock');assert.equal(requests.at(-1).body.expected_sha256,'empty');assert.match(text('mcp-artifact-changes'),/<script>fixture<\/script>/);
 click('mcp-artifact-cancel');assert.equal(dom['mcp-artifact-plan'].hidden,true);assert.equal(dom['mcp-artifact-archive'].value,'D:/fixtures/complete.zip');assert.equal(writes,0);
 click('mcp-artifact-close');click('mcp-artifact-open');await tick();assert.equal(dom['mcp-artifact-archive'].value,'D:/fixtures/complete.zip');
 click('mcp-artifact-preview');await tick();dom['mcp-artifact-archive'].value='D:/fixtures/other.zip';dom['mcp-artifact-archive'].events.input();click('mcp-artifact-confirm');await tick();assert.equal(writes,0);
 click('mcp-artifact-preview');await tick();fail='PLAN_STALE: ZIP changed';click('mcp-artifact-confirm');await tick();assert.equal(writes,0);assert.equal(dom['mcp-artifact-archive'].value,'D:/fixtures/other.zip');assert.equal(dom['mcp-artifact-plan'].hidden,true);fail='';
 click('mcp-artifact-preview');await tick();click('mcp-artifact-confirm');click('mcp-artifact-confirm');await tick();assert.equal(writes,1);assert.equal(refreshes,1);assert.equal(dom['mcp-artifact-archive'].value,'');assert.equal(sandbox.view.hasDraft(),false);assert.equal(dom['mcp-artifact-preview'].disabled,true);
 dom['mcp-artifact-history'].querySelectorAll('button')[0].click();await tick();assert.equal(requests.at(-1).body.action,'rollback');assert.equal(requests.at(-1).body.snapshot,'latest');click('mcp-artifact-cancel');
 pending=true;sandbox.view.sync();const before=plans;dom['mcp-artifact-history'].querySelectorAll('button')[0].click();await tick();assert.equal(plans,before);pending=false;
 current.artifacts[0].recovery_required=true;click('mcp-artifact-reload');await tick();assert.equal(dom['mcp-artifact-preview'].disabled,true);assert.equal(dom['mcp-artifact-history'].querySelectorAll('button')[0].disabled,true);click('mcp-artifact-recover');await tick();assert.equal(recoveries,1);assert.equal(dom['mcp-artifact-archive'].value,'');
 dom['mcp-artifact-archive'].value='preserved.zip';dom['mcp-artifact-archive'].events.input();fail='RECOVERY_CONFLICT';click('mcp-artifact-reload');await tick();assert.equal(dom['mcp-artifact-preview'].disabled,true);assert.equal(dom['mcp-artifact-current'].querySelectorAll().some(n=>(n.textContent||'').includes('文件与锁匹配')),false);assert.equal(dom['mcp-artifact-archive'].value,'preserved.zip');fail='';
 dom['mcp-artifact-recovery-path'].value='mcp-artifacts/old';dom['mcp-artifact-recovery-path'].events.input();click('mcp-artifact-recover');await tick();assert.equal(recoveries,2);assert.equal(requests.findLast(r=>r.url.endsWith('/recover')).body.artifact_path,'mcp-artifacts/old');
 current.artifacts[0].status='OCCUPIED';click('mcp-artifact-reload');await tick();assert.equal(dom['mcp-artifact-preview'].disabled,true);assert.match(text('mcp-artifact-current'),/保留并阻断/);
 assert(!requests.some(r=>/exec|start|install\/apply/.test(r.url)));
 console.log('PASS MCP artifact: lock-bound import, safe text, cancel, preserved drafts, stale confirmation, duplicate guard, rollback, occupied target and independent recovery');
})().catch(e=>{console.error(e);process.exitCode=1;});
