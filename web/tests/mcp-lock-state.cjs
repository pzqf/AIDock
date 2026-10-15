'use strict';
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element{
 constructor(tag='div'){this.tag=tag;this.children=[];this.events={};this.value='';this.disabled=false;this.hidden=false;this.classList={toggle(){}};}
 append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}
 addEventListener(name,fn){this.events[name]=fn;}setAttribute(name,value){this[name]=value;}
 querySelectorAll(){return this.children.flatMap(n=>[n,...n.querySelectorAll()]);}showModal(){this.open=true;}close(){this.open=false;}click(){return this.events.click?.();}
}
const html=fs.readFileSync(path.join(__dirname,'../assets/source.html'),'utf8');
const dom=Object.fromEntries([...html.matchAll(/id="([^"]+)"/g)].map(x=>[x[1],new Element()]));
const clone=v=>JSON.parse(JSON.stringify(v));
const doc=JSON.stringify({schema_version:1,plugins:[]},null,2);
let current={sha256:'empty',document:doc,definitions:['fixture'],verification_sha256:'initial',plugins:[],checks:[],present:false,artifact_root:'isolated/local/mcp-artifacts'};
const candidate={...current,present:true,document:JSON.stringify({schema_version:1,plugins:[{name:'fixture'}]},null,2),plugins:[{name:'fixture',artifact_status:'PASS',dependency_status:'PASS',trust_level:'L0',execution_ready:false,checks:[{status:'UNKNOWN',code:'MCP_SOURCE_TRUST_UNVERIFIED',detail:'source remains L0'}]}]};
let busy=false,pending=false,stale=false;const requests=[];
const sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element(tag)}};
vm.createContext(sandbox);vm.runInContext(fs.readFileSync(path.join(__dirname,'../assets/mcp-lock-view.js'),'utf8')+'\nthis.view=MCPLockView;',sandbox);
const ctx={busy:()=>busy,pending:()=>pending,setBusy:v=>{busy=v;sandbox.view.sync();},refresh:async()=>{},message(){},api:async(url,body)=>{
 requests.push({url,body:body&&clone(body)});
 if(url==='/api/mcp/lock/state')return {items:clone(current)};
 if(url==='/api/mcp/lock/inspect')return {items:{expected_sha256:current.sha256,document:candidate.document,candidate:clone(candidate)}};
 if(url==='/api/mcp/lock/plan'){if(stale)throw new Error('PLAN_STALE');return {items:{plan:{id:'lock-plan',digest:'lock-digest',changes:[{path:'plugins.lock'}]},candidate:{...clone(candidate),document:body.document}}};}
 if(url==='/api/mcp/lock/apply'){if(stale)throw new Error('PLAN_STALE');assert.deepEqual(clone(body),{plan_id:'lock-plan',digest:'lock-digest'});current={...clone(candidate),sha256:'saved',document:requests.findLast(x=>x.url==='/api/mcp/lock/plan').body.document};return {items:{state:'committed'}};}
 throw new Error(url);
}};
const click=id=>dom[id].click(),tick=()=>new Promise(r=>setImmediate(r));
const inspect=()=>dom['mcp-lock-inspect-form'].events.submit({preventDefault(){}});
(async()=>{
 sandbox.view.init(ctx);click('mcp-lock-open');await tick();assert.equal(dom['mcp-lock-dialog'].open,true);assert.equal(sandbox.view.hasDraft(),false);
 dom['mcp-lock-name'].value='fixture';dom['mcp-lock-path'].value='fixture-1.2.3';dom['mcp-lock-runtime-kind'].value='node';dom['mcp-lock-runtime-version'].value='22.14.0';dom['mcp-lock-runtime-path'].value='runtime/node';
 inspect();await tick();assert.equal(sandbox.view.hasDraft(),true);assert.equal(dom['mcp-lock-inspected'].hidden,false);assert.equal(dom['mcp-lock-plan'].hidden,true);
 assert.equal(requests.findLast(x=>x.url==='/api/mcp/lock/inspect').body.artifact_path,'mcp-artifacts/fixture-1.2.3');
 assert.equal(requests.findLast(x=>x.url==='/api/mcp/lock/inspect').body.runtime.constraint,'=22.14.0');
 assert.match(dom['mcp-lock-inspected-checks'].querySelectorAll().map(n=>n.textContent||'').join('\n'),/L0/);
 const calls=requests.length;inspect();await tick();assert.equal(requests.length,calls); // Draft cannot be overwritten by another generation.
 click('mcp-lock-preview');await tick();assert.equal(dom['mcp-lock-plan'].hidden,false);assert.equal(requests.some(x=>x.url.endsWith('/apply')),false);
 click('mcp-lock-cancel');assert.equal(sandbox.view.hasDraft(),true);assert.equal(dom['mcp-lock-confirm'].disabled,true);
 click('mcp-lock-preview');await tick();stale=true;click('mcp-lock-confirm');await tick();assert.match(dom['mcp-lock-message'].textContent,/PLAN_STALE/);assert.equal(dom['mcp-lock-confirm'].disabled,true);assert.equal(sandbox.view.hasDraft(),true);stale=false;
 click('mcp-lock-preview');await tick();current.verification_sha256='artifact changed';click('mcp-lock-close');click('mcp-lock-open');await tick();assert.match(dom['mcp-lock-message'].textContent,/本地产物已变化/);assert.equal(dom['mcp-lock-confirm'].disabled,true);assert.equal(sandbox.view.hasDraft(),true);
 click('mcp-lock-preview');await tick();click('mcp-lock-confirm');await tick();assert.equal(sandbox.view.hasDraft(),false);assert.match(dom['mcp-lock-message'].textContent,/回退/);
 dom['mcp-lock-document'].value+='\n';dom['mcp-lock-document'].events.input();pending=true;const n=requests.length;click('mcp-lock-preview');await tick();assert.equal(requests.length,n);pending=false;
 click('mcp-lock-reload');assert.equal(sandbox.view.hasDraft(),true);click('mcp-lock-reload');await tick();assert.equal(sandbox.view.hasDraft(),false);
 dom['mcp-lock-file'].files=[{size:4,text:async()=>'{bad'}];dom['mcp-lock-file'].events.change();await tick();assert.equal(sandbox.view.hasDraft(),true);assert.equal(dom['mcp-lock-document'].value,'{bad');
 click('mcp-lock-close');click('mcp-lock-open');await tick();assert.equal(dom['mcp-lock-document'].value,'{bad');
 assert.equal(requests.some(x=>/exec|start|install/.test(x.url)),false);
 console.log('PASS MCP lock: local candidate, separate saved checks, drafts, preview/cancel/confirm, artifact drift, source drift, recovery gate and no launch');
})().catch(e=>{console.error(e);process.exitCode=1;});
