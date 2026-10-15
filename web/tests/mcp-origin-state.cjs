'use strict';
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element {
 constructor(tag='div'){this.tag=tag;this.children=[];this.events={};this.value='';this.disabled=false;this.hidden=false;this.classList={toggle(){}};}
 append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}
 addEventListener(name,fn){this.events[name]=fn;}setAttribute(name,value){this[name]=value;}
 querySelectorAll(){return this.children.flatMap(n=>[n,...n.querySelectorAll()]);}showModal(){this.open=true;}close(){this.open=false;}click(){return this.events.click?.();}closest(){return this;}
}
const html=fs.readFileSync(path.join(__dirname,'../assets/source.html'),'utf8');
const dom=Object.fromEntries([...html.matchAll(/id="([^"]+)"/g)].map(x=>[x[1],new Element()]));
const clone=v=>JSON.parse(JSON.stringify(v));
let current={sha256:'initial',root:'isolated/origin',boundary:'local only',keys:{schema_version:1,keys:[]},subjects:[{name:'fixture',sha256:'subject'}],results:[{name:'fixture',status:'UNKNOWN',checks:[{code:'MCP_ORIGIN_EVIDENCE_MISSING',detail:'missing'}]}],reports:[],checks:[],history:[],audit_count:0,audit_head:'',recovery_required:false,execution_ready:false};
let busy=false,pending=false,stale=false,previewed=null,refreshed=0;const requests=[];
const sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element(tag)}};
vm.createContext(sandbox);vm.runInContext(fs.readFileSync(path.join(__dirname,'../assets/mcp-origin-view.js'),'utf8')+'\nthis.view=MCPOriginView;',sandbox);
const ctx={busy:()=>busy,pending:()=>pending,setBusy:v=>{busy=v;sandbox.view.sync();},message(){},refreshChecks:async()=>{refreshed++;},api:async(url,body)=>{
 requests.push({url,body:body&&clone(body)});
 if(url==='/api/mcp/origin/state')return {items:clone(current)};
 if(url==='/api/mcp/origin/plan'){
   if(stale)throw new Error('PLAN_STALE');assert.equal(body.expected_sha256,current.sha256);
   const candidate=clone(current);if(body.action==='keys')candidate.keys=clone(body.keys);
   previewed={plan:{id:'origin-plan',digest:'origin-digest',changes:[{path:body.action==='keys'?'keys.json':'reports/subject.json'}]},before:clone(current),candidate};return {items:clone(previewed)};
 }
 if(url==='/api/mcp/origin/apply'){
   if(stale)throw new Error('PLAN_STALE');assert.deepEqual(clone(body),{plan_id:'origin-plan',digest:'origin-digest'});
   current={...clone(previewed.candidate),sha256:'saved-'+requests.length,audit_count:current.audit_count+1};return {items:{state:'committed'}};
 }
 if(url==='/api/mcp/origin/recover'){current.recovery_required=false;return {items:{recovered:1}};}
 throw new Error(url);
}};
const click=id=>dom[id].click(),tick=()=>new Promise(r=>setImmediate(r));
const input=(label,value)=>{const n=dom['mcp-origin-keys'].querySelectorAll().find(n=>n['aria-label']===label);assert.ok(n,label);n.value=value;n.events.input();};
(async()=>{
 sandbox.view.init(ctx);click('mcp-origin-open');await tick();assert.equal(dom['mcp-origin-dialog'].open,true);assert.equal(sandbox.view.hasDraft(),false);
 click('mcp-origin-add-key');input('公钥名称 1','Fixture');input('Ed25519 公钥 Base64 1','fixture-public-key');input('公钥来源标识 1','fixture');assert.equal(sandbox.view.hasDraft(),true);
 dom['mcp-origin-evidence'].value='unsigned fixture editor draft';dom['mcp-origin-evidence'].events.input();
 click('mcp-origin-preview-keys');await tick();assert.equal(dom['mcp-origin-plan'].hidden,false);assert.equal(requests.some(x=>x.url.endsWith('/apply')),false);
 click('mcp-origin-cancel');assert.equal(sandbox.view.hasDraft(),true);assert.equal(dom['mcp-origin-confirm'].disabled,true);
 click('mcp-origin-preview-keys');await tick();stale=true;click('mcp-origin-confirm');await tick();assert.match(dom['mcp-origin-message'].textContent,/PLAN_STALE/);assert.equal(sandbox.view.hasDraft(),true);stale=false;
 click('mcp-origin-preview-keys');await tick();click('mcp-origin-confirm');await tick();assert.equal(current.keys.keys[0].label,'Fixture');assert.equal(dom['mcp-origin-evidence'].value,'unsigned fixture editor draft');assert.equal(sandbox.view.hasDraft(),true);assert.ok(refreshed);
 input('公钥名称 1','unrelated key draft');click('mcp-origin-preview-evidence');await tick();click('mcp-origin-confirm');await tick();assert.equal(sandbox.view.hasDraft(),true);assert.equal(dom['mcp-origin-evidence'].value,'');assert.equal(dom['mcp-origin-keys'].querySelectorAll().find(n=>n['aria-label']==='公钥名称 1').value,'unrelated key draft');
 click('mcp-origin-preview-keys');await tick();current.results[0].status='FAIL';click('mcp-origin-close');click('mcp-origin-open');await tick();assert.match(dom['mcp-origin-message'].textContent,/有效性已变化/);assert.equal(dom['mcp-origin-confirm'].disabled,true);
 pending=true;const n=requests.length;click('mcp-origin-preview-keys');await tick();assert.equal(requests.length,n);pending=false;
 click('mcp-origin-reload');assert.equal(sandbox.view.hasDraft(),true);click('mcp-origin-reload');await tick();assert.equal(sandbox.view.hasDraft(),false);
 current.recovery_required=true;click('mcp-origin-close');click('mcp-origin-open');await tick();assert.equal(dom['mcp-origin-recover'].hidden,false);click('mcp-origin-recover');await tick();assert.equal(current.recovery_required,false);
 assert.equal(requests.some(x=>/exec|install|start/.test(x.url)),false);
 console.log('PASS MCP origin: key fields, independent drafts, preview/cancel/confirm, stale/expiry refresh, recovery gating and no execution');
})().catch(e=>{console.error(e);process.exitCode=1;});
