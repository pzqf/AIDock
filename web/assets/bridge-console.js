'use strict';
(() => {
  const $ = id => document.getElementById(id);
  let csrf = '', live = null, busy = false;
  const message = (text, error = false) => {
    $('message').textContent = text;
    $('message').classList.toggle('error', error);
  };
  const setBusy = value => {
    busy = value;
    $('check-bridge').disabled = value;
    $('close').disabled = value;
  };
  async function api(path, body) {
    const response = await fetch(path, {
      method: body === undefined ? 'GET' : 'POST', credentials: 'same-origin',
      headers: body === undefined ? {} : {'Content-Type': 'application/json', 'X-AIDock-CSRF': csrf},
      body: body === undefined ? undefined : JSON.stringify(body)
    });
    if (!response.ok) throw new Error(`请求失败（${response.status}），请重新打开控制台。`);
    const data = await response.json();
    if (data.ok === false) throw new Error(`${data.code}：${data.summary}`);
    return data;
  }
  async function check() {
    if (busy) return;
    setBusy(true);
    renderBridge(null);
    try {
      const report = (await api('/api/bridge')).items;
      renderBridge(report);
      message(report.status === 'FAIL' ? '外部源存在静态失败，完整候选不可用。' : report.status === 'STALE' ? '外部源已漂移，请核对候选与批准基线。' : '只读检查完成；工具发现和生成器能力尚未验证。', ['FAIL', 'STALE'].includes(report.status));
    } catch (error) { message(error.message, true); }
    finally { setBusy(false); }
  }
  $('check-bridge').addEventListener('click', check);
  $('close').addEventListener('click', async () => {
    if (busy) return;
    setBusy(true);
    try {
      await api('/api/close', {});
      live?.close();
      message('控制台已关闭，可以关闭此页面。');
    } catch (error) { message(error.message, true); setBusy(false); }
  });
  (async () => {
    setBusy(true);
    try {
      const token = new URLSearchParams(location.hash.slice(1)).get('token');
      history.replaceState(null, '', location.pathname);
      csrf = (await api('/api/session', token ? {token} : undefined)).csrf;
      const app = await api('/api/state');
      $('version').textContent = `v${app.version}`;
      $('binding').textContent = `登记文件：${app.bridge_file}`;
      live = new EventSource('/api/events');
      setBusy(false);
      await check();
    } catch (error) { message(error.message, true); }
  })();
})();
