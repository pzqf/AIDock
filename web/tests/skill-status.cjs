'use strict';
const {readFileSync} = require('node:fs');
const {join} = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
class Element {
  constructor(tag) { this.tagName=tag; this.children=[]; this.textContent=''; }
  append(...children) { this.children.push(...children); }
  setAttribute(name,value) { this[name]=value; }
}
const sandbox={document:{createElement:tag=>new Element(tag)}};
vm.createContext(sandbox);
vm.runInContext(readFileSync(join(__dirname,'../assets/skill-status.js'),'utf8')+'\nthis.status=SkillStatus;',sandbox);
const view=sandbox.status;
const item={skill:{id:'one',native_name:'review',revision:'sha256:first',display_name:'<img src=x onerror=bad()>'}};
const state={root:'D:/fixture/.aidock',index:{owner:'project',scope_id:'server'},profile:{skills:{enable:['one']}},effective_profile:{skills:{disable:['one'],pins:{one:'sha256:first'}}},profile_report:{trace:[{path:'/skills/one',layer:'project'},{path:'/skills/one',layer:'local'},{path:'/skills/pins/one',layer:'defaults'}]}};
view.bind('copy-A');
const report={owner:'project',scope_id:'server',copy:'copy-A',root:'D:\\fixture',created:'2026-10-16',tools:[{tool:'codex-cli',L1:'FAIL',L2:'FAIL',L3:'PASS',L4:'PASS',skills:[{id:'one',native_name:'review',revision:'sha256:first',status:'PASS',L1:'PASS',L2:'PASS',L3:'UNKNOWN',L4:'UNKNOWN',file_detail:'owned exact bytes',detail:'isolated discovery'},{id:'two',native_name:'review',revision:'sha256:first',L1:'FAIL',L2:'FAIL'}]}]};
view.update(report);
let selected=view.selection(state,item);
assert.equal(selected.shared,true);assert.equal(selected.effective,false);assert.equal(selected.origin,'本机覆盖');assert.equal(selected.pinOrigin,'全局默认');
assert.deepEqual(Array.from(selected.chain),['项目共享','本机覆盖']);
let evidence=view.evidence(state,item,'codex-cli');assert.equal(evidence.usable,false);assert.equal(evidence.states.L1,'UNKNOWN');assert.match(evidence.note,/已停用/);
state.effective_profile.skills={enable:['one'],pins:{one:'sha256:first'}};
evidence=view.evidence(state,item,'codex-cli');assert.equal(evidence.states.L1,'PASS');assert.equal(evidence.states.L2,'PASS');assert.equal(evidence.states.L3,'UNKNOWN');assert.equal(evidence.states.L4,'UNKNOWN');
state.root='D:/fixture/custom-source';assert.equal(view.evidence(state,item,'codex-cli').states.L1,'PASS');state.root='D:/fixture/.aidock';
// Aggregate failure in another package and aggregate session results never leak.
assert.equal(view.evidence(state,{skill:{...item.skill,id:'two'}},'codex-cli').states.L1,'UNKNOWN');
assert.equal(view.evidence(state,item,'claude-code').states.L1,'UNKNOWN');
assert.match(view.evidence(state,item,'claude-code').note,/未覆盖此工具/);
for(const change of [{revision:'sha256:changed'},{id:'another'},{native_name:'renamed'}]) assert.equal(view.evidence(state,{skill:{...item.skill,...change}},'codex-cli').states.L2,'UNKNOWN');
const legacy=JSON.parse(JSON.stringify(report));delete legacy.tools[0].skills[0].L1;delete legacy.tools[0].skills[0].L2;delete legacy.tools[0].skills[0].L3;delete legacy.tools[0].skills[0].L4;
view.update(legacy);evidence=view.evidence(state,item,'codex-cli');assert.equal(evidence.states.L1,'UNKNOWN');assert.equal(evidence.states.L2,'PASS');assert.equal(evidence.states.L3,'UNKNOWN');
view.update({...report,stale:true,stale_reason:'source changed'});evidence=view.evidence(state,item,'codex-cli');assert.equal(evidence.states.L1,'UNKNOWN');assert.match(evidence.note,/已过期/);
for(const changed of [{owner:'other'},{scope_id:'client'},{copy:'copy-B'},{root:'D:/other'}]){view.update({...report,...changed});assert.equal(view.evidence(state,item,'codex-cli').states.L2,'UNKNOWN');}
view.update(report);state.profile_error='bad configuration';assert.equal(view.selection(state,item).effective,null);assert.equal(view.evidence(state,item,'codex-cli').states.L1,'UNKNOWN');delete state.profile_error;
state.recovery_required=true;assert.equal(view.evidence(state,item,'codex-cli').states.L1,'UNKNOWN');delete state.recovery_required;
state.retirements=[{id:'one'}];assert.equal(view.selection(state,item).effective,false);assert.match(view.evidence(state,item,'codex-cli').note,/已退役/);state.retirements=[];
const tree=view.render(state,item),all=[];function walk(n){all.push(n);for(const child of n.children)walk(child);}walk(tree);
assert.equal(all.filter(n=>n.className==='skill-tool-row').length,2);assert.equal(all.filter(n=>n.className?.startsWith('skill-level')).length,8);assert(all.some(n=>n.textContent.includes('会话加载')));assert.equal(tree['aria-label'],'<img src=x onerror=bad()> 工具生效状态');assert(all.every(n=>n.innerHTML===undefined));
view.clear();assert.equal(view.evidence(state,item,'codex-cli').states.L2,'UNKNOWN');
console.log('PASS skill status: exact identity/revision/copy/root, per-skill L1-L4, selection provenance, stale/legacy/missing evidence, recovery and safe text');
