'use strict';
const ProfileConfiguration=(()=>{
 const node=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;},owns=(o,k)=>Object.prototype.hasOwnProperty.call(o||{},k),pointer=s=>s.replace(/~/g,'~0').replace(/\//g,'~1');
 const clone=v=>v===undefined?null:JSON.parse(JSON.stringify(v));
 function render(root,state,draft,changed,busy){
  root.replaceChildren();const servers=new Map((state.mcp||[]).map(s=>[s.name,s]));
  for(const name of Object.keys(draft.config_overrides||{}))if(!servers.has(name))servers.set(name,{name});
  let shown=0;
  for(const [name,server]of servers){
   const config=server.configuration,fields=config?.fields||[],present=owns(draft.config_overrides,name),prefix='/'+pointer(name),removals=(draft.config_remove||[]).filter(p=>p===prefix||p.startsWith(prefix+'/'));
   if(!fields.length&&!present&&!config&&!removals.length)continue;shown++;
   const card=node('article');card.className='profile-plugin-configuration';card.append(node('h4',name));
   card.append(node('p',config?.status==='PASS'?(config.delivery_observed?'已保存版本有配置交付记录，不证明业务或模型采用。修改后须另行保存、授权并应用。':'已保存字段已校验；授权会话启动时才交付给该插件。'):config?.code||'尚无已核验的配置交付合同；可清除此插件的本层配置，或先修复清单。'));
   const clear=node('button','恢复此插件本层继承');clear.type='button';clear.className='btn ghost';clear.disabled=busy()||!present;clear.ruleBoundary=!present;clear.setAttribute('aria-label','清除本层插件配置 '+name);clear.addEventListener('click',()=>{if(busy()||!present)return;delete draft.config_overrides[name];changed();render(root,state,draft,changed,busy);});card.append(clear);
   if(removals.length){const notice=node('p','本层有 '+removals.length+' 条显式删除声明。被删除的字段需先协调，其他字段仍可编辑。'),remove=node('button','清除此插件的删除声明');remove.type='button';remove.className='btn ghost';remove.addEventListener('click',()=>{if(busy())return;draft.config_remove=draft.config_remove.filter(p=>!removals.includes(p));changed();render(root,state,draft,changed,busy);});card.append(notice,remove);}
   for(const field of fields){
    const row=node('div');row.className='profile-config-field';const label=node('label',field.label||field.name),mode=node('select');
    for(const [key,text]of [['inherit','继承 / 使用默认值'],['value','设置本层值'],...(field.nullable?[['null','明确设为 null']]:[])]){const option=node('option',text);option.value=key;mode.append(option);}
    const values=draft.config_overrides?.[name],explicit=values&&typeof values==='object'&&!Array.isArray(values)&&owns(values,field.name),raw=explicit?values[field.name]:undefined;
    const rawPath=prefix+'/'+pointer(field.name),blocked=removals.some(p=>p===prefix||p===rawPath||p.startsWith(rawPath+'/'));
    mode.value=explicit?(raw===null?'null':'value'):'inherit';mode.setAttribute('aria-label','插件配置来源 '+name+' '+field.name);mode.ruleBoundary=blocked;mode.disabled=busy()||blocked;
    const set=value=>{(draft.config_overrides||={})[name]||={};draft.config_overrides[name][field.name]=value;changed();};
    const fallback=()=>{const inherited=state.report?.effective?.config_overrides?.[name]?.[field.name];if(inherited!==undefined)return clone(inherited);if(field.has_default)return clone(field.default);return ({boolean:false,integer:0,number:0,'multi-select':[]})[field.type]??(field.type==='select'?field.options[0]:'');};
    mode.addEventListener('change',()=>{if(busy()||blocked)return;if(mode.value==='inherit'){if(draft.config_overrides?.[name]&&typeof draft.config_overrides[name]==='object'){delete draft.config_overrides[name][field.name];if(!Object.keys(draft.config_overrides[name]).length)delete draft.config_overrides[name];}changed();}else set(mode.value==='null'?null:fallback());render(root,state,draft,changed,busy);});
    label.append(mode);row.append(label);
    if(mode.value==='value'){
     const input=node(field.type==='select'||field.type==='multi-select'?'select':'input');input.setAttribute('aria-label','插件配置值 '+name+' '+field.name);input.ruleBoundary=blocked;input.disabled=busy()||blocked;
     if(field.type==='select'||field.type==='multi-select'){input.multiple=field.type==='multi-select';for(const value of field.options){const option=node('option',value);option.value=value;option.selected=input.multiple?Array.isArray(raw)&&raw.includes(value):raw===value;input.append(option);}if(!input.multiple)input.value=raw;input.addEventListener('change',()=>{if(!busy())set(input.multiple?[...input.selectedOptions].map(o=>o.value):input.value);});}
     else if(field.type==='boolean'){input.type='checkbox';input.checked=raw===true;input.addEventListener('change',()=>{if(!busy())set(input.checked);});}
     else{input.type=['integer','number'].includes(field.type)?'number':'text';if(input.type==='number')input.step=field.type==='integer'?'1':'any';input.value=raw??'';input.addEventListener('input',()=>{if(busy())return;const value=input.type==='number'&&input.value!==''?Number(input.value):input.value;set(value);});}
     row.append(input);
    }
    const effective=state.report?.effective?.config_overrides?.[name],hasEffective=owns(effective,field.name),saved=hasEffective?effective[field.name]:field.has_default?field.default:undefined,fieldPath='/config_overrides'+prefix+'/'+pointer(field.name),source=(state.report?.trace||[]).filter(e=>e.path===fieldPath||fieldPath.startsWith(e.path+'/')&&['set','replace'].includes(e.action)).at(-1)?.layer,sourceLabel=hasEffective?({local:'本机覆盖',project:'项目共享',defaults:'全局默认'})[source]||source||'已保存配置':'插件默认值声明';
    row.append(node('small',!hasEffective&&config?.omitted_fields?.includes(field.name)?'已保存配置明确删除此字段，不交付插件默认值':sourceLabel+' · '+(saved===undefined?'未设置':JSON.stringify(saved))));
    if(field.description)row.append(node('small',field.description));if(field.required)row.append(node('small','必填；可从上层或插件默认值继承。'));card.append(row);
   }
   if(config?.sha256){const detail=node('details');detail.append(node('summary','已校验的配置摘要'),node('code',config.sha256));card.append(detail);}
   root.append(card);
  }
  if(!shown)root.append(node('p','尚无声明配置交付合同的插件。先在 MCP 定义中导入并核验插件清单；普通密钥仍在密钥管理中单独授权。'));
 }
 return {render};
})();
