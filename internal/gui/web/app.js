'use strict';

const token = new URLSearchParams(location.search).get('t') || '';
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

const state = {
  info: null,         // /api/state payload
  settings: null,
  job: null,          // current job incl. events
  fonts: [],          // custom.File[]
  style: 'regular',
  assign: {},         // slot -> face id (manual choices only)
  outputs: null,      // null = all
  plan: null,         // last Evaluation
};

// ---------------------------------------------------------------- utilities
function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

async function api(path, body) {
  const opts = { method: body === undefined ? 'GET' : 'POST', headers: { 'X-Token': token } };
  if (body instanceof FormData) {
    opts.body = body;
  } else if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  let data = null;
  try { data = await res.json(); } catch { /* empty */ }
  if (!res.ok) throw new Error((data && data.error) || `${res.status} ${res.statusText}`);
  return data;
}

let toastTimer = 0;
function toast(msg) {
  const el = $('#toast');
  el.textContent = msg;
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, 3200);
}

function mib(n) { return (n / 1048576).toFixed(1) + ' MiB'; }
function pct(r) { return (r * 100).toFixed(r >= 0.999 && r < 1 ? 1 : 0) + '%'; }

function shortSource(name) {
  return String(name || '').replace(/^SarasaUiSC-/, '').replace(/\.ttf$/, '');
}

// ---------------------------------------------------------------- tabs
function showTab(name) {
  $$('.tabs button').forEach((b) => b.setAttribute('aria-selected', String(b.dataset.tab === name)));
  $$('.panel').forEach((p) => { p.hidden = p.dataset.panel !== name; });
  if (name === 'system') loadSystem();
  if (name === 'settings' || name === 'presets') refreshDisplay();
}
// Scaling and monitors can change while the window is open.
window.addEventListener('focus', () => { if (state.info) refreshDisplay(); });
$$('.tabs button').forEach((b) => b.addEventListener('click', () => showTab(b.dataset.tab)));

// ---------------------------------------------------------------- presets
function renderPresets() {
  const { presets, slots } = state.info;
  const by = Object.fromEntries(presets.map((p) => [p.id, p]));
  const head = (p) => `<span class="preset-name">${esc(p.name)}</span><span class="preset-sub">原 ${esc(p.branch)} 分支</span>`;
  $('#col-semibold').innerHTML = head(by.semibold);
  $('#col-regular').innerHTML = head(by.regular);
  $('#matrix-body').innerHTML = slots.map((s) => {
    const a = by.semibold.map[s.id];
    const b = by.regular.map[s.id];
    const cell = (src) => `<td class="src">${esc(shortSource(src))}</td>`;
    return `<tr class="${a !== b ? 'diff' : ''}"><th scope="row">${esc(s.title.replace(/ \d+$/, ''))}<span class="w">${s.id === 'simsun' || s.id === 'segoe' ? '' : s.weight}</span></th>${cell(a)}${cell(b)}</tr>`;
  }).join('');
  $$('[data-preset]').forEach((b) => { b.title = by[b.dataset.preset].summary; });
  renderSourceLine();
}

// effectiveHinted mirrors display.Resolve on the server.
function effectiveHinted() {
  const st = state.settings;
  if (st.hintMode === 'hinted') return true;
  if (st.hintMode === 'unhinted') return false;
  const d = state.info && state.info.display;
  return d ? d.recommendation.hinted : true;
}

function hintLabel() {
  const kind = effectiveHinted() ? 'hinted' : 'unhinted';
  return state.settings.hintMode === 'auto' ? `${kind}，按屏幕自动判定` : kind;
}

function renderHintDetect() {
  const el = $('#hint-detect');
  const d = state.info && state.info.display;
  if (!el || !d) return;
  const kind = d.recommendation.hinted ? 'hinted' : 'unhinted';
  const local = state.settings.useLocal ? '（当前使用本地 Sarasa 文件，这项不起作用）' : '';
  el.innerHTML = `检测结果：${esc(d.recommendation.basis)}。<br>自动判定为 <strong>${kind}</strong>：${esc(d.recommendation.reason)}。检测的是运行本程序的这台电脑；给别的电脑生成时请手动选择。${local}`;
}

async function refreshDisplay() {
  try {
    state.info.display = await api('/api/display');
    renderHintDetect();
    renderSourceLine();
  } catch { /* keep the last result */ }
}

function renderSourceLine() {
  const st = state.settings;
  if (!st) return;
  let src;
  if (st.useLocal) {
    src = st.sarasaLocal ? `字体来源：本地 ${esc(st.sarasaLocal)}` : '字体来源：本地（还没选文件夹）';
  } else {
    const v = st.version ? ` Sarasa ${esc(st.version)}` : '最新版 Sarasa';
    src = `字体来源：自动下载${v}（${hintLabel()}）`;
    const cached = (state.info.cached || []).filter((c) => c.endsWith(effectiveHinted() ? '-hinted' : '-unhinted'));
    if (cached.length) src += `，已缓存 ${esc(cached.map((c) => c.split('-')[0]).join('、'))}`;
  }
  const out = `输出到 ${esc(st.outRoot)}${st.zip ? '，并打包 ZIP' : ''}`;
  $('#source-line').innerHTML = `${src}。${out}。 <button class="link" data-goto="settings">更改</button>`;
}

