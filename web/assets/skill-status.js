'use strict';
// Only backend-checked, identity- and revision-bound observations are current.
const SkillStatus = (() => {
  let report = null, bundle = null, copy = '';
  const tools = [['claude-code', 'Claude Code'], ['codex-cli', 'Codex CLI']];
  const levels = [['L1', '文件一致'], ['L2', '隔离发现'], ['L3', '会话加载'], ['L4', '能力验证']];
  const labels = {PASS:'已验证', FAIL:'失败', UNKNOWN:'未验证', NOT_APPLIED:'待应用', SKIPPED:'无需检查'};
  function node(tag, text, cls) {
    const n = document.createElement(tag);
    if (text !== undefined) n.textContent = text;
    if (cls) n.className = cls;
    return n;
  }
  function layerName(id, state) {
    return ({defaults:'全局默认', project:state.index.owner === 'global' ? '全局默认' : '项目共享', local:'本机覆盖', session:'临时会话'})[id] || (id?.startsWith('extends:') ? `继承 ${id.slice(8)}` : '未设置');
  }
  function selection(state, item) {
    const id = item.skill.id, shared = state.profile?.skills || {};
    const retired = (state.retirements || []).some(r => r.id === id);
    const sharedEnabled = !retired && Boolean(shared.enable?.includes(id) && !shared.disable?.includes(id));
    const resolved = Boolean(state.effective_profile) && !state.profile_error && !state.recovery_required;
    const effective = resolved ? !retired && Boolean(state.effective_profile.skills?.enable?.includes(id) && !state.effective_profile.skills?.disable?.includes(id)) : null;
    const events = (state.profile_report?.trace || []).filter(e => e.path === `/skills/${id}`);
    const pinEvents = (state.profile_report?.trace || []).filter(e => e.path === `/skills/pins/${id}`);
    const origin = events.length ? layerName(events.at(-1).layer, state) : resolved ? '默认停用' : '未解析';
    const chain = [...new Set(events.map(e => layerName(e.layer, state)))];
    return {shared:sharedEnabled, effective, origin, chain, retired, pinOrigin:pinEvents.length ? layerName(pinEvents.at(-1).layer, state) : '资产固定版本', pin:state.effective_profile?.skills?.pins?.[id] || item.skill.revision};
  }
  const path = value => value?.replace(/\\/g, '/').replace(/\/$/, '').toLocaleLowerCase();
  function boundReport(value, state) {
    const sourceRoot = path(state.root), parent = sourceRoot?.slice(0, sourceRoot.lastIndexOf('/'));
    return Boolean(value && !state.recovery_required && value.owner === state.index.owner && value.scope_id === state.index.scope_id && (value.copy || '') === copy && path(value.root) === parent);
  }
  function currentReport(state) {
    return boundReport(report, state) ? report : null;
  }
  function resolvedVersion(state, item, selected) {
    return state.selected_skills?.find(s=>s.skill.id===item.skill.id&&s.skill.revision===selected.pin)?.skill || [item.skill,...(item.available_versions||[])].find(s=>s.revision===selected.pin);
  }
  function evidence(state, item, toolID) {
    const current = currentReport(state), tool = current?.tools?.find(t => t.tool === toolID);
    const selected = selection(state, item);
    const resolvedSkill = resolvedVersion(state, item, selected);
    const skill = tool?.skills?.find(s => resolvedSkill && s.id === resolvedSkill.id && s.revision === resolvedSkill.revision && s.native_name === resolvedSkill.native_name);
    const states = Object.fromEntries(levels.map(([level]) => [level, skill?.[level] || (level === 'L2' ? skill?.status : '') || 'UNKNOWN']));
    const stale = Boolean(current?.stale);
    let note = !current ? '尚无当前检查记录' : stale ? `记录已过期：${current.stale_reason || '请重新检查'}` : !tool ? '本次记录未覆盖此工具' : !skill ? '本次记录未覆盖此技能的当前版本' : skill.file_detail || '旧记录未提供逐技能文件证据';
    if (selected.effective === false) note = selected.retired ? '技能已退役，不参与工具应用' : '有效配置已停用，不参与工具应用';
    if (selected.effective === null) note = '有效配置未解析，先处理配置检查';
    const usable = !stale && selected.effective === true && Boolean(skill);
    // A still-current independent probe may supplement this file observation.
    // Full-publication candidate checks below always remain historical.
    const last = bundle?.last, oldTool = last?.tools?.find(t=>t.tool===toolID);
    if (usable && states.L1==='PASS' && boundReport(last,state) && !last.stale && last.source_snapshot && last.source_snapshot === current.source_snapshot && oldTool?.installed && tool?.installed && oldTool.target === tool.target && oldTool.binary_digest && oldTool.binary_digest === tool.binary_digest) {
      const oldSkill=oldTool.skills?.find(s=>s.id===resolvedSkill.id&&s.revision===resolvedSkill.revision&&s.native_name===resolvedSkill.native_name);
      if (oldSkill) states.L2=oldSkill.L2 || oldSkill.status || 'UNKNOWN';
    }
    if (!usable) for (const [level] of levels) states[level] = 'UNKNOWN';
    const history=(current&&resolvedSkill?bundle?.publications||[]:[]).flatMap(p=>{
      const check=p.history?.check;
      if(!boundReport(check,state)||check.stale!==true||p.member?.id!==copy||path(p.member?.root)!==path(state.root))return[];
      const old=check.tools?.find(t=>t.tool===toolID)?.skills?.find(s=>s.id===resolvedSkill.id&&s.revision===resolvedSkill.revision&&s.native_name===resolvedSkill.native_name);
      return old?[{publication:p.publication_id,created:check.created,L2:old.L2||old.status||'UNKNOWN'}]:[];
    });
    return {states, stale, note, skill, tool, report:current, usable, history};
  }
  function render(state, item) {
    const box = node('section', undefined, 'skill-tool-status');
    box.setAttribute('aria-label', `${item.skill.display_name || item.skill.native_name} 工具生效状态`);
    const selected = selection(state, item);
    box.append(node('p', selected.effective === null ? '有效配置未解析 · 查看 Profile 检查' : `有效${selected.effective ? '启用' : '停用'} · ${selected.origin}${selected.chain.length > 1 ? ` · 覆盖：${selected.chain.join(' → ')}` : ''}`, 'skill-selection'));
    const applicableTools = state.index.owner === 'global' ? [] : tools;
    if (!applicableTools.length) box.append(node('p', '全局库版本由各项目采纳后分别检查工具生效。', 'caption'));
    for (const [id, name] of applicableTools) {
      const result = evidence(state, item, id), row = node('div', undefined, 'skill-tool-row');
      row.append(node('strong', name));
      for (const [level, title] of levels) {
        const status = result.states[level];
        const badge = node('span', `${level} ${labels[status] || '未验证'}`, `skill-level ${status === 'PASS' ? 'check-pass' : status === 'FAIL' ? 'check-fail' : 'check-unknown'}`);
        badge.title = `${title}：${labels[status] || '未验证'}。${result.note}`;
        row.append(badge);
      }
      box.append(row);
    }
    const details = node('details', undefined, 'skill-evidence-detail');
    details.append(node('summary', '版本、来源与检查详情'));
    details.append(node('code', `共享固定修订：${item.skill.revision}`),node('code', `最终固定修订：${selected.pin}`), node('small', `版本来源：${selected.pinOrigin}${selected.pin !== item.skill.revision ? ' · 工具应用使用最终配置选中的完整版本' : ''}`));
    for (const [id, name] of applicableTools) {
      const result = evidence(state, item, id);
      details.append(node('p', `${name} · ${result.note}`));
      if (result.usable && result.skill?.detail) details.append(node('small', `L2：${result.skill.detail}`));
      if (result.report) details.append(node('small', `检查时间：${result.report.created}${result.stale ? ' · 历史结论不代表当前状态' : ''}`));
      for(const old of result.history)details.append(node('small',`${name} · 完整批次历史候选 L2 ${labels[old.L2]||'未验证'} · ${old.created} · ${old.publication}；不代表当前会话加载或能力验证`));
    }
    details.append(node('small', 'L1 文件一致；L2 仅隔离项目发现；L3 实际会话加载；L4 能力验证。未验证项保持未验证。'));
    box.append(details);
    return box;
  }
  function clear(){report=bundle=null;}
  return {selection, evidence, render, bind:value=>{copy=value || '';clear();}, update:value=>{bundle=value?.schema_version===1&&value.current?value:null;report=bundle?bundle.current:value||null;}, clear};
})();
