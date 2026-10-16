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
}
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

function renderSourceLine() {
  const st = state.settings;
  if (!st) return;
  let src;
  if (st.useLocal) {
    src = st.sarasaLocal ? `字体来源：本地 ${esc(st.sarasaLocal)}` : '字体来源：本地（还没选文件夹）';
  } else {
    const v = st.version ? ` Sarasa ${esc(st.version)}` : '最新版 Sarasa';
    src = `字体来源：自动下载${v}（${st.hinted ? 'hinted' : 'unhinted'}）`;
    const cached = (state.info.cached || []).filter((c) => c.endsWith(st.hinted ? '-hinted' : '-unhinted'));
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
    </div>${warn}</div>`;
}

function sep() { return state.info && state.info.os === 'windows' ? '\\' : '/'; }

function setBusy(busy) {
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
    if (j.kind === 'backup' && j.state === 'done') loadSystem();
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

async function loadSystem() {
  const box = $('#system-body');
  try {
    const r = await api('/api/system');
    if (!r.supported) {
      box.innerHTML = '<p class="note">只有在 Windows 上运行时才能查看和备份系统字体。</p>';
      return;
    }
    const rows = r.files.map((f) => `<tr>
      <td>${esc(f.name)}</td>
      <td><span class="state ${esc(f.state)}">${esc(stateLabel[f.state] || f.state)}</span>${f.copyright && f.state !== 'original' ? `<div class="file-meta">${esc(f.copyright.slice(0, 80))}</div>` : ''}</td>
      <td>${f.size ? mib(f.size) : ''}</td>
      <td class="hash" ${f.sha256 ? `data-copy="${esc(f.sha256)}" title="点击复制"` : ''}>${esc(f.sha256 || f.error || '')}</td></tr>`).join('');
    box.innerHTML = `<table class="sys"><thead><tr><th>文件</th><th>状态</th><th>大小</th><th>SHA-256</th></tr></thead><tbody>${rows}</tbody></table>
      <div class="sys-actions">
        <button class="primary" id="backup">备份这 6 个文件</button>
        <button data-reveal="${esc(r.dir)}">打开 ${esc(r.dir)}</button>
      </div>
      <p class="note">备份会复制到输出文件夹下的「系统字体备份-日期」中，并逐个校验哈希。读取系统字体不需要管理员权限；替换需要在 WinPE/恢复环境或你惯用的方式中进行。</p>`;
    $('#backup').addEventListener('click', () => api('/api/system/backup', {}).catch((e) => toast(e.message)));
    setBusy(state.job && state.job.state === 'running');
  } catch (err) {
    box.innerHTML = `<p class="file-error">${esc(err.message)}</p>`;
  }
}

// ---------------------------------------------------------------- settings
function fillSettings() {
  const st = state.settings;
  const f = $('#settings');
  f.elements.outRoot.value = st.outRoot || '';
  f.elements.hinted.checked = !!st.hinted;
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
    hinted: f.elements.hinted.checked,
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