document.addEventListener('click', (e) => {
  const go = e.target.closest('[data-goto]');
  if (go) showTab(go.dataset.goto);
});

$$('[data-preset]').forEach((b) => b.addEventListener('click', async () => {
  try {
    await api('/api/preset', { preset: b.dataset.preset });
  } catch (err) { toast(err.message); }
}));

// ---------------------------------------------------------------- job panel
function lastOf(events, kind) {
  for (let i = events.length - 1; i >= 0; i--) if (events[i].kind === kind) return events[i];
  return null;
}

function renderJob() {
  const j = state.job;
  const box = $('#job');
  $('#cancel').hidden = !(j && j.state === 'running');
  setBusy(j && j.state === 'running');
  if (!j) {
    box.innerHTML = '<p class="idle">还没有任务。点左边的生成按钮开始，进度和结果会显示在这里。</p>';
    return;
  }
  const ev = j.events || [];
  const stage = lastOf(ev, 'stage');
  const step = lastOf(ev, 'step');
  const prog = lastOf(ev, 'progress');
  let bar = '<div class="bar indeterminate"><i></i></div>';
  let stageText = stage ? stage.msg : '准备中';
  if (j.state !== 'running') {
    bar = `<div class="bar"><i style="width:${j.state === 'done' ? 100 : 0}%"></i></div>`;
    stageText = { done: '已完成', canceled: '已取消', failed: '出错了，详情见下方' }[j.state] || '';
  } else if (step && step.total) {
    bar = `<div class="bar"><i style="width:${Math.round((100 * step.done) / step.total)}%"></i></div>`;
    stageText = `${stage ? stage.msg : '生成中'}（${step.done}/${step.total}）`;
  } else if (prog && prog.total && (!stage || ev.indexOf(prog) > ev.indexOf(stage))) {
    bar = `<div class="bar"><i style="width:${Math.round((100 * prog.done) / prog.total)}%"></i></div>`;
    stageText = `${prog.msg}：${mib(prog.done)} / ${mib(prog.total)}`;
  }
  const label = { running: '进行中', done: '完成', failed: '失败', canceled: '已取消' }[j.state] || j.state;
  const lines = ev.filter((e) => e.kind === 'log' || e.kind === 'stage').slice(-400).map((e) => {
    const cls = e.kind === 'stage' ? 'stage' : (e.level || 'info');
    return `<li class="${cls}">${esc(e.msg)}</li>`;
  }).join('');
  box.innerHTML = `
    <div><span class="job-title">${esc(j.title)}</span><span class="job-state ${esc(j.state)}">${label}</span></div>
    <p class="stage">${esc(stageText)}</p>
    ${bar}
    <ul class="log" id="log">${lines}</ul>
    ${renderResult(j)}`;
  const log = $('#log');
  log.scrollTop = log.scrollHeight;
}

function renderResult(j) {
  if (j.state === 'running') return '';
  if (j.state !== 'done') {
    return `<div class="result failed"><p class="err">${esc(j.error || '未完成')}</p></div>`;
  }
  if (j.kind === 'system') {
    const reboot = j.action && j.action !== 'cancel' && j.action !== 'forget'
      ? '<button class="primary" data-reboot>现在重启</button>' : '';
    return `<div class="result"><p>${esc(actionDone[j.action] || '完成')}</p>
      <div class="result-actions">${reboot}<button data-goto="system">查看系统字体</button></div></div>`;
  }
  if (j.backup) {
    return `<div class="result"><p>已备份到：</p><p class="hash">${esc(j.backup)}</p>
      <div class="result-actions"><button data-reveal="${esc(j.backup)}">打开备份文件夹</button></div></div>`;
  }
  const r = j.result;
  if (!r) return '';
  const files = (r.outputs || []).map((o) => `<li><span>${esc(o.name)}</span><span class="size">${mib(o.size)}</span>
      <span class="hash" title="点击复制完整 SHA-256&#10;${esc(o.sha256)}" data-copy="${esc(o.sha256)}">${esc(o.sha256.slice(0, 16))}…</span></li>`).join('');
  const warn = r.warnings && r.warnings.length ? `<p class="result-warn">生成时有 ${r.warnings.length} 条提示，已写入包内的安装说明。</p>` : '';
  const dir = r.packageDir || r.fontsDir;
  return `<div class="result">
    <p>用时 ${r.seconds.toFixed(1)} 秒，${(r.outputs || []).length} 个文件已逐个校验。</p>
    <ul class="result-files">${files}</ul>
    <div class="result-actions">
      <button class="primary" data-reveal="${esc(r.packageDir ? r.packageDir + sep() + 'fonts' : dir)}">打开字体文件夹</button>
      ${r.zipPath ? `<button data-reveal="${esc(r.zipPath)}">显示 ZIP</button>` : ''}
      ${r.packageDir && state.info.canElevate ? `<button data-install="${esc(r.packageDir)}">安装到系统…</button>` : ''}
    </div>${warn}</div>`;
}

