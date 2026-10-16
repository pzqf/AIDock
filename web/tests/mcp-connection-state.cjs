'use strict';
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element {
 constructor(tag='div'){this.tag=tag;this.children=[];this.events={};this.checked=true;this.disabled=false;this.hidden=false;this.classList={toggle(){}};}
 append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}
 addEventListener(name,fn){this.events[name]=fn;}click(){return this.events.click?.();}
 querySelectorAll(selector){const all=this.children.flatMap(n=>[n,...n.querySelectorAll()]);return selector==='button'?all.filter(n=>n.tag==='button'):all;}
}
const html=fs.readFileSync(path.join(__dirname,'../assets/source.html'),'utf8');
const dom=Object.fromEntries([...html.matchAll(/id="(mcp-(?:connect-[^"]+|native-connect))"/g)].map(x=>[x[1],new Element(x[1].includes('claude-code')||x[1].includes('codex-cli')?'input':'div')]));
const clone=v=>JSON.parse(JSON.stringify(v)),requests=[];
let reviews=0,busy=false,plugin='fixture',session={name:plugin,expected_sha256:'permission',expected_review_sha256:'review',expected_runtime_attempt:'runtime'},fail='',writes=0,recoveries=0,history=[],versions=null,hold=false,held=[];
const configuration={files:[{contract:{path:'.mcp.json'},fields:[{name:'personal',ownership:'UNMANAGED'}],checks:[]},{contract:{path:'.codex/config.toml'},fields:[],checks:[]}]};
const makePlan=body=>({id:'plan',digest:'digest',request:body,changes:3,boundary:'安装不启动插件',transaction:{targets:[{path:'.mcp.json',before:{exists:false},after:{exists:true}},{path:'.codex/config.toml',before:{exists:false},after:{exists:true}},{path:'.aidock-mcp-ownership.json',before:{exists:false},after:{exists:true}}]}});
const sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element(tag)}};
vm.createContext(sandbox);vm.runInContext(fs.readFileSync(path.join(__dirname,'../assets/mcp-connection-view.js'),'utf8')+'\nthis.view=MCPConnectionView;',sandbox);
const ctx={review:()=>reviews++,busy:()=>busy,setBusy:v=>{busy=v;sandbox.view.sync();},plugin:()=>plugin,session:()=>session,api:async(url,body)=>{
 requests.push({url,body:body&&clone(body)});if(fail)throw Error(fail);
 if(hold&&body===undefined)return new Promise(resolve=>held.push(()=>resolve({items:url.endsWith('/versions')?{id:'obsolete',created:'2026-10-16T00:00:00Z'}:[]})));
 if(url==='/api/native/mcp/versions')return {items:versions&&clone(versions)};
 if(url==='/api/native/mcp/versions/check'){versions={id:'version',created:'2026-10-16T00:00:00Z',checks:[{status:'PASS',detail:'固定测试版本'}]};return {items:clone(versions)};}
 if(url==='/api/native/mcp/history')return {items:clone(history)};
 if(url==='/api/native/config')return {items:clone(configuration)};
 if(url==='/api/native/mcp/plan')return {items:makePlan(clone(body))};
 if(url==='/api/native/mcp/rollback/plan')return {items:{id:'reverse',digest:'reverse-digest',action:history[0].plan.action==='rollback'?'undo-rollback':'rollback',targets:makePlan({}).transaction.targets}};
 if(url.endsWith('/apply')){assert(body.digest);writes++;history=[{plan:{id:body.plan_id,action:body.plan_id==='reverse'?'rollback':'apply'},state:'committed',updated:'2026-10-16T00:00:00Z'}];return {items:{}};}
 if(url.endsWith('/recover')){recoveries++;history=[];return {items:{recovered:1}};}throw Error(url);
}};
const click=id=>dom[id].click(),tick=()=>new Promise(r=>setImmediate(r)),flush=async()=>{await tick();await tick();},text=id=>[dom[id],...dom[id].querySelectorAll()].map(n=>n.textContent||'').join('\n');
(async()=>{
 sandbox.view.init(ctx);await sandbox.view.load();assert.match(text('mcp-connect-plugin-state'),/fixture/);click('mcp-connect-review');assert.equal(reviews,1);assert.equal(dom['mcp-connect-plan'].disabled,true);assert.equal(dom['mcp-connect-remove'].disabled,false);
 click('mcp-connect-versions');await flush();assert.deepEqual(requests.find(r=>r.url.endsWith('/versions/check')).body.tools,['claude-code','codex-cli']);assert.equal(dom['mcp-connect-plan'].disabled,false);
 click('mcp-connect-plan');await flush();assert.equal(writes,0);assert.equal(dom['mcp-connect-preview'].hidden,false);assert.match(text('mcp-connect-changes'),/\.mcp\.json/);assert.match(text('mcp-connect-changes'),/字段归属记录/);assert.equal(requests.at(-1).body.check_id,'version');
 click('mcp-connect-cancel');assert.equal(writes,0);assert.equal(dom['mcp-connect-preview'].hidden,true);
 click('mcp-connect-plan');await flush();dom['mcp-connect-codex-cli'].checked=false;dom['mcp-connect-codex-cli'].events.change();assert.equal(dom['mcp-connect-preview'].hidden,true);click('mcp-connect-confirm');await flush();assert.equal(writes,0);
 click('mcp-connect-plan');await flush();click('mcp-connect-confirm');click('mcp-connect-confirm');await flush();assert.equal(writes,1);assert.equal(dom['mcp-connect-preview'].hidden,true);assert.match(text('mcp-connect-message'),/没有启动插件/);
 dom['mcp-connect-history'].querySelectorAll('button')[0].click();await flush();assert.match(text('mcp-connect-changes'),/回退连接变化/);click('mcp-connect-confirm');await flush();assert.equal(writes,2);
 dom['mcp-connect-history'].querySelectorAll('button')[0].click();await flush();assert.match(text('mcp-connect-changes'),/撤销连接回退/);click('mcp-connect-cancel');assert.equal(writes,2);
 session=null;sandbox.view.sync();assert.equal(dom['mcp-connect-plan'].disabled,true);assert.equal(dom['mcp-connect-remove'].disabled,false);click('mcp-connect-remove');await flush();assert.equal(requests.at(-1).body.session.name,'fixture');assert.equal(requests.at(-1).body.check_id,undefined);assert.match(text('mcp-connect-changes'),/移除/);
 fail='PLAN_STALE';click('mcp-connect-confirm');await flush();assert.equal(writes,2);assert.equal(dom['mcp-connect-preview'].hidden,true);assert.match(text('mcp-connect-message'),/PLAN_STALE/);fail='';
 history=[{plan:{id:'pending',action:'apply'},state:'prepared',updated:'2026-10-16T00:00:00Z'}];await sandbox.view.load();assert.equal(dom['mcp-connect-remove'].disabled,true);assert.equal(dom['mcp-connect-recover'].hidden,false);click('mcp-connect-recover');await flush();assert.equal(recoveries,1);assert.equal(dom['mcp-connect-remove'].disabled,false);
 fail='RECOVERY_CONFLICT';await sandbox.view.load();assert.equal(dom['mcp-connect-remove'].disabled,true);assert.match(text('mcp-connect-message'),/RECOVERY_CONFLICT/);fail='';
 hold=true;const older=sandbox.view.load();hold=false;versions=null;const current=sandbox.view.load();await current;held.splice(0).forEach(resolve=>resolve());await older;session={name:'fixture'};sandbox.view.sync();assert.equal(dom['mcp-connect-plan'].disabled,true,'obsolete asynchronous version response replaced latest state');
 dom['mcp-connect-claude-code'].checked=false;sandbox.view.sync();assert.equal(dom['mcp-connect-remove'].disabled,true);assert.equal(dom['mcp-connect-versions'].disabled,true);
 console.log('MCP connection UI: explicit version/preview/confirm, cancellation, selection invalidation, single confirmation, rollback/undo, revoke-safe removal, stale/error recovery, async generation and empty selection passed');
})().catch(e=>{console.error(e);process.exitCode=1;});
