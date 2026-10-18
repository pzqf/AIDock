'use strict';
const {readFileSync}=require('node:fs');
const {join}=require('node:path');
const vm=require('node:vm');
const assert=require('node:assert/strict');
class Element {
 constructor(id='',tag='div'){this.id=id;this.tag=tag;this.children=[];this.events={};this.dataset={};this.value='';this.hidden=false;this.disabled=false;this.textContent='';this.classList={toggle(){}};}
 append(...nodes){this.children.push(...nodes);}
 replaceChildren(...nodes){this.children=nodes;}
 addEventListener(event,fn){this.events[event]=fn;}
 showModal(){this.open=true;} close(){this.open=false;}
 reset(){this.resets=(this.resets||0)+1;}
 querySelectorAll(){return this.children.flatMap(n=>[n,...n.querySelectorAll()]);}
}
const html=readFileSync(join(__dirname,'../assets/source.html'),'utf8');
const ids=[...html.matchAll(/id="([^"]+)"/g)].map(x=>x[1]);
const dom=Object.fromEntries(ids.map(id=>[id,new Element(id)]));
dom['projects-mode'].value='create';dom['projects-root'].value='D:\\tests\\project-a';dom['projects-name'].value='项目 A';
const requests=[],navigations=[];
let busy=false,dirty=false,applyFailure=false,listFailure=false,referenceFailure=false,archivedItems=[];
const first={id:'a'.repeat(64),name:'项目 A',root:'test-a/.aidock',scope_id:'workspace',status:'READY'};
const second={...first,id:'b'.repeat(64),name:'项目 B',root:'test-b/.aidock'};
const plan={id:'plan-a',digest:'digest-a',binding:first,request:{create:true},batch:{bound_roots:[{path:'source',root:first.root},{path:'registry',root:'local/registry'}]}};
let history=[],catalog=[first,second];
const sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element('',tag)},location:{assign:url=>navigations.push(url)},encodeURIComponent};
vm.createContext(sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/projects-view.js'),'utf8')+'\nthis.ProjectsView=ProjectsView;',sandbox);
const ctx={binding:'',hasDrafts:()=>dirty,busy:()=>busy,setBusy:v=>{busy=v;sandbox.ProjectsView.sync();},api:async(path,body)=>{
 requests.push({path,body});
 if(path==='/api/projects/list'){if(listFailure)throw new Error('pending registry');return {items:catalog};}
 if(path==='/api/projects/history')return {items:history};
 if(path==='/api/projects/archived')return {items:archivedItems};
 if(path==='/api/projects/plan')return {items:plan};
 if(path==='/api/projects/remove'){
   if(referenceFailure){const e=new Error('BINDING_REFERENCED');e.bindingReferences=[{kind:'source',id:'snapshot-1',reason:'恢复快照仍引用'}];throw e;}
   return {items:{...plan,kind:'project-removal',request:{create:false}}};
 }
 if(path==='/api/projects/archive'||path==='/api/projects/restore')return {items:{...plan,kind:path.endsWith('/restore')?'project-restore':'project-archive',request:{create:false}}};
 if(path==='/api/projects/apply'){if(applyFailure)throw new Error('PLAN_STALE');return {items:{state:'committed'}};}
 if(path==='/api/projects/recover')return {items:{state:'recovered'}};
 throw new Error(path);
}};
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const click=id=>dom[id].events.click();
const submit=()=>dom['projects-form'].events.submit({preventDefault(){}});
const buttons=element=>element.querySelectorAll().filter(n=>n.tag==='button');
const openButtons=element=>buttons(element).filter(n=>n.dataset.current !== undefined);
const app={project_workspace:true};
(async()=>{
 await sandbox.ProjectsView.init(ctx,app);
 assert.equal(dom['project-home'].hidden,false);assert.equal(dom['source-page-view'].hidden,true);
 assert.equal(requests.filter(x=>x.path.includes('/source/')).length,0);
 assert.equal(dom['projects-home-list'].children.length,2);
 submit();await tick();assert.equal(dom['projects-plan'].hidden,false);
 assert.equal(requests.filter(x=>x.path.endsWith('/apply')).length,0);
 click('projects-cancel');assert.equal(dom['projects-plan'].hidden,true);assert.equal(dom['projects-confirm'].disabled,true);
 submit();await tick();dom['projects-form'].events.input();click('projects-confirm');await tick();
 assert.equal(requests.filter(x=>x.path.endsWith('/apply')).length,0,'changed preview was applied');
 submit();await tick();click('projects-confirm');await tick();
 assert.equal(requests.filter(x=>x.path.endsWith('/apply')).length,1);assert.equal(dom['projects-form'].resets,1);
 assert.equal(navigations.length,0,'registration silently navigated away');
 openButtons(dom['projects-home-list'])[1].events.click();assert.equal(navigations[0],'/source.html?binding='+second.id);
 dirty=true;openButtons(dom['projects-home-list'])[0].events.click();
 assert.equal(navigations.length,1);assert.equal(dom['projects-draft-dialog'].open,true);
 assert.equal(dom['projects-draft-open'].href,'/source.html?binding='+first.id);
 click('projects-draft-stay');assert.equal(dom['projects-draft-dialog'].hidden,true);assert.equal(dirty,true);
 busy=true;openButtons(dom['projects-home-list'])[0].events.click();assert.equal(navigations.length,1);busy=false;
 submit();await tick();applyFailure=true;click('projects-confirm');await tick();
 assert.match(dom['projects-message'].textContent,/PLAN_STALE/);assert.equal(dom['projects-confirm'].disabled,true);
 const before=requests.length;click('projects-confirm');await tick();assert.equal(requests.length,before,'failed preview reused');
 listFailure=true;history=[{plan,state:'prepared',pending:true}];click('projects-refresh');await tick();
 const recover=buttons(dom['projects-history'])[0];assert.equal(recover.textContent,'预览恢复范围');recover.events.click();
 assert.equal(dom['projects-plan'].hidden,false);assert.match(dom['projects-plan-title'].textContent,/恢复中断/);
 click('projects-cancel');assert.equal(requests.filter(x=>x.path.endsWith('/recover')).length,0);
 recover.events.click();click('projects-confirm');await tick();
 assert.equal(requests.filter(x=>x.path.endsWith('/recover')).length,1);
 assert.deepEqual(JSON.parse(JSON.stringify(requests.find(x=>x.path.endsWith('/recover')).body)),{plan_id:'plan-a'});
 listFailure=false;applyFailure=false;history=[];ctx.binding=first.id;app.project_workspace=false;dirty=true;
 const removeButton=()=>buttons(dom['projects-home-list']).find(n=>n.textContent==='移除登记');
 let removalCalls=requests.filter(x=>x.path.endsWith('/remove')).length;
 removeButton().events.click();await tick();assert.equal(requests.filter(x=>x.path.endsWith('/remove')).length,removalCalls,'dirty current project removed');
 dirty=false;removeButton().events.click();await tick();assert.equal(dom['projects-confirm'].textContent,'确认移除登记');
 const applies=requests.filter(x=>x.path.endsWith('/apply')).length;
 click('projects-cancel');assert.equal(requests.filter(x=>x.path.endsWith('/apply')).length,applies);
 referenceFailure=true;removeButton().events.click();await tick();assert.match(dom['projects-plan-title'].textContent,/恢复快照/);assert.equal(dom['projects-confirm'].disabled,true);
 assert.match(dom['projects-plan-scope'].children[0].textContent,/snapshot-1/);
 referenceFailure=false;removeButton().events.click();await tick();dirty=true;click('projects-confirm');await tick();
 assert.equal(requests.filter(x=>x.path.endsWith('/apply')).length,applies,'draft created after preview discarded');
 dirty=false;removeButton().events.click();await tick();click('projects-confirm');await tick();
 assert.equal(navigations.at(-1),'/source.html');assert.match(dom['projects-message'].textContent,/文件保留/);
 const archiveButton=()=>buttons(dom['projects-home-list']).find(n=>n.textContent==='归档');
 dirty=true;const archives=requests.filter(x=>x.path.endsWith('/archive')).length;
 archiveButton().events.click();await tick();assert.equal(requests.filter(x=>x.path.endsWith('/archive')).length,archives);
 dirty=false;archiveButton().events.click();await tick();assert.equal(dom['projects-confirm'].textContent,'确认归档');
 click('projects-cancel');archiveButton().events.click();await tick();click('projects-confirm');await tick();
 assert.match(dom['projects-message'].textContent,/已归档/);assert.equal(navigations.at(-1),'/source.html');
 archivedItems=[{binding:{...first,status:'ARCHIVED'},references:[{id:'snapshot-1'}]}];app.project_workspace=true;ctx.binding=first.id;click('projects-refresh');await tick();
 assert.equal(dom['projects-home-archived'].children.length,1);assert.match(dom['projects-home-archived'].children[0].children[2].textContent,/1 个恢复引用/);
 buttons(dom['projects-home-archived'])[0].events.click();await tick();assert.equal(dom['projects-confirm'].textContent,'确认恢复登记');
 const restoresBefore=requests.filter(x=>x.path.endsWith('/apply')).length;click('projects-cancel');assert.equal(requests.filter(x=>x.path.endsWith('/apply')).length,restoresBefore);
 buttons(dom['projects-home-archived'])[0].events.click();await tick();click('projects-confirm');await tick();assert.match(dom['projects-message'].textContent,/已恢复.*登记/);
 assert.equal(openButtons(dom['projects-home-list'])[0].textContent,'打开项目','restored stale link was marked as already open');
 openButtons(dom['projects-home-list'])[0].events.click();assert.equal(navigations.at(-1),'/source.html?binding='+first.id);
 catalog=['workspace','server','client'].map((scope,i)=>({...first,id:String(i+1).repeat(64),name:'同名项目 <literal>',scope_id:scope,root:'copy/'+scope+'/.aidock'}));click('projects-refresh');await tick();const shortcuts=buttons(dom['project-shortcuts']);assert.deepEqual(shortcuts.map(n=>n.textContent),catalog.map(i=>i.name+' · '+i.scope_id));assert.equal(new Set(shortcuts.map(n=>n.textContent)).size,3);shortcuts[2].events.click();assert.equal(navigations.at(-1),'/source.html?binding='+catalog[2].id);assert.match(shortcuts[2].title,/client.*copy\/client/);
 const routeBefore=navigations.length;dirty=true;sandbox.ProjectsView.openBinding(catalog[0].id);assert.equal(navigations.length,routeBefore);assert.equal(dom['projects-draft-open'].href,'/source.html?binding='+catalog[0].id);click('projects-draft-stay');dirty=false;sandbox.ProjectsView.openBinding('missing');assert.equal(navigations.length,routeBefore);sandbox.ProjectsView.openBinding(catalog[1].id);assert.equal(navigations.at(-1),'/source.html?binding='+catalog[1].id);
 console.log('PASS projects: preview/cancel/stale/save, workspace home, navigation, dirty draft guard, busy isolation and recovery with unavailable catalog');
})().catch(e=>{console.error(e);process.exitCode=1;});