function sep() { return state.info && state.info.os === 'windows' ? '\\' : '/'; }

function setBusy(busy) {
  $$('[data-ok]').forEach((b) => { b.disabled = !!busy || b.dataset.ok !== '1'; });
  $$('[data-preset], #build-custom, #backup').forEach((b) => {
    if (b.id === 'build-custom') b.disabled = busy || !(state.plan && state.plan.canBuild);
    else b.disabled = !!busy;
  });
}

$('#cancel').addEventListener('click', () => api('/api/cancel', {}).catch((e) => toast(e.message)));

document.addEventListener('click', async (e) => {
  const rv = e.target.closest('[data-reveal]');
  if (rv) {
    try { await api('/api/reveal', { path: rv.dataset.reveal }); } catch (err) { toast(err.message); }
    return;
  }
  const cp = e.target.closest('[data-copy]');
  if (cp) {
    try { await navigator.clipboard.writeText(cp.dataset.copy); toast('已复制 SHA-256'); } catch { toast(cp.dataset.copy); }
  }
});

// ---------------------------------------------------------------- events
let renderQueued = false;
function queueRender() {
  if (renderQueued) return;
  renderQueued = true;
  requestAnimationFrame(() => { renderQueued = false; renderJob(); });
}

function connect() {
  const es = new EventSource(`/api/events?t=${encodeURIComponent(token)}`);
  es.addEventListener('hello', (m) => {
    const d = JSON.parse(m.data);
    state.job = d.job || null;
    renderJob();
  });
  es.addEventListener('job', (m) => {
    const j = JSON.parse(m.data);
    const events = state.job && state.job.id === j.id ? state.job.events || [] : [];
    state.job = { ...j, events };
    renderJob();
    if (j.state !== 'running') refreshCached();
    if ((j.kind === 'backup' || j.kind === 'system') && j.state !== 'running') loadSystem();
  });
  es.addEventListener('event', (m) => {
    const d = JSON.parse(m.data);
    if (!state.job || state.job.id !== d.job) return;
    const evs = state.job.events || (state.job.events = []);
    const e = d.event;
    const last = evs[evs.length - 1];
    if (e.kind === 'progress' && last && last.kind === 'progress' && last.msg === e.msg) evs[evs.length - 1] = e;
    else evs.push(e);
    queueRender();
  });
}

async function refreshCached() {
  try {
    const info = await api('/api/state');
    state.info.cached = info.cached;
    renderSourceLine();
  } catch { /* ignore */ }
}

// ---------------------------------------------------------------- custom fonts
function setFonts(files, errors) {
  state.fonts = files || [];
  const known = new Set(allFaces().map((f) => f.id));
  for (const k of Object.keys(state.assign)) if (!known.has(state.assign[k])) delete state.assign[k];
  $('#font-errors').innerHTML = (errors || []).map((e) => `<p class="file-error">${esc(e)}</p>`).join('');
  renderFonts();
  refreshPlan();
}

function allFaces() {
  return state.fonts.flatMap((f) => f.faces || []);
}

function coverage(face, id) {
  return (face.coverage || []).find((c) => c.id === id) || { ratio: 0, have: 0, total: 0 };
}

const openCollections = new Set();
$('#font-list').addEventListener('toggle', (e) => {
  const d = e.target;
  if (!d.dataset || !d.dataset.file) return;
  if (d.open) openCollections.add(d.dataset.file); else openCollections.delete(d.dataset.file);
}, true);

