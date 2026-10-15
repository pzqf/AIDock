'use strict';
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element{
 constructor(tag='div'){this.tag=tag;this.children=[];this.events={};this.value='';this.disabled=false;this.hidden=false;this.classList={toggle(){}};}
 append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}
 addEventListener(name,fn){this.events[name]=fn;}querySelectorAll(){return this.children.flatMap(n=>[n,...n.querySelectorAll()]);}showModal(){this.open=true;}close(){this.open=false;}click(){return this.events.click?.();}
}
const html=fs.readFileSync(path.join(__dirname,'../assets/source.html'),'utf8');
const dom=Object.fromEntries([...html.matchAll(/id="([^"]+)"/g)].map(x=>[x[1],new Element()]));
const clone=v=>JSON.parse(JSON.stringify(v));
let current={plugins:[{name:'alias',status:'PASS',artifact_sha256:'artifact',manifest_sha256:'manifest',manifest_status:'PASS',permission_status:'PASS',dependency_status:'PASS',installation_state:'PREPARED',sbom_status:'UNKNOWN',document:'<script>never execute</script>',manifest:{name:'publisher',version:'1.2.3',description:'<b>text only</b>',permissions:{exec:false},dependencies:[{ecosystem:'npm',name:'child',version:'2.0.0'}]},checks:[{status:'PASS',code:'MCP_MANIFEST_VALID',detail:'清单有效'}]},{name:'missing',status:'UNKNOWN',manifest_status:'UNKNOWN',checks:[{status:'UNKNOWN',code:'MCP_MANIFEST_MISSING',detail:'缺少清单'}]}],checks:[],boundary:'预备产物，没有正式 SBOM'};
let busy=false,error=false,refreshes=0;const requests=[];
const sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element(tag)}};
vm.createContext(sandbox);vm.runInContext(fs.readFileSync(path.join(__dirname,'../assets/mcp-manifest-view.js'),'utf8')+'\nthis.view=MCPManifestView;',sandbox);
const ctx={busy:()=>busy,setBusy:v=>{busy=v;sandbox.view.sync();},message(){},refreshChecks:async()=>{refreshes++;},api:async(url,body)=>{requests.push({url,body});assert.equal(body,undefined);if(error)throw new Error('RECOVERY_REQUIRED');return{items:clone(current)};}};
const tick=()=>new Promise(r=>setImmediate(r)),click=id=>dom[id].click();
const text=()=>dom['mcp-manifest-results'].querySelectorAll().map(n=>n.textContent||'').join('\n');
(async()=>{
 sandbox.view.init(ctx);click('mcp-manifest-open');await tick();assert.equal(dom['mcp-manifest-dialog'].open,true);assert.equal(dom['mcp-manifest-results'].children.length,2);assert.match(text(),/publisher/);assert.match(text(),/2.0.0/);assert.match(text(),/尚未登记安装/);assert.match(text(),/<script>/);assert.equal(refreshes,0);
 dom['mcp-manifest-filter'].value='missing';dom['mcp-manifest-filter'].events.change();assert.equal(dom['mcp-manifest-results'].children.length,1);assert.doesNotMatch(text(),/publisher/);
 click('mcp-manifest-close');current.plugins[1].checks[0].detail='变化后的清单';click('mcp-manifest-open');await tick();assert.equal(dom['mcp-manifest-filter'].value,'missing');assert.match(text(),/变化后的清单/);
 busy=true;const n=requests.length;click('mcp-manifest-reload');click('mcp-manifest-close');await tick();assert.equal(requests.length,n);assert.equal(dom['mcp-manifest-dialog'].open,true);busy=false;
 error=true;click('mcp-manifest-reload');await tick();assert.equal(dom['mcp-manifest-results'].children.length,0);assert.match(dom['mcp-manifest-message'].textContent,/RECOVERY_REQUIRED/);error=false;
 current={plugins:[],checks:[],boundary:'no install'};click('mcp-manifest-reload');await tick();assert.match(text(),/没有已保存的安装锁条目/);assert.equal(dom['mcp-manifest-filter'].value,'');assert.equal(requests.every(x=>x.url==='/api/mcp/manifest/state'),true);
 console.log('PASS MCP manifest: read-only, direct dependencies, prepared/SBOM boundary, safe text, filtering, refresh, recovery error clears stale evidence, busy gate and no write');
})().catch(e=>{console.error(e);process.exitCode=1;});
