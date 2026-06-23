'use strict';
// gopaste admin console. Talks to the gated /admin/api endpoints. On 401 it
// bounces to /admin/login (session expired).

const $ = (id) => document.getElementById(id);
let pastes = [];

function api(method, path, body) {
  return fetch(path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  }).then((r) => {
    if (r.status === 401) { location.href = '/admin/login'; throw new Error('unauthorized'); }
    if (!r.ok) throw new Error('HTTP ' + r.status);
    return r.status === 204 ? null : r.json();
  });
}

function humanBytes(n) {
  if (n < 1024) return n + ' B';
  const u = ['KB', 'MB', 'GB', 'TB'];
  let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
  return n.toFixed(n < 10 ? 1 : 0) + ' ' + u[i];
}

function rel(sec, future) {
  const d = Math.abs(Date.now() / 1000 - sec);
  const units = [[31536000, 'y'], [2592000, 'mo'], [86400, 'd'], [3600, 'h'], [60, 'm']];
  for (const [s, label] of units) if (d >= s) {
    const v = Math.floor(d / s);
    return future ? 'in ' + v + label : v + label + ' ago';
  }
  return future ? 'soon' : 'just now';
}

function fmtDate(sec) {
  const dt = new Date(sec * 1000);
  return dt.toISOString().slice(0, 16).replace('T', ' ');
}

function esc(s) {
  return s.replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
}

function render() {
  const q = $('filter').value.trim().toLowerCase();
  const rows = $('rows');
  const shown = pastes.filter((p) => !q || p.key.toLowerCase().includes(q));
  if (!shown.length) {
    rows.innerHTML = '<tr><td colspan="5" class="muted pad-lg">no pastes' + (q ? ' match the filter' : '') + '</td></tr>';
    return;
  }
  rows.innerHTML = shown.map((p) => {
    const builtin = p.builtin;
    const created = p.created ? `${fmtDate(p.created)} &middot; ${rel(p.created, false)}` : '<span class="muted">unknown</span>';
    const expires = p.expiration
      ? `<span class="pill warn">${rel(p.expiration, true)}</span>`
      : '<span class="pill never">never</span>';
    const del = builtin
      ? '<button class="iconbtn del" disabled title="built-in document">delete</button>'
      : `<button class="iconbtn del" data-key="${esc(p.key)}">delete</button>`;
    return `<tr data-key="${esc(p.key)}">
      <td class="key"><a href="/${encodeURIComponent(p.key)}" target="_blank" rel="noopener">${esc(p.key)}</a>${builtin ? ' <span class="muted">(built-in)</span>' : ''}</td>
      <td class="size">${humanBytes(p.size)}</td>
      <td class="muted">${created}</td>
      <td>${expires}</td>
      <td><div class="row-actions">
        <a class="iconbtn" href="/raw/${encodeURIComponent(p.key)}" target="_blank" rel="noopener">raw</a>
        ${del}
      </div></td>
    </tr>`;
  }).join('');
}

function confirmDelete(btn) {
  const key = btn.dataset.key;
  const cell = btn.closest('td');
  const tr = btn.closest('tr');
  tr.classList.add('confirm');
  cell.innerHTML = `<div class="row-actions">
    <span class="confirm-msg">delete this paste?</span>
    <button class="iconbtn del" data-confirm="${esc(key)}">confirm</button>
    <button class="iconbtn" data-cancel="1">cancel</button>
  </div>`;
}

function toast(msg, isErr) {
  const t = $('toast');
  t.textContent = msg;
  t.className = 'toast show' + (isErr ? ' error' : '');
  setTimeout(() => { t.className = 'toast'; }, 2600);
}

async function load() {
  try {
    const data = await api('GET', '/admin/api/pastes');
    pastes = data.pastes || [];
    const who = data.identity || {};
    const label = (who.user || 'admin') + (who.groups && who.groups.length ? ' · ' + who.groups[0] : '');
    $('who').textContent = label;
    $('sbUser').innerHTML = 'session <b>' + esc(who.user || 'admin') + '</b>';
    $('stat').innerHTML = `<b>${data.stats.count}</b> pastes · <b>${humanBytes(data.stats.bytes)}</b>`;
    $('sbCount').textContent = data.stats.count;
    $('sbBytes').textContent = humanBytes(data.stats.bytes);
    $('footnote').innerHTML = 'Admin console - reachable only by authorized admins. To everyone else <code>/admin</code> returns 404.';
    render();
  } catch (e) { if (e.message !== 'unauthorized') toast('load failed: ' + e.message, true); }
}

document.addEventListener('click', async (e) => {
  const del = e.target.closest('[data-key].del, .iconbtn.del[data-key]');
  if (del && del.dataset.key && !del.dataset.confirm) { confirmDelete(del); return; }
  if (e.target.dataset.cancel) { load(); return; }
  if (e.target.dataset.confirm) {
    const key = e.target.dataset.confirm;
    try {
      await api('DELETE', '/admin/api/pastes/' + encodeURIComponent(key));
      pastes = pastes.filter((p) => p.key !== key);
      render(); loadStats(); toast('deleted');
    } catch (err) { if (err.message !== 'unauthorized') toast('delete failed: ' + err.message, true); }
  }
});

async function loadStats() {
  // refresh counts after a mutation without a full reload
  try {
    const data = await api('GET', '/admin/api/pastes');
    pastes = data.pastes || [];
    $('stat').innerHTML = `<b>${data.stats.count}</b> pastes · <b>${humanBytes(data.stats.bytes)}</b>`;
    $('sbCount').textContent = data.stats.count;
    $('sbBytes').textContent = humanBytes(data.stats.bytes);
  } catch (_) {}
}

$('filter').addEventListener('input', render);
$('refreshBtn').addEventListener('click', load);
$('purgeBtn').addEventListener('click', async () => {
  if (!confirm('Delete all expired pastes now?')) return;
  try {
    const r = await api('POST', '/admin/api/purge');
    toast('purged ' + r.purged + ' expired');
    load();
  } catch (e) { if (e.message !== 'unauthorized') toast('purge failed: ' + e.message, true); }
});

// theme toggle (persisted)
const themeBtn = $('themeBtn');
const saved = localStorage.getItem('gopaste-theme') || 'rake';
document.documentElement.setAttribute('data-theme', saved);
themeBtn.textContent = '[ theme: ' + saved + ' ]';
themeBtn.addEventListener('click', () => {
  const n = document.documentElement.getAttribute('data-theme') === 'rake' ? 'arctic' : 'rake';
  document.documentElement.setAttribute('data-theme', n);
  localStorage.setItem('gopaste-theme', n);
  themeBtn.textContent = '[ theme: ' + n + ' ]';
});

load();