function renderFonts() {
  const list = $('#font-list');
  if (!state.fonts.length) { list.innerHTML = ''; return; }
  list.innerHTML = `<div class="files">${state.fonts.map((f) => {
    const id = f.error ? 'err:' + f.path : f.id;
    const used = new Set(state.plan ? state.plan.slots.map((x) => x.faceId) : []);
    const row = (x) => {
      const gb = coverage(x, 'gb2312-1');
      const errs = (x.issues || []).filter((i) => i.severity === 'error').length;
      const warns = (x.issues || []).filter((i) => i.severity === 'warn').length;
      const flag = errs ? `<span style="color:var(--err)">不可用</span>` : warns ? `<span style="color:var(--warn)">${warns} 条注意</span>` : '<span class="muted">正常</span>';
      const name = [x.facts.family, x.facts.subfamily].filter(Boolean).join(' / ');
      return `<div class="face-row${used.has(x.id) ? ' in-use' : ''}"><span>${f.faces.length > 1 ? `#${x.index} ` : ''}${esc(name)}${x.facts.variable ? '（可变）' : ''}${used.has(x.id) ? '（已使用）' : ''}</span>
        <span class="num">字重 ${x.weight}</span>
        <span class="muted hide-narrow">${esc(x.facts.outline)}</span>
        <span class="hide-narrow">汉字 ${pct(gb.ratio)} · ${flag}</span></div>`;
    };
    const all = f.faces || [];
    let faces;
    if (all.length > 3) {
      // Big collections (e.g. Noto Sans CJK: JP/KR/SC/TC/HK...): show the
      // faces in use, tuck the rest away.
      const shown = all.filter((x) => used.has(x.id));
      const rest = all.filter((x) => !used.has(x.id));
      faces = shown.map(row).join('') + `<details class="more-faces" data-file="${esc(f.id)}" ${openCollections.has(f.id) ? 'open' : ''}><summary>${shown.length ? `其余 ${rest.length} 个字体` : `包含 ${all.length} 个字体`}</summary>${rest.map(row).join('')}</details>`;
    } else {
      faces = all.map(row).join('');
    }
    return `<div class="file">
      <div class="file-head"><span class="file-name">${esc(f.name)}</span>
        <span><span class="file-meta">${mib(f.size)}</span> <button class="ghost" data-remove="${esc(id)}" aria-label="移除 ${esc(f.name)}">移除</button></span></div>
      ${f.error ? `<p class="file-error">${esc(f.error)}</p>` : faces}
    </div>`;
  }).join('')}</div>`;
}

$('#font-list').addEventListener('click', async (e) => {
  const b = e.target.closest('[data-remove]');
  if (!b) return;
  try {
    const r = await api('/api/fonts/remove', { id: b.dataset.remove });
    setFonts(r.files, r.errors);
  } catch (err) { toast(err.message); }
});

$('#pick-fonts').addEventListener('click', async () => {
  if (!state.info.canPick) { toast('这个系统上没有原生对话框，请把文件拖进来'); return; }
  try {
    const r = await api('/api/pick', { purpose: 'fonts' });
    if (r.paths && r.paths.length) await addPaths(r.paths);
  } catch (err) { toast(err.message); }
});

$('#font-path').addEventListener('keydown', async (e) => {
  if (e.key !== 'Enter') return;
  const v = e.target.value.trim();
  if (!v) return;
  await addPaths(v.split(/\r?\n/));
  e.target.value = '';
});

async function addPaths(paths) {
  const dz = $('#dropzone');
  dz.setAttribute('aria-busy', 'true');
  try {
    const r = await api('/api/fonts/add', { paths });
    setFonts(r.files, r.errors);
  } catch (err) { toast(err.message); } finally { dz.removeAttribute('aria-busy'); }
}

async function uploadFiles(fileList) {
  const fd = new FormData();
  for (const f of fileList) fd.append('files', f, f.name);
  toast(`正在读取 ${fileList.length} 个文件…`);
  try {
    const r = await api('/api/fonts/upload', fd);
    setFonts(r.files, r.errors);
  } catch (err) { toast(err.message); }
}

(function dragAndDrop() {
  const dz = $('#dropzone');
  let depth = 0;
  const isFiles = (e) => e.dataTransfer && Array.from(e.dataTransfer.types || []).includes('Files');
  window.addEventListener('dragenter', (e) => { if (isFiles(e)) { depth++; dz.classList.add('over'); } });
  window.addEventListener('dragleave', () => { depth = Math.max(0, depth - 1); if (!depth) dz.classList.remove('over'); });
  window.addEventListener('dragover', (e) => { if (isFiles(e)) e.preventDefault(); });
  window.addEventListener('drop', (e) => {
    if (!isFiles(e)) return;
    e.preventDefault();
    depth = 0;
    dz.classList.remove('over');
    showTab('custom');
    if (e.dataTransfer.files.length) uploadFiles(e.dataTransfer.files);
  });
})();

$$('.segmented button').forEach((b) => b.addEventListener('click', () => {
  state.style = b.dataset.style;
  $$('.segmented button').forEach((x) => x.setAttribute('aria-checked', String(x === b)));
  refreshPlan();
}));

let planSeq = 0;
async function refreshPlan() {
  const seq = ++planSeq;
  if (!allFaces().length) {
    state.plan = null;
    $('#plan').hidden = true;
    setBusy(state.job && state.job.state === 'running');
    return;
  }
  try {
    const plan = await api('/api/custom/plan', { style: state.style, assign: state.assign, outputs: state.outputs });
    if (seq !== planSeq) return;
    state.plan = plan.empty ? null : plan;
    renderPlan();
    renderFonts();
  } catch (err) { toast(err.message); }
}

function issueList(issues) {
  if (!issues || !issues.length) return '';
  return `<ul class="issues">${issues.map((i) => `<li class="${esc(i.severity)}">${esc(i.msg)}</li>`).join('')}</ul>`;
}

function faceOptionLabel(f) {
  const name = [f.facts.family, f.facts.subfamily].filter(Boolean).join(' ');
  return `${f.weight} · ${name}（${f.label}）`;
}

