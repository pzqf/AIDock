'use strict';
// Browser-independent regression for project boundaries and asynchronous scans.
// This test does not claim layout, focus, CSP enforcement or browser acceptance.
const {readFileSync} = require('node:fs');
const {join} = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
class Element {
  constructor(tag='div'){this.tagName=tag;this.children=[];this.dataset={};this.events={};this.value='';this.checked=false;this.classList={toggle(){}};this.hidden=false;this.textContent='';}
  append(...nodes){this.children.push(...nodes);}
  replaceChildren(...nodes){this.children=nodes;}
  setAttribute(){}
  addEventListener(name,fn){this.events[name]=fn;}
  get firstChild(){return this.children[0];}
}
const ids=['version','scopes','copy','message','refresh','mirrors','search','role','skills','sources','count','pass','issues','checked','detail','empty','close'];
const dom=Object.fromEntries(ids.map(id=>[id,new Element()]));
const bindings=[
  {project:'project-A',scope:'server',copy:'one',project_name:'项目 A'},
  {project:'project-A',scope:'server',copy:'two',project_name:'项目 A'},
  {project:'project-B',scope:'server',copy:'one',project_name:'项目 B'},
];
const pending=[];
const sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element(tag)},location:{hash:'#token=one-use',pathname:'/'},history:{replaceState(){}},URLSearchParams,AbortController,Date,Map,Set,EventSource:class{close(){}},fetch:async(path,options={})=>{
  if(path==='/api/session')return {ok:true,json:async()=>({csrf:'csrf'})};
  if(path==='/api/state')return {ok:true,json:async()=>({version:'dev',manifest:{bindings}})};
  if(path.startsWith('/api/inventory'))return new Promise(resolve=>pending.push({path,options,resolve}));
  throw new Error(path);
}};
vm.createContext(sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/app.js'),'utf8'),sandbox);
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const result=name=>({ok:true,json:async()=>({ok:true,items:{checked_at:'2026-10-13T10:00:00+08:00',read_only:true,sources:[],counts:{PASS:1},skills:[{candidate_id:name,name,description:'test',path:'skills/test',scope:'server',copy:'one',role:'owned',files:[],findings:[],layers:{L1:'UNKNOWN',L2:'UNKNOWN',L3:'UNKNOWN',L4:'UNKNOWN'},static_status:'PASS'}]}})});
(async()=>{
  await tick();assert.equal(dom.scopes.children.length,2,'same scope collapsed independent projects');
  assert.equal(new URLSearchParams(pending[0].path.split('?')[1]).get('project'),'project-A');
  pending.shift().resolve(result('A'));await tick();assert.equal(dom.skills.children[0].children[0].children[0].textContent,'A');
  dom.scopes.children[1].events.click();assert.equal(dom.skills.children.length,0,'old project shown while new scan pending');
  assert.equal(dom.copy.children.length,2,'copy choices leaked from another project');
  const older=pending.shift();assert.equal(new URLSearchParams(older.path.split('?')[1]).get('project'),'project-B');
  dom.scopes.children[0].events.click();const latest=pending.shift();assert.equal(older.options.signal.aborted,true,'previous scan was not cancelled');
  latest.resolve(result('latest A'));await tick();older.resolve(result('stale B'));await tick();
  assert.equal(dom.skills.children[0].children[0].children[0].textContent,'latest A','late response overwrote current project');
  dom.search.value='no match';dom.search.events.input();assert.equal(dom.skills.children.length,0);assert.equal(dom.empty.hidden,false);
  console.log('PASS 5 console state checks: independent projects, scoped copies, immediate clearing, cancelled/stale scans, search');
})().catch(err=>{console.error(err);process.exitCode=1;});
