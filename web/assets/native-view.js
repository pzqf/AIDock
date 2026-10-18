'use strict';
// Shared source-page session. Probes run only after explicit confirmation;
// File installation needs a fresh explicit discovery report and confirmation.
const NativePreview = (() => {
  const el = id => document.getElementById(id);
  let context = null, plan = null, profileApplication = null, fullPreparation = false, lastCheck = null, generation = 0, checkGeneration = 0, configurationGeneration = 0, probeSelection = null, probeRules = false, probePrepare = false;
  const resolutions = new Map();
  const unifiedPlan = () => fullPreparation && plan ? {...plan,application_scope:'full',write_supported:Boolean(profileApplication)&&plan.write_supported,...(profileApplication?{id:profileApplication.id,digest:profileApplication.digest,profile_application:profileApplication}:{})} : plan;
  const hasChanges = p => p?.profile_application ? p.profile_application.changes > 0 : p?.targets?.some(t=>t.changes?.length || t.rule?.changes?.length);
  const applicationScope = () => el('native-application-scope').value || 'full';
  const scopeLabel = scope => scope === 'skills-rules' ? '仅技能与规则' : '技能、规则与全部有效 MCP';
  function renderExcluded(id, checks) {
    const container = el(id); container.replaceChildren();
    if (!checks?.length) return;
    const detail = node('details'); detail.append(node('summary', `MCP 独立管理 · ${checks.length} 项范围外诊断`), node('p', '以下结果保留供审查；本次技能与规则应用不处理 MCP 连接，不能据此判定 MCP 已生效。'));
    for (const finding of checks) detail.append(node('p', `${finding.status} · ${finding.code}：${finding.detail}`));
    container.append(detail);
  }
  const ruleDecisions = (chosen = selection()) => chosen.filter(id=>resolutions.has(id)).map(id=>resolutions.get(id));
  const node = (tag, text) => { const n = document.createElement(tag); if (text !== undefined) n.textContent = text; return n; };
  function sync() {
    el('native-recheck').disabled = !context || context.busy() || context.pending() || !plan || (fullPreparation && !profileApplication);
    el('native-apply').disabled = !context || context.busy() || context.pending() || !unifiedPlan()?.write_supported || unifiedPlan().blocked;
    for (const id of ['native-prepare', 'native-check', 'native-probe', 'native-rule-probe', 'native-last-check', 'native-probe-confirm-run']) el(id).disabled = !context || context.busy() || context.pending();
    el('native-probe-cancel').disabled = Boolean(context?.busy());
    el('native-config-check').disabled = !context || context.busy();
    el('native-application-scope').disabled = !context || context.busy() || context.pending();
    if (typeof NativeRehearsal !== 'undefined') NativeRehearsal.sync();
    if (typeof NativeWrite !== 'undefined') NativeWrite.sync();
    if (typeof ProfileApplicationView !== 'undefined') ProfileApplicationView.sync();
  }
  function invalidate(clearCheck = true, keepDecisions = false) {
    configurationGeneration++;
    el('native-config-result').hidden = true;
    if (clearCheck && !keepDecisions) resolutions.clear();
    generation++;
    plan = null; profileApplication = null; fullPreparation = false;
    el('native-result').hidden = true;
    el('native-prepare-result').hidden = true;
    if (clearCheck) { lastCheck = null; checkGeneration++; renderCheck(null); closeProbe(); }
    if (typeof NativeRehearsal !== 'undefined') NativeRehearsal.invalidate();
    if (typeof NativeWrite !== 'undefined') NativeWrite.invalidate();
    if (typeof ProjectSnapshotView !== 'undefined') ProjectSnapshotView.invalidate();
    if (typeof ProfileApplicationView !== 'undefined') ProfileApplicationView.invalidate();
    sync();
  }
  function closeProbe() {
    el('native-probe-confirm').close?.(); el('native-probe-confirm').hidden = true; probeSelection = null; probeRules = false; probePrepare = false;
  }
  function selection() { return ['claude-code', 'codex-cli'].filter(id => el(`native-${id}`).checked); }
  async function inspectConfiguration() {
    if (!context || context.busy()) return;
    const tools = selection();
    if (!tools.length) { context.message('请选择至少一个工具。', true); return; }
    const current = ++configurationGeneration;
    el('native-config-result').hidden = true;
    context.setBusy(true);
    try {
      const data = await context.api('/api/native/config', {tools});
      if (current !== configurationGeneration) return;
      const report = data.items;
      el('native-config-summary').textContent = report.status === 'FAIL' ? '项目 MCP 配置 · 发现需处理的问题' : '项目 MCP 配置 · 已读取字段归属';
      el('native-config-boundary').textContent = report.boundary;
      el('native-config-time').textContent = `${new Date(report.created).toLocaleString('zh-CN')} · 当前磁盘读取；外部编辑后请重新检查`;
      const files = el('native-config-files'); files.replaceChildren();
      for (const file of report.files || []) {
        const card = node('article'); card.className = 'native-check-card';
        card.append(node('h3', {'claude-code':'Claude Code','codex-cli':'Codex CLI'}[file.tool] || file.tool), node('code', file.contract.path));
        card.append(node('p', {ABSENT:'项目文件不存在 · 其它配置层未检查', READABLE:'语法与服务器结构可读取 · 实际加载未验证', INVALID:'配置读取或结构检查失败 · 原文件保留', DRIFT:'托管字段被外部修改 · 保留手改'}[file.state] || '检查结果未知'));
        if (file.state === 'READABLE' || file.state === 'DRIFT') {
          card.append(node('p', `${file.fields.length} 个服务器项 · ${file.fields.filter(f=>f.ownership==='MANAGED').length} 个已登记托管`));
          for (const field of file.fields) {
            const item = node('p', `${field.name} · ${field.ownership==='MANAGED'?'当前项目副本已登记且字节一致':field.ownership==='DRIFT'?'已托管字段存在手改，先协调':field.aidock_prefix?'未登记的 AIDock 前缀项，不能自动接管':'未托管，保留原值'}`);
            if (field.ownership!=='MANAGED' && field.aidock_prefix) item.className = 'native-health pending';
            card.append(item);
          }
        }
        for (const check of file.checks || []) {
          if (check.status === 'FAIL') {
            const finding = node('p', check.detail); finding.className = 'error'; card.append(finding);
          }
        }
        const technical = node('details');
        technical.append(node('summary','检查摘要与管理范围'),node('p',`适配器 ${file.adapter_version} · ${file.contract.format.toUpperCase()} · ${file.contract.namespace} 的独立服务器项`),node('p','在 MCP 项目授权页单独预览安装、移除或回退连接；保留模型设置、用户配置和其它字段。'));
        if (file.sha256) technical.append(node('code',file.sha256),node('p',`${file.bytes} B · 完整项目文件摘要`));
        for (const check of file.checks || []) technical.append(node('p',`${check.status} · ${check.code}：${check.detail}`));
        if (file.contract.reference) {
          const reference = node('a','官方 MCP 配置文档'); reference.href = file.contract.reference; reference.target = '_blank'; reference.rel = 'noopener noreferrer';
          technical.append(reference,node('p',`配置合同核对 ${file.contract.contract_checked}`));
        }
        card.append(technical); files.append(card);
      }
      el('native-config-result').hidden = false; el('native-config-result').open = true;
      context.message(report.status === 'FAIL' ? '项目 MCP 配置存在读取或结构问题；原文件保留，详情见检查结果。' : '已读取项目 MCP 字段归属；配置值不显示，原文件保留。', report.status === 'FAIL');
    } catch (error) {
      if (current === configurationGeneration) context.message(error.message, true);
    } finally { context.setBusy(false); sync(); }
  }
  function renderCheck(report, current = false, evidence = report) {
    lastCheck = current ? evidence?.last&&!evidence.last.stale?evidence.last:null : report;
    context?.evidence?.(evidence);
    el('native-evidence-history').replaceChildren();
    el('native-evidence-history').hidden=true;
    el('native-check-result').hidden = !report;
    if (!report) return;
    el('native-check-result').open = true;
    renderExcluded('native-check-excluded', report.excluded_checks);
    el('native-check-summary').textContent = report.rule_probe ? '工具发现与规则加载检查' : report.probe ? '工具发现检查' : '项目文件检查';
    el('native-check-summary').textContent += ` · ${scopeLabel(report.application_scope)}`;
    el('native-check-freshness').textContent = report.stale ? '已过期 · 请重新检查' : current ? '本次只读文件核对' : '检查记录';
    el('native-check-freshness').className = report.stale ? 'badge check-stale' : 'badge';
    el('native-check-time').textContent = `${new Date(report.created).toLocaleString('zh-CN')} · ${report.stale ? report.stale_reason : '实际项目会话生效尚未验证'}`;
    el('native-check-boundary').textContent = report.boundary;
    el('native-check-id').textContent = current ? '本次观察不保存为检查记录，不提供安装或应用资格。' : `检查 ${report.id} · ${report.digest}`;
    el('native-check-tools').replaceChildren();
    const label = {PASS:'已验证', FAIL:'检查失败', UNKNOWN:'未验证', SKIPPED:'无需检查', NOT_APPLIED:'待应用'};
    for (const tool of report.tools) {
      const card = node('article'); card.className = 'native-check-card';
      const head = node('div'); head.className = 'list-heading';
      head.append(node('h3', {'claude-code':'Claude Code', 'codex-cli':'Codex CLI'}[tool.tool] || tool.tool));
      const installed = node('span', tool.installed ? tool.version ? `v${tool.version}` : '已安装' : '未找到程序'); installed.className = 'badge'; head.append(installed); card.append(head);
      const layers = node('dl'); layers.className = 'native-evidence-layers';
      for (const [level, title] of [['L1','项目文件一致'], ['L2','隔离项目发现'], ['L3','实际会话加载'], ['L4','能力验证']]) {
        const state = node('dd', report.stale ? `${label[tool[level]] || tool[level]}（历史）` : label[tool[level]] || tool[level]);
        state.className = report.stale ? 'check-unknown' : tool[level] === 'PASS' ? 'check-pass' : tool[level] === 'FAIL' ? 'check-fail' : 'check-unknown';
        layers.append(node('dt', title), state);
      }
      card.append(layers);
      for (const skill of tool.skills || []) {
        const line = node('p', `${skill.native_name} · 文件 ${label[skill.L1] || '未验证'} · 隔离发现 ${label[skill.L2 || skill.status] || '未验证'}${report.stale ? '（历史）' : ''}`);
        line.className = skill.status === 'FAIL' ? 'check-fail' : ''; card.append(line);
        const detail = node('small', current ? skill.file_detail || '未提供逐技能文件证据' : skill.detail); card.append(detail);
      }
      if (tool.rules) {
        const rules = node('section'); rules.className = 'native-rule-evidence';
        const status = report.stale ? '历史证据 · 待重新核对' : tool.rules.status === 'PASS' ? '隔离临时会话已加载' : tool.rules.status === 'FAIL' ? '规则检查失败' : '规则加载未验证';
        rules.append(node('h4', `${tool.rules.path} · ${status}`), node('p', tool.rules.detail));
        rules.className += !report.stale && tool.rules.status === 'FAIL' ? ' check-fail' : ' check-unknown';
        card.append(rules);
      }
      const details = node('details'); details.append(node('summary', '目录与诊断详情'), node('code', tool.target));
      if (tool.executable) details.append(node('code', tool.executable));
      for (const finding of tool.findings || []) {
        const line = node('p', `${finding.code}：${finding.detail}`);
        if (finding.status === 'FAIL') { line.className = 'check-fail'; card.append(line); } else details.append(line);
      }
      card.append(details); el('native-check-tools').append(card);
    }
  }
  function renderEvidence(data) {
    if(data?.schema_version!==1||!data.current||!Array.isArray(data.current.tools)||!Array.isArray(data.publications))throw Error('生效证据格式不完整，请重新核对。');
    renderCheck(data.current,true,data);
    const box=el('native-evidence-history');
    for(const warning of data.warnings||[])box.append(node('p',`${({applications:'已应用状态',publications:'完整批次历史',last:'独立检查历史'})[warning.area]||'历史证据'}暂不可读：${warning.code}。当前文件状态单独核对，未采用部分历史。`));
    if(data.last){const d=node('details');d.append(node('summary',`最近独立检查 · ${data.last.stale?'已过期':'输入仍匹配'}`),node('p',`${data.last.created} · ${data.last.stale?data.last.stale_reason:'L2 仅代表隔离项目发现，L3/L4 仍单独验证。'}`));box.append(d);}
    for(const p of data.publications){
      const d=node('details');d.append(node('summary',`完整批次候选历史 · ${p.member.name}`),node('p',`${p.history.check.created} · ${p.publication_id}`),node('p',p.history.boundary));
      for(const tool of p.history.check.tools||[])d.append(node('p',`${{'claude-code':'Claude Code','codex-cli':'Codex CLI'}[tool.tool]||tool.tool} · 历史隔离发现 ${tool.L2==='PASS'?'已通过':tool.L2==='SKIPPED'?'无需检查':'未通过或未验证'}；不是当前会话结论`));
      d.append(node('p',`原发起副本：${p.initiator.name}。进入该副本的跨副本页面查看完整批次和恢复记录。`));
      if(p.initiator_binding){const b=node('button','打开原发起副本');b.type='button';b.className='btn ghost';b.addEventListener('click',()=>{if(!context.busy()&&typeof ProjectsView!=='undefined')ProjectsView.openBinding(p.initiator_binding);});d.append(b);}
      box.append(d);
    }
    if(data.truncated)box.append(node('p',`仅列最近 ${data.publications.length} 个相关批次，共 ${data.publications_total} 个；其它历史可到原发起副本查看。`));
    box.hidden=!box.children.length;
  }
  async function restore() {
    if (!context || context.busy() || context.pending()) return;
    const requestGeneration = ++checkGeneration;
    renderCheck(null);
    try {
      const data = await context.api('/api/native/evidence');
      if (requestGeneration === checkGeneration) {
        renderEvidence(data.items);
        context.message(`当前技能文件已核对${data.items.publications.length?`，已关联 ${data.items.publications.length} 个完整批次候选历史`:''}${data.items.warnings?.length?'；部分历史暂不可读，请查看详情':''}；实际会话与能力仍须分别验证。`,Boolean(data.items.warnings?.length));
      }
    } catch (error) { if (requestGeneration === checkGeneration) { renderCheck(null); context.message(error.message, true); } }
  }
  async function check(probe, chosen = selection(), ruleProbe = false) {
    if (!context || context.busy() || context.pending()) return;
    if (!chosen.length) { context.message('请选择至少一个工具。', true); return; }
    closeProbe();
    const requestGeneration = ++checkGeneration;
    context.setBusy(true);
    el('native-probe').textContent = probe ? '正在验证…' : '验证工具发现';
    try {
      const data = await context.api('/api/native/check', {tools:chosen, application_scope:applicationScope(), probe, ...(ruleProbe ? {rule_probe:true} : {}), ...(resolutions.size ? {rule_resolutions:ruleDecisions(chosen)} : {})});
      if (requestGeneration !== checkGeneration) return;
      renderCheck(data.items);
      context.message(ruleProbe ? '隔离规则加载检查已完成；仅覆盖独立候选，实际项目 L3/L4 仍待验证。' : probe ? '隔离发现检查已完成；请查看各项结果。实际项目文件和会话状态分别显示。' : '项目文件状态已检查，工具进程未启动。');
    } catch (error) {
      if (requestGeneration === checkGeneration) { renderCheck(null); context.message(error.message, true); }
    } finally { context.setBusy(false); el('native-probe').textContent = '验证工具发现'; sync(); }
  }
  function renderPreparation(report) {
    const ready = report.status === 'READY' && report.plan.write_supported && !report.plan.blocked;
    const changed=hasChanges(report.plan);
    el('native-check-result').open = !ready;
    el('native-prepare-title').textContent = ready ? changed ? '已准备，可核对文件变化' : '工具文件已与保存版本一致' : report.status === 'BLOCKED' ? '需要先处理阻断项' : '检查完成，仍有待处理条件';
    el('native-prepare-note').textContent = ready ? changed ? '查看下方各工具的拟议变化，再确认安装。安装会自动留存恢复点。' : '本次无文件变化。实际会话加载仍需独立验证；已安装记录可回退。' : '查看下方工具检查中的具体原因，处理后重新准备应用。当前预览可查看，尚不可安装。';
    el('native-prepare-stages').replaceChildren();
    const tools = report.check.tools || [];
    const discovery = tools.every(t=>t.L2==='PASS'||t.L2==='SKIPPED') ? '已通过' : tools.some(t=>t.L2==='FAIL') ? '检查失败' : '尚未通过';
    const rules = tools.filter(t=>t.rules);
    const ruleStatus = !rules.length ? report.plan.blocked && report.plan.source?.rules?.length ? '等待解决阻断' : '本次无规则' : rules.every(t=>t.rules.status==='PASS') ? '已通过' : '尚未通过';
    for (const [title, status] of [['源与目标',report.plan.blocked ? '存在冲突' : '已核对'],['工具发现',discovery],['规则来源',ruleStatus],['文件预览',ready ? changed ? '待确认安装' : '已一致' : '仅可查看']]) {
      const item=node('li');item.append(node('strong',title),node('span',status));el('native-prepare-stages').append(item);
    }
    el('native-prepare-gates').replaceChildren();
    for (const gate of report.gates || []) el('native-prepare-gates').append(node('p',gate.detail));
    if(report.mcp){for(const entry of report.mcp.entries||[])el('native-prepare-gates').append(node('p',entry.name+' · '+(entry.status==='READY'?'已有 MCP 安装、授权与运行时记录已核对':'需处理 '+entry.code)));}
    el('native-prepare-boundary').textContent=report.boundary;
    el('native-prepare-result').hidden=false;
  }
  async function prepare(chosen) {
    if (!context || context.busy() || context.pending() || !chosen?.length) return;
    const decisions=ruleDecisions(chosen);
    closeProbe();invalidate(true,true);
    const requestGeneration=generation;
    context.setBusy(true);
    el('native-prepare').textContent='正在检查并准备…';
    context.message('正在核对源与目标、验证所选工具并生成预览。检查只使用磁盘上的已保存版本。');
    try {
      const joint=applicationScope()==='full';
      const data=await context.api(joint?'/api/native/profile/prepare':'/api/native/prepare',{tools:chosen,application_scope:applicationScope(),probe:true,rule_probe:true,...(decisions.length ? {rule_resolutions:decisions} : {})});
      if(requestGeneration!==generation)return;
      const report=joint?{...data.items,plan:data.items.native_plan}:data.items;
      renderCheck(report.check);plan=report.plan;fullPreparation=joint;profileApplication=joint?data.items.plan||null:null;
      render(unifiedPlan());renderPreparation({...report,plan:unifiedPlan()});
      const changes=hasChanges(unifiedPlan());
      context.message(data.items.status==='READY' ? changes ? '工具应用已准备，请核对文件变化后确认安装；实际会话生效仍待验证。' : '工具文件已一致，无需重复安装；实际会话生效仍待验证。' : '检查与预览已完成，尚有待处理条件，请查看具体原因。',data.items.status==='BLOCKED');
    } catch(error) {
      if(requestGeneration===generation){invalidate();context.message(error.message,true);}
    } finally {context.setBusy(false);el('native-prepare').textContent='检查并准备应用';sync();}
  }
  function render(p) {
    el('native-result').hidden = false;
    el('native-summary').textContent = `${p.blocked ? '存在阻断项' : '预览已生成'} · ${p.targets.length} 个工具 · ${p.write_supported ? '可安装工具文件' : p.targets.some(t => t.rule) ? '规则加载待验收 · 可独立预演' : '需要当前工具发现检查'}`;
    el('native-summary').textContent += ` · ${p.profile_application?'技能、规则与全部有效 MCP 联合应用':scopeLabel(p.application_scope)}`;
    renderExcluded('native-excluded-checks', p.profile_application ? [] : p.excluded_checks);
    if(p.profile_application){const info=node('p',p.profile_application.request.sessions.length+' 个有效 MCP 已核对安装、独立授权与运行时；连接变化与技能规则共用一次确认及恢复。');el('native-excluded-checks').append(info);}
    el('native-plan-id').textContent = `计划 ${p.id} · ${p.digest}`;
    el('native-evidence').textContent = `L1 ${p.evidence.L1} · L2 ${p.evidence.L2} · L3 ${p.evidence.L3} · L4 ${p.evidence.L4}。生成预览不证明实际生效。`;
    el('native-boundary').textContent = p.profile_application?.boundary || p.boundary;
    el('native-targets').replaceChildren();
    for (const t of p.targets) {
      const card = node('article'); card.className = 'native-target';
      const toolName = {'claude-code':'Claude Code', 'codex-cli':'Codex CLI'}[t.adapter.id] || t.adapter.id;
      card.append(node('h3', toolName));
      const health = node('p', t.status === 'FAIL' ? '存在目标冲突或静态错误，预览已阻断' : '完整包预览已生成，实际工具生效待验证');
      health.className = t.status === 'FAIL' ? 'native-health error' : t.status === 'UNKNOWN' ? 'native-health pending' : 'native-health'; card.append(health);
      card.append(node('p', `${t.packages.length} 个已启用完整包 · ${(t.changes || []).length} 项拟议路径变化`));
      if (t.rule) {
        const rule = node('section'); rule.className = 'native-rule-preview';
        rule.append(node('h4', `规则 · ${t.rule.path}`), node('p', t.rule.managed ? `${t.rule.changes.length ? '待写入托管区块' : '与已生成文件一致'} · ${t.rule.changes.length} 项文件变化` : '存在规则冲突 · 原文件保留'));
        if (t.rule.managed) {
          rule.append(node('p', 'AIDock 托管内容'), node('pre', t.rule.managed));
          const manual = node('details'); manual.append(node('summary', '手写内容 · 只读保留'), node('pre', t.rule.handwritten_before || '（前部无手写内容）'), node('pre', t.rule.handwritten_after || '（后部无手写内容）'));
          rule.append(manual);
        } else rule.append(node('p', '规则目标存在阻断项，原文件保留。'));
        const conflict = (t.findings || []).some(f=>['RULES_MANAGED_DRIFT','RULES_OWNERSHIP_REQUIRED'].includes(f.code));
        if (conflict || t.rule.resolution) {
          if (t.rule.observed_body !== undefined && t.rule.observed_body !== null) {
            const observed=node('details');observed.append(node('summary','当前文件的托管正文'),node('pre',t.rule.observed_body));rule.append(observed);
          }
          const actions=node('div');actions.className='source-actions';
          if (conflict) {
            if (t.rule.observed_body !== undefined && t.rule.observed_body !== null) {
              const keep=node('button','保留手改并合并到源');keep.type='button';keep.className='btn';keep.disabled=context.busy() || context.pending();
              keep.addEventListener('click',()=>{if(!context.busy()&&!context.pending()&&typeof ProfileView!=='undefined')ProfileView.importRules({tool:t.adapter.id,sha256:t.rule.before.entries.content.sha256,body:t.rule.observed_body});});actions.append(keep);
            }
            const overwrite=node('button','覆盖回规范源');overwrite.type='button';overwrite.className='btn';
            overwrite.addEventListener('click',()=>{if(context.busy()||context.pending())return;resolutions.set(t.adapter.id,{tool:t.adapter.id,action:'overwrite',observed_digest:t.rule.observed_digest});invalidate(true,true);run(false);});actions.append(overwrite);
          } else rule.append(node('p','本次明确选择覆盖回规范源；尚未写入，确认应用后可回退到当前手改文件。'));
          const postpone=node('button','稍后处理');postpone.type='button';postpone.className='btn ghost';
          postpone.addEventListener('click',()=>{if(context.busy()||context.pending())return;resolutions.delete(t.adapter.id);invalidate(true,true);context.message('规则冲突已暂缓；原生文件与共享源保留。');});actions.append(postpone);rule.append(actions);
        }
        card.append(rule);
      }
      const technical = node('details'); technical.append(node('summary', '目标目录与适配器证据'), node('code', t.root), node('p', `适配器 ${t.adapter.version} · 官方契约核对 ${t.adapter.contract_checked} · 文件安装版本 ${(t.adapter.verified_versions || []).join('、') || '尚未验收'}`));
      const reference = node('a', '官方技能文档'); reference.href = t.adapter.reference; reference.target = '_blank'; reference.rel = 'noopener noreferrer'; technical.append(reference); card.append(technical);
      for (const pkg of t.packages) {
        const detail = node('details');
        detail.append(node('summary', `${pkg.native_name} · ${pkg.layer} · ${Object.keys(pkg.tree.entries).length} 项包内容`), node('code', pkg.revision));
        for (const path of Object.keys(pkg.tree.entries).sort()) {
          const e = pkg.tree.entries[path];
          detail.append(node('p', e.directory ? `${path}/ · 目录` : `${path} · ${e.bytes} B · ${e.sha256}`));
        }
        card.append(detail);
      }
      if (t.changes?.length) {
        const changes = node('details'); changes.append(node('summary', `查看 ${t.changes.length} 项拟议路径变化`));
        for (const c of t.changes) changes.append(node('p', `${c.before ? (c.after ? '修改' : '移除已归属输出') : '新增'} · ${c.path} · ${c.before?.bytes || 0} → ${c.after?.bytes || 0} B`));
        card.append(changes);
      }
      const diagnostics = node('details'); diagnostics.append(node('summary', '未验证条件与检查详情'));
      for (const f of t.findings || []) {
        const line = node('p', `${f.status} · ${f.code}：${f.detail}`);
        if (f.status === 'FAIL') line.className = 'error';
        if (f.status === 'FAIL') card.append(line); else diagnostics.append(line);
      }
      if (diagnostics.children.length > 1) card.append(diagnostics);
      el('native-targets').append(card);
    }
    sync();
  }
  async function run(recheck) {
    if(recheck && fullPreparation && !profileApplication){context?.message('先处理全部 MCP 条件，再重新准备完整应用；当前不会安装部分 Profile。',true);return;}
    if (recheck && profileApplication && context && !context.busy() && !context.pending()) {
      const current=profileApplication,g=generation;context.setBusy(true);
      try{const data=await context.api('/api/native/diff',{plan_id:current.id,digest:current.digest});if(g===generation){profileApplication=data.items;render(unifiedPlan());context.message('联合预览的技能、规则、MCP 授权和目标仍一致。');}}
      catch(e){if(g===generation){invalidate();context.message(e.message,true);}}
      finally{context.setBusy(false);sync();}return;
    }
    if (!context || context.busy() || context.pending() || recheck && !plan) return;
    const selected = ['claude-code', 'codex-cli'].filter(id => el(`native-${id}`).checked);
    if (!recheck && !selected.length) { invalidate(); context.message('请选择至少一个工具。', true); return; }
    const previous = plan;
    const eligibleCheck = (lastCheck?.application_scope || 'full') === applicationScope() && JSON.stringify(lastCheck?.rule_resolutions || []) === JSON.stringify(ruleDecisions(selected)) && lastCheck?.probe && !lastCheck.stale && !lastCheck.tools.some(t => t.findings?.some(f => f.code === 'RULES_SESSION_UNVERIFIED') && !(lastCheck.rule_probe && t.rules?.status === 'PASS' && t.rules?.code === 'RULES_ISOLATED_LOAD')) && selected.length === lastCheck.tools.length && selected.every(id => lastCheck.tools.some(t => t.tool === id)) ? lastCheck.id : undefined;
    invalidate(false);
    const requestGeneration = generation;
    context.setBusy(true);
    try {
      const data = await context.api(recheck ? '/api/native/diff' : '/api/native/plan', recheck ? {plan_id: previous.id, digest: previous.digest} : {tools: selected, application_scope:applicationScope(), ...(resolutions.size ? {rule_resolutions:ruleDecisions(selected)} : {}), ...(eligibleCheck ? {check_id:eligibleCheck} : {})});
      if (requestGeneration !== generation) return;
      plan = data.items;
      render(plan);
      context.message(recheck ? '预览输入仍一致；请查看文件安装条件和实际生效边界。' : '已根据磁盘保存版本生成预览；页面未保存草稿保留，不进入此计划。');
    } catch (error) {
      if (requestGeneration === generation) { invalidate(false); context.message(error.message, true); }
    } finally { context.setBusy(false); sync(); }
  }
  function init(ctx, app) {
    context = ctx;
    el('nav-native').hidden = !app.native_root;
    el('native-binding').textContent = app.native_root ? `已绑定工具项目根：${app.native_root}` : '启动控制台时用 --native-root 显式绑定专用测试项目。';
    if (typeof NativeRehearsal !== 'undefined') NativeRehearsal.init(ctx, () => fullPreparation ? null : plan);
    if (typeof NativeWrite !== 'undefined') NativeWrite.init(ctx, unifiedPlan, () => invalidate());
    if (typeof ProfileApplicationView !== 'undefined') ProfileApplicationView.init({...ctx,prepare:()=>{el('native-application-scope').value='skills-rules';invalidate(true,true);openProbe(true,true);}},()=>plan,()=>invalidate());
    sync();
  }
  el('native-preview').addEventListener('click', () => run(false));
  el('native-recheck').addEventListener('click', () => run(true));
  el('native-check').addEventListener('click', () => check(false));
  el('native-config-check').addEventListener('click', inspectConfiguration);
  el('native-last-check').addEventListener('click', restore);
  function openProbe(rules, preparing = false) {
    if (!context || context.busy() || context.pending()) return;
    probeSelection = selection(); probeRules = rules; probePrepare = preparing;
    if (!probeSelection.length) { context.message('请选择至少一个工具。', true); return; }
    el('native-probe-title').textContent = preparing ? '检查并准备工具应用' : rules ? '验证规则的隔离会话加载' : '验证技能发现';
    el('native-probe-description').textContent = preparing ? `所选工具：${probeSelection.map(id=>({'claude-code':'Claude Code','codex-cli':'Codex CLI'})[id]).join('、')}。将核对已保存的技能与规则，在空配置独立项目验证工具发现和规则加载来源，然后生成文件安装预览。最多复制 32 KiB 规则与手写内容；不会将凭据或 MCP 带入检查。` : rules ? '将在空配置的独立临时项目检查技能发现，并为支持的工具创建临时会话，核对规则文件的加载来源。最多复制 32 KiB 规则与手写内容；不会将项目配置、凭据或 MCP 带入检查。' : '将在独立临时项目中初始化所选 CLI，复制已保存并启用的完整技能包，读取工具返回的技能列表。';
    el('native-probe-description').textContent += ` 应用内容：${scopeLabel(applicationScope())}。${applicationScope() === 'skills-rules' ? 'MCP 原生配置保留，连接和诊断独立管理。' : ''}`;
    el('native-probe-confirm').hidden = false; el('native-probe-confirm').showModal?.();
  }
  el('native-probe').addEventListener('click', () => openProbe(false));
  el('native-prepare').addEventListener('click', () => openProbe(true, true));
  el('native-rule-probe').addEventListener('click', () => openProbe(true));
  el('native-probe-cancel').addEventListener('click', () => { if (!context?.busy()) closeProbe(); });
  el('native-probe-confirm').addEventListener('cancel', event => { event.preventDefault(); if (!context?.busy()) closeProbe(); });
  el('native-probe-confirm-run').addEventListener('click', () => probeSelection && (probePrepare ? prepare(probeSelection) : check(true, probeSelection, probeRules)));
  for (const id of ['claude-code', 'codex-cli']) el(`native-${id}`).addEventListener('change', () => { invalidate(); checkGeneration++; closeProbe(); });
  el('native-application-scope').addEventListener('change', () => { invalidate(true,true); context?.message('应用内容已切换，请重新检查并准备；已有安装和页面草稿保留。'); });
  return {init, invalidate, sync, restore};
})();