function renderPlan() {
  const p = state.plan;
  $('#plan').hidden = !p;
  if (!p) return;
  const faces = allFaces().filter((f) => f.usable).sort((a, b) => a.weight - b.weight || a.label.localeCompare(b.label));
  const slotsMeta = Object.fromEntries(state.info.slots.map((s) => [s.id, s]));
  $('#slot-body').innerHTML = p.slots.map((s) => {
    const meta = slotsMeta[s.slot];
    const opts = faces.map((f) => `<option value="${esc(f.id)}" ${f.id === s.faceId ? 'selected' : ''}>${esc(faceOptionLabel(f))}</option>`).join('');
    const auto = s.auto ? '<span class="auto">自动选择</span>' : `<button class="link auto" data-unassign="${esc(s.slot)}">恢复自动</button>`;
    const ideal = s.idealWeight !== s.identityWeight ? `，按 ${s.idealWeight} 挑选` : '';
    const issues = issueList(s.issues) || '<span class="ok-note">没有问题</span>';
    return `<tr>
      <th scope="row">${esc(meta.title)}<span class="w">Windows 看到 ${s.identityWeight}${ideal}</span></th>
      <td><select data-slot="${esc(s.slot)}" aria-label="${esc(meta.title)} 使用的字体">${opts}</select><div>${auto}</div></td>
      <td>${issues}</td></tr>`;
  }).join('');
  $('#plan-issues').innerHTML = issueList(p.issues);

  $('#outputs').innerHTML = p.outputs.map((o) => {
    const desc = (state.info.outputs.find((x) => x.name === o.name) || {}).desc || '';
    return `<label><input type="checkbox" data-output="${esc(o.name)}" ${o.selected ? 'checked' : ''}>
      <span>${esc(o.name)}${o.blocked && o.selected ? ' <span class="blocked-tag">有错误</span>' : ''}<span class="desc">${esc(desc)}</span>${issueList(o.issues)}</span></label>`;
  }).join('');

  const notes = p.faces || [];
  $('#face-notes').hidden = !notes.length;
  $('#face-notes-body').innerHTML = notes.map((g) => `<div class="group"><strong>${esc(g.label)}</strong>
    ${issueList(g.issues)}${g.fixes && g.fixes.length ? `<ul class="issues">${g.fixes.map((f) => `<li class="fix">已自动修正：${esc(f.msg)}</li>`).join('')}</ul>` : ''}</div>`).join('');

  const blockers = [];
  p.slots.forEach((s) => (s.issues || []).forEach((i) => {
    const used = p.outputs.some((o) => o.selected && o.slots.includes(s.slot));
    if (i.severity === 'error' && used) blockers.push(`${slotsMeta[s.slot].title}：${i.msg}`);
  }));
  (p.issues || []).forEach((i) => { if (i.severity === 'error') blockers.push(i.msg); });
  $('#build-blocked').textContent = p.canBuild ? '' : (blockers[0] || '有错误需要处理') + (blockers.length > 1 ? `（另有 ${blockers.length - 1} 条）` : '');
  setBusy(state.job && state.job.state === 'running');
}

$('#slot-body').addEventListener('change', (e) => {
  const sel = e.target.closest('select[data-slot]');
  if (!sel) return;
  state.assign[sel.dataset.slot] = sel.value;
  refreshPlan();
});
$('#slot-body').addEventListener('click', (e) => {
  const b = e.target.closest('[data-unassign]');
  if (!b) return;
  delete state.assign[b.dataset.unassign];
  refreshPlan();
});
$('#outputs').addEventListener('change', () => {
  state.outputs = $$('[data-output]').filter((c) => c.checked).map((c) => c.dataset.output);
  refreshPlan();
});

$('#build-custom').addEventListener('click', async () => {
  try {
    await api('/api/custom/build', { style: state.style, assign: state.assign, outputs: state.outputs });
  } catch (err) { toast(err.message); }
});

// ---------------------------------------------------------------- system fonts
const stateLabel = {
  original: 'Windows 11 原版',
  microsoft: '微软字体（其他版本）',
  ours: '本工具生成',
  replaced: '已被替换',
  missing: '缺失',
  unknown: '无法读取',
};

const actionDone = {
  install: '已登记安装，重启后生效（同时清空字体缓存）',
  restore: '已登记还原备份，重启后生效（同时清空字体缓存）',
  forget: '已删除这份备份',
  'clear-cache': '已登记清空字体缓存，重启后生效',
  cancel: '已取消待重启的操作',
};

