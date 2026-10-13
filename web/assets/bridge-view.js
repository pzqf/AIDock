'use strict';
// Shared by the standalone read-only bridge page and the owned-source page.
function renderBridge(report) {
  const get = id => document.getElementById(id);
  get('bridge-panel').hidden = !report;
  if (!report) return;
  const labels = {PASS: '通过', WARN: '提示', UNKNOWN: '未验证', STALE: '已漂移', FAIL: '失败'};
  const node = (tag, text, cls) => {
    const element = document.createElement(tag);
    if (text !== undefined) element.textContent = text;
    if (cls) element.className = cls;
    return element;
  };
  get('bridge-summary').textContent = `外部规范源：${labels[report.status] || report.status} · ${report.bridges.length} 个登记 · 检查时间 ${report.checked_at}`;
  get('bridge-results').replaceChildren();
  for (const bridge of report.bridges) {
    const row = node('article', undefined, 'managed-skill');
    row.append(node('strong', `${bridge.scope}/${bridge.copy} · ${labels[bridge.status] || bridge.status}`));
    row.append(node('code', `${bridge.project} · ${bridge.id}`));
    row.append(node('small', `${bridge.root} / ${bridge.source_path}`));
    row.append(node('small', `${bridge.source_role} · ${bridge.format} · 生成器 ${bridge.generator || '未登记'} · 外部源只读`));
    if (bridge.current_revision) row.append(node('code', `当前候选 · ${bridge.current_revision}`));
    else row.append(node('p', '完整候选不可用；先处理扫描失败或缺失包。'));
    if (bridge.approved_revision) row.append(node('code', `登记的批准基线 · ${bridge.approved_revision}`));
    for (const finding of bridge.findings) row.append(node('p', `${labels[finding.status] || finding.status} · ${finding.code}：${finding.detail}`));
    for (const candidate of bridge.candidates) {
      const detail = node('details');
      detail.append(node('summary', `${candidate.skill.name} · ${candidate.skill.files.length} 个文件 · ${candidate.skill.path}`));
      if (candidate.revision) detail.append(node('code', `包候选 · ${candidate.revision}`));
      detail.append(node('code', `文件清单摘要 · ${candidate.skill.package_digest}`));
      for (const file of candidate.skill.files) detail.append(node('code', `${file.path} · ${file.type} · ${file.bytes} B · ${file.sha256}`));
      for (const finding of candidate.skill.findings) detail.append(node('p', `${finding.status} · ${finding.code}：${finding.detail}`));
      row.append(detail);
    }
    get('bridge-results').append(row);
  }
}
