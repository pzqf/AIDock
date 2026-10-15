'use strict';
const {readFileSync}=require('node:fs'),{join}=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element {
 constructor(id='',tag='div'){this.id=id;this.tag=tag;this.children=[];this.events={};this.value='';this.hidden=false;this.disabled=false;this.textContent='';this.checked=false;this.classList={toggle(){}};}
 append(...nodes){this.children.push(...nodes);} replaceChildren(...nodes){this.children=nodes;}
 addEventListener(name,fn){this.events[name]=fn;} setAttribute(name,value){this[name]=value;}
 showModal(){this.open=true;} close(){this.open=false;}
 querySelectorAll(){return this.children.flatMap(n=>[n,...n.querySelectorAll()]);}
}
const html=readFileSync(join(__dirname,'../assets/source.html'),'utf8');
const dom=Object.fromEntries([...html.matchAll(/id="([^"]+)"/g)].map(x=>[x[1],new Element(x[1])]));
const skill=id=>({skill:{id,native_name:'guide-'+id,revision:'sha256:'+'a'.repeat(64)},layer:'owned'});
let current={profile:{schema_version:2,name:'project',skills:{enable:[],disable:['a','b'],pins:{a:'rev-a',b:'rev-b'}},config_overrides:{keep:{nullValue:null}}},sha256:'original',skills:[skill('a'),skill('b')],checks:[],report:{effective:{name:'project'}},boundary:'scope'};
let busy=false,pending=false,stale=false;
const requests=[];
const sandbox={document:{getElementById:id=>dom[id],createElement:tag=>new Element('',tag)}};
vm.createContext(sandbox);vm.runInContext(readFileSync(join(__dirname,'../assets/profile-view.js'),'utf8')+'\nthis.ProfileView=ProfileView;',sandbox);
const clone=x=>JSON.parse(JSON.stringify(x));
const ctx={busy:()=>busy,pending:()=>pending,setBusy:v=>{busy=v;sandbox.ProfileView.sync();},message(){},refresh:async()=>{},api:async(path,body)=>{
 requests.push({path,body:clone(body||{})});
 if(path==='/api/profile/state')return {items:clone(current)};
 if(path==='/api/profile/plan'){if(stale)throw new Error('PLAN_STALE');return {items:{plan:{id:'plan',digest:'digest',changes:[{path:'profile.yaml'}]},preflight:{}}};}
 if(path==='/api/source/apply'){current={...current,profile:requests.findLast(x=>x.path==='/api/profile/plan').body.profile,sha256:'saved'};return {items:{state:'committed'}};}
 throw new Error(path);
}};
const click=id=>dom[id].events.click();
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const submit=()=>dom['profile-form'].events.submit({preventDefault(){}});
(async()=>{
 sandbox.ProfileView.init(ctx);click('nav-profile');await tick();
 assert.equal(dom['profile-dialog'].open,true);assert.equal(sandbox.ProfileView.hasDraft(),false);
 click('profile-all-on');assert.equal(sandbox.ProfileView.hasDraft(),true);
 submit();await tick();let req=requests.findLast(x=>x.path==='/api/profile/plan');
 assert.deepEqual(req.body.profile.skills.enable,['a','b']);assert.equal(req.body.expected_sha256,'original');assert.deepEqual(req.body.profile.config_overrides,{keep:{nullValue:null}});
 assert.equal(requests.some(x=>x.path==='/api/source/apply'),false);
 click('profile-cancel');assert.equal(sandbox.ProfileView.hasDraft(),true);assert.equal(dom['profile-confirm'].disabled,true);
 click('profile-close');assert.equal(dom['profile-dialog'].hidden,true);assert.equal(sandbox.ProfileView.hasDraft(),true);
 click('nav-profile');await tick();assert.equal(sandbox.ProfileView.hasDraft(),true,'opening discarded draft');
 stale=true;submit();await tick();assert.match(dom['profile-message'].textContent,/PLAN_STALE/);assert.equal(sandbox.ProfileView.hasDraft(),true);stale=false;
 click('profile-add-rule');const textarea=dom['profile-rules'].querySelectorAll().find(x=>x.tag==='textarea');textarea.value='line one\nline two';textarea.events.input();
 submit();await tick();assert.deepEqual(requests.findLast(x=>x.path==='/api/profile/plan').body.profile.rules,['line one\nline two']);
 const calls=requests.length;busy=true;click('profile-confirm');await tick();assert.equal(requests.length,calls);busy=false;
 click('profile-confirm');await tick();assert.equal(sandbox.ProfileView.hasDraft(),false);assert.equal(current.profile.skills.enable.length,2);
 assert.match(dom['profile-message'].textContent,/共用一个回退点/);
 // Explicit native rule import is a source-only draft and carries the exact
 // observed file digest plus the selected merge mode to the source preview.
 await sandbox.ProfileView.importRules({tool:'codex-cli',sha256:'b'.repeat(64),body:'imported one\n\nimported two'});
 assert.equal(dom['profile-import-origin'].hidden,false);assert.equal(sandbox.ProfileView.hasDraft(),true);
 dom['profile-import-mode'].value='append';dom['profile-import-mode'].events.change();submit();await tick();
 const imported=requests.findLast(x=>x.path==='/api/profile/plan');assert.equal(imported.body.native_rule.tool,'codex-cli');assert.equal(imported.body.native_rule.sha256,'b'.repeat(64));assert.equal(imported.body.native_rule.mode,'append');assert.match(imported.body.native_rule.body,/imported two/);
 assert.deepEqual(imported.body.native_rule.merged_rules, imported.body.profile.rules);
 assert.equal(requests.some(x=>x.path==='/api/native/apply'),false);
 click('profile-all-off');click('profile-reload');assert.equal(sandbox.ProfileView.hasDraft(),true);assert.match(dom['profile-reload'].textContent,/确认放弃/);
 click('profile-reload');await tick();assert.equal(sandbox.ProfileView.hasDraft(),false);
 pending=true;click('profile-all-off');let plans=requests.filter(x=>x.path==='/api/profile/plan').length;submit();await tick();assert.equal(requests.filter(x=>x.path==='/api/profile/plan').length,plans);
 console.log('PASS Profile: batch preview/cancel/confirm, preserved full fields and multiline rules, drafts across close/reopen, stale preservation, busy/pending guards and explicit discard');
})().catch(e=>{console.error(e);process.exitCode=1;});