let systemSeq = 0;
async function loadSystem() {
  const seq = ++systemSeq;
  const box = $('#system-body');
  try {
    const [r, pk] = await Promise.all([api('/api/system'), api('/api/packages').catch(() => ({ packages: [] }))]);
    if (seq !== systemSeq) return;
    if (!r.supported) {
      $('#sys-status').innerHTML = '';
      $('#sys-actions').innerHTML = '';
      box.innerHTML = '<p class="note">只有在 Windows 上运行时才能查看、安装和还原系统字体。</p>';
      return;
    }
    renderSysStatus(r);
    renderSysActions(r, pk.packages || []);
    const rows = r.files.map((f) => {
      let note = '';
      if (f.expected !== undefined && f.expected !== null) note = f.matchesInstalled ? '与安装记录一致' : '与安装记录不一致';
      if (f.backedUp) note = note ? note + '，已备份' : '已备份';
      return `<tr>
      <td>${esc(f.name)}</td>
      <td><span class="state ${esc(f.state)}">${esc(stateLabel[f.state] || f.state)}</span>${note ? `<div class="file-meta">${esc(note)}</div>` : ''}${f.copyright && f.state !== 'original' && f.state !== 'ours' ? `<div class="file-meta">${esc(f.copyright.slice(0, 80))}</div>` : ''}</td>
      <td>${f.size ? mib(f.size) : ''}</td>
      <td class="hash" ${f.sha256 ? `data-copy="${esc(f.sha256)}" title="点击复制"` : ''}>${esc(f.sha256 || f.error || '')}</td></tr>`;
    }).join('');
    box.innerHTML = `<table class="sys"><thead><tr><th>文件</th><th>状态</th><th>大小</th><th>SHA-256</th></tr></thead><tbody>${rows}</tbody></table>
      <p class="note">状态由文件哈希和字体里的版权、厂商信息判断；与金标准哈希一致即为 Windows 11 原版。Windows 更新或 <code>sfc /scannow</code> 可能把字体换回原版，到时重新安装即可。</p>`;
    setBusy(state.job && state.job.state === 'running');
  } catch (err) {
    box.innerHTML = `<p class="file-error">${esc(err.message)}</p>`;
  }
}

function renderSysStatus(r) {
  const rep = r.report;
  if (!rep) {
    $('#sys-status').innerHTML = r.error ? `<div class="sys-card failed"><span class="msg">${esc(r.error)}</span></div>` : '';
    return;
  }
  const snaps = rep.snapshots || [];
  const nonOrig = snaps.some((s) => s.nonOriginal && s.nonOriginal.length);
  const orig = snaps.length
    ? `备份：${snaps.length} 份，共 ${mib(rep.backupBytes || 0)}${nonOrig ? '（含非原版字体）' : ''}；最近一份：${esc(snaps[0].label)} <button class="link" data-reveal="${esc(rep.paths.dataDir + sep() + 'backups')}">打开</button>`
    : '备份：还没有（每次安装或还原前都会自动把当前字体照原样备份）';
  let actions = '';
  if (rep.pendingActive) {
    actions = `<div class="row"><button class="primary" data-reboot>现在重启</button><button data-sys="cancel" data-ok="${r.canElevate ? 1 : 0}">取消这次操作…</button></div>`;
  }
  const pend = rep.pendingActive ? `<span class="meta">已登记 ${rep.pendingCount} 个重启时执行的操作</span>` : '';
  $('#sys-status').innerHTML = `<div class="sys-card ${esc(rep.phase)}">
    <span class="msg">${esc(rep.message)}</span>
    <span class="meta">${orig}</span>${pend}${actions}</div>`;
}

let packageChoice = '';
function renderSysActions(r, packages) {
  const rep = r.report;
  const canRun = !!r.canElevate && !!rep;
  const opts = packages.map((p) => `<option value="${esc(p.dir)}" ${p.dir === packageChoice ? 'selected' : ''}>${esc(p.title || p.name)}（${esc((p.created || '').replace('T', ' ').slice(0, 16))}）</option>`).join('');
  const other = packageChoice && !packages.some((p) => p.dir === packageChoice)
    ? `<option value="${esc(packageChoice)}" selected>${esc(packageChoice)}</option>` : '';
  $('#sys-actions').innerHTML = `<div class="sys-ops">
    <div class="install">
      <label class="visually-hidden" for="pkg-select">要安装的字体包</label>
      <select id="pkg-select">${opts}${other}${!opts && !other ? '<option value="">输出文件夹里还没有字体包</option>' : ''}</select>
      ${state.info.canPick ? '<button id="pkg-other">其他文件夹…</button>' : ''}
      <button class="primary" id="pkg-install" data-ok="${canRun ? 1 : 0}">安装到系统…</button>
    </div>
    <div class="row">
      <button data-sys="restore" data-ok="${canRun && rep.snapshots && rep.snapshots.length ? 1 : 0}" title="${rep && rep.snapshots && rep.snapshots.length ? '' : '还没有备份'}">还原备份…</button>
      <button data-sys="clear-cache" data-ok="${canRun ? 1 : 0}">只清空字体缓存…</button>
      <button id="backup">把当前 6 个文件另存一份</button>
      <button data-reveal="${esc(r.dir)}">打开 Fonts 文件夹</button>
    </div>
  </div>`;
  $('#pkg-select').addEventListener('change', (e) => { packageChoice = e.target.value; });
  if (!packageChoice && packages[0]) packageChoice = packages[0].dir;
  const other2 = $('#pkg-other');
  if (other2) other2.addEventListener('click', async () => {
    try {
      const p = await api('/api/pick', { purpose: 'package' });
      if (p.paths && p.paths[0]) { packageChoice = p.paths[0]; loadSystem(); }
    } catch (err) { toast(err.message); }
  });
  $('#pkg-install').addEventListener('click', () => {
    const v = $('#pkg-select').value;
    if (!v) { toast('先生成一个字体包，或选择其他文件夹'); return; }
    openPlan({ action: 'install', package: v });
  });
  $('#backup').addEventListener('click', () => api('/api/system/backup', {}).catch((e) => toast(e.message)));
}

