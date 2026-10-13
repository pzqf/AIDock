'use strict';
const {readFileSync} = require('node:fs');
const {join} = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
class Element {
  constructor(){this.children=[];this.events={};this.checked=true;this.disabled=false;this.hidden=false;this.textContent='';}
  addEventListener(name,fn){this.events[name]=fn;}
  append(...children){this.children.push(...children);}
  replaceChildren(...children){this.children=children;}
}
const ids=[...readFileSync(join(__dirname,'../assets/source.html'),'utf8').matchAll(/id="([^"]+)"/g)].map(x=>x[1]);
const dom=Object.fromEntries(ids.map(id=>[id,new Element()]));
const sandbox={document:{getElementById:id=>dom[id],createElement:()=>new Element()}};
vm.createContext(sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/native-view.js'),'utf8')+'\nthis.nativeView = NativePreview;',sandbox);
const view=sandbox.nativeView;
const pending=[];let busy=false,needsRecovery=false,lastMessage='',calls=[];
view.init({busy:()=>busy,pending:()=>needsRecovery,setBusy:value=>{busy=value;view.sync();},message:m=>lastMessage=m,api:async(path,body)=>{calls.push({path,body});return new Promise((resolve,reject)=>pending.push({resolve,reject}));}},{native_root:'synthetic/project'});
const plan={id:'preview',digest:'digest',blocked:false,evidence:{L1:'PREVIEW_ONLY',L2:'UNKNOWN',L3:'UNKNOWN',L4:'UNKNOWN'},boundary:'No target writes',targets:[{root:'synthetic/.agents/skills',adapter:{id:'codex-cli',version:'0.1',contract_checked:'test',observed_cli_version:'unverified',reference:'https://developers.openai.com/codex/skills/'},status:'UNKNOWN',findings:[{status:'UNKNOWN',code:'TOOL_VERSION_UNVERIFIED',detail:'not observed discovery'}],changes:[{path:'guide/assets/data.bin',after:{bytes:3}}],packages:[{native_name:'guide',layer:'external',revision:'pinned',tree:{entries:{'assets/data.bin':{bytes:3,sha256:'binary'},empty:{directory:true}}}}]}]};
const tick=()=>new Promise(resolve=>setImmediate(resolve));
(async()=>{
  assert.equal(dom['nav-native'].hidden,false);assert.equal(dom['native-recheck'].disabled,true);assert.equal(dom['native-apply'].disabled,true);
  const first=dom['native-preview'].events.click();assert.equal(busy,true);assert.equal(calls[0].path,'/api/native/plan');assert.deepEqual(Array.from(calls[0].body.tools),['claude-code','codex-cli']);
  await dom['native-preview'].events.click();assert.equal(calls.length,1);
  pending.shift().resolve({items:plan});await first;
  assert.equal(dom['native-result'].hidden,false);assert.match(dom['native-evidence'].textContent,/L1 PREVIEW_ONLY.*L2 UNKNOWN/);assert.equal(dom['native-recheck'].disabled,false);assert.equal(dom['native-apply'].disabled,true);assert.match(lastMessage,/未保存草稿保留/);
  const recheck=dom['native-recheck'].events.click();assert.equal(calls[1].path,'/api/native/diff');assert.equal(calls[1].body.plan_id,'preview');assert.equal(calls[1].body.digest,'digest');
  pending.shift().reject(new Error('PLAN_STALE: target changed'));await recheck;
  assert.equal(dom['native-result'].hidden,true);assert.equal(dom['native-recheck'].disabled,true);assert.match(lastMessage,/PLAN_STALE/);
  const delayed=dom['native-preview'].events.click();dom['native-codex-cli'].checked=false;dom['native-codex-cli'].events.change();pending.shift().resolve({items:plan});await delayed;assert.equal(dom['native-result'].hidden,true);
  const single=dom['native-preview'].events.click();assert.deepEqual(Array.from(calls.at(-1).body.tools),['claude-code']);pending.shift().resolve({items:{...plan,blocked:true,targets:[{...plan.targets[0],status:'FAIL',findings:[{status:'FAIL',code:'NATIVE_TARGET_COLLISION',detail:'manual package'}],changes:[]}]}});await single;
  assert.match(dom['native-summary'].textContent,/存在阻断项/);assert.equal(dom['native-apply'].disabled,true);
  view.invalidate();assert.equal(dom['native-result'].hidden,true);assert.equal(dom['native-recheck'].disabled,true);
  needsRecovery=true;await dom['native-preview'].events.click();assert.equal(calls.length,4);
  needsRecovery=false;dom['native-claude-code'].checked=false;await dom['native-preview'].events.click();assert.equal(calls.length,4);assert.match(lastMessage,/至少一个/);
  view.init({busy:()=>false,pending:()=>false},{});assert.equal(dom['nav-native'].hidden,true);
  await tick();assert(!calls.some(c=>c.path.endsWith('/apply')));
  console.log('PASS native preview state: binding, saved-version boundary, evidence, busy/recovery gates, selection invalidation, late response, stale diff, conflicts and no native apply');
})().catch(e=>{console.error(e);process.exitCode=1;});
