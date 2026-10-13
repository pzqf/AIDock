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
 get firstChild(){return this.children[0]||{textContent:''};}
}
const ids=[...readFileSync(join(__dirname,'../assets/source.html'),'utf8').matchAll(/id="([^"]+)"/g)].map(x=>x[1]);
const dom=Object.fromEntries(ids.map(id=>[id,new Element(id,id==='package-file'?'select':'div')]));
const state={root:'synthetic/.aidock',index:{owner:'project-A',scope_id:'server',skills:[{id:'skill-A',native_name:'guide',revision:'hash',files:[{path:'SKILL.md'},{path:'binary.bin'}]}]},profile:{skills:{}},history:[],recovery_required:false};
const pending=[];let planCounter=0,applyCount=0,lastRequest=null,planError=null,applyError=null;
const impact={id:'skill-A',owner:'project-A',scope_id:'server',reference_scope:'CURRENT_BOUND_SOURCE',native_before:'guide',native_after:'guide',references:[{kind:'dependency',from_name:'caller',path:'skills.yaml/caller/dependencies.skills/0',blocks_retirement:true}],unknown:['其他项目和离线范围未登记']};
const response=items=>({ok:true,json:async()=>({ok:true,items})});
const sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element('',tag),querySelectorAll:()=>Object.values(dom)},location:{hash:'#token=synthetic',pathname:'/source.html'},history:{replaceState(){}},URLSearchParams,EventSource:class{close(){}},fetch:async(path,options={})=>{
 if(path==='/api/session')return {ok:true,json:async()=>({csrf:'csrf'})};
 if(path==='/api/state')return {ok:true,json:async()=>({version:'dev'})};
 if(path==='/api/source/state')return response(state);
 if(path.startsWith('/api/source/file'))return new Promise(resolve=>pending.push({path,resolve}));
 if(path.startsWith('/api/source/references'))return response(impact);
 if(path==='/api/source/plan'){lastRequest=JSON.parse(options.body);if(planError)return {ok:true,json:async()=>planError};return response({plan:{id:String(++planCounter),digest:'digest',action:lastRequest.action,changes:[]},findings:[],impact:['rename','retire'].includes(lastRequest.action)?impact:null});}
 if(path==='/api/source/apply'){applyCount++;if(applyError)return {ok:true,json:async()=>applyError};return response({state:'committed'});}
 throw new Error(path);
}};
vm.createContext(sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/source.js'),'utf8'),sandbox);
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const submit=()=>dom.editor.events.submit({preventDefault(){}});
(async()=>{
 await tick();assert.match(dom.binding.textContent,/project-A/);
 dom['skill-name'].value='guide';dom.description.value='example';submit();await tick();assert.equal(dom['plan-panel'].hidden,false);
 dom.editor.events.input({target:dom.content});assert.equal(dom['plan-panel'].hidden,true);await dom['apply-plan'].events.click();assert.equal(applyCount,0,'invalidated preview was applied');
 submit();await tick();await dom['apply-plan'].events.click();assert.equal(applyCount,1);assert.equal(dom['skill-name'].value,'','stale editor was kept after save');
 const row=dom['managed-skills'].children[0];row.children.find(x=>x.textContent==='编辑包').events.click();const old=pending.shift();
 dom['package-file'].value='binary.bin';dom['package-file'].events.change();const latest=pending.shift();
 latest.resolve(response({type:'binary',content:'',bytes:3,sha256:'binary'}));await tick();old.resolve(response({type:'text',content:'late response',bytes:1,sha256:'old'}));await tick();
 assert.equal(dom.content.value,'');assert.equal(dom.content.disabled,true);assert.equal(dom.preview.disabled,true);
 row.children.find(x=>x.textContent==='启用').events.click();await tick();assert.equal(dom.preview.disabled,true,'busy reset enabled binary editor');
 dom['package-file'].value='SKILL.md';dom['package-file'].events.change();pending.shift().resolve(response({type:'text',content:'original',bytes:8,sha256:'doc'}));await tick();
 dom.content.value='draft';dom.editor.events.input({target:dom.content});
 dom['package-file'].value='binary.bin';dom['package-file'].events.change();pending.shift().resolve(response({type:'binary',content:'',bytes:3,sha256:'bin'}));await tick();
 dom['package-file'].value='SKILL.md';dom['package-file'].events.change();pending.shift().resolve(response({type:'text',content:'original',bytes:8,sha256:'doc'}));await tick();assert.equal(dom.content.value,'draft','file switch lost draft');
 submit();await tick();assert.equal(lastRequest.files[0].content,'draft');
 assert.equal(lastRequest.files[0].expected_sha256,'doc');
 const action=label=>dom['managed-skills'].children[0].children.find(x=>x.textContent===label).events.click();
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
 const retiredButtons=dom['managed-skills'].children[0].children.filter(x=>x.tagName==='button').map(x=>x.textContent);assert.deepEqual(retiredButtons,['引用与影响','恢复管理']);action('恢复管理');await tick();assert.equal(lastRequest.action,'reactivate');assert.match(dom.message.textContent,/仍保持本层停用/);
 dom['new'].events.click();assert.equal(dom.content.disabled,false);assert.equal(dom.preview.disabled,false);
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
 const inheritedButtons=dom['managed-skills'].children[0].children.filter(x=>x.tagName==='button').map(x=>x.textContent);
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
 state.recovery_required=true;state.profile=null;await dom.reload.events.click();assert.equal(dom['recovery-panel'].hidden,false);assert.equal(dom.preview.disabled,true);assert.equal(dom.recover.disabled,false);
 console.log('PASS 28 source state checks: prior 18 plus explicit adoption version, inherited actions, read-only submit guard, override base, fork provenance, stale base display, restore selected upper pin, draft protection, offline catalog and global mode');
})().catch(e=>{console.error(e);process.exitCode=1;});
