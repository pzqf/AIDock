'use strict';
const $ = id => document.getElementById(id);
const projectBinding = new URLSearchParams(location.search || '').get('binding') || '';
let csrf = '', state = null, mode = 'create', selected = '', plan = null, planSkill = '', live = null;
let busy = false, loadingFile = false, binaryFile = false, selectionGeneration = 0, impactGeneration = 0;
let loadedFile = '', loadedHash = '';
let discardArmed = false;
let sourceRevision = '', readOnlyFile = false;
const drafts = new Map();
const governanceDrafts = new Map();
let checkGeneration = 0;
let governanceDiscardArmed = false;
let sourceCopy = '';
let projectName = '';
let activeView = 'source';
let formBaseline = null;
const formDrafts = new Map();
const formFields = ['skill-name', 'display-name', 'skill-tags', 'retire-reason', 'description', 'package-path', 'content'];
const actionLabels = {create: '创建技能', import: '导入完整包', edit: '编辑技能', metadata: '更新展示信息', governance: '更新治理声明', rename: '原生改名', retire: '退役技能', reactivate: '恢复管理', enable: '启用技能', disable: '停用技能', inherit: '采纳固定版本', override: '创建项目覆盖', fork: '独立分叉', 'restore-inheritance': '恢复继承', 'external-adopt': '采纳外部技能', 'external-select': '选择外部版本', migrate: '升级 Profile', rollback: '源回退'};
function actionName(action) { return actionLabels[action?.split(':')[0]] || action; }
function formatTime(value) {
  if (!value) return '暂无';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', {month:'2-digit', day:'2-digit', hour:'2-digit', minute:'2-digit'});
}
function openPanel(id) { const panel = $(id); panel.hidden = false; if (!panel.open) panel.showModal?.(); }
function hidePanel(id) { const panel = $(id); panel.close?.(); panel.hidden = true; }
function rememberForm() {
  if (['edit', 'governance'].includes(mode)) return;
  const values = Object.fromEntries(formFields.map(id => [id, $(id).value]));
  const changed = formBaseline === null ? Object.values(values).some(Boolean) : JSON.stringify(values) !== formBaseline;
  if (changed) formDrafts.set(`${mode}:${selected}`, {values, revision: sourceRevision});
  else formDrafts.delete(`${mode}:${selected}`);
}
function closeEditor() {
  if (busy) return;
  rememberForm(); hidePanel('editor-panel');
  if (state) { render(); setBusy(busy); }
  message('已关闭编辑；未保存修改保留在当前页面。');
}
function effectiveSkills() { return state.effective_skills || state.index.skills.map(skill => ({skill, layer: 'owned', owner: state.index.owner, scope_id: state.index.scope_id})); }
function isEnabled(id) { const s = switches(); return Boolean(s.enable?.includes(id) && !s.disable?.includes(id) && !(state.retirements || []).some(r => r.id === id)); }

