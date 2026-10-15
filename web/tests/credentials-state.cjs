'use strict';
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element{
 constructor(tag='div'){this.tag=tag;this.children=[];this.events={};this.value='';this.checked=false;this.disabled=false;this.hidden=false;this.classList={toggle(){}};this.type='text';}
 append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}
 addEventListener(name,fn){this.events[name]=fn;}querySelectorAll(){return this.children.flatMap(n=>[n,...n.querySelectorAll()]);}showModal(){this.open=true;}close(){this.open=false;}click(){return this.events.click?.();}
}
const html=fs.readFileSync(path.join(__dirname,'../assets/source.html'),'utf8');
const dom=Object.fromEntries([...html.matchAll(/id="([^"]+)"/g)].map(x=>[x[1],new Element()]));dom['credential-action'].value='add';
let current={vault:{available:true,provider:'Windows Credential Manager',entries:[],history:[],pending:[],boundary:'OS only'},references:[{name:'missing_key',plugin:'tool',project:'p1',copy:'copy1',present:false,status:'registered'}],unknown:[],review:'review-1',boundary:'known only'};
let busy=false,error='',writes=0;const requests=[],page={};
const sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element(tag)},window:{addEventListener:(name,fn)=>page[name]=fn}};
vm.createContext(sandbox);vm.runInContext(fs.readFileSync(path.join(__dirname,'../assets/credentials-view.js'),'utf8')+'\nthis.view=CredentialsView;',sandbox);
const clone=v=>JSON.parse(JSON.stringify(v)),tick=()=>new Promise(r=>setImmediate(r)),click=id=>dom[id].click();
const text=id=>dom[id].querySelectorAll().map(n=>n.textContent||'').join('\n');
const ctx={busy:()=>busy,setBusy:v=>{busy=v;sandbox.view.sync();},message(){},api:async(url,body)=>{
 requests.push({url,body:clone(body||{})});if(error)throw new Error(error);
 if(url.endsWith('/change')){writes++;assert.equal(body.confirm,true);assert(!Object.hasOwn(body,'command'));if(body.action==='add')current.vault.entries=[{name:body.name,revision:'revision-1',updated:'now'}];if(body.action==='rotate'){assert.equal(body.expected_revision,'revision-1');current.vault.entries[0].revision='revision-2';}if(body.action==='delete'){assert.equal(body.expected_references,'review-1');assert.equal(body.references_reviewed,true);current.vault.entries=[];}return{items:{}};}
 if(url.endsWith('/reconcile')){current.vault.pending=[];return{items:{}};}return{items:clone(current)};
}};
const input=(id,value)=>{dom[id].value=value;dom[id].events.input?.();},confirm=()=>{dom['credential-confirm'].checked=true;dom['credential-confirm'].events.change();};
(async()=>{
 sandbox.view.init(ctx);click('nav-credentials');await tick();assert(dom['credentials-dialog'].open);assert.match(text('credential-missing'),/missing_key/);assert(dom['credential-save'].disabled);
 input('credential-name','key');input('credential-value','fake-local-input');assert.equal(dom['credential-value'].type,'password');dom['credential-visible'].checked=true;dom['credential-visible'].events.change();assert.equal(dom['credential-value'].type,'text');
 confirm();input('credential-value','changed-fake-input');assert(!dom['credential-confirm'].checked);click('credential-save');await tick();assert.equal(writes,0);
 confirm();error='CREDENTIAL_STALE';click('credential-save');await tick();assert.equal(dom['credential-value'].value,'changed-fake-input');assert.match(dom['credential-message'].textContent,/CREDENTIAL_STALE/);
 error='';click('credential-save');await tick();assert.equal(writes,1);assert.equal(dom['credential-value'].value,'');assert.equal(dom['credential-value'].type,'password');assert(!dom['credential-visible'].checked);
 const row=dom['credential-entries'].children[0];row.children.at(-1).children[0].click();assert.equal(dom['credential-action'].value,'rotate');assert(dom['credential-name'].readOnly);input('credential-value','rotated-fake-input');confirm();click('credential-save');await tick();assert.equal(writes,2);
 current.references=[{name:'key',plugin:'<script>text only</script>',project:'p1',copy:'copy1',present:true,status:'registered'}];click('credential-reload');await tick();dom['credential-entries'].children[0].children.at(-1).children[1].click();assert(dom['credential-value-field'].hidden);assert.match(text('credential-impact'),/<script>text only/);assert.match(text('credential-impact'),/无法从 AIDock 回退/);confirm();click('credential-save');await tick();assert.equal(writes,3);
 current.vault.pending=[{id:'pending'}];click('credential-reload');await tick();assert(!dom['credential-reconcile'].hidden);assert(dom['credential-save'].disabled);click('credential-reconcile');await tick();assert(dom['credential-reconcile'].hidden);
 error='OFFLINE';click('credential-reload');await tick();assert(dom['credential-save'].disabled);assert.equal(dom['credential-entries'].children.length,0);error='';click('credential-reload');await tick();
 input('credential-value','close-clears-input');click('credentials-close');assert.equal(dom['credential-value'].value,'');assert(!dom['credentials-dialog'].open);
 busy=true;const n=requests.length;click('nav-credentials');await tick();assert.equal(requests.length,n);busy=false;input('credential-value','pagehide-clears');page.pagehide();assert.equal(dom['credential-value'].value,'');
 assert(requests.every(r=>r.url.startsWith('/api/credentials/')));console.log('PASS credentials: masking, explicit consent, input changes revoke consent, failed draft retained, successful value cleared, revision-bound rotate/delete, reference warning, pending reconciliation, stale read disabled, busy and pagehide cleanup');
})().catch(e=>{console.error(e);process.exitCode=1;});