document.addEventListener('click', async (e) => {
  const b = e.target.closest('[data-sys]');
  if (b) { openPlan({ action: b.dataset.sys }); return; }
  const rb = e.target.closest('[data-reboot]');
  if (rb) {
    if (!window.confirm('现在重启电脑？5 秒后开始重启，请先保存其他程序里的工作。')) return;
    try { await api('/api/system/reboot', {}); toast('5 秒后重启'); } catch (err) { toast(err.message); }
    return;
  }
  const inst = e.target.closest('[data-install]');
  if (inst) {
    packageChoice = inst.dataset.install;
    showTab('system');
    openPlan({ action: 'install', package: packageChoice });
  }
});

const planTitles = { install: '安装字体包到系统', restore: '还原备份', 'clear-cache': '清空字体缓存', cancel: '取消待重启的操作' };
let planReq = null;

function list(items, cls) {
  if (!items || !items.length) return '';
  return `<ul class="issues ${cls || ''}">${items.map((x) => `<li class="${cls === 'blockers' ? 'error' : cls === 'warnings' ? 'warn' : ''}">${esc(x)}</li>`).join('')}</ul>`;
}

async function openPlan(req) {
  planReq = req;
  const dlg = $('#plan-dialog');
  $('#plan-title').textContent = planTitles[req.action] || req.action;
  $('#plan-body').innerHTML = '<p class="note">正在检查…</p>';
  $('#plan-run').disabled = true;
  if (!dlg.open) dlg.showModal();
  let plan;
  try {
    plan = await api('/api/system/plan', req);
  } catch (err) {
    $('#plan-body').innerHTML = `<p class="file-error">${esc(err.message)}</p>`;
    return;
  }
  const parts = [];
  if (plan.package) parts.push(`<p><strong>${esc(plan.package.title || plan.package.name)}</strong><br><span class="hash">${esc(plan.package.dir)}</span></p>`);
  if (plan.files && plan.files.length) {
    parts.push(`<h3>会改变的文件</h3><table><thead><tr><th>文件</th><th>现在</th><th>重启后</th></tr></thead><tbody>${plan.files.map((f) => `<tr><td>${esc(f.name)}</td><td>${esc(stateLabel[f.current] || f.current)}</td><td>${esc(f.after)}</td></tr>`).join('')}</tbody></table>`);
  }
  if (plan.backup && plan.backup.length) {
    parts.push(`<h3>安装前会照原样备份的当前文件</h3><table><thead><tr><th>文件</th><th>现在是</th><th>备份</th></tr></thead><tbody>${plan.backup.map((b) => `<tr><td>${esc(b.name)}</td><td>${esc(stateLabel[b.state] || b.state)}</td><td>${b.state === 'missing' ? '记为不存在' : b.stored ? '已有相同备份' : '新保存'}</td></tr>`).join('')}</tbody></table>`);
  }
  if (plan.snapshots && plan.snapshots.length) {
    parts.push(`<h3>还原到哪一份备份</h3><div class="snaps" role="radiogroup" aria-label="备份">${plan.snapshots.map((sn) => {
      const extra = [];
      if (sn.nonOriginal && sn.nonOriginal.length) extra.push('非原版：' + sn.nonOriginal.join('、'));
      if (sn.missing && sn.missing.length) extra.push('不存在：' + sn.missing.join('、'));
      if (!sn.complete) extra.push('文件不全');
      return `<label class="radio"><input type="radio" name="snap" value="${esc(sn.id)}" ${sn.id === plan.snapshot ? 'checked' : ''}> ${esc(sn.label)}<span class="file-meta">${esc(mib(sn.size))}${extra.length ? '；' + esc(extra.join('；')) : ''}</span></label>`;
    }).join('')}</div>
    <p><button type="button" class="link" id="snap-forget">删除选中的这份备份…</button></p>`);
  }
  if (plan.steps && plan.steps.length) parts.push(`<h3>步骤</h3><ol>${plan.steps.map((s) => `<li>${esc(s)}</li>`).join('')}</ol>`);
  parts.push(list(plan.warnings, 'warnings'));
  parts.push(list(plan.blockers, 'blockers'));
  $('#plan-body').innerHTML = parts.join('');
  $$('input[name="snap"]').forEach((r) => r.addEventListener('change', () => openPlan({ action: 'restore', snapshot: r.value })));
  const forget = $('#snap-forget');
  if (forget) forget.addEventListener('click', async () => {
    const sel = $('input[name="snap"]:checked');
    if (!sel) return;
    const sn = plan.snapshots.find((x) => x.id === sel.value);
    const warn = sn && sn.nonOriginal && sn.nonOriginal.length ? '\n\n这份备份里有非原版字体（' + sn.nonOriginal.join('、') + '），删除后可能很难再找回。' : '';
    if (!window.confirm('删除备份「' + (sn ? sn.label : sel.value) + '」？' + warn)) return;
    planReq = null;
    $('#plan-dialog').close('cancel');
    try { await api('/api/system/run', { action: 'forget', snapshot: sel.value }); } catch (err) { toast(err.message); }
  });
  $('#plan-run').disabled = !!(plan.blockers && plan.blockers.length) || (state.job && state.job.state === 'running');
}

