'use strict';
(() => {
  let theme = null;
  try { theme = localStorage.getItem('aidock-theme'); } catch {}
  if (!theme && window.matchMedia && matchMedia('(prefers-color-scheme: dark)').matches) theme = 'dark';
  document.documentElement.setAttribute('data-theme', theme === 'dark' ? 'dark' : 'light');
})();
