'use strict';
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element {
 constructor(tag='div'){this.tag=tag;this.children=[];this.events={};this.value='';this.disabled=false;this.hidden=false;this.classList={toggle(){}};}
 append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}
 addEventListener(name,fn){this.events[name]=fn;}setAttribute(name,value){this[name]=value;}
 querySelectorAll(){return this.children.flatMap(n=>[n,...n.querySelectorAll()]);}showModal(){this.open=true;}close(){this.open=false;}click(){return this.events.click?.();}
}
const html=fs.readFileSync(path.join(__dirname,'../assets/source.html'),'utf8');
const dom=Object.fromEntries([...html.matchAll(/id="([^"]+)"/g)].map(x=>[x[1],new Element()]));
const clone=v=>JSON.parse(JSON.stringify(v));let relayFailure=false;let current={config:{schema_version:1,servers:[]},sha256:'empty',servers:[]},busy=false,pending=false,stale=false;
const requests=[];const sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element(tag)}};
vm.createContext(sandbox);vm.runInContext(fs.readFileSync(path.join(__dirname,'../assets/mcp-view.js'),'utf8')+'\nthis.view=MCPView;',sandbox);
const ctx={busy:()=>busy,pending:()=>pending,setBusy:v=>{busy=v;sandbox.view.sync();},refresh:async()=>{},message(){},api:async(url,body)=>{
 requests.push({url,body:body&&clone(body)});
 if(url==='/api/mcp/relay/check'){if(relayFailure)throw new Error('FIXTURE_FAILED');assert.deepEqual(clone(body),{probe:true});return {items:{status:'PASS',execution_ready:false,boundary:'fixed fixture only',findings:[{status:'PASS',detail:'EOF cleanup'}]}};}
 if(url==='/api/mcp/state')return {items:clone(current)};
 if(url==='/api/mcp/plan'){if(stale)throw new Error('PLAN_STALE');return {items:{plan:{id:'plan',digest:'digest',changes:[{path:'mcp.yaml'}]},candidate:{config:clone(body.config),servers:body.config.servers.map(s=>({name:s.name,enabled:s.enabled,checks:[{status:'UNKNOWN',code:'MCP_LOCK_MISSING',detail:'missing'}]}))}}};}
 if(url==='/api/mcp/apply'){assert.deepEqual(clone(body),{plan_id:'plan',digest:'digest'});current={...current,config:clone(requests.findLast(x=>x.url==='/api/mcp/plan').body.config),sha256:'saved'};return {items:{state:'committed'}};}
 throw new Error(url);
}};
const click=id=>dom[id].click(),tick=()=>new Promise(r=>setImmediate(r));
const submit=()=>dom['mcp-form'].events.submit({preventDefault(){}});
const field=label=>dom['mcp-servers'].querySelectorAll().find(n=>n['aria-label']===label);
(async()=>{
 sandbox.view.init(ctx);click('nav-mcp');await tick();assert.equal(dom['mcp-dialog'].open,true);assert.equal(sandbox.view.hasDraft(),false);
 click('mcp-add');field('包名 1').value='@fixture/server';field('包名 1').events.input();field('精确版本或提交 1').value='1.2.3';field('精确版本或提交 1').events.input();
 assert.equal(sandbox.view.hasDraft(),true);submit();await tick();assert.equal(requests.some(x=>x.url==='/api/mcp/apply'),false);
 assert.equal(requests.findLast(x=>x.url==='/api/mcp/plan').body.config.servers[0].enabled,false);
 assert.match(dom['mcp-changes'].querySelectorAll().map(n=>n.textContent||'').join('\n'),/文件：只读/);
 const beforeRelay=requests.length;click('mcp-relay-start');assert.equal(requests.length,beforeRelay);click('mcp-relay-cancel');assert.equal(requests.length,beforeRelay);assert.equal(dom['mcp-relay-prompt'].hidden,true);
 click('mcp-relay-start');click('mcp-relay-confirm');await tick();assert.equal(sandbox.view.hasDraft(),true);assert.equal(dom['mcp-confirm'].disabled,false);assert.equal(dom['mcp-relay-result'].hidden,false);assert.match(dom['mcp-relay-summary'].textContent,/固定夹具/);
 relayFailure=true;click('mcp-relay-start');click('mcp-relay-confirm');await tick();assert.equal(sandbox.view.hasDraft(),true);assert.equal(dom['mcp-confirm'].disabled,false);assert.match(dom['mcp-message'].textContent,/保存预览保留/);relayFailure=false;
 click('mcp-cancel');assert.equal(sandbox.view.hasDraft(),true);assert.equal(dom['mcp-confirm'].disabled,true);
 click('mcp-close');click('nav-mcp');await tick();assert.equal(field('包名 1').value,'@fixture/server');
 stale=true;submit();await tick();assert.match(dom['mcp-message'].textContent,/PLAN_STALE/);assert.equal(sandbox.view.hasDraft(),true);assert.equal(dom['mcp-confirm'].disabled,true);stale=false;
 submit();await tick();const calls=requests.length;busy=true;click('mcp-confirm');await tick();assert.equal(requests.length,calls);busy=false;
 click('mcp-confirm');await tick();assert.equal(sandbox.view.hasDraft(),false);assert.equal(current.config.servers.length,1);assert.match(dom['mcp-message'].textContent,/回退/);
 field('声明启用 1').checked=true;field('声明启用 1').events.change();submit();await tick();click('mcp-cancel');assert.equal(sandbox.view.hasDraft(),true);
 pending=true;const plans=requests.filter(x=>x.url==='/api/mcp/plan').length;submit();await tick();assert.equal(requests.filter(x=>x.url==='/api/mcp/plan').length,plans);pending=false;
 click('mcp-reload');assert.equal(sandbox.view.hasDraft(),true);click('mcp-reload');await tick();assert.equal(sandbox.view.hasDraft(),false);
 // Existing install declarations are preserved without an execution path.
 current.config.servers[0].source.install={command:'never-run',args:['literal']};click('mcp-reload');await tick();
 field('名称 1').value='renamed';field('名称 1').events.input();submit();await tick();assert.deepEqual(requests.findLast(x=>x.url==='/api/mcp/plan').body.config.servers[0].source.install,{command:'never-run',args:['literal']});
 // A concurrent source edit invalidates the old plan but keeps the draft.
 current.sha256='external';click('mcp-close');click('nav-mcp');await tick();assert.match(dom['mcp-message'].textContent,/发生变化/);assert.equal(dom['mcp-confirm'].disabled,true);assert.equal(sandbox.view.hasDraft(),true);
 assert.equal(requests.some(x=>/exec|start|install/.test(x.url)),false);
 current.servers=[{name:'saved',enabled:false,checks:[{code:'MCP_ARTIFACT_MATCH',status:'PASS',detail:'current lock'}]}];
 const retained=field('名称 1').value;await sandbox.view.refreshChecks();assert.equal(field('名称 1').value,retained);assert.equal(sandbox.view.hasDraft(),true);assert.match(dom['mcp-checks'].querySelectorAll().map(n=>n.textContent||'').join('\n'),/MCP_ARTIFACT_MATCH/);assert.equal(dom['mcp-confirm'].disabled,true);
 click('mcp-reload');click('mcp-reload');await tick();
 field('需要的密钥名称 1（每行一个）').value='api_key';field('需要的密钥名称 1（每行一个）').events.input();
 field('密钥环境变量 1（每行 变量=占位符）').value='API_TOKEN=${SECRET:api_key}\nAPI_TOKEN=${SECRET:api_key}';field('密钥环境变量 1（每行 变量=占位符）').events.input();
 const mappingPlans=requests.filter(x=>x.url==='/api/mcp/plan').length;submit();await tick();assert.equal(requests.filter(x=>x.url==='/api/mcp/plan').length,mappingPlans);assert.match(dom['mcp-message'].textContent,/唯一大写变量/);
 field('来源类型 1').value='github';field('来源类型 1').events.change();assert.match(field('密钥环境变量 1（每行 变量=占位符）').value,/API_TOKEN=.*\nAPI_TOKEN=/);
 field('来源类型 1').value='npm';field('来源类型 1').events.change();
 field('密钥环境变量 1（每行 变量=占位符）').value='API_TOKEN=${SECRET:api_key}';field('密钥环境变量 1（每行 变量=占位符）').events.input();submit();await tick();
 assert.equal(requests.findLast(x=>x.url==='/api/mcp/plan').body.config.servers[0].env.API_TOKEN,'${SECRET:api_key}');assert.match(dom['mcp-changes'].querySelectorAll().map(n=>n.textContent||'').join('\n'),/声明密钥：api_key/);assert.match(dom['mcp-changes'].querySelectorAll().map(n=>n.textContent||'').join('\n'),/API_TOKEN=/);
 console.log('PASS MCP: definition drafts, preservation, preview/cancel/confirm, stale, recovery gate and no launch');
})().catch(e=>{console.error(e);process.exitCode=1;});
