'use strict';
const $ = id => document.getElementById(id);
let csrf = '', manifest = null, report = null, selectedScope = '', selectedProject = '', selection = '', scanGeneration = 0, live = null, scanController = null;
const labels = {PASS:'通过',FAIL:'失败',WARN:'警告',SKIP:'跳过',UNKNOWN:'未验证',STALE:'已过期'};
const roles = {'external-source':'存量原源（声明）',owned:'AIDock 原源（声明）',generated:'生成镜像（声明）',plugin:'插件附带（声明）',unknown:'来源未知'};
function el(tag, text, cls) { const n = document.createElement(tag); if (text !== undefined) n.textContent = text; if (cls) n.className = cls; return n; }
async function api(path, options = {}) {
  const res = await fetch(path,{...options,credentials:'same-origin',headers:{...options.headers,...(options.method === 'POST' ? {'X-AIDock-CSRF':csrf} : {})}});
  if (!res.ok) throw new Error(res.status === 401 ? '会话失效，请从 CLI 重新打开控制台。' : `请求失败（${res.status}）`);
  return res.json();
}
function message(text, failed = false) { $('message').textContent = text; $('message').classList.toggle('error',failed); }
async function initialize() {
  try {
    const fragment = new URLSearchParams(location.hash.slice(1)), token = fragment.get('token');
    history.replaceState(null,'',location.pathname);
    const session = token ? await api('/api/session',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token})}) : await api('/api/session');
    csrf = session.csrf;
    const state = await api('/api/state'); manifest = state.manifest; $('version').textContent = `v${state.version}`;
    const bindings = [...new Map(manifest.bindings.map(b => [`${b.project}/${b.scope}`,b])).values()];
    selectedScope = bindings[0]?.scope || ''; selectedProject = bindings[0]?.project || '';
    for (const binding of bindings) {
      const button = el('button',binding.project_name || binding.scope); button.type = 'button'; button.dataset.scope = binding.scope; button.dataset.project = binding.project;
      button.addEventListener('click',() => { selectedScope = binding.scope; selectedProject = binding.project; selection = ''; setCopies(); updateScopes(); scan(); }); $('scopes').append(button);
    }
    setCopies(); updateScopes();
    live = new EventSource('/api/events');
    await scan();
  } catch (err) { message(err.message,true); }
}
function updateScopes() { for (const b of $('scopes').children) { const active = b.dataset.scope === selectedScope && b.dataset.project === selectedProject; b.classList.toggle('active',active); b.setAttribute('aria-current',active ? 'page' : 'false'); } }
function setCopies() {
  $('copy').replaceChildren(el('option','全部副本')); $('copy').firstChild.value = '';
  for (const copy of [...new Set(manifest.bindings.filter(b=>b.scope === selectedScope && b.project === selectedProject).map(b=>b.copy))]) { const n=el('option',copy); n.value=copy; $('copy').append(n); }
}
async function scan() {
  if (!manifest) return;
  const generation = ++scanGeneration;
  scanController?.abort(); scanController = new AbortController();
  report = null; selection = ''; $('skills').replaceChildren(); $('sources').replaceChildren();
  for (const id of ['count','pass','issues']) $(id).textContent = '—';
  $('checked').textContent = ''; renderDetail();
  $('refresh').disabled = true; message('正在只读盘点指定技能源…');
  const params = new URLSearchParams({project:selectedProject,scope:selectedScope,copy:$('copy').value,include_generated:String($('mirrors').checked)});
  try {
    const result = await api(`/api/inventory?${params}`,{signal:scanController.signal}); if (generation !== scanGeneration) return;
    report = result.items; selection = ''; render();
    message(result.ok ? '盘点完成。未写入配置；生成一致性和工具生效尚未验证。' : '盘点完成，发现静态检查失败或来源不可读取，请查看具体原因。',!result.ok);
  } catch (err) { if (generation === scanGeneration) message(err.message,true); }
  finally { if (generation === scanGeneration) $('refresh').disabled = false; }
}
function render() {
  if (!report) return;
  const query = $('search').value.trim().toLowerCase(), role = $('role').value;
  const items = report.skills.filter(s=>(!role || s.role === role) && `${s.name} ${s.description} ${s.path}`.toLowerCase().includes(query));
  $('count').textContent = report.skills.length; $('pass').textContent = report.counts.PASS || 0; $('issues').textContent = `${report.counts.WARN || 0} / ${report.counts.FAIL || 0}`;
  $('checked').textContent = new Date(report.checked_at).toLocaleTimeString(); $('skills').replaceChildren();
  for (const s of items) {
    const tr=el('tr'); tr.classList.toggle('selected',selection === s.candidate_id);
    const name=el('td'), button=el('button',s.name,'skill-name'); button.type='button'; button.addEventListener('click',()=>{selection=s.candidate_id;render();});
    name.append(button,el('div',s.path,'path')); const source=el('td'); source.append(el('div',s.copy),el('small',roles[s.role] || s.role));
    const status=el('td'); status.append(el('span',labels[s.static_status] || s.static_status,`status ${s.static_status.toLowerCase()}`)); tr.append(name,source,el('td',String(s.files.length)),status); $('skills').append(tr);
  }
  $('empty').hidden=items.length>0;
  $('sources').replaceChildren(); for (const s of report.sources) { const n=el('p'); n.append(el('strong',`${s.copy} · ${s.path} · ${labels[s.status]}`),el('span',` — ${s.reason}`)); $('sources').append(n); }
  const chosen=report.skills.find(s=>s.candidate_id === selection); renderDetail(chosen);
}
function renderDetail(s) {
  const d=$('detail');d.replaceChildren(el('h2',s ? s.name : '完整技能包'));
  d.scrollTop = 0;
  if (!s) { d.append(el('p','选择技能查看文件清单、原始字节摘要与检查原因。'),el('p','候选 locator 不代表已登记资产身份；此版本不执行或同步技能。','note'));return; }
  d.append(el('p',s.description),el('div',`${s.scope} / ${s.copy}`,'pill'),el('p','只读候选 · 尚未纳管','caption'),el('h3','包内容摘要'),el('code',s.package_digest,'digest'));
  const layers=el('div',undefined,'layers');for (const [layer,status] of Object.entries(s.layers)) layers.append(el('span',`${layer} ${labels[status]}`,`status ${status.toLowerCase()}`));d.append(layers);
  d.append(el('p','包摘要只绑定本次文件清单，并非批准修订或来源可信证明。','note'),el('h3','检查与缺口'));
  for (const f of s.findings) {const n=el('div',undefined,'finding');n.append(el('span',labels[f.status],`status ${f.status.toLowerCase()}`),el('strong',f.code),el('p',f.detail));d.append(n);}
  d.append(el('h3',`文件清单 · ${s.files.length} 项`));for (const file of s.files) {const n=el('details');n.append(el('summary',`${file.path} · ${file.bytes} B · ${file.type}`),el('code',file.sha256,'digest'));d.append(n);}
}
$('refresh').addEventListener('click',scan);$('copy').addEventListener('change',scan);$('mirrors').addEventListener('change',scan);
$('search').addEventListener('input',render);$('role').addEventListener('change',render);
$('close').addEventListener('click',async()=>{try{await api('/api/close',{method:'POST'});live?.close();message('控制台已关闭，可以关闭此页面。');$('refresh').disabled=true;}catch(err){message(err.message,true);}});
initialize();
