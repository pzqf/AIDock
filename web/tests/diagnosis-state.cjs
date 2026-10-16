'use strict';
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element{constructor(){this.children=[];this.events={};this.hidden=false;this.disabled=false;}append(...v){this.children.push(...v);}replaceChildren(...v){this.children=v;}addEventListener(k,f){this.events[k]=f;}showModal(){this.hidden=false;}close(){this.hidden=true;}}
const html=fs.readFileSync(path.join(__dirname,'../assets/source.html'),'utf8'),dom=Object.fromEntries([...html.matchAll(/id="([^"]+)"/g)].map(m=>[m[1],new Element()])),sandbox={document:{getElementById:id=>dom[id],createElement:()=>new Element(),querySelectorAll:()=>[]}};
vm.createContext(sandbox);vm.runInContext(fs.readFileSync(path.join(__dirname,'../assets/diagnosis-view.js'),'utf8')+'\nthis.view=DiagnosisView;',sandbox);
const view=sandbox.view,calls=[],waiting=[],navigation=[];let busy=false;
view.init({busy:()=>busy,setBusy:v=>{busy=v;view.sync();},navigate:v=>navigation.push(v),api:async(route,body)=>{calls.push({route,body});return new Promise((resolve,reject)=>waiting.push({resolve,reject}));}});
const report={status:'ACTION_REQUIRED',root:'/dedicated/.aidock',scope_id:'server',copy:'one',observed:'2026-10-16T00:00:00Z',registered_copies:5,boundary:'sequential observation',sections:[{id:'source',status:'FAIL',summary:'<source text>',findings:[{status:'FAIL',code:'PACKAGE_CHANGED',detail:'preserve bytes'}]}],tools:[{tool:'codex-cli',target:'/dedicated/.agents/skills',installed:true,files:'NOT_APPLIED',discovery:'UNKNOWN',session:'UNKNOWN',capability:'UNKNOWN',rules:'UNKNOWN',evidence:{id:'old',created:'2026-10-15T00:00:00Z',stale:true,stale_reason:'source changed',L1:'PASS',L2:'PASS',L3:'UNKNOWN',L4:'UNKNOWN'},findings:[]}],local_origins:[{path:'/skills/id',previous_layer:'project'}],layers:[{id:'project',name:'共享'},{id:'local',name:'本机'}],recoveries:[{kind:'replica',id:'pending-publication'}]};
const text=n=>[n.textContent||'',...n.children.map(text)].join(' ');
(async()=>{
 let task=dom['nav-diagnosis'].events.click();assert.equal(calls[0].route,'/api/diagnosis');assert.equal(calls[0].body,undefined);assert(dom['diagnosis-refresh'].disabled);
 waiting.shift().resolve({items:report});await task;
 assert(!dom['diagnosis-result'].hidden);assert.match(text(dom['diagnosis-tools']),/待安装.*未验证.*历史检查已过期.*source changed.*L2 已核对/s);assert.match(text(dom['diagnosis-origins']),/本机层覆盖 project/);assert.match(text(dom['diagnosis-recoveries']),/pending-publication/);
 const findButton=(n,label)=>n.children.find(c=>c.textContent===label)||n.children.map(c=>findButton(c,label)).find(Boolean);
 findButton(dom['diagnosis-recoveries'],'查看正式同步恢复').events.click();assert.equal(navigation[0],'replica');assert(dom['diagnosis-dialog'].hidden);
 task=dom['nav-diagnosis'].events.click();waiting.shift().resolve({items:{...report,registered_copies:null}});await task;assert.match(dom['diagnosis-time'].textContent,/副本数量暂不可读取/);assert.doesNotMatch(dom['diagnosis-time'].textContent,/0 个已登记副本/);
 task=dom['nav-diagnosis'].events.click();view.invalidate();waiting.shift().resolve({items:report});await task;assert(dom['diagnosis-result'].hidden);
 task=dom['diagnosis-refresh'].events.click();waiting.shift().reject(Error('read unavailable'));await task;assert(dom['diagnosis-result'].hidden);assert.equal(dom['diagnosis-message'].textContent,'read unavailable');assert(!busy);
 console.log('PASS project diagnosis: read-only route, current vs stale evidence, local provenance, recovery navigation, invalidation and failed refresh');
})().catch(e=>{console.error(e);process.exitCode=1;});
