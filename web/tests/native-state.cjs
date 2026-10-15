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
plan.targets[0].rule={path:'AGENTS.md',changes:[{path:'content'}],handwritten_before:'# handwritten\r\n',managed:'<!-- aidock:begin -->\n规则正文\n<!-- aidock:end -->',handwritten_after:'tail'};
(async()=>{
  assert.equal(dom['nav-native'].hidden,false);assert.equal(dom['native-recheck'].disabled,true);assert.equal(dom['native-apply'].disabled,true);
  const first=dom['native-preview'].events.click();assert.equal(busy,true);assert.equal(calls[0].path,'/api/native/plan');assert.deepEqual(Array.from(calls[0].body.tools),['claude-code','codex-cli']);
  await dom['native-preview'].events.click();assert.equal(calls.length,1);
  pending.shift().resolve({items:plan});await first;
  assert.equal(dom['native-result'].hidden,false);assert.match(dom['native-evidence'].textContent,/L1 PREVIEW_ONLY.*L2 UNKNOWN/);assert.equal(dom['native-recheck'].disabled,false);assert.equal(dom['native-apply'].disabled,true);assert.match(lastMessage,/未保存草稿保留/);
  const rules=dom['native-targets'].children[0].children.find(c=>c.className==='native-rule-preview');
  assert(rules);assert.equal(rules.children[0].textContent,'规则 · AGENTS.md');assert.equal(rules.children[3].textContent,plan.targets[0].rule.managed);
  const recheck=dom['native-recheck'].events.click();assert.equal(calls[1].path,'/api/native/diff');assert.equal(calls[1].body.plan_id,'preview');assert.equal(calls[1].body.digest,'digest');
  pending.shift().reject(new Error('PLAN_STALE: target changed'));await recheck;
  assert.equal(dom['native-result'].hidden,true);assert.equal(dom['native-recheck'].disabled,true);assert.match(lastMessage,/PLAN_STALE/);
  const delayed=dom['native-preview'].events.click();dom['native-codex-cli'].checked=false;dom['native-codex-cli'].events.change();pending.shift().resolve({items:plan});await delayed;assert.equal(dom['native-result'].hidden,true);
  const single=dom['native-preview'].events.click();assert.deepEqual(Array.from(calls.at(-1).body.tools),['claude-code']);pending.shift().resolve({items:{...plan,blocked:true,targets:[{...plan.targets[0],status:'FAIL',findings:[{status:'FAIL',code:'NATIVE_TARGET_COLLISION',detail:'manual package'}],changes:[]}]}});await single;
  assert.match(dom['native-summary'].textContent,/存在阻断项/);assert.equal(dom['native-apply'].disabled,true);
  view.invalidate();assert.equal(dom['native-result'].hidden,true);assert.equal(dom['native-recheck'].disabled,true);
  needsRecovery=true;await dom['native-preview'].events.click();assert.equal(calls.length,4);
  needsRecovery=false;dom['native-claude-code'].checked=false;await dom['native-preview'].events.click();assert.equal(calls.length,4);assert.match(lastMessage,/至少一个/);
  dom['native-codex-cli'].checked=true;
  await dom['native-probe'].events.click();assert.equal(dom['native-probe-confirm'].hidden,false);assert.equal(calls.length,4);
  dom['native-probe-cancel'].events.click();assert.equal(dom['native-probe-confirm'].hidden,true);assert.equal(calls.length,4);
  dom['native-probe'].events.click();const checking=dom['native-probe-confirm-run'].events.click();assert.equal(calls.at(-1).path,'/api/native/check');assert.equal(calls.at(-1).body.probe,true);assert.equal(busy,true);assert.equal(dom['native-probe'].disabled,true);
  const report={id:'check',digest:'checkdigest',created:'2026-10-14T01:00:00Z',probe:true,stale:false,boundary:'isolated only',tools:[{tool:'codex-cli',installed:true,version:'0.162.1',target:'synthetic/.agents/skills',L1:'NOT_APPLIED',L2:'PASS',L3:'UNKNOWN',L4:'UNKNOWN',skills:[{native_name:'guide',status:'PASS',detail:'isolated discovery'}],findings:[]}]};
  pending.shift().resolve({items:report});await checking;assert.equal(dom['native-check-result'].hidden,false);assert.equal(dom['native-probe-confirm'].hidden,true);assert.equal(busy,false);assert.equal(dom['native-apply'].disabled,true);
  const layerLabels=dom['native-check-tools'].children[0].children.find(c=>c.className==='native-evidence-layers').children.map(c=>c.textContent);
  assert.deepEqual(Array.from(layerLabels),['项目文件一致','待应用','隔离项目发现','已验证','实际会话加载','未验证','能力验证','未验证']);
  const restore=view.restore();assert.equal(calls.at(-1).path,'/api/native/check/latest');assert.equal(calls.at(-1).body,undefined);pending.shift().resolve({items:{...report,stale:true,stale_reason:'source changed'}});await restore;assert.match(dom['native-check-freshness'].textContent,/已过期/);assert.match(dom['native-check-time'].textContent,/source changed/);
  const stat=dom['native-check'].events.click();assert.equal(calls.at(-1).body.probe,false);pending.shift().resolve({items:{...report,probe:false}});await stat;assert.match(dom['native-check-summary'].textContent,/文件检查/);
  const delayedLast=view.restore();view.invalidate();pending.shift().resolve({items:report});await delayedLast;assert.equal(dom['native-check-result'].hidden,true);
  dom['native-probe'].events.click();needsRecovery=true;const previousCalls=calls.length;await dom['native-probe-confirm-run'].events.click();assert.equal(calls.length,previousCalls);needsRecovery=false;dom['native-probe-cancel'].events.click();
  dom['native-probe'].events.click();dom['native-codex-cli'].events.change();assert.equal(dom['native-probe-confirm'].hidden,true);
  const rulesCheck=view.restore();pending.shift().resolve({items:{...report,tools:report.tools.map(t=>({...t,findings:[{status:'UNKNOWN',code:'RULES_SESSION_UNVERIFIED'}]}))}});await rulesCheck;
  const rulesPreview=dom['native-preview'].events.click();assert.equal(calls.at(-1).body.check_id,undefined);pending.shift().resolve({items:plan});await rulesPreview;
  view.init({busy:()=>false,pending:()=>false},{});assert.equal(dom['nav-native'].hidden,true);
  await tick();assert(!calls.some(c=>c.path.endsWith('/apply')));
  console.log('PASS native state: preview, isolated probe confirmation/cancel, layered status, historical expiry, static check, late-response invalidation, busy/recovery gates and no native writes');
})().catch(e=>{console.error(e);process.exitCode=1;});
