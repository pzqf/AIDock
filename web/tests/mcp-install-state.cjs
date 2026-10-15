'use strict';
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element{
 constructor(tag='div'){this.tag=tag;this.children=[];this.events={};this.value='';this.checked=false;this.disabled=false;this.hidden=false;this.classList={toggle(){}};}
 append(...nodes){this.children.push(...nodes);if(!this.value&&this.tag==='select'&&nodes[0])this.value=nodes[0].value;}replaceChildren(...nodes){this.children=nodes;if(this.tag==='select')this.value='';}
 addEventListener(name,fn){this.events[name]=fn;}querySelectorAll(selector){const all=this.children.flatMap(n=>[n,...n.querySelectorAll()]);return selector==='button'?all.filter(n=>n.tag==='button'):all;}showModal(){this.open=true;}close(){this.open=false;}click(){return this.events.click?.();}
}
const html=fs.readFileSync(path.join(__dirname,'../assets/source.html'),'utf8'),dom=Object.fromEntries([...html.matchAll(/id="([^"]+)"/g)].map(x=>[x[1],new Element(x[1]==='mcp-install-name'?'select':'div')]));
const clone=v=>JSON.parse(JSON.stringify(v)),record={id:'installation',plugin:{name:'fixture',version:'1.2.3',artifact:{sha256:'artifact'}},installed_at:'time',trust_level:'L0',minimum_trust:'L2',risk_reason:'reviewed'};
let current={sha256:'saved',root:'test-local',installations:[],checks:[],history:[],audit_count:0,candidates:[{plugin:record.plugin,check:{checks:[{status:'UNKNOWN',code:'CVE_MISSING',detail:'安全评估待验证'}]},manifest:{manifest:{name:'publisher',description:'<script>text only</script>',permissions:{exec:false},dependencies:[{ecosystem:'npm',name:'direct',version:'2.0.0'}]}}}]};
let busy=false,pending=false,fail='',writes=0,plans=0,refreshes=0;const requests=[];
const sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element(tag)}};vm.createContext(sandbox);vm.runInContext(fs.readFileSync(path.join(__dirname,'../assets/mcp-install-view.js'),'utf8')+'\nthis.view=MCPInstallView;',sandbox);
const ctx={busy:()=>busy,pending:()=>pending,setBusy:v=>{busy=v;sandbox.view.sync();},message(){},refreshChecks:async()=>{refreshes++;},api:async(url,body)=>{
 requests.push({url,body});if(fail)throw new Error(fail);
 if(url.endsWith('/state'))return {items:clone(current)};
 if(url.endsWith('/plan')){plans++;return {items:{plan:{id:'plan',digest:'digest',changes:[{path:'installed/fixture.json',after:{}}]},before:clone(current),candidate:{installations:[{record,status:'CURRENT'}]}}};}
 if(url.endsWith('/apply')){assert.equal(body.plan_id,'plan');assert.equal(body.digest,'digest');writes++;current.installations=[{record,status:'CURRENT',sbom:{dependencies:[{name:'direct'}]},sbom_sha256:'sbom-digest'}];current.history=[{id:'saved',action:'install-install',state:'committed',updated:'time'}];current.audit_count++;current.sha256='new';return {items:{state:'committed'}};}
 if(url.endsWith('/recover')){current.recovery_required=false;return {items:{recovered:1}};}
 throw new Error(url);
}};
const tick=()=>new Promise(r=>setImmediate(r)),click=id=>dom[id].click(),change=id=>dom[id].events[id==='mcp-install-reason'?'input':'change'](),text=id=>dom[id].querySelectorAll().map(n=>n.textContent||'').join('\n');
(async()=>{
 sandbox.view.init(ctx);click('mcp-install-open');await tick();assert.equal(dom['mcp-install-dialog'].open,true);assert.equal(dom['mcp-install-preview'].disabled,true);assert.match(text('mcp-install-review'),/<script>/);
 dom['mcp-install-allow'].checked=true;change('mcp-install-allow');assert.equal(dom['mcp-install-preview'].disabled,true);dom['mcp-install-reason'].value='独立项目测试';change('mcp-install-reason');assert.equal(dom['mcp-install-preview'].disabled,false);assert.equal(sandbox.view.hasDraft(),true);
 click('mcp-install-preview');await tick();assert.equal(plans,1);assert.equal(writes,0);assert.equal(requests.at(-1).body.allow_unverified,true);assert.equal(dom['mcp-install-plan'].hidden,false);click('mcp-install-cancel');assert.equal(writes,0);assert.equal(dom['mcp-install-reason'].value,'独立项目测试');
 click('mcp-install-preview');await tick();dom['mcp-install-reason'].value='修改理由';change('mcp-install-reason');assert.equal(dom['mcp-install-plan'].hidden,true);click('mcp-install-confirm');await tick();assert.equal(writes,0);
 click('mcp-install-preview');await tick();fail='PLAN_STALE';click('mcp-install-confirm');await tick();assert.equal(writes,0);assert.equal(dom['mcp-install-reason'].value,'修改理由');assert.equal(dom['mcp-install-plan'].hidden,true);fail='';
 click('mcp-install-preview');await tick();click('mcp-install-confirm');click('mcp-install-confirm');await tick();assert.equal(writes,1);assert.equal(refreshes,1);assert.equal(dom['mcp-install-allow'].checked,false);assert.equal(dom['mcp-install-reason'].value,'');assert.equal(sandbox.view.hasDraft(),false);assert.match(text('mcp-install-current'),/查看本机安装的 SBOM/);
 const remove=dom['mcp-install-current'].querySelectorAll('button')[0];remove.click();await tick();assert.equal(requests.at(-1).body.action,'remove');click('mcp-install-cancel');
 dom['mcp-install-allow'].checked=true;change('mcp-install-allow');dom['mcp-install-reason'].value='恢复登记的风险理由';change('mcp-install-reason');dom['mcp-install-history'].querySelectorAll('button')[0].click();await tick();assert.equal(requests.at(-1).body.action,'rollback');assert.equal(requests.at(-1).body.risk_reason,'恢复登记的风险理由');click('mcp-install-cancel');
 click('mcp-install-close');click('mcp-install-open');await tick();assert.equal(dom['mcp-install-reason'].value,'恢复登记的风险理由');assert.equal(dom['mcp-install-allow'].checked,true);
 current.installations[0].status='STALE';click('mcp-install-reload');await tick();assert.match(text('mcp-install-current'),/历史清单/);pending=true;sandbox.view.sync();assert.equal(dom['mcp-install-preview'].disabled,true);const count=plans;click('mcp-install-preview');await tick();assert.equal(plans,count);pending=false;
 current.recovery_required=true;click('mcp-install-reload');await tick();assert.equal(dom['mcp-install-recover'].hidden,false);assert.equal(dom['mcp-install-preview'].disabled,true);click('mcp-install-recover');await tick();assert.equal(dom['mcp-install-recover'].hidden,true);
 current.candidates[0].check.trust_level='L2';dom['mcp-install-allow'].checked=false;dom['mcp-install-reason'].value='';click('mcp-install-reload');await tick();assert.equal(dom['mcp-install-preview'].disabled,false);assert.match(dom['mcp-install-risk'].textContent,/达到默认 L2/);click('mcp-install-preview');await tick();assert.equal(requests.at(-1).body.allow_unverified,false);assert.equal(requests.at(-1).body.risk_reason,'');click('mcp-install-cancel');current.candidates[0].check.trust_level='L1';click('mcp-install-reload');await tick();assert.equal(dom['mcp-install-preview'].disabled,true);dom['mcp-install-reason'].value='恢复登记的风险理由';
 fail='read unavailable';click('mcp-install-reload');await tick();assert.equal(dom['mcp-install-current'].children.length,0);assert.equal(dom['mcp-install-preview'].disabled,true);assert.equal(dom['mcp-install-reason'].value,'恢复登记的风险理由');assert.match(dom['mcp-install-message'].textContent,/read unavailable/);
 console.log('PASS MCP install: explicit L0 risk and reason, preview/cancel/confirm, stale and duplicate guard, SBOM, remove/rollback, preserved drafts, recovery and failed-read clearing');
})().catch(e=>{console.error(e);process.exitCode=1;});
