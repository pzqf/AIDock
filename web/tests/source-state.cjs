'use strict';
// State regressions use synthetic HTTP responses; browser acceptance is separate.
const {readFileSync}=require('node:fs');
const {join}=require('node:path');
const vm=require('node:vm');
const assert=require('node:assert/strict');
class Element {
 constructor(id='',tag='div'){this.id=id;this.tagName=tag;this.children=[];this.events={};this.dataset={};this.value='';this.disabled=false;this.hidden=false;this.textContent='';this.classList={toggle(){}};}
 setAttribute(name,value){this[name]=value;}
 append(...nodes){this.children.push(...nodes);if(this.tagName==='select'&&!this.value&&nodes[0])this.value=nodes[0].value;}
 replaceChildren(...nodes){this.children=nodes;if(this.tagName==='select')this.value='';}
 addEventListener(name,fn){this.events[name]=fn;}
 click(){return this.events.click?.();}
 showModal(){this.open=true;}
 close(){this.open=false;}
 focus(){this.focused=true;}
 get firstChild(){return this.children[0]||{textContent:''};}
}
const ids=[...readFileSync(join(__dirname,'../assets/source.html'),'utf8').matchAll(/id="([^"]+)"/g)].map(x=>x[1]);
assert.equal(new Set(ids).size,ids.length,'duplicate DOM IDs break real browser element binding');
const dom=Object.fromEntries(ids.map(id=>[id,new Element(id,id==='package-file'?'select':'div')]));
const state={root:'synthetic/.aidock',index:{owner:'project-A',scope_id:'server',skills:[{id:'skill-A',native_name:'guide',revision:'hash',files:[{path:'SKILL.md'},{path:'binary.bin'}]}]},profile:{skills:{}},history:[],recovery_required:false};
const pending=[],eventSources=[];let planCounter=0,applyCount=0,lastRequest=null,planError=null,applyError=null,networkError=false;
const impact={id:'skill-A',owner:'project-A',scope_id:'server',reference_scope:'CURRENT_BOUND_SOURCE',native_before:'guide',native_after:'guide',references:[{kind:'dependency',from_name:'caller',path:'skills.yaml/caller/dependencies.skills/0',blocks_retirement:true}],unknown:['其他项目和离线范围未登记']};
const preflight={status:'FAIL',platform:'windows',boundary:'仅静态证据',counts:{PASS:0,FAIL:1,UNKNOWN:0,WARN:0},skills:[{id:'skill-A',native_name:'guide',status:'FAIL',enabled:true,checks:[{status:'FAIL',code:'DEPENDENCY_MISSING',detail:'missing required'}]}]};
const response=items=>({ok:true,json:async()=>({ok:true,items})});
const bindingHeaders=[], cleanedURLs=[];
const sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element('',tag),querySelectorAll:selector=>['[data-console-connection-notice]','dialog[open]'].includes(selector)?[]:Object.values(dom),addEventListener(){}},location:{hash:'#token=synthetic',pathname:'/source.html',search:'?binding=registered-A'},history:{replaceState(a,b,url){cleanedURLs.push(url);}},URLSearchParams,EventSource:class{constructor(){this.readyState=1;eventSources.push(this);}close(){this.readyState=2;}},fetch:async(path,options={})=>{
 if(networkError)throw new Error('synthetic network outage');
 bindingHeaders.push(options.headers?.['X-AIDock-Binding']);
 if(path==='/api/session')return {ok:true,json:async()=>({csrf:'csrf'})};
 if(path==='/api/state')return {ok:true,json:async()=>({version:'dev',source_copy:'test',bridge_file:'fixture'})};
 if(path==='/api/bridge')return response({status:'STALE',checked_at:'fixture',bridges:[{id:'bridge-A',project:'project-A',scope:'server',copy:'test',root:'external',source_path:'skills',status:'STALE',current_revision:'set-new',findings:[],candidates:[{revision:'package-new',skill:{name:'external-guide',path:'skills/guide',files:[],findings:[]}}]}]});
 if(path==='/api/source/state')return response(state);
 if(path.startsWith('/api/source/file'))return new Promise(resolve=>pending.push({path,resolve}));
 if(path.startsWith('/api/source/references'))return response(impact);
 if(path==='/api/source/check')return response(preflight);
 if(path==='/api/source/plan'){lastRequest=JSON.parse(options.body);if(planError)return {ok:true,json:async()=>planError};return response({plan:{id:String(++planCounter),digest:'digest',action:lastRequest.action,changes:[]},findings:[],impact:['rename','retire'].includes(lastRequest.action)?impact:null});}
 if(path==='/api/source/apply'){applyCount++;if(applyError)return {ok:true,json:async()=>applyError};return response({state:'committed'});}
 throw new Error(path);
}};
vm.createContext(sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/skill-status.js'),'utf8'),sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/console-connection.js'),'utf8'),sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/bridge-view.js'),'utf8'),sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/source.js'),'utf8'),sandbox);
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const submit=()=>dom.editor.events.submit({preventDefault(){}});
const descendants=element=>element.children.flatMap(child=>[child,...descendants(child)]);
(async()=>{
 await tick();assert.match(dom.binding.textContent,/project-A/);
 assert.equal(cleanedURLs[0],'/source.html?binding=registered-A');assert(bindingHeaders.length>2&&bindingHeaders.every(x=>x==='registered-A'),'request lost immutable project binding');
 dom['skill-name'].value='guide';dom.description.value='example';submit();await tick();assert.equal(dom['plan-panel'].hidden,false);
 dom.editor.events.input({target:dom.content});assert.equal(dom['plan-panel'].hidden,true);await dom['apply-plan'].events.click();assert.equal(applyCount,0,'invalidated preview was applied');
 submit();await tick();await dom['apply-plan'].events.click();assert.equal(applyCount,1);assert.equal(dom['skill-name'].value,'','stale editor was kept after save');
 dom['new'].events.click();assert.equal(dom.content.value,'');assert(!dom['editor-draft-state'].textContent.includes('未保存'),'new blank form inherited a saved draft flag');dom['editor-close'].events.click();
 const row=dom['managed-skills'].children[0];descendants(row).find(x=>x.textContent==='编辑包').events.click();const old=pending.shift();
 dom['package-file'].value='binary.bin';dom['package-file'].events.change();const latest=pending.shift();
 latest.resolve(response({type:'binary',content:'',bytes:3,sha256:'binary'}));await tick();old.resolve(response({type:'text',content:'late response',bytes:1,sha256:'old'}));await tick();
 assert.equal(dom.content.value,'');assert.equal(dom.content.disabled,true);assert.equal(dom.preview.disabled,true);
 descendants(row).find(x=>x.textContent==='启用').events.click();await tick();assert.equal(dom.preview.disabled,true,'busy reset enabled binary editor');
 dom['package-file'].value='SKILL.md';dom['package-file'].events.change();pending.shift().resolve(response({type:'text',content:'original',bytes:8,sha256:'doc'}));await tick();
 dom.content.value='draft';dom.editor.events.input({target:dom.content});
 dom['package-file'].value='binary.bin';dom['package-file'].events.change();pending.shift().resolve(response({type:'binary',content:'',bytes:3,sha256:'bin'}));await tick();
 dom['package-file'].value='SKILL.md';dom['package-file'].events.change();pending.shift().resolve(response({type:'text',content:'original',bytes:8,sha256:'doc'}));await tick();assert.equal(dom.content.value,'draft','file switch lost draft');
 submit();await tick();assert.equal(lastRequest.files[0].content,'draft');
 assert.equal(lastRequest.files[0].expected_sha256,'doc');
 const action=label=>descendants(dom['managed-skills'].children[0]).find(x=>x.textContent===label).events.click();
 // A draft must retain the hash originally loaded, even if a later read changed.
 dom['package-file'].events.change();pending.shift().resolve(response({type:'text',content:'external',bytes:8,sha256:'external'}));await tick();
 assert.equal(dom.content.value,'draft');assert.match(dom.message.textContent,/外部发生变化/);submit();await tick();assert.equal(lastRequest.files[0].expected_sha256,'doc');
 const plansBefore=planCounter;action('退役并保留');assert.equal(planCounter,plansBefore);assert.match(dom.message.textContent,/未保存/);assert.equal(dom.content.value,'draft');
 // A failed save preserves the draft and invalidates its old preview.
 submit();await tick();applyError={ok:false,code:'PLAN_STALE',summary:'external change'};await dom['apply-plan'].events.click();assert.equal(dom['plan-panel'].hidden,true);
 action('编辑包');pending.shift().resolve(response({type:'text',content:'external',bytes:8,sha256:'external'}));await tick();assert.equal(dom.content.value,'draft');
 applyError=null;submit();await tick();await dom['apply-plan'].events.click();
 action('编辑包');pending.shift().resolve(response({type:'text',content:'external',bytes:8,sha256:'external'}));await tick();dom.content.value='new draft';dom.editor.events.input({target:dom.content});
 await dom['discard-draft'].events.click();assert.equal(dom.content.value,'new draft');assert.match(dom['discard-draft'].textContent,/确认/);
 const discardRead=dom['discard-draft'].events.click();pending.shift().resolve(response({type:'text',content:'external current',bytes:8,sha256:'external-current'}));await discardRead;assert.equal(dom.content.value,'external current');submit();await tick();assert.equal(lastRequest.files[0].expected_sha256,'external-current');
 action('展示名与标签');assert.equal(dom['display-label'].hidden,false);assert.equal(dom['name-label'].hidden,true);
 dom['display-name'].value='项目约定';dom['skill-tags'].value='server， 项目';submit();await tick();assert.equal(lastRequest.action,'metadata');assert.equal(lastRequest.display_name,'项目约定');assert.deepEqual(lastRequest.tags,['server','项目']);
 dom['display-name'].value='';dom['skill-tags'].value='';dom.editor.events.input({target:dom['display-name']});assert.equal(dom['plan-panel'].hidden,true);submit();await tick();assert.deepEqual(lastRequest.tags,[]);assert.equal(lastRequest.display_name,'');
 action('原生改名');assert.equal(dom['name-label'].hidden,false);assert.equal(dom['content-label'].hidden,true);dom['skill-name'].value='guide-v2';submit();await tick();assert.equal(lastRequest.action,'rename');assert.equal(lastRequest.name,'guide-v2');assert.equal(dom['impact-panel'].hidden,false);
 action('引用与影响');await tick();assert.equal(dom['plan-panel'].hidden,true);assert.equal(dom['impact-panel'].hidden,false);assert.match(dom['impact-unknown'].children[0].textContent,/UNKNOWN/);assert.match(dom['impact-references'].children[0].textContent,/阻止退役/);
 action('退役并保留');dom['retire-reason'].value='被新版替代';planError={ok:false,code:'SKILL_REFERENCED',summary:'declared reference',recovery:impact};submit();await tick();assert.equal(lastRequest.action,'retire');assert.equal(lastRequest.reason,'被新版替代');assert.equal(dom['plan-panel'].hidden,true);assert.equal(dom['impact-panel'].hidden,false);assert.match(dom.message.textContent,/SKILL_REFERENCED/);planError=null;
 state.retirements=[{id:'skill-A',reason:'unused'}];state.profile.skills={disable:['skill-A']};await dom.reload.events.click();
 const retiredButtons=descendants(dom['managed-skills'].children[0]).filter(x=>x.tagName==='button').map(x=>x.textContent);assert.deepEqual(retiredButtons.sort(),['引用与影响','恢复管理'].sort());action('恢复管理');await tick();assert.equal(lastRequest.action,'reactivate');assert.match(dom.message.textContent,/仍保持本层停用/);
 dom['new'].events.click();assert.equal(dom.content.disabled,false);assert.equal(dom.preview.disabled,false);
 // Viewing saved metadata alone must not invent an unsaved draft.
 state.retirements=[];state.index.skills[0].display_name='已保存名称';await dom.reload.events.click();action('展示名与标签');dom['editor-close'].events.click();action('展示名与标签');assert.equal(dom['display-name'].value,'已保存名称');assert(!dom.message.textContent.includes('已恢复'));dom['display-name'].value='';dom.editor.events.input({target:dom['display-name']});dom['editor-close'].events.click();action('展示名与标签');assert.equal(dom['display-name'].value,'','clearing saved metadata was lost');dom['editor-close'].events.click();delete state.index.skills[0].display_name;
 // New inheritance controls choose a revision explicitly and preserve drafts.
 state.retirements=[];state.library_root='synthetic/global';
 const skill=state.index.skills[0];
 state.effective_skills=[{skill,layer:'inherited',owner:'global',scope_id:'global',read_only:true,status:'PINNED'}];
 state.inherited_skills=[{owner:'global',scope_id:'global',skill}];
 state.library=[{skill:{...skill,revision:'old'},current:false},{skill:{...skill,revision:'new'},current:true}];
 await dom.reload.events.click();assert.equal(dom['library-panel'].hidden,false);
 const libraryRow=dom['library-skills'].children[0];const picker=libraryRow.children.find(x=>x.tagName==='label').children[0];
 assert.equal(picker.value,'new');picker.value='old';picker.events.change();assert.equal(dom['plan-panel'].hidden,true);
 libraryRow.children.find(x=>x.tagName==='button').events.click();await tick();assert.equal(lastRequest.action,'inherit');assert.equal(lastRequest.revision,'old');
 const inheritedButtons=descendants(dom['managed-skills'].children[0]).filter(x=>x.tagName==='button').map(x=>x.textContent);
 assert(inheritedButtons.includes('查看包'));assert(!inheritedButtons.includes('编辑包'));assert(!inheritedButtons.includes('退役并保留'));
 action('查看包');pending.shift().resolve(response({type:'text',read_only:true,content:'inherited',bytes:9,sha256:'cache'}));await tick();assert.equal(dom.content.disabled,true);assert.equal(dom.preview.disabled,true);
 const beforeReadOnlySubmit=planCounter;submit();await tick();assert.equal(planCounter,beforeReadOnlySubmit);
 action('创建项目覆盖');await tick();assert.equal(lastRequest.action,'override');assert.equal(lastRequest.revision,'hash');
 action('独立分叉');dom['skill-name'].value='new-guide';submit();await tick();assert.equal(lastRequest.action,'fork');assert.equal(lastRequest.revision,'hash');assert.equal(lastRequest.name,'new-guide');
 state.effective_skills=[{skill:{...skill,revision:'custom',base_revision:'hash'},layer:'override',owner:'project-A',scope_id:'server',status:'BASE_STALE'}];
 state.inherited_skills=[{owner:'global',scope_id:'global',skill:{...skill,revision:'new'}}];await dom.reload.events.click();
 assert(dom['managed-skills'].children[0].children.some(x=>x.textContent.includes('BASE_STALE')));action('恢复继承');await tick();assert.equal(lastRequest.action,'restore-inheritance');assert.equal(lastRequest.revision,'new');
 action('编辑包');pending.shift().resolve(response({type:'text',content:'custom',bytes:6,sha256:'custom-hash'}));await tick();dom.content.value='keep draft';dom.editor.events.input({target:dom.content});
 const beforeDraftRestore=planCounter;action('恢复继承');await tick();assert.equal(planCounter,beforeDraftRestore);assert.equal(dom.content.value,'keep draft');assert.match(dom.message.textContent,/未保存/);
 state.library_error='unavailable';state.library=[];await dom.reload.events.click();assert.match(dom['library-error'].textContent,/已有固定缓存/);assert.equal(dom['managed-skills'].children.length,1);
 state.index.owner='global';state.effective_skills=[{skill,layer:'owned',owner:'global',scope_id:'global'}];state.library_root='';await dom.reload.events.click();assert.equal(dom['source-title'].textContent,'全局技能库');assert.equal(dom['library-panel'].hidden,true);
 action('治理声明');assert.match(dom.message.textContent,/未保存/);
 action('编辑包');pending.shift().resolve(response({type:'text',content:'current',bytes:7,sha256:'doc-now'}));await tick();await dom['discard-draft'].events.click();const discarding=dom['discard-draft'].events.click();pending.shift().resolve(response({type:'text',content:'current',bytes:7,sha256:'doc-now'}));await discarding;
 action('治理声明');assert.equal(dom['governance-label'].hidden,false);assert.equal(dom['content-label'].hidden,true);assert.deepEqual(JSON.parse(dom.governance.value),{dependencies:{},compatibility:{},requirements:{}});
 dom.governance.value='{ invalid';dom.editor.events.input({target:dom.governance});const invalidBefore=planCounter;submit();await tick();assert.equal(planCounter,invalidBefore);assert.match(dom.message.textContent,/有效 JSON/);
 dom.governance.value=JSON.stringify({dependencies:{},compatibility:{platforms:['windows']},requirements:{}});dom.editor.events.input({target:dom.governance});submit();await tick();assert.equal(lastRequest.action,'governance');assert.equal(lastRequest.revision,'hash');assert.deepEqual(lastRequest.governance.compatibility.platforms,['windows']);
 dom.governance.value=JSON.stringify({dependencies:{},compatibility:{tools:{'codex-cli':'>=1'}},requirements:{}});dom.editor.events.input({target:dom.governance});assert.equal(dom['plan-panel'].hidden,true);
 const govBefore=planCounter;action('独立分叉');await tick();assert.equal(planCounter,govBefore);assert.match(dom.message.textContent,/治理草稿/);
 applyError={ok:false,code:'PLAN_STALE',summary:'source changed'};submit();await tick();await dom['apply-plan'].events.click();action('治理声明');assert.match(dom.governance.value,/codex-cli/);assert.equal(dom['discard-governance'].hidden,false);applyError=null;
 await dom['discard-governance'].events.click();assert.match(dom['discard-governance'].textContent,/确认/);await dom['discard-governance'].events.click();assert.deepEqual(JSON.parse(dom.governance.value),{dependencies:{},compatibility:{},requirements:{}});
 state.effective_skills=[{skill:{...skill,dependencies:{skills:[]}},layer:'inherited',owner:'global',scope_id:'global',read_only:true}];await dom.reload.events.click();action('查看声明');const readBefore=planCounter;assert.equal(dom.governance.disabled,true);assert.equal(dom.preview.disabled,true);submit();await tick();assert.equal(planCounter,readBefore);
 await dom.check.events.click();assert.equal(dom['check-panel'].hidden,false);assert.match(dom['check-summary'].textContent,/失败/);assert.match(dom['check-summary'].textContent,/生效尚未验证/);assert(dom['check-results'].children[0].children.some(x=>x.textContent.includes('DEPENDENCY_MISSING')));
 dom.editor.events.input({target:dom.governance});assert.equal(dom['check-panel'].hidden,true);
 state.effective_skills=[{skill,layer:'owned',owner:'global',scope_id:'global'}];await dom.reload.events.click();planError={ok:false,code:'SKILL_PREFLIGHT_FAILED',summary:'blocked',recovery:preflight};action('启用');await tick();assert.equal(dom['plan-panel'].hidden,true);assert.equal(dom['check-panel'].hidden,false);planError=null;
 state.index.owner='project-A';state.effective_skills=[{skill,layer:'external',owner:'project-A',scope_id:'server',read_only:true}];
 const approval={approval_id:'approved-first',binding:{scope:'server',copy:'test',root:'external'},package_path:'skills/guide',candidate_revision:'package-first',bridge_revision:'set-first',skill};
 state.external_skills=[approval];state.external_history=[approval,{...approval,approval_id:'approved-second',candidate_revision:'package-second'}];await dom.reload.events.click();
 const externalButtons=descendants(dom['managed-skills'].children[0]).filter(x=>x.tagName==='button').map(x=>x.textContent);
 assert(externalButtons.includes('查看包'));assert(!externalButtons.includes('创建项目覆盖'));assert(!externalButtons.includes('独立分叉'));assert(!externalButtons.includes('治理声明'));
 assert.equal(dom['external-panel'].hidden,false);const extRow=dom['external-skills'].children[0];const extPicker=extRow.children.find(x=>x.tagName==='label').children[0];extPicker.value='approved-second';extPicker.events.change();assert.equal(dom['plan-panel'].hidden,true);
 await extRow.children.find(x=>x.tagName==='button').events.click();assert.equal(lastRequest.action,'external-select');assert.equal(lastRequest.approval,'approved-second');assert.match(dom['plan-summary'].textContent,/外部目录保持只读/);
 await dom['bridge-check'].events.click();assert.equal(dom['plan-panel'].hidden,true);assert.equal(dom['bridge-panel'].hidden,false);const candidateButton=dom['bridge-results'].children[0].children.find(x=>x.textContent==='预览采纳此包');await candidateButton.events.click();assert.equal(lastRequest.action,'external-adopt');assert.equal(lastRequest.package_path,'skills/guide');assert.equal(lastRequest.revision,'package-new');assert.equal(lastRequest.bridge_revision,'set-new');
 await dom.reload.events.click();assert.equal(dom['bridge-panel'].hidden,true);assert.equal(dom['external-panel'].hidden,false);
 const retainedText=dom.content.value;dom['plan-panel'].hidden=false;dom['nav-native'].events.click();assert.equal(dom['source-view'].hidden,true);assert.equal(dom['source-details'].hidden,true);assert.equal(dom['native-view'].hidden,false);assert.equal(dom.content.value,retainedText);
 dom['nav-source'].events.click();assert.equal(dom['source-view'].hidden,false);assert.equal(dom['source-details'].hidden,false);assert.equal(dom['plan-panel'].hidden,false);assert.equal(dom['native-view'].hidden,true);
 // List workflow uses saved status and metadata; filtering never writes.
 state.effective_skills=[{skill,layer:'owned',owner:'project-A',scope_id:'server'},{skill:{...skill,id:'skill-B',native_name:'review',display_name:'代码审查',tags:['质量']},layer:'fork',owner:'project-A',scope_id:'server'}];
 state.skill_descriptions={'skill-A':'接口约定','skill-B':'检查代码风险与测试覆盖'};
 state.profile.skills={enable:['skill-B'],disable:[]};await dom.reload.events.click();
 assert.equal(dom['total-count'].textContent,'2 个技能');assert.equal(dom['enabled-count'].textContent,'1 个');
 const beforeFilters=applyCount;dom['skill-search'].value='测试覆盖';dom['skill-search'].events.input();assert.equal(dom['managed-skills'].children.length,1);assert.equal(dom['managed-skills'].children[0].dataset.skillId,'skill-B');
 dom['skill-search'].value='';dom['status-filter'].value='disabled';dom['status-filter'].events.change();assert.equal(dom['managed-skills'].children[0].dataset.skillId,'skill-A');
 dom['status-filter'].value='all';dom['layer-filter'].value='fork';dom['layer-filter'].events.change();assert.equal(dom['managed-skills'].children[0].dataset.skillId,'skill-B');
 const toggle=descendants(dom['managed-skills'].children[0]).find(x=>x.role==='switch');assert.equal(toggle['aria-checked'],'true');toggle.events.click();await tick();assert.equal(lastRequest.action,'disable');assert.equal(dom['plan-panel'].open,true);assert.equal(toggle['aria-checked'],'true','preview changed saved switch');assert.equal(dom['enabled-count'].textContent,'1 个');dom['cancel-plan'].events.click();assert.equal(dom['plan-panel'].open,false);assert.equal(applyCount,beforeFilters);
 dom['skill-search'].value='no match';dom['skill-search'].events.input();assert.equal(dom['list-count'].textContent,'0 / 2 个技能');descendants(dom['managed-skills'].children[0]).find(x=>x.textContent==='清除筛选').events.click();assert.equal(dom['managed-skills'].children.length,2);
 state.effective_profile={skills:{enable:['skill-A'],disable:['skill-B']}};state.profile_report={trace:[{path:'/skills/skill-A',layer:'local'},{path:'/skills/skill-B',layer:'local'}]};await dom.reload.events.click();
 dom['status-filter'].value='effective-enabled';dom['status-filter'].events.change();assert.equal(dom['managed-skills'].children.length,1);assert.equal(dom['managed-skills'].children[0].dataset.skillId,'skill-A');
 dom['status-filter'].value='effective-disabled';dom['status-filter'].events.change();assert.equal(dom['managed-skills'].children[0].dataset.skillId,'skill-B');
 dom['status-filter'].value='overridden';dom['status-filter'].events.change();assert.equal(dom['managed-skills'].children.length,2);assert(descendants(dom['managed-skills'].children[0]).some(n=>n.textContent.includes('本机覆盖')));assert.equal(applyCount,beforeFilters);
 state.profile_error='invalid';await dom.reload.events.click();assert.equal(dom['list-count'].textContent,'0 / 2 个技能');delete state.profile_error;delete state.effective_profile;delete state.profile_report;dom['status-filter'].value='all';await dom.reload.events.click();
 let evidenceInvalidations=0;sandbox.NativePreview={invalidate:()=>evidenceInvalidations++,sync(){}};await dom.reload.events.click();assert(evidenceInvalidations>0,'source refresh allowed late tool evidence to survive');delete sandbox.NativePreview;
 // Closing and reopening the drawer preserves both creation and file drafts.
 dom['new'].events.click();dom['skill-name'].value='draft-guide';dom.description.value='描述';dom.content.value='创建草稿';dom.editor.events.input({target:dom.content});dom['editor-close'].events.click();assert.equal(dom['editor-panel'].hidden,true);dom['new'].events.click();assert.equal(dom.content.value,'创建草稿');assert.equal(dom['skill-name'].value,'draft-guide');
 submit();await tick();assert.equal(dom['plan-panel'].open,true);dom['cancel-plan'].events.click();assert.equal(dom['editor-panel'].open,true);assert.equal(dom.content.value,'创建草稿');dom['editor-close'].events.click();
 action('编辑包');pending.shift().resolve(response({type:'text',content:'saved',bytes:5,sha256:'saved'}));await tick();dom.content.value='drawer draft';dom.editor.events.input({target:dom.content});dom['editor-close'].events.click();action('编辑包');pending.shift().resolve(response({type:'text',content:'saved',bytes:5,sha256:'saved'}));await tick();assert.equal(dom.content.value,'drawer draft');dom['editor-close'].events.click();
 state.history=[{id:'new',action:'rollback:old',state:'committed',restorable:true,updated:'2026-10-14T03:00:00Z'},{id:'old',action:'edit',state:'committed',restorable:true,updated:'2026-10-14T02:00:00Z'}];await dom.reload.events.click();dom['nav-history'].events.click();assert.equal(dom['crumb-name'].textContent,'时间线与回退');descendants(dom.history.children[0]).find(x=>x.textContent==='撤销此次回退').events.click();await tick();assert.equal(lastRequest.action,'rollback');assert.equal(lastRequest.snapshot,'new');assert.equal(dom['apply-plan'].textContent,'确认回退源');dom['cancel-plan'].events.click();assert.equal(applyCount,beforeFilters);
 state.recovery_required=true;state.profile=null;await dom.reload.events.click();assert.equal(dom['recovery-panel'].hidden,false);assert.equal(dom.preview.disabled,true);assert.equal(dom.recover.disabled,false);
 const draftBeforeDisconnect=dom.content.value;eventSources[0].onerror();assert.equal(dom.preview.disabled,true);assert.equal(dom['console-connection'].hidden,false);const writesBeforeDisconnect=applyCount;
 await assert.rejects(vm.runInContext('api("/api/source/apply",{plan_id:"must-not-send"})',sandbox),/连接尚未恢复/);assert.equal(applyCount,writesBeforeDisconnect);assert.equal(dom.content.value,draftBeforeDisconnect);
 eventSources[0].readyState=2;dom['console-reconnect'].events.click();await tick();assert.equal(eventSources.length,2,'closed SSE stream was not recreated after manual reconnect');assert.equal(dom['console-connection'].hidden,true);assert.equal(dom.preview.disabled,true,'reconnect bypassed existing recovery gate');assert.equal(dom.content.value,draftBeforeDisconnect);
 networkError=true;await assert.rejects(vm.runInContext('api("/api/source/state")',sandbox),/草稿保留/);networkError=false;assert.equal(dom['console-connection'].hidden,false);eventSources[1].onopen();await tick();assert.equal(dom['console-connection'].hidden,true);assert.equal(eventSources.length,2,'healthy SSE stream was unnecessarily replaced');
 console.log('PASS source state: original checks plus list/draft/rollback workflows, disconnected write guard, friendly errors, SSE reconnect, CSRF refresh and existing recovery gate retained');
})().catch(e=>{console.error(e);process.exitCode=1;});
