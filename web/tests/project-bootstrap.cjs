'use strict';
// Run the actual page scripts against an unavailable binding. It must reach
// project recovery without silently loading the server's default source.
const {readFileSync}=require('node:fs');
const {join}=require('node:path');
const vm=require('node:vm');
const assert=require('node:assert/strict');
class Element {
 constructor(id='',tag='div'){this.id=id;this.tag=tag;this.children=[];this.events={};this.value='';this.hidden=false;this.dataset={};this.classList={toggle(){}};}
 append(...nodes){this.children.push(...nodes);} replaceChildren(...nodes){this.children=nodes;}
 addEventListener(name,fn){this.events[name]=fn;} querySelectorAll(){return this.children.flatMap(n=>[n,...n.querySelectorAll()]);}
 showModal(){this.open=true;} close(){this.open=false;} setAttribute(){}
}
const ids=[...readFileSync(join(__dirname,'../assets/source.html'),'utf8').matchAll(/id="([^"]+)"/g)].map(x=>x[1]);
const dom=Object.fromEntries(ids.map(id=>[id,new Element(id)]));
const requests=[],urls=[];
const sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element('',tag),querySelectorAll:()=>Object.values(dom),addEventListener(){}},location:{hash:'',pathname:'/source.html',search:'?binding=bad-entry'},history:{replaceState(a,b,url){urls.push(url);}},URLSearchParams,encodeURIComponent,EventSource:class{},fetch:async(path,options)=>{
 const binding=options.headers['X-AIDock-Binding']; requests.push({path,binding});
 if(path==='/api/session')return {ok:true,json:async()=>({csrf:'synthetic'})};
 if(path==='/api/state')return {ok:true,json:async()=>binding?{ok:false,code:'PROJECT_NOT_REGISTERED',summary:'missing entry'}:{version:'test',source_root:'server-default-must-not-open',native_root:'server-default'}};
 if(path==='/api/projects/list'||path==='/api/projects/history')return {ok:true,json:async()=>({ok:true,items:[]})};
 throw new Error('Unexpected source request: '+path);
}};
vm.createContext(sandbox);
for(const file of ['projects-view.js','source.js'])vm.runInContext(readFileSync(join(__dirname,'../assets',file),'utf8'),sandbox);
setImmediate(()=>{
 try {
  assert.equal(dom['project-home'].hidden,false);assert.equal(dom['source-page-view'].hidden,true);assert.equal(dom.rightPanel.hidden,true);
  assert.match(dom['projects-home-message'].textContent,/PROJECT_NOT_REGISTERED/);
  assert.equal(urls[0],'/source.html?binding=bad-entry');
  assert.deepEqual(requests.filter(x=>x.path==='/api/state').map(x=>x.binding),['bad-entry',undefined]);
  assert(!requests.some(x=>x.path.startsWith('/api/source/')),'silently opened another project');
  dom['projects-manage'].events.click();assert.equal(dom['projects-dialog'].open,true);
  console.log('PASS project bootstrap: unavailable binding opens project recovery, retains URL and never falls back to source operations');
 } catch(e){console.error(e);process.exitCode=1;}
});