function invalidatePlan() {
  if (typeof NativePreview !== 'undefined') NativePreview.invalidate();
	checkGeneration++;
	$('check-panel').hidden = true;
  plan = null;
  planSkill = '';
  hidePanel('plan-panel');
}
function clearImpact() {
  impactGeneration++;
  $('impact-panel').hidden = true;
}
function node(tag, text, cls) {
  const e = document.createElement(tag);
  if (text !== undefined) e.textContent = text;
  if (tag === 'button') e.className = 'btn sm';
  if (cls) e.className = cls;
  return e;
}
function message(text, error = false) {
  $('message').textContent = text;
  $('message').classList.toggle('error', error);
  for (const id of ['editor-message', 'plan-message']) { $(id).textContent = text; $(id).classList.toggle('error', error); }
}
function syncShell(state) {
  if (!state) return;
  const scope = `${state.index?.owner === 'global' ? '全局' : '项目'} / ${state.index?.scope_id || '—'}`;
  const binding = `${state.index?.owner === 'global' ? '全局技能库' : '项目技能源'} · ${state.index?.scope_id || '—'}`;
  const scopeLabel = $('scope-label');
  const scopeBadge = $('source-scope-badge');
  const rpState = $('rp-binding-state');
  const rpPath = $('rp-binding-path');
  if (scopeLabel) scopeLabel.textContent = scope;
  if (scopeBadge) scopeBadge.textContent = state.index?.owner === 'global' ? '全局库' : '项目源';
  if (rpState) rpState.textContent = state.recovery_required ? '需恢复' : '已绑定';
  if (rpPath) rpPath.textContent = `${binding} · ${state.root || '显式 .aidock 根目录'}`;
  $('project-name').textContent = state.index.owner === 'global' ? '全局技能库' : projectName || state.root?.replace(/\\/g, '/').split('/').filter(Boolean).slice(-2, -1)[0] || '当前项目';
  $('crumb-sub').textContent = `${$('project-name').textContent} / ${state.index.scope_id}`;
}
async function api(path, body, binding = projectBinding) {
  const res = await fetch(path, {
    method: body === undefined ? 'GET' : 'POST', credentials: 'same-origin',
    headers: {...(binding ? {'X-AIDock-Binding': binding} : {}), ...(body === undefined ? {} : {'Content-Type': 'application/json', 'X-AIDock-CSRF': csrf})},
    body: body === undefined ? undefined : JSON.stringify(body)
  });
  if (!res.ok) throw new Error(`请求失败（${res.status}），请重新打开控制台。`);
  const data = await res.json();
  if (data.ok === false) {
    const error = new Error(`${data.code}：${data.summary}`);
    error.impact = data.recovery?.reference_scope ? data.recovery : null;
	  error.bindingReferences = data.code === 'BINDING_REFERENCED' && Array.isArray(data.recovery) ? data.recovery : null;
	  error.preflight = data.recovery?.skills && data.recovery?.counts ? data.recovery : null;
    throw error;
  }
  return data;
}
function view(history = false, native = false, replica = false) {
  activeView = replica ? 'replica' : native ? 'native' : history ? 'history' : 'source';
  if (history || native || replica) hidePanel('editor-panel');
  if (native || replica) $('plan-panel').close?.();
  else if (!$('plan-panel').hidden && !$('plan-panel').open) $('plan-panel').showModal?.();
  $('source-view').hidden = history || native || replica;
  $('history-view').hidden = !history;
  $('native-view').hidden = !native;
  $('replica-view').hidden = !replica;
  $('source-details').hidden = native || replica;
  $('nav-source').classList.toggle('active', !history && !native && !replica);
  $('nav-history').classList.toggle('active', history);
  $('nav-native').classList.toggle('active', native);
  $('nav-replica').classList.toggle('active', replica);
  $('main-badge').textContent = replica ? '项目只读' : native ? '接入检查' : '仅保存源';
  $('crumb-name').textContent = replica ? '跨副本' : native ? 'AI 工具' : history ? '时间线与回退' : '技能库';
  $('source-title').textContent = replica ? '同项目多副本' : native ? 'AI 工具' : history ? '时间线与回退' : state?.index.owner === 'global' ? '全局技能库' : '项目技能库';
  $('header-description').textContent = replica ? '登记项目副本，核对完整技能包与开关的三方差异。' : native ? '检查技能发现和文件状态，再预览应用到项目的变化。' : history ? '每次保存都留有恢复点，回退前可以检查影响。' : '把项目约定整理成技能，在这里编辑、启停和管理版本。';
}
function resetEditor(next = 'create', skill = null, remember = true) {
  if (remember) rememberForm();
  mode = next;
  selected = skill?.id || '';
  sourceRevision = skill?.revision || '';
  readOnlyFile = Boolean(skill?.read_only);
  selectionGeneration++;
  loadingFile = false;
  binaryFile = false;
  loadedFile = loadedHash = '';
  discardArmed = false;
  governanceDiscardArmed = false;
  $('discard-governance').textContent = '放弃治理草稿并重读';
  $('discard-draft').textContent = '放弃此文件草稿并重读';
  invalidatePlan();
  clearImpact();
  const titles = {create: '创建技能', import: '导入本地完整包', edit: readOnlyFile ? '查看固定版本包' : '编辑包', governance: readOnlyFile ? '查看固定版本声明' : '编辑治理声明', metadata: '展示名与标签', rename: '原生名称改名', retire: '退役并保留', fork: '创建独立分叉'};
  $('editor-title').textContent = titles[next] + (skill ? ` · ${skill.display_name || skill.native_name}` : '');
  const fields = {
    'name-label': ['create', 'rename', 'fork'], 'display-label': ['metadata'], 'tags-label': ['metadata'],
    'reason-label': ['retire'], 'description-label': ['create'], 'package-label': ['import'],
    'file-label': ['edit'], 'content-label': ['create', 'edit'], 'governance-label': ['governance'], 'governance-help': ['governance']
  };
  for (const [id, modes] of Object.entries(fields)) $(id).hidden = !modes.includes(next);
  $('content-label').firstChild.textContent = next === 'edit' ? '文件内容' : '技能正文';
  for (const id of ['skill-name', 'display-name', 'skill-tags', 'retire-reason', 'description', 'package-path', 'content']) $(id).value = '';
  $('skill-name').value = next === 'rename' ? skill.native_name : '';
  $('display-name').value = skill?.display_name || '';
  $('skill-tags').value = (skill?.tags || []).join(', ');
  formBaseline = JSON.stringify(Object.fromEntries(formFields.map(id => [id, $(id).value])));
  const formDraft = formDrafts.get(`${next}:${selected}`);
  if (formDraft) {
    for (const id of formFields) $(id).value = formDraft.values[id];
    sourceRevision = formDraft.revision;
  }
  $('skill-name').required = ['create', 'rename', 'fork'].includes(next);
  $('skill-name').pattern = '[a-z0-9][a-z0-9\\-]{0,63}';
  $('skill-name').maxLength = 64;
  $('description').required = next === 'create';
  $('package-path').required = next === 'import';
  $('retire-reason').required = next === 'retire';
  $('skill-name').title = '使用小写字母、数字和连字符，例如 api-conventions';
  const governanceDraft = readOnlyFile ? null : governanceDrafts.get(selected);
  $('governance').value = next === 'governance' ? governanceDraft?.text ?? JSON.stringify({dependencies: skill.dependencies || {}, compatibility: skill.compatibility || {}, requirements: skill.requirements || {}}, null, 2) : '';
  if (next === 'governance' && governanceDraft) sourceRevision = governanceDraft.revision;
  $('file-info').textContent = '';
  const notes = {
    metadata: '只修改资产展示信息，原生名称、包文件和固定版本保持原值。',
    governance: '治理声明参与固定修订计算。未解决的依赖可先保存；已知静态错误阻止启用。未验证条件会保留在报告中。',
    rename: '稳定 ID 与旧修订保留。正文中的名称调用需人工检查；旧工具输出的清理等待后续工具应用。',
    retire: '退役会将本层开关设为停用，完整包与版本全部保留。可恢复管理或回退；已声明引用会阻止退役。',
    fork: `分叉使用新 ID 和不同的原生名称，记录来源 ${sourceRevision}，初始停用。正文中的名称调用需人工检查。`
  };
  $('editor-note').textContent = notes[next] || '';
  $('editor-note').hidden = !notes[next];
  $('file-info').hidden = next !== 'edit';
  $('file-details').hidden = next !== 'edit';
  $('editor-draft-state').textContent = readOnlyFile ? '只读固定版本' : formDraft || governanceDraft ? '有未保存修改 · 关闭后可继续编辑' : '填写后预览，确认后保存';
  $('draft-note').hidden = next !== 'edit';
  setBusy(busy);
  view();
  openPanel('editor-panel');
  message(formDraft ? '已恢复当前页面的未保存修改。' : readOnlyFile ? '固定版本只读，可查看完整包。' : '填写后预览变更，确认后保存。');
}
function setBusy(value) {
  busy = value;
  for (const n of document.querySelectorAll('button,input,select,textarea')) {
    n.disabled = value || n.dataset?.retired === 'true' || Boolean(state?.recovery_required && !['recover', 'reload', 'close', 'nav-source', 'nav-history'].includes(n.id));
  }
  $('content').disabled = busy || loadingFile || binaryFile || (mode === 'edit' && readOnlyFile) || Boolean(state?.recovery_required);
  $('governance').disabled = busy || (mode === 'governance' && readOnlyFile) || Boolean(state?.recovery_required);
  $('preview').disabled = busy || loadingFile || binaryFile || (['edit', 'governance'].includes(mode) && readOnlyFile) || Boolean(state?.recovery_required);
  $('apply-plan').disabled = busy || !plan || Boolean(state?.recovery_required);
  $('discard-draft').hidden = mode !== 'edit' || !drafts.get(selected)?.has(loadedFile);
  $('discard-draft').disabled = busy || loadingFile || Boolean(state?.recovery_required);
  $('discard-governance').hidden = mode !== 'governance' || readOnlyFile || !governanceDrafts.has(selected);
  if (typeof NativePreview !== 'undefined') NativePreview.sync();
  if (typeof ReplicaView !== 'undefined') ReplicaView.sync();
  if (typeof IsolationView !== 'undefined') IsolationView.sync();
  if (typeof GeneratorView !== 'undefined') GeneratorView.sync();
  if (typeof ProjectsView !== 'undefined') ProjectsView.sync();
}
async function refresh() {
  renderBridge(null);
  state = (await api('/api/source/state')).items;
  $('binding').textContent = `${state.index.owner} / ${state.index.scope_id}${sourceCopy ? ' / ' + sourceCopy : ''} · ${state.root}`;
  view(activeView === 'history', activeView === 'native', activeView === 'replica');
  syncShell(state);
  $('recovery-panel').hidden = !state.recovery_required;
  $('migrate').hidden = state.profile?.schema_version !== 1;
  render();
  setBusy(busy);
}
function switches() { return state.profile?.skills || {}; }
function render() {
  const retirements = new Map((state.retirements || []).map(r => [r.id, r]));
  $('managed-skills').replaceChildren();
  const effective = effectiveSkills();
  const query = $('skill-search').value.trim().toLocaleLowerCase();
  const statusFilter = $('status-filter').value || 'all', layerFilter = $('layer-filter').value || 'all';
  const filtered = effective.filter(item => {
    const skill = item.skill, retired = retirements.has(skill.id), enabled = isEnabled(skill.id);
    const searchable = [skill.native_name, skill.display_name, ...(skill.tags || []), state.skill_descriptions?.[skill.id]].filter(Boolean).join(' ').toLocaleLowerCase();
    return (!query || searchable.includes(query)) && (layerFilter === 'all' || item.layer === layerFilter) && (statusFilter === 'all' || statusFilter === 'retired' && retired || statusFilter === 'enabled' && enabled || statusFilter === 'disabled' && !enabled && !retired);
  });
  $('total-count').textContent = `${effective.length} 个技能`;
  $('nav-skill-count').textContent = String(effective.length);
  $('enabled-count').textContent = `${effective.filter(item => isEnabled(item.skill.id)).length} 个`;
  $('list-count').textContent = query || statusFilter !== 'all' || layerFilter !== 'all' ? `${filtered.length} / ${effective.length} 个技能` : `${effective.length} 个技能`;
  if (!filtered.length) {
    const empty = node('div', undefined, 'library-empty');
    empty.append(node('div', effective.length ? '⌕' : '⚡', 'empty-icon'), node('h3', effective.length ? '没有匹配的技能' : '为项目添加第一个技能'), node('p', effective.length ? '试试其他名称、描述或标签，或清除筛选条件。' : '创建项目约定，或者导入已有的 SKILL.md 完整包。每次保存都可以回退。'));
    const b = node('button', effective.length ? '清除筛选' : '＋ 创建技能', 'btn primary');
    b.type = 'button';
    b.addEventListener('click', () => { if (busy) return; if (effective.length) { $('skill-search').value = ''; $('status-filter').value = $('layer-filter').value = 'all'; render(); setBusy(busy); } else resetEditor(); });
    empty.append(b); $('managed-skills').append(empty);
  }
  for (const item of filtered) {
    const skill = {...item.skill, read_only: item.read_only};
    const retired = retirements.get(skill.id);
    const enabled = isEnabled(skill.id);
    const row = node('article', undefined, 'managed-skill');
    row.dataset.skillId = skill.id;
    const head = node('div', undefined, 'skill-heading');
    const identity = node('div', undefined, 'skill-identity');
    const name = node('div'); name.append(node('strong', skill.display_name || skill.native_name), node('small', skill.native_name));
    identity.append(node('span', '⚡', 'skill-icon'), name);
    const badges = node('div', undefined, 'skill-badges');
    const layers = {owned: state.index.owner === 'global' ? '全局自定义' : '项目自定义', inherited: '全局继承 · 只读', external: '外部批准 · 只读', override: '项目覆盖', fork: '独立分叉'};
    badges.append(node('span', layers[item.layer] || item.layer, 'badge type'), node('span', retired ? '已退役' : enabled ? '源已启用' : '源已停用', `skill-status ${enabled ? 'enabled' : 'disabled'}`));
    head.append(identity, badges); row.append(head);
    row.append(node('p', state.skill_descriptions?.[skill.id] || '保存版本的描述暂不可用，可打开技能包查看。', 'skill-description'));
    const meta = node('div', undefined, 'skill-meta');
    meta.append(node('span', `${skill.files.length} 个文件`));
    for (const tag of skill.tags || []) meta.append(node('span', tag, 'skill-tag'));
    const hasDraft = drafts.get(skill.id)?.size || governanceDrafts.has(skill.id) || Array.from(formDrafts.keys()).some(key => key.endsWith(`:${skill.id}`));
    if (hasDraft) meta.append(node('span', '有未保存修改', 'draft-badge'));
    row.append(meta);
    const technical = node('details', undefined, 'skill-technical');
    technical.append(node('summary', '版本与来源'), node('code', `资产 ID：${skill.id}`), node('code', `固定修订：${skill.revision}`), node('small', `来源：${item.owner} / ${item.scope_id}`));
    if (skill.base_revision) technical.append(node('code', `覆盖基础：${skill.base_revision}`));
    if (skill.upstream) technical.append(node('small', `分叉来源：${skill.upstream.owner} / ${skill.upstream.scope_id} · ${skill.upstream.id} · ${skill.upstream.revision}`));
    if (item.status === 'BASE_STALE') row.append(node('p', 'BASE_STALE · 上层已选 pin 与覆盖基础不同；恢复继承可使用已选版本，原生应用将等待协调。'));
    if (retired) row.append(node('p', `退役原因：${retired.reason}`));
    const toggle = [[enabled ? '停用' : '启用', enabled ? 'disable' : 'enable']];
    const actions = retired ? [['引用与影响', 'references'], ['恢复管理', 'reactivate']] : item.layer === 'external' ? [['查看包', 'edit'], ...toggle, ['查看声明', 'governance'], ['引用与影响', 'references']] : item.read_only ? [
      ['查看包', 'edit'], ...toggle, ['查看声明', 'governance'], ['创建项目覆盖', 'override'], ['独立分叉', 'fork'], ['引用与影响', 'references']
    ] : [
      ['编辑包', 'edit'], ...toggle, ['展示名与标签', 'metadata'],
      ['治理声明', 'governance'],
      ['原生改名', 'rename'], ['独立分叉', 'fork'], ['引用与影响', 'references'], ['退役并保留', 'retire'],
      ...(item.layer === 'override' ? [['恢复继承', 'restore-inheritance']] : [])
    ];
    const actionBar = node('div', undefined, 'skill-actions');
    const more = node('details', undefined, 'skill-more'); more.append(node('summary', '更多'));
    const menu = node('div', undefined, 'skill-menu'); more.append(menu);
    more.addEventListener('toggle', () => {
      if (!more.open) return;
      for (const other of document.querySelectorAll('.skill-more[open]')) if (other !== more) other.open = false;
      const rect = more.getBoundingClientRect();
      const below = window.innerHeight - rect.bottom;
      more.classList.toggle('opens-up', below < menu.scrollHeight && rect.top > below);
    });
    for (const [label, action] of actions) {
      const b = node('button', label);
      b.type = 'button';
      b.disabled = state.recovery_required;
      b.addEventListener('click', () => {
        if (busy) return;
        more.open = false;
        if (action === 'edit') return edit(skill);
        if (action === 'references') return references(skill.id);
        if (['override', 'restore-inheritance', 'fork', 'rename', 'retire'].includes(action) && governanceDrafts.has(skill.id)) {
          invalidatePlan(); message('此技能有未保存的治理草稿，请先保存或核对。草稿仍在本页。', true); return;
        }
        if (['override', 'restore-inheritance', 'fork', 'governance'].includes(action) && drafts.get(skill.id)?.size) {
          invalidatePlan(); message('此技能有未保存的草稿，请先保存或核对草稿，再改变继承关系。草稿仍在本页。', true); return;
        }
        if (['override', 'restore-inheritance'].includes(action)) {
          const upper = state.inherited_skills?.find(p => p.skill.id === skill.id);
          return preview({action, id: skill.id, revision: upper?.skill.revision || skill.revision});
        }
        if (['metadata', 'rename', 'retire', 'fork', 'governance'].includes(action)) {
          if (['rename', 'retire'].includes(action) && drafts.get(skill.id)?.size) {
            invalidatePlan();
            message('此技能有未保存的文本草稿，请先编辑并保存，再改名或退役。草稿仍在本页。', true);
            return;
          }
          resetEditor(action, skill);
          return;
        }
        return preview({action, id: skill.id});
      });
      if (['enable', 'disable'].includes(action)) {
        b.className = `skill-switch ${enabled ? 'on' : ''}`;
        b.setAttribute('role', 'switch'); b.setAttribute('aria-checked', String(enabled));
        b.setAttribute('aria-label', `${label} ${skill.display_name || skill.native_name}`);
        b.title = '确认预览后才会改变保存的源状态';
        actionBar.append(b);
      } else if (['edit', 'reactivate'].includes(action)) actionBar.append(b);
      else menu.append(b);
    }
    menu.append(technical);
    actionBar.append(more);
    row.append(actionBar);
    $('managed-skills').append(row);
  }
  renderLibrary();
  renderExternal();
  $('history').replaceChildren();
  const saved = state.history.filter(item => item.state === 'committed');
  $('last-save').textContent = formatTime(saved[0]?.updated);
  $('recent-activity').replaceChildren();
  for (const item of saved.slice(0, 4)) {
    const activity = node('div', undefined, 'rp-act');
    const detail = node('div'); detail.append(node('div', actionName(item.action), 't'), node('div', formatTime(item.updated), 'd'));
    activity.append(node('span', '✓', 'time'), detail); $('recent-activity').append(activity);
  }
  if (!saved.length) $('recent-activity').append(node('p', '还没有保存记录。保存后会自动出现在这里。', 'rp-note'));
  for (const item of state.history) {
    const row = node('article', undefined, 'history-entry');
    row.append(node('strong', actionName(item.action)), node('p', `${item.state === 'committed' ? '已保存' : item.state === 'prepared' ? '待恢复' : item.state} · ${formatTime(item.updated)}`));
    const detail = node('details'); detail.append(node('summary', '事务详情'), node('code', item.id), node('code', item.action)); row.append(detail);
    if (item.state === 'committed' && item.restorable) {
      const b = node('button', item.action.startsWith('rollback:') ? '撤销此次回退' : '回退到此次保存前');
      b.type = 'button';
      b.disabled = state.recovery_required;
      b.addEventListener('click', () => preview({action: 'rollback', snapshot: item.id}));
      row.append(b);
    } else if (item.state === 'committed') row.append(node('p', '首次初始化；尚无更早的项目源可回退。'));
    $('history').append(row);
  }
  if (!state.history.length) $('history').append(node('p', '还没有保存记录。'));
}
function renderExternal() {
  $('external-panel').hidden = !(state.external_history?.length);
  $('external-skills').replaceChildren();
  for (const current of state.external_skills || []) {
    const row = node('article', undefined, 'managed-skill');
    row.append(node('strong', current.skill.native_name), node('small', `${current.binding.scope}/${current.binding.copy} · 外部原源：${current.binding.root} / ${current.package_path}`));
    row.append(node('code', `已选批准：${current.approval_id}`), node('code', `已采纳包基线：${current.candidate_revision}`), node('code', `当时的源集合：${current.bridge_revision}`));
    const label = node('label', '历史批准版本');
    const select = node('select'); select.setAttribute('aria-label', `${current.skill.native_name} 历史批准版本`);
    for (const a of state.external_history || []) {
      if (a.skill.id !== current.skill.id) continue;
      const option = node('option', `${a.approved_at} · ${a.candidate_revision}`); option.value = a.approval_id; select.append(option);
    }
    select.value = current.approval_id;
    select.addEventListener('change', () => { invalidatePlan(); setBusy(busy); });
    label.append(select); row.append(label);
    const button = node('button', '预览选择批准版本'); button.type = 'button';
    button.addEventListener('click', () => {
      if (busy) return;
      return preview({action: 'external-select', id: current.skill.id, approval: select.value});
    });
    row.append(button); $('external-skills').append(row);
  }
  if (!state.external_skills?.length) $('external-skills').append(node('p', '当前恢复点未选择外部资产；批准历史与缓存已保留，可撤销回退后再次选择。'));
}
function renderLibrary() {
  $('library-panel').hidden = !state.library_root;
  $('library-binding').textContent = state.library_root || '';
  $('library-error').textContent = state.library_error ? `全局库暂不可用：${state.library_error}。已有固定缓存可继续使用。` : '';
  $('library-skills').replaceChildren();
  const groups = new Map();
  for (const candidate of state.library || []) {
    if (!groups.has(candidate.skill.id)) groups.set(candidate.skill.id, []);
    groups.get(candidate.skill.id).push(candidate);
  }
  for (const [id, candidates] of groups) {
    const current = candidates.find(c => c.current) || candidates[0];
    const row = node('article', undefined, 'managed-skill');
    row.append(node('strong', current.skill.display_name || current.skill.native_name), node('code', id));
    const label = node('label', '采纳版本');
    const select = node('select'); select.setAttribute('aria-label', `${current.skill.native_name} 采纳版本`);
    for (const candidate of candidates) {
      const option = node('option', `${candidate.current ? '当前候选' : '历史版本'} · ${candidate.skill.revision}`);
      option.value = candidate.skill.revision; select.append(option);
    }
    select.value = current.skill.revision;
    select.addEventListener('change', () => { invalidatePlan(); setBusy(busy); });
    label.append(select); row.append(label);
    const button = node('button', current.retired ? '已退役 · 不能新采纳' : '预览采纳固定版本');
    button.type = 'button'; button.dataset.retired = String(Boolean(current.retired));
    button.addEventListener('click', () => {
      if (busy || current.retired) return;
      if (drafts.get(id)?.size || governanceDrafts.has(id)) {invalidatePlan(); message('此技能有未保存的草稿，请先保存或核对再采纳全局版本。', true); return;}
      return preview({action: 'inherit', id, revision: select.value});
    });
    row.append(button); $('library-skills').append(row);
  }
}
function renderImpact(impact) {
  $('impact-panel').hidden = !impact;
  if (!impact) return;
  $('impact-summary').replaceChildren(node('p', `检查范围：当前绑定 ${impact.owner} / ${impact.scope_id} · ${impact.id}`));
  if (impact.native_before) $('impact-summary').append(node('p', `原生名称：${impact.native_before} → ${impact.native_after}`));
  if (impact.native_cleanup) $('impact-summary').append(node('p', '旧工具输出待后续独立应用时检查与清理。'));
  $('impact-references').replaceChildren();
  for (const ref of impact.references || []) {
    $('impact-references').append(node('p', `${ref.blocks_retirement ? '阻止退役' : '已知引用'} · ${ref.kind} · ${ref.from_name || '当前 Profile'} · ${ref.path}${ref.revision ? ' · ' + ref.revision : ''}`));
  }
  if (!impact.references?.length) $('impact-references').append(node('p', '当前绑定内未找到已声明引用。'));
  $('impact-unknown').replaceChildren();
  for (const scope of impact.unknown || []) $('impact-unknown').append(node('p', `UNKNOWN · ${scope}`));
}
function renderCheck(report) {
  $('check-panel').hidden = !report;
  if (!report) return;
  const labels = {PASS: '通过', WARN: '提示', UNKNOWN: '未验证', FAIL: '失败'};
  $('check-summary').textContent = `${report.candidate ? '保存候选预检（尚未保存）' : '当前源静态预检'}：${labels[report.status] || report.status} · ${report.platform} · ${report.skills.length} 个技能。工具实际生效尚未验证。`;
  $('check-boundary').textContent = report.boundary;
  $('check-results').replaceChildren();
  for (const skill of report.skills) {
    const row = node('article', undefined, 'managed-skill');
    row.append(node('strong', `${skill.native_name} · ${labels[skill.status] || skill.status} · ${report.candidate ? '候选' : '本层'}${skill.enabled ? '启用' : '未启用'}`));
    for (const finding of skill.checks) row.append(node('p', `${labels[finding.status] || finding.status} · ${finding.code}：${finding.detail}`));
    $('check-results').append(row);
  }
}
$('check').addEventListener('click', async () => {
  if (busy) return;
  invalidatePlan(); clearImpact();
  const generation = checkGeneration;
  setBusy(true);
  try {
    const report = (await api('/api/source/check')).items;
    if (generation !== checkGeneration) return;
    renderCheck(report);
    message('只读预检完成；源未修改。缺少证据的条件显示为未验证。');
  } catch (e) { message(e.message, true); }
  finally { setBusy(false); }
});
$('bridge-check').addEventListener('click', async () => {
  if (busy) return;
  invalidatePlan(); clearImpact(); renderBridge(null);
  setBusy(true);
  try {
    const report = (await api('/api/bridge')).items;
    renderBridge(report, state.index.owner !== 'global' && sourceCopy ? (bridge, candidate) => {
      if (busy) return;
      if (bridge.project !== state.index.owner || bridge.scope !== state.index.scope_id || bridge.copy !== sourceCopy) {
        invalidatePlan(); message('此候选属于其他 project/scope/copy，请在对应项目绑定中采纳。', true); return;
      }
      return preview({action: 'external-adopt', bridge_id: bridge.id, package_path: candidate.skill.path, revision: candidate.revision, bridge_revision: bridge.current_revision});
    } : undefined);
    message('外部源只读检查完成；候选 revision 未自动采纳。', ['FAIL', 'STALE'].includes(report.status));
  } catch (e) { message(e.message, true); }
  finally { setBusy(false); }
});
async function references(id) {
  invalidatePlan();
  const generation = ++impactGeneration;
  setBusy(true);
  try {
    const data = (await api(`/api/source/references?${new URLSearchParams({id})}`)).items;
    if (generation !== impactGeneration) return;
    renderImpact(data);
    message('引用检查完成；未知范围仍需后续登记和验证。');
  } catch (e) { message(e.message, true); }
  finally { setBusy(false); }
}
async function edit(skill) {
  if (busy) return;
  if (governanceDrafts.has(skill.id)) { invalidatePlan(); message('此技能有未保存的治理草稿，请先保存或放弃并重读，再编辑包。', true); return; }
  resetEditor('edit', skill);
  $('package-file').replaceChildren();
  for (const file of skill.files) {
    const opt = node('option', file.path);
    opt.value = file.path;
    $('package-file').append(opt);
  }
  await loadFile();
}
async function loadFile() {
  invalidatePlan();
  discardArmed = false;
  $('discard-draft').textContent = '放弃此文件草稿并重读';
  const generation = ++selectionGeneration;
  const id = selected, file = $('package-file').value;
  loadingFile = binaryFile = true;
  loadedFile = loadedHash = '';
  setBusy(busy);
  $('content').value = '';
  try {
    const data = (await api(`/api/source/file?${new URLSearchParams({id, path: file})}`)).items;
    if (generation !== selectionGeneration || id !== selected) return;
    binaryFile = data.type !== 'text';
    readOnlyFile = Boolean(data.read_only);
    const draft = drafts.get(id)?.get(file);
    loadedFile = file;
    loadedHash = draft?.sha256 || data.sha256;
    $('content').value = binaryFile ? '' : draft?.content ?? data.content;
    $('content-label').hidden = binaryFile;
    $('editor-draft-state').textContent = readOnlyFile ? '只读版本 · 可创建覆盖或分叉后编辑' : draft ? '有未保存修改 · 关闭后可继续编辑' : '修改后预览，确认后保存';
    $('file-info').textContent = `${data.type} · ${data.bytes} B · ${data.sha256}`;
    if (draft && draft.sha256 !== data.sha256) {
      message('文件在外部发生变化，页面草稿已保留；保存预览将拒绝覆盖，请先核对外部修改。', true);
    } else message(readOnlyFile ? '固定版本包只读。外部包更新须重新检查并采纳；历史批准版本可在缓存中选择。' : binaryFile ? '二进制附件保留原字节，本版本不提供文本编辑。' : '编辑后生成预览，再确认保存。');
  } catch (e) { if (generation === selectionGeneration) message(e.message, true); }
  finally { if (generation === selectionGeneration) { loadingFile = false; setBusy(busy); } }
}
async function preview(request) {
  if (busy) return;
  invalidatePlan();
  clearImpact();
  setBusy(true);
  try {
    const data = (await api('/api/source/plan', request)).items;
    plan = data.plan;
    planSkill = data.skill_id || request.id || '';
    const skill = effectiveSkills().find(item => item.skill.id === planSkill)?.skill;
    const title = request.action === 'rollback' ? '确认源回退' : `${actionName(request.action)}${skill ? ` · ${skill.display_name || skill.native_name}` : request.name ? ` · ${request.name}` : ''}`;
    $('plan-title').textContent = title;
    $('plan-summary').textContent = request.action === 'rollback' ? '将当前项目整个源状态恢复到所选保存之前。回退前的状态会自动保留，可在时间线撤销此次回退。' : `${plan.changes.length} 项路径变更。确认后保存，并自动保留本次保存前的恢复点。`;
    $('plan-change-count').textContent = `查看 ${plan.changes.length} 项路径变更`;
    $('apply-plan').textContent = request.action === 'rollback' ? '确认回退源' : '确认保存源';
    $('cancel-plan').textContent = ['create', 'import', 'edit', 'metadata', 'governance', 'rename', 'retire', 'fork'].includes(request.action) ? '返回修改' : '取消';
    if (request.action.startsWith('external-')) $('plan-summary').textContent = `外部版本选择 · ${plan.changes.length} 项路径变更。只保存完整包缓存、批准记录和项目固定版本；外部目录保持只读，工具应用与跨副本发布尚未执行。`;
    $('plan-changes').replaceChildren();
    for (const change of plan.changes) {
      const kind = change.before ? (change.after ? '修改' : '删除') : '新增';
      $('plan-changes').append(node('p', `${kind} · ${change.path} · ${change.before?.bytes || 0} → ${change.after?.bytes || 0} B`));
    }
    for (const effect of plan.asset_effects || []) $('plan-changes').append(node('p', `技能状态：${effect.native_name} · ${effect.before} → ${effect.after} · 完整包保留`));
    $('plan-findings').replaceChildren();
    for (const finding of data.findings || []) if (finding.status === 'WARN') $('plan-findings').append(node('p', `${finding.code}：${finding.detail}`));
    if (data.preflight) $('plan-findings').append(node('p', `候选静态检查：${data.preflight.status}。工具生效仍待验证。`));
    for (const ref of data.impact?.references || []) $('plan-findings').append(node('p', `已声明引用：${ref.from_name || '当前 Profile'} · ${ref.path}`));
    renderImpact(data.impact);
    renderCheck(data.preflight);
    message(request.action === 'reactivate' ? '恢复管理预览已生成；恢复后仍保持本层停用，启用须另行预览。' : '预览已生成。确认只保存当前技能源。');
    openPanel('plan-panel');
  } catch (e) {
    invalidatePlan();
    renderImpact(e.impact);
    renderCheck(e.preflight);
    message(e.message, true);
    if (e.impact || e.preflight) { rememberForm(); hidePanel('editor-panel'); $(e.impact ? 'impact-panel' : 'check-panel').scrollIntoView?.({block: 'start', behavior: 'smooth'}); }
  } finally { setBusy(false); }
}
$('editor').addEventListener('input', e => {
  invalidatePlan();
  discardArmed = false;
  governanceDiscardArmed = false;
  $('discard-governance').textContent = '放弃治理草稿并重读';
  $('discard-draft').textContent = '放弃此文件草稿并重读';
  clearImpact();
  if (e.target === $('content') && mode === 'edit' && !loadingFile && !binaryFile && !readOnlyFile) {
    if (!drafts.has(selected)) drafts.set(selected, new Map());
    drafts.get(selected).set(loadedFile, {content: $('content').value, sha256: loadedHash});
  }
  if (e.target === $('governance') && mode === 'governance' && !readOnlyFile) governanceDrafts.set(selected, {text: $('governance').value, revision: sourceRevision});
  $('editor-draft-state').textContent = '有未保存修改 · 关闭后可继续编辑';
  setBusy(busy);
});
$('editor').addEventListener('submit', e => {
  e.preventDefault();
  if (busy || loadingFile || binaryFile || (['edit', 'governance'].includes(mode) && readOnlyFile) || state?.recovery_required) return;
  if (mode === 'governance') {
    let governance;
    try { governance = JSON.parse($('governance').value); }
    catch { invalidatePlan(); message('治理声明不是有效 JSON；草稿已保留，请修正后预览。', true); return; }
    return preview({action: 'governance', id: selected, revision: sourceRevision, governance});
  }
  if (mode === 'create') return preview({action: 'create', name: $('skill-name').value, description: $('description').value, document: $('content').value});
  if (mode === 'import') return preview({action: 'import', package_path: $('package-path').value, source_role: 'local-original'});
  if (mode === 'rename') return preview({action: 'rename', id: selected, name: $('skill-name').value});
  if (mode === 'fork') return preview({action: 'fork', id: selected, revision: sourceRevision, name: $('skill-name').value});
  if (mode === 'metadata') return preview({action: 'metadata', id: selected, display_name: $('display-name').value, tags: $('skill-tags').value.split(/[,，]/).map(x => x.trim()).filter(Boolean)});
  if (mode === 'retire') return preview({action: 'retire', id: selected, reason: $('retire-reason').value});
  const files = new Map(drafts.get(selected) || []);
  files.set(loadedFile, {content: $('content').value, sha256: loadedHash});
  return preview({action: 'edit', id: selected, files: Array.from(files, ([path, draft]) => ({path, content: draft.content, expected_sha256: draft.sha256}))});
});
$('new').addEventListener('click', () => resetEditor());
$('discard-governance').addEventListener('click', async () => {
  if (busy || mode !== 'governance' || !governanceDrafts.has(selected)) return;
  invalidatePlan();
  if (!governanceDiscardArmed) {
    governanceDiscardArmed = true;
    $('discard-governance').textContent = '确认放弃治理草稿并重读';
    message('再次点击将放弃此技能的未保存治理草稿并重读；请先复制需要保留的声明。', true);
    return;
  }
  const id = selected;
  setBusy(true);
  try {
    await refresh();
    const item = state.effective_skills?.find(x => x.skill.id === id) || {skill: state.index.skills.find(x => x.id === id)};
    if (!item.skill) { message('技能已变化；草稿保留，请核对源状态。', true); return; }
    governanceDrafts.delete(id);
    resetEditor('governance', {...item.skill, read_only: item.read_only});
    message('已重读当前固定修订与治理声明。');
  } catch (e) { message(e.message, true); }
  finally { setBusy(false); }
});
$('import').addEventListener('click', () => resetEditor('import'));
$('package-file').addEventListener('change', loadFile);
$('discard-draft').addEventListener('click', async () => {
  if (busy || loadingFile || mode !== 'edit' || !drafts.get(selected)?.has(loadedFile)) return;
  invalidatePlan();
  if (!discardArmed) {
    discardArmed = true;
    $('discard-draft').textContent = '确认放弃此文件草稿并重读';
    message('再次点击将放弃本页此文件的未保存草稿，重新读取磁盘版本；其他文件草稿保留。请先复制需要的文本。', true);
    return;
  }
  drafts.get(selected).delete(loadedFile);
  await loadFile();
});
$('nav-source').addEventListener('click', () => view());
$('nav-history').addEventListener('click', () => view(true));
$('nav-native').addEventListener('click', () => view(false, true));
$('nav-replica').addEventListener('click', () => { view(false,false,true); if (typeof ReplicaView !== 'undefined') ReplicaView.load(); });
$('editor-close').addEventListener('click', closeEditor);
$('editor-panel').addEventListener('cancel', e => { e.preventDefault(); closeEditor(); });
$('plan-panel').addEventListener('cancel', e => { e.preventDefault(); if (!busy) $('cancel-plan').click(); });
$('skill-search').addEventListener('input', () => { if (state) { render(); setBusy(busy); } });
for (const id of ['status-filter', 'layer-filter']) $(id).addEventListener('change', () => { if (state) { render(); setBusy(busy); } });
function focusSearch() { if (!state || busy || !$('editor-panel').hidden || !$('plan-panel').hidden) return; view(); $('skill-search').focus?.(); }
$('search-trigger').addEventListener('click', focusSearch);
document.addEventListener('keydown', e => { if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); focusSearch(); } });
$('context-toggle').addEventListener('click', () => { const panel = $('rightPanel'); panel.hidden = !panel.hidden; $('context-toggle').setAttribute('aria-expanded', String(!panel.hidden)); });
$('rp-new')?.addEventListener('click', () => { view(); resetEditor(); });
$('rp-reload')?.addEventListener('click', () => $('reload').click());
$('rp-native')?.addEventListener('click', () => {
  if (!$('nav-native').hidden) $('nav-native').click();
  else message('当前会话未绑定工具预览根目录。', true);
});
$('sidebar-toggle')?.addEventListener('click', () => $('sidebar').classList.toggle('collapsed'));
$('theme-toggle')?.addEventListener('click', () => {
  const next = document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
  document.documentElement.setAttribute('data-theme', next);
  try { localStorage.setItem('aidock-theme', next); } catch {}
});
$('cancel-plan').addEventListener('click', () => { if (busy) return; invalidatePlan(); clearImpact(); message('已取消预览；源未修改，未保存修改仍保留。'); setBusy(busy); });
$('apply-plan').addEventListener('click', async () => {
  if (!plan || busy) return;
  setBusy(true);
  try {
    const savedAction = plan.action, savedSkill = planSkill;
    await api('/api/source/apply', {plan_id: plan.id, digest: plan.digest});
    rememberForm();
    if (savedAction === 'edit') drafts.delete(savedSkill);
    if (savedAction === 'governance') governanceDrafts.delete(savedSkill);
    formDrafts.delete(`${savedAction}:${savedSkill}`);
    if (['create', 'import'].includes(savedAction)) formDrafts.delete(`${savedAction}:`);
    invalidatePlan(); hidePanel('editor-panel');
    if (mode === savedAction && (!selected || selected === savedSkill)) {
      mode = 'create'; selected = ''; sourceRevision = ''; loadedFile = loadedHash = ''; selectionGeneration++; loadingFile = binaryFile = readOnlyFile = false;
      for (const id of formFields) $(id).value = '';
      formBaseline = JSON.stringify(Object.fromEntries(formFields.map(id => [id, $(id).value])));
    }
    await refresh();
    message('技能源已保存，恢复点已保留；尚未应用到工具或副本。');
  } catch (e) {
    invalidatePlan();
    message(e.message, true);
    try { await refresh(); } catch {}
  } finally { setBusy(false); }
});
$('migrate').addEventListener('click', () => preview({action: 'migrate'}));
$('recover').addEventListener('click', async () => {
  if (busy) return;
  setBusy(true);
  try {
    invalidatePlan(); clearImpact();
    await api('/api/source/recover', {});
    await refresh();
    message('未完成事务已恢复；可以重新预览保存。');
  } catch (e) { message(e.message, true); }
  finally { setBusy(false); }
});
$('reload').addEventListener('click', async () => {
  if (busy) return;
  setBusy(true);
  try { invalidatePlan(); clearImpact(); await refresh(); message('源状态已刷新；本页草稿保留。'); }
  catch (e) { message(e.message, true); }
  finally { setBusy(false); }
});
$('close').addEventListener('click', async () => {
  if (busy) { message('正在处理源事务，请等待完成后关闭。'); return; }
  try {
    await api('/api/close', {});
    live?.close();
    message('控制台已关闭，可以关闭此页面。');
    for (const n of document.querySelectorAll('button,input,select,textarea')) n.disabled = true;
  } catch (e) { message(e.message, true); }
});
(async () => {
  try {
    const token = new URLSearchParams(location.hash.slice(1)).get('token');
    history.replaceState(null, '', location.pathname + (location.search || ''));
    csrf = (await api('/api/session', token ? {token} : undefined)).csrf;
    let app, bindingError = '';
    try { app = await api('/api/state'); }
    catch (e) {
      if (!projectBinding) throw e;
      bindingError = `此项目入口暂不可用：${e.message}。可重新选择项目，或从接入历史恢复。`;
      app = {...await api('/api/state', undefined, ''), project_workspace:true};
    }
    projectName = app.project_name || '';
    $('version').textContent = `v${app.version}`;
    live = new EventSource('/api/events');
    if (typeof ProjectsView !== 'undefined') await ProjectsView.init({api,setBusy,busy:()=>busy,binding:projectBinding,bindingError,hasDrafts:()=>{ rememberForm(); return drafts.size > 0 || governanceDrafts.size > 0 || formDrafts.size > 0; }},app);
    if (app.project_workspace) return;
    sourceCopy = app.source_copy || '';
    if (typeof IsolationView !== 'undefined') IsolationView.init({api,message,setBusy,busy:()=>busy,pending:()=>Boolean(state?.recovery_required)});
    if (typeof GeneratorView !== 'undefined') GeneratorView.init({api,message,setBusy,busy:()=>busy,pending:()=>Boolean(state?.recovery_required)});
    if (typeof NativePreview !== 'undefined') NativePreview.init({api, message, setBusy, busy: () => busy, pending: () => Boolean(state?.recovery_required)}, app);
    $('rp-native').hidden = !app.native_root;
    $('rp-discovery').hidden = !app.native_root;
    $('bridge-check').hidden = !app.bridge_file;
    $('version').textContent = `v${app.version}`;
    await refresh();
    if (typeof ReplicaView !== 'undefined') ReplicaView.init({api,message,setBusy,busy:()=>busy,pending:()=>Boolean(state?.recovery_required),sourceRoot:()=>state?.root},app);
    if (typeof NativePreview !== 'undefined' && app.native_root) await NativePreview.restore();
    if (typeof NativeRehearsal !== 'undefined' && app.native_root) await NativeRehearsal.restore();
    message(state.index.owner === 'global' ? '当前管理显式绑定的全局技能库。发布新候选不改变项目固定版本。' : '当前管理显式绑定的项目技能源与固定继承。');
  } catch (e) { message(e.message, true); }
})();
