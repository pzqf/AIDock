'use strict';
const {readFileSync}=require('node:fs'),{join}=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element{constructor(){this.events={};this.children=[];this.hidden=false;this.disabled=false;}addEventListener(k,f){this.events[k]=f;}append(...items){this.children.push(...items);}replaceChildren(...items){this.children=items;}showModal(){this.hidden=false;}close(){this.hidden=true;}}
const ids=[...readFileSync(join(__dirname,'../assets/source.html'),'utf8').matchAll(/id="([^"]+)"/g)].map(x=>x[1]),dom=Object.fromEntries(ids.map(id=>[id,new Element()]));
const sandbox={document:{getElementById:id=>dom[id],createElement:()=>new Element()}};vm.createContext(sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/replica-checks.js'),'utf8')+'\nthis.view=ReplicaChecks;',sandbox);
const view=sandbox.view,calls=[],waiting=[],previewed=[];let busy=false,message='',canPreview=true;
view.init({busy:()=>busy,setBusy:v=>{busy=v;view.sync();},message:m=>message=m,api:async(path,body)=>{calls.push({path,body});return new Promise((resolve,reject)=>waiting.push({resolve,reject}));}},{preview:(p,scope)=>{previewed.push(p);view.setCandidate(p,scope);},previewAllowed:()=>canPreview});
const candidate={id:'generation',kind:'replica-generation',digest:'exact-generation',targets:[{copy_id:'two'}],tools:['codex-cli'],repair_only:true};
const report={id:'check',expires:'2026-10-15T00:00:00Z',created:'2026-10-14T00:00:00Z',candidate_status:'PASS',publication_status:'BLOCKED',write_supported:false,probe:true,targets:[{copy:{id:'two',name:'copy2',root:'/isolated/two/.aidock'},candidate_source:'own-source',preserved:{names:['manual']},inspection:{tools:[{tool:'codex-cli',version:'0.162.1',L1:'NOT_APPLIED',L2:'PASS',skills:[{native_name:'guide',status:'PASS',detail:'found candidate'}],findings:[]}]}}],gates:[{code:'PUBLICATION_ACCEPTANCE',status:'UNAVAILABLE',detail:'formal publication is closed'}]};
const text=n=>[n.textContent||'',...n.children.map(text)].join(' ');
(async()=>{
 assert(dom['replica-check-run'].disabled);
 assert(dom['replica-check-publish'].disabled);
 view.setCandidate(candidate);assert(!dom['replica-check-run'].disabled);
 dom['replica-check-probe'].events.click();assert(!dom['replica-check-probe-dialog'].hidden);assert.equal(calls.length,0);assert.equal(dom['replica-check-rules'].checked,false);dom['replica-check-rules'].checked=true;
 dom['replica-check-probe-cancel'].events.click();assert(dom['replica-check-probe-dialog'].hidden);assert.equal(calls.length,0);
 const checking=dom['replica-check-run'].events.click();assert.equal(calls.at(-1).body.probe,false);assert.equal(calls.at(-1).body.rule_probe,undefined);assert.equal(calls.at(-1).body.digest,'exact-generation');assert(dom['replica-check-run'].disabled);assert(dom['replica-check-rules'].disabled);
 waiting.shift().resolve({items:{...report,probe:false,candidate_status:'UNKNOWN'}});await checking;assert.match(dom['replica-check-summary'].textContent,/静态检查.*未验证.*尚未开放/);
 dom['replica-check-probe'].events.click();assert.equal(dom['replica-check-rules'].checked,false);const probing=dom['replica-check-probe-run'].events.click();assert.equal(calls.at(-1).body.probe,true);assert.equal(calls.at(-1).body.rule_probe,undefined);assert(dom['replica-check-probe-dialog'].hidden);
 waiting.shift().resolve({items:report});await probing;assert.match(dom['replica-check-summary'].textContent,/显式发现验证.*通过.*尚未开放/);assert.match(text(dom['replica-check-matrix']),/本机排除项保留.*manual/);
 assert(!calls.some(c=>/apply|rehearsal/.test(c.path)));
 assert(dom['replica-check-publish'].disabled);
 dom['replica-check-probe'].events.click();dom['replica-check-rules'].checked=true;const rules=dom['replica-check-probe-run'].events.click();assert.equal(calls.at(-1).body.rule_probe,true);assert(dom['replica-check-rules'].disabled);
 const ruleReport={...report,rule_probe:true,targets:[{...report.targets[0],inspection:{tools:[{...report.targets[0].inspection.tools[0],rules:{path:'AGENTS.md',status:'PASS',detail:'unique candidate source'}}]}}]};
 waiting.shift().resolve({items:ruleReport});await rules;assert.match(dom['replica-check-summary'].textContent,/发现与规则来源/);assert.match(text(dom['replica-check-matrix']),/AGENTS.md.*规则来源 通过.*unique candidate source/);
 const beforePreview=calls.length;dom['replica-check-preview'].events.click();assert.equal(previewed.at(-1).digest,'exact-generation');assert.equal(calls.length,beforePreview);assert(!dom['replica-check-results'].hidden);assert.match(dom['replica-check-summary'].textContent,/规则来源/);
 canPreview=false;view.sync();assert(dom['replica-check-preview'].disabled);dom['replica-check-preview'].events.click();assert.equal(previewed.length,1);canPreview=true;
 const history=dom['replica-check-history-load'].events.click();waiting.shift().resolve({items:[ruleReport]});await history;
 assert.match(text(dom['replica-check-history']),/有效性待核对/);
 assert.match(text(dom['replica-check-history']),/显式发现与规则来源/);
 const opening=dom['replica-check-history'].children[0].children.at(-1).events.click();assert.equal(calls.at(-1).body.check_id,'check');waiting.shift().resolve({items:{...ruleReport,stale:true,stale_reason:'tool bytes changed'}});await opening;
 assert.match(dom['replica-check-summary'].textContent,/已过期/);assert.match(text(dom['replica-check-matrix']),/历史 通过/);
 assert.match(text(dom['replica-check-matrix']),/规则来源 历史 通过/);
 const delayed=dom['replica-check-run'].events.click();view.invalidate();waiting.shift().resolve({items:report});await delayed;assert(dom['replica-check-results'].hidden);assert(dom['replica-check-probe'].disabled);
 assert(dom['replica-check-preview'].disabled);
 view.setCandidate({...candidate,digest:'blocked-generation',blocked:true});assert(dom['replica-check-probe'].disabled);const before=calls.length;await dom['replica-check-probe-run'].events.click();assert.equal(calls.length,before);
 const stale=dom['replica-check-run'].events.click();waiting.shift().reject(new Error('PLAN_STALE'));await stale;assert.match(message,/PLAN_STALE/);
 // Reload leaves only persistent history. Opening a report must not silently
 // replace an unrelated active candidate or reuse its comparison decisions.
 view.invalidate();
 const historyAgain=dom['replica-check-history-load'].events.click();waiting.shift().resolve({items:[report]});await historyAgain;
 const openingAgain=dom['replica-check-history'].children[0].children.at(-1).events.click();
 const boundReport={...report,generation_id:candidate.id,generation_digest:candidate.digest};
 waiting.shift().resolve({items:boundReport});await openingAgain;assert(dom['replica-check-preview'].disabled);assert(!dom['replica-check-reopen'].disabled);
 const reopening=dom['replica-check-reopen'].events.click();assert.equal(calls.at(-1).path,'/api/replica/generation/reopen');assert.equal(calls.at(-1).body.check_id,report.id);assert(dom['replica-check-reopen'].disabled);
 const restored={generation:candidate,check:boundReport,current:true,source:{id:'one',name:'original publisher'},targets:[{id:'two',name:'original target'}]};
 waiting.shift().resolve({items:restored});await reopening;assert(!dom['replica-check-run'].disabled);assert.match(dom['replica-check-identity'].textContent,/对应当前候选/);assert.match(dom['replica-check-candidate'].textContent,/exact-generation/);
 assert(!calls.some(c=>/apply|rehearsal/.test(c.path)));
 // Expired evidence stays historical even when the underlying candidate is
 // current, so it can be rechecked without creating a new generation.
 const expiry=dom['replica-check-reopen'].events.click();waiting.shift().resolve({items:{...restored,check:{...boundReport,stale:true,stale_reason:'expired check'}}});await expiry;
 assert.match(dom['replica-check-summary'].textContent,/已过期/);assert(!dom['replica-check-run'].disabled);assert.match(message,/原候选仍有效.*重新检查/);
 const refresh=dom['replica-check-run'].events.click();waiting.shift().resolve({items:boundReport});await refresh;assert(!/已过期/.test(dom['replica-check-summary'].textContent));
 // A changed source permits only reading the original saved candidate.
 const drift=dom['replica-check-reopen'].events.click();waiting.shift().resolve({items:{...restored,current:false,reason:'source changed',check:{...boundReport,stale:true,stale_reason:'source changed'}}});await drift;
 assert(dom['replica-check-run'].disabled);assert(dom['replica-check-probe'].disabled);assert(!dom['replica-check-preview'].disabled);assert.match(dom['replica-check-candidate'].textContent,/只读查看/);
 const blockedCalls=calls.length;await dom['replica-check-run'].events.click();assert.equal(calls.length,blockedCalls);
 const mismatched=dom['replica-check-reopen'].events.click();waiting.shift().resolve({items:{...restored,generation:{...candidate,id:'unrelated'}}});await mismatched;assert.match(message,/绑定不一致/);assert(dom['replica-check-run'].disabled);
 const late=dom['replica-check-reopen'].events.click();view.invalidate();waiting.shift().resolve({items:restored});await late;assert(dom['replica-check-preview'].disabled);assert(dom['replica-check-results'].hidden);assert(dom['replica-check-reopen'].disabled);
 console.log('PASS replica candidate checks: explicit probe/cancel, static default, immutable digest, busy guard, stale history, late response and closed publication');
})().catch(e=>{console.error(e);process.exitCode=1;});