$('#plan-dialog').addEventListener('close', async () => {
  const dlg = $('#plan-dialog');
  if (dlg.returnValue !== 'run' || !planReq) return;
  try {
    await api('/api/system/run', planReq);
  } catch (err) { toast(err.message); }
});

// ---------------------------------------------------------------- settings
function fillSettings() {
  const st = state.settings;
  const f = $('#settings');
  f.elements.outRoot.value = st.outRoot || '';
  $$('input[name="hintMode"]', f).forEach((r) => { r.checked = (st.hintMode || 'auto') === r.value; });
  renderHintDetect();
  f.elements.zip.checked = !!st.zip;
  f.elements.version.value = st.version || '';
  f.elements.sarasaLocal.value = st.sarasaLocal || '';
  f.elements.proxy.value = st.proxy || '';
  f.elements.mirror.value = st.mirror || '';
  $$('input[name="useLocal"]', f).forEach((r) => { r.checked = String(!!st.useLocal) === r.value; });
  $$('input[name="proxyMode"]', f).forEach((r) => { r.checked = st.proxyMode === r.value; });
  $('#sys-proxy').textContent = state.info.sysProxy ? `：当前 ${state.info.sysProxy}` : '：当前未开启';
  $('#about').innerHTML = `<span>版本 ${esc(state.info.version)}</span>
    <span>缓存与设置：<code>${esc(state.info.cacheDir)}</code></span>
    ${state.info.logPath ? `<span>运行日志：<code>${esc(state.info.logPath)}</code></span>` : ''}`;
  $$('[data-pick]').forEach((b) => { b.hidden = !state.info.canPick; });
}

let saveTimer = 0;
function collectSettings() {
  const f = $('#settings');
  return {
    outRoot: f.elements.outRoot.value.trim(),
    hintMode: ($('input[name="hintMode"]:checked', f) || {}).value || 'auto',
    zip: f.elements.zip.checked,
    version: f.elements.version.value.trim(),
    useLocal: ($('input[name="useLocal"]:checked', f) || {}).value === 'true',
    sarasaLocal: f.elements.sarasaLocal.value.trim(),
    proxyMode: ($('input[name="proxyMode"]:checked', f) || {}).value || 'auto',
    proxy: f.elements.proxy.value.trim(),
    mirror: f.elements.mirror.value.trim(),
  };
}

async function saveSettings() {
  const status = $('#settings-status');
  try {
    state.settings = await api('/api/settings', collectSettings());
    status.style.color = '';
    status.textContent = '已保存';
    renderSourceLine();
    renderHintDetect();
  } catch (err) {
    status.style.color = 'var(--err)';
    status.textContent = err.message;
  }
}

$('#settings').addEventListener('input', (e) => {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(saveSettings, e.target.type === 'text' || !e.target.type ? 600 : 0);
});
$('#settings').addEventListener('submit', (e) => e.preventDefault());
$$('[data-pick]').forEach((b) => b.addEventListener('click', async () => {
  try {
    const r = await api('/api/pick', { purpose: b.dataset.pick });
    if (!r.paths || !r.paths.length) return;
    const f = $('#settings');
    if (b.dataset.pick === 'output') {
      f.elements.outRoot.value = r.paths[0];
    } else {
      f.elements.sarasaLocal.value = r.paths[0];
      $$('input[name="useLocal"]', f).forEach((x) => { x.checked = x.value === 'true'; });
    }
    saveSettings();
  } catch (err) { toast(err.message); }
}));

// ---------------------------------------------------------------- boot
async function boot() {
  if (!token) {
    document.body.innerHTML = '<p style="padding:24px">缺少访问令牌，请从程序重新打开窗口。</p>';
    return;
  }
  try {
    state.info = await api('/api/state');
  } catch (err) {
    document.body.innerHTML = `<p style="padding:24px">无法连接到程序：${esc(err.message)}</p>`;
    return;
  }
  state.settings = state.info.settings;
  state.job = state.info.job;
  renderPresets();
  fillSettings();
  setFonts(state.info.fonts, []);
  renderJob();
  connect();
}
boot();
