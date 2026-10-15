'use strict';
const {readFileSync}=require('node:fs'),{join}=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element {
  constructor(id='',tag='div'){Object.assign(this,{id,tag,children:[],events:{},value:'',hidden:false,disabled:false,textContent:'',checked:false,classList:{toggle(){}}});}
  append(...nodes){this.children.push(...nodes);} replaceChildren(...nodes){this.children=nodes;}
  addEventListener(name,fn){this.events[name]=fn;} setAttribute(name,value){this[name]=value;}
  showModal(){this.open=true;} close(){this.open=false;}
  querySelectorAll(){return this.children.flatMap(n=>[n,...n.querySelectorAll()]);}
}
const clone=x=>JSON.parse(JSON.stringify(x));
const html=readFileSync(join(__dirname,'../assets/source.html'),'utf8');
const dom=Object.fromEntries([...html.matchAll(/id="([^"]+)"/g)].map(x=>[x[1],new Element(x[1])]));
function fixture(project) {
  const skills=Array.from({length:111},(_,i)=>({layer:i<100?'owned':i<110?'inherited':'external',skill:{id:`${project}-${i}`,native_name:`${project}-guide-${i}`,display_name:i<2?'同名代码审查':`技能 ${i}`,description:i<10?'批量 验收':'其它说明',revision:'sha256:'+(i<10?'b':'a').repeat(64)}}));
  return {sha256:project,rules_sha256:project+'-rules',skills,rule_files:[{id:'rule',name:'保留规则',enabled:true,body:'原规则\r\n'}],profile:{schema_version:2,name:project,skills:{enable:skills.map(x=>x.skill.id),disable:[],pins:Object.fromEntries(skills.map(x=>[x.skill.id,x.skill.revision]))},config_overrides:{keep:{value:null}}},report:{effective:{name:project}},layers:{local_available:true,local_sha256:'local',digest:'layers',history:[]},checks:[]};
}
const projects=[fixture('A'),fixture('B')];let current=projects[0],busy=false,pending=false,stale=false,saved=0;
const requests=[],sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element('',tag)}};
vm.createContext(sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/profile-view.js'),'utf8')+'\nthis.view=ProfileView;',sandbox);
const ctx={busy:()=>busy,pending:()=>pending,setBusy:v=>{busy=v;sandbox.view.sync();},message(){},refresh:async()=>{},api:async(path,body)=>{
  requests.push({path,body:clone(body||{})});
  if(path==='/api/profile/state')return {items:clone(current)};
  if(path==='/api/profile/plan'){if(stale)throw Error('PLAN_STALE');return {items:{plan:{id:'plan',digest:'digest',changes:[{path:'profile.yaml'}]}}};}
  if(path==='/api/source/apply'){const req=requests.findLast(x=>x.path==='/api/profile/plan').body;current.profile=clone(req.profile);current.rule_files=clone(req.rule_files);current.sha256='saved-'+ ++saved;return {items:{state:'committed'}};}
  if(path==='/api/profile/apply'){const req=requests.findLast(x=>x.path==='/api/profile/plan').body;assert.equal(body.target,'local');current.layers.local=clone(req.profile);current.layers.local_sha256='local-'+ ++saved;return {items:{state:'committed'}};}
  throw Error(path);
}};
const click=id=>dom[id].events.click(),tick=()=>new Promise(resolve=>setImmediate(resolve));
const field=(id,value,event='change')=>{dom[id].value=value;dom[id].events[event]();};
const search=text=>field('profile-skill-search',text,'input');
const rows=()=>dom['profile-skills'].children.filter(x=>x['data-skill-id']);
const ids=()=>rows().map(x=>x['data-skill-id']);
const preview=async()=>{dom['profile-form'].events.submit({preventDefault(){}});await tick();return requests.findLast(x=>x.path==='/api/profile/plan').body;};
const changes=()=>dom['profile-changes'].children.map(x=>x.textContent).join('\n');
(async()=>{
  sandbox.view.init(ctx);click('nav-profile');await tick();
  assert.equal(rows().length,111);assert.equal(rows().filter(x=>x.children[1].children[0].textContent==='同名代码审查').length,2);
  search('代码审查');assert.deepEqual(ids(),['A-0','A-1']);
  search('A-109');assert.deepEqual(ids(),['A-109']);
  search('sha256:'+'b'.repeat(64));assert.equal(rows().length,10);
  search('批量 验收');assert.equal(rows().length,10);
  field('profile-skill-source','inherited');assert.equal(rows().length,0);assert.equal(dom['profile-filtered-off'].disabled,true);
  const count=requests.length;click('profile-filtered-off');assert.equal(sandbox.view.hasDraft(),false);assert.equal(requests.length,count);
  click('profile-skill-clear');field('profile-skill-source','owned');assert.equal(rows().length,100);assert.equal(sandbox.view.hasDraft(),false);
  search('批量 验收');const original=clone(current),other=clone(projects[1]);
  click('profile-filtered-off');assert.equal(rows().length,10);assert.match(dom['profile-message'].textContent,/筛选的 10 项/);
  let req=await preview();assert.deepEqual(req.profile.skills.disable,Array.from({length:10},(_,i)=>`A-${i}`).sort());
  assert.equal(req.profile.skills.enable.length,101);assert.deepEqual(req.profile.skills.pins,original.profile.skills.pins);
  assert.deepEqual(req.rule_files,original.rule_files);assert.deepEqual(req.profile.config_overrides,original.profile.config_overrides);
  assert.match(changes(),/A-guide-0：明确启用 → 明确停用/);assert(!changes().includes('A-guide-10：'));
  assert.deepEqual(current,original,'preview wrote saved source');assert.deepEqual(projects[1],other);
  // Searching is view-only: it preserves the preview and unrelated rule input.
  const rule=dom['profile-rule-files'].querySelectorAll().find(x=>x['aria-label']==='规则文件正文 1');
  search('A-0');assert.equal(dom['profile-plan'].hidden,false);assert.equal(dom['profile-rule-files'].querySelectorAll().find(x=>x['aria-label']==='规则文件正文 1'),rule);
  click('profile-cancel');click('profile-close');click('nav-profile');await tick();assert.equal(sandbox.view.hasDraft(),true);assert.equal(rows().length,1);
  stale=true;await preview();assert.match(dom['profile-message'].textContent,/PLAN_STALE/);assert.equal(sandbox.view.hasDraft(),true);assert.deepEqual(current,original);stale=false;
  await preview();click('profile-confirm');await tick();assert.equal(sandbox.view.hasDraft(),false);assert.deepEqual(projects[1],other);
  assert.equal(current.profile.skills.disable.length,10);
  click('profile-skill-clear');field('profile-skill-choice','disable');assert.equal(rows().length,10);
  click('profile-filtered-inherit');assert.equal(rows().length,0);assert.equal(dom['profile-filtered-inherit'].disabled,true);
  req=await preview();assert.deepEqual(req.profile.skills.disable,[]);assert.equal(req.profile.skills.enable.length,101);
  assert.deepEqual(req.profile.skills.pins,original.profile.skills.pins);assert.match(changes(),/明确停用 → 继承上层/);
  click('profile-cancel');click('profile-reload');click('profile-reload');await tick();
  // Existing all-skills actions retain their explicit meaning under a filter.
  click('profile-skill-clear');search('A-0');click('profile-all-off');req=await preview();assert.equal(req.profile.skills.disable.length,111);assert.deepEqual(req.profile.skills.enable,[]);
  click('profile-reload');click('profile-reload');await tick();click('profile-skill-clear');
  // A filtered local edit cannot alter shared switches, pins or the other project.
  field('profile-layer','local');field('profile-skill-source','inherited');assert.equal(rows().length,10);
  click('profile-filtered-off');req=await preview();assert.equal(req.target,'local');assert.equal(req.profile.skills.disable.length,10);assert.equal(req.rule_files,undefined);
  assert.match(changes(),/继承上层 → 明确停用/);const shared=clone(current.profile);click('profile-confirm');await tick();assert.deepEqual(current.profile,shared);assert.deepEqual(projects[1],other);
  click('profile-filtered-inherit');req=await preview();assert.deepEqual(req.profile.skills.disable,[]);assert.match(changes(),/明确停用 → 继承上层/);
  click('profile-reload');click('profile-reload');await tick();
  field('profile-layer','shared');click('profile-skill-clear');search('A-1');
  busy=true;const calls=requests.length;click('profile-filtered-off');assert.equal(sandbox.view.hasDraft(),false);assert.equal(requests.length,calls);busy=false;sandbox.view.sync();
  // A second project resolves its own IDs and retains both same-name assets.
  current=projects[1];click('profile-close');click('nav-profile');await tick();assert.equal(rows().length,0);
  click('profile-skill-clear');search('代码审查');assert.deepEqual(ids(),['B-0','B-1']);click('profile-filtered-off');req=await preview();
  assert.deepEqual(req.profile.skills.disable,['B-0','B-1']);assert(req.profile.skills.enable.every(id=>id.startsWith('B-')));
  assert.deepEqual(projects[0].profile,shared);assert.equal(requests.some(x=>x.path.includes('/native/')||x.path.includes('/replica/')),false);
  // A single-row edit removes itself from the current choice filter and clears
  // the preview; clearing filters never discards the hidden edits.
  click('profile-cancel');click('profile-skill-clear');field('profile-skill-choice','disable');
  const select=rows()[0].children[0];select.value='inherit';select.events.change();assert.deepEqual(ids(),['B-1']);
  click('profile-skill-clear');req=await preview();assert.deepEqual(req.profile.skills.disable,['B-1']);assert.equal(dom['profile-filtered-off'].textContent,'停用筛选项（111）');
  console.log('PASS Profile skills: 2 projects / 100 owned each, scoped batch and all semantics, search/filters, three-state diffs, pins/rules, local isolation, cancel/stale/busy and draft preservation');
})().catch(e=>{console.error(e);process.exitCode=1;});
