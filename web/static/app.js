/* [Panel] Web-Oberfläche. Kein Build-Schritt: reines JavaScript + three.js (r128). */
'use strict';
(function () {
  const $ = (s, el) => (el || document).querySelector(s);
  const $$ = (s, el) => [...(el || document).querySelectorAll(s)];
  const root = document.documentElement;
  const lightMQ = matchMedia('(prefers-color-scheme: light)');
  const rmMQ = matchMedia('(prefers-reduced-motion: reduce)');
  const store = {
    get(k) { try { return localStorage.getItem(k); } catch (e) { return null; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch (e) { /* egal */ } }
  };
  let motion = store.get('gp-motion') ? store.get('gp-motion') === '1' : !rmMQ.matches;
  const t0 = store.get('gp-theme');
  if (t0 === 'dark' || t0 === 'light') root.dataset.theme = t0;
  const tok = n => getComputedStyle(root).getPropertyValue(n).trim();
  const isDark = () => root.dataset.theme ? root.dataset.theme === 'dark' : !lightMQ.matches;
  const clamp = (v, a, b) => Math.max(a, Math.min(b, v));
  const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const gb = mb => (mb / 1024).toFixed(mb >= 10240 ? 0 : 1).replace('.', ',');
  const hasGL = !!window.THREE && (() => { try { const c = document.createElement('canvas'); return !!(c.getContext('webgl') || c.getContext('experimental-webgl')); } catch (e) { return false; } })();

  const S = {
    user: '', role: '', twoFactor: false, license: null, version: '', settings: null,
    servers: [], tpls: [], tplById: {}, host: null,
    sel: null, pending: {}, consoleId: null, es: null,
    hist: { cpu: [], ram: [], disk: [], run: [] }
  };

  /* ---------------- API ---------------- */
  async function api(method, path, body) {
    const opt = { method, headers: { 'X-Panel': '1' }, credentials: 'same-origin' };
    if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
    const r = await fetch(path, opt);
    let data = null;
    try { data = await r.json(); } catch (e) { /* kein JSON */ }
    if (r.status === 401 && !/\/api\/(login|setup|login\/2fa)$/.test(path)) { stopApp(); closeDialogs(); showAuth('login'); throw new Error('Bitte anmelden'); }
    if (!r.ok) throw new Error((data && data.error) || ('Fehler ' + r.status));
    return data;
  }

  function closeDialogs() { $$('dialog[open]').forEach(d => d.close()); }

  /* ---------------- Toasts ---------------- */
  function toast(msg, kind) {
    const el = document.createElement('div');
    el.className = 'toast' + (kind ? ' ' + kind : '');
    el.innerHTML = '<i></i><span></span>';
    el.lastChild.textContent = msg;
    $('#toasts').appendChild(el);
    setTimeout(() => el.remove(), kind === 'bad' ? 6000 : 3400);
  }

  /* ---------------- Status ---------------- */
  const ST = {
    preparing: { l: 'Wird vorbereitet', t: '--accent' },
    installing: { l: 'Installiert', t: '--accent' },
    starting: { l: 'Startet', t: '--accent' },
    running: { l: 'Läuft', t: '--ok' },
    stopped: { l: 'Gestoppt', t: '--muted' },
    error: { l: 'Fehler', t: '--bad' }
  };
  const stInfo = s => ST[s.status] || ST.stopped;
  const statusLabel = s => s.status === 'installing' && s.progress > 0 ? 'Installiert · ' + Math.round(s.progress * 100) + ' %' : stInfo(s).l;
  const isOn = s => ['running', 'starting', 'installing', 'preparing'].includes(s.status);
  const byId = id => S.servers.find(s => s.id === id);
  const addr = () => (S.settings && S.settings.public_host) || location.hostname;
  const RANK = { viewer: 1, operator: 2, admin: 3 };
  const can = role => (RANK[S.role] || 0) >= RANK[role];
  const ROLE = { admin: 'Admin', operator: 'Betreiber', viewer: 'Zuschauer' };
  const portList = s => (s.ports || []).map(p => p.host + '/' + p.proto).join(', ');
  const pad2 = n => String(n).padStart(2, '0');
  const hasPl = s => s.status === 'running' && s.players >= 0;
  const load = s => s.status !== 'running' ? 0 : clamp(0.55 * (s.mem_mb / Math.max(1, s.memory_mb)) + 0.45 * Math.min(1, s.cpu / 100), 0, 1);

  /* ============ 3D: gemeinsamer Partikel-Kern (KI + Anmeldung) ============ */
  const CORE_P = {
    idle: { spin: .2, amp: .035, freq: 1.3, ring: .25, scale: 1 },
    listening: { spin: .45, amp: .09, freq: 3.2, ring: .7, scale: 1.06 },
    thinking: { spin: 1.7, amp: .17, freq: 7, ring: 2.6, scale: .92 },
    executing: { spin: .9, amp: .12, freq: 9, ring: 1.7, scale: 1.1 },
    done: { spin: .5, amp: .05, freq: 2, ring: .5, scale: 1.16 }
  };
  function makeCore(canvas, camZ) {
    const C = { state: 'idle', p: Object.assign({}, CORE_P.idle), canvas };
    C.r = new THREE.WebGLRenderer({ canvas, antialias: true, alpha: true });
    C.r.setPixelRatio(Math.min(devicePixelRatio, 2));
    C.scene = new THREE.Scene();
    C.cam = new THREE.PerspectiveCamera(40, 1, .1, 60);
    C.cam.position.set(0, 0, camZ || 6.4);
    const N = 760, base = new Float32Array(N * 3), gold = Math.PI * (3 - Math.sqrt(5));
    for (let i = 0; i < N; i++) {
      const y = 1 - (i / (N - 1)) * 2, rr = Math.sqrt(1 - y * y), th = gold * i;
      base[i * 3] = Math.cos(th) * rr * 1.45; base[i * 3 + 1] = y * 1.45; base[i * 3 + 2] = Math.sin(th) * rr * 1.45;
    }
    C.base = base;
    const geo = new THREE.BufferGeometry();
    geo.setAttribute('position', new THREE.BufferAttribute(base.slice(), 3));
    C.pmat = new THREE.PointsMaterial({ size: .05, transparent: true, opacity: .95 });
    C.points = new THREE.Points(geo, C.pmat);
    C.rmat = new THREE.MeshBasicMaterial({ transparent: true, opacity: .75 });
    C.r1 = new THREE.Mesh(new THREE.TorusGeometry(2.0, .012, 8, 180), C.rmat); C.r1.rotation.x = 1.2;
    C.r2 = new THREE.Mesh(new THREE.TorusGeometry(2.3, .008, 8, 180), C.rmat); C.r2.rotation.set(1.9, .5, 0);
    C.imat = new THREE.MeshBasicMaterial({ transparent: true, opacity: .22 });
    C.inner = new THREE.Mesh(new THREE.SphereGeometry(.6, 32, 32), C.imat);
    C.scene.add(C.points, C.r1, C.r2, C.inner);
    C.col = new THREE.Color(tok('--accent')); C.tcol = C.col.clone();
    const fit = () => { const w = canvas.clientWidth, h = canvas.clientHeight; if (!w || !h) return; C.r.setSize(w, h, false); C.cam.aspect = w / h; C.cam.updateProjectionMatrix(); };
    new ResizeObserver(fit).observe(canvas); fit();
    C.set = st => { C.state = st; C.tcol = new THREE.Color(tok(st === 'done' ? '--ok' : '--accent')); };
    C.tick = (t, dt) => {
      if (!canvas.offsetParent && canvas.id !== 'authBg') return;
      const tp = CORE_P[C.state], k = 1 - Math.exp(-dt * 3), m = motion ? 1 : 0;
      for (const key in tp) C.p[key] += (tp[key] - C.p[key]) * (motion ? k : 1);
      const p = C.p; C.col.lerp(C.tcol, motion ? k * 1.5 : 1);
      C.pmat.color.copy(C.col); C.rmat.color.copy(C.col); C.imat.color.copy(C.col);
      const pos = C.points.geometry.attributes.position.array, b = C.base, n = b.length / 3;
      for (let i = 0; i < n; i++) {
        const f = (1 + m * p.amp * Math.sin(t * p.freq + i * .37) + m * p.amp * .5 * Math.sin(t * p.freq * .5 + b[i * 3 + 1] * 3)) * p.scale;
        pos[i * 3] = b[i * 3] * f; pos[i * 3 + 1] = b[i * 3 + 1] * f; pos[i * 3 + 2] = b[i * 3 + 2] * f;
      }
      C.points.geometry.attributes.position.needsUpdate = true;
      C.points.rotation.y += dt * p.spin * m; C.points.rotation.x = m ? Math.sin(t * .3) * .2 : 0;
      C.r1.rotation.z += dt * p.ring * m; C.r2.rotation.z -= dt * p.ring * .8 * m;
      C.inner.scale.setScalar(p.scale * (1 + m * .08 * Math.sin(t * p.freq * .6)));
      C.r.render(C.scene, C.cam);
    };
    return C;
  }

  /* ============ 3D: Server-Landschaft ============ */
  const L = { blocks: new Map(), theta: .62, phi: 1.0, r: 18, tr: 18, base: 18, hover: null, drag: false, w: 1, h: 1, pw: 0, pd: 0 };
  const SP = 4.2;
  function initLand() {
    const cv = $('#land');
    L.renderer = new THREE.WebGLRenderer({ canvas: cv, antialias: true, alpha: true });
    L.renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
    L.scene = new THREE.Scene();
    L.cam = new THREE.PerspectiveCamera(36, 1, .1, 300);
    L.target = new THREE.Vector3(); L.tt = new THREE.Vector3();
    L.scene.add(new THREE.AmbientLight(0xffffff, .62));
    const d = new THREE.DirectionalLight(0xffffff, .7); d.position.set(6, 14, 9); L.scene.add(d);
    L.glow = new THREE.PointLight(0xff5a1f, .6, 30); L.glow.position.set(0, 5, 0); L.scene.add(L.glow);
    L.world = new THREE.Group(); L.scene.add(L.world);
    L.slabMat = new THREE.MeshStandardMaterial({ roughness: .95, metalness: 0 });
    L.edgeMat = new THREE.LineBasicMaterial();
    L.gridMat = new THREE.LineBasicMaterial({ transparent: true, opacity: .5 });
    L.ringGeo = new THREE.RingGeometry(1.6, 1.68, 48);
    L.dotGeo = new THREE.SphereGeometry(.09, 10, 10);
    L.dotMat = new THREE.MeshBasicMaterial();
    L.ray = new THREE.Raycaster(); L.ndc = new THREE.Vector2(); L.v = new THREE.Vector3();
    bindLandInput(cv);
    new ResizeObserver(sizeLand).observe($('#stage')); sizeLand();
    applySceneColors();
  }
  function sizeLand() {
    const r = $('#stage').getBoundingClientRect(); L.w = r.width; L.h = r.height;
    if (!L.renderer || !L.w) return;
    L.renderer.setSize(L.w, L.h, false); L.cam.aspect = L.w / L.h; L.cam.updateProjectionMatrix();
    fitBase(); if (!S.sel) L.tr = L.base;
  }
  function fitBase() { const span = Math.max(L.pw, L.pd, 13); L.base = span * (L.w < 560 ? 2.05 : L.w < 820 ? 1.6 : 1.38); }
  function layout(n) {
    const cols = Math.min(4, Math.max(2, Math.ceil(Math.sqrt(Math.max(1, n)))));
    const rows = Math.max(2, Math.ceil(n / cols));
    return { cols, rows, pos: i => [((i % cols) - (cols - 1) / 2) * SP, (Math.floor(i / cols) - (rows - 1) / 2) * SP] };
  }
  function buildPlatform(w, dpt) {
    if (L.slab) { L.world.remove(L.slab, L.slabEdge, L.grid); L.slab.geometry.dispose(); L.slabEdge.geometry.dispose(); L.grid.geometry.dispose(); }
    const g = new THREE.BoxGeometry(w, .5, dpt);
    L.slab = new THREE.Mesh(g, L.slabMat); L.slab.position.y = -.25;
    L.slabEdge = new THREE.LineSegments(new THREE.EdgesGeometry(g), L.edgeMat); L.slabEdge.position.y = -.25;
    const pts = [], hw = w / 2, hd = dpt / 2;
    for (let x = -Math.floor(hw); x <= hw; x++) pts.push(x, .005, -hd, x, .005, hd);
    for (let z = -Math.floor(hd); z <= hd; z++) pts.push(-hw, .005, z, hw, .005, z);
    const gg = new THREE.BufferGeometry(); gg.setAttribute('position', new THREE.Float32BufferAttribute(pts, 3));
    L.grid = new THREE.LineSegments(gg, L.gridMat);
    L.world.add(L.slab, L.slabEdge, L.grid);
    L.pw = w; L.pd = dpt; fitBase(); if (!S.sel) L.tr = L.base;
  }
  function syncLand() {
    if (!L.renderer) return;
    const lay = layout(S.servers.length);
    const w = lay.cols * SP + .6, dpt = lay.rows * SP + .4;
    if (w !== L.pw || dpt !== L.pd) buildPlatform(w, dpt);
    const ids = new Set(S.servers.map(s => s.id));
    L.blocks.forEach((b, id) => { if (!ids.has(id)) { L.world.remove(b.g); b.lab.remove(); L.blocks.delete(id); } });
    S.servers.forEach((s, i) => {
      let b = L.blocks.get(s.id);
      if (!b) b = addBlock(s);
      b.s = s; const [x, z] = lay.pos(i); b.tx = x; b.tz = z;
      tintBlock(b); updateLabel(b);
    });
    $('#stageEmpty').hidden = S.servers.length > 0;
  }
  function addBlock(s) {
    const g = new THREE.Group();
    const geo = new THREE.BoxGeometry(2.6, 1, 2.6);
    const mesh = new THREE.Mesh(geo, new THREE.MeshStandardMaterial({ roughness: .7, metalness: .05 }));
    mesh.userData.id = s.id;
    const edges = new THREE.LineSegments(new THREE.EdgesGeometry(geo), new THREE.LineBasicMaterial());
    const cap = new THREE.Mesh(new THREE.PlaneGeometry(2.6, 2.6), new THREE.MeshBasicMaterial({ transparent: true, opacity: .9, side: THREE.DoubleSide }));
    cap.rotation.x = -Math.PI / 2;
    const r = 1.75, pg = new THREE.BufferGeometry();
    pg.setAttribute('position', new THREE.Float32BufferAttribute([-r, .012, -r, r, .012, -r, r, .012, r, -r, .012, r], 3));
    const pad = new THREE.LineLoop(pg, new THREE.LineBasicMaterial({ transparent: true, opacity: .85 }));
    const beam = new THREE.Mesh(new THREE.CylinderGeometry(.07, .07, 16, 12, 1, true), new THREE.MeshBasicMaterial({ transparent: true, opacity: 0, depthWrite: false }));
    beam.position.y = 8;
    const ring = new THREE.Mesh(L.ringGeo, new THREE.MeshBasicMaterial({ transparent: true, opacity: 0, side: THREE.DoubleSide, depthWrite: false }));
    ring.rotation.x = -Math.PI / 2; ring.position.y = .02;
    g.add(mesh, edges, cap, pad, beam, ring);
    L.world.add(g);
    const lab = document.createElement('div'); lab.className = 'lab'; $('#labels').appendChild(lab);
    const b = { s, g, mesh, edges, cap, pad, beam, ring, lab, h: .01, tx: 0, tz: 0, ph: Math.random() * 3, dots: [] };
    L.blocks.set(s.id, b);
    return b;
  }
  function tintBlock(b) {
    const c = new THREE.Color(tok(stInfo(b.s).t));
    b.mesh.material.color.set(tok('--panel-2')); b.mesh.material.emissive.copy(c);
    b.edges.material.color.copy(c); b.cap.material.color.copy(c); b.pad.material.color.copy(c); b.ring.material.color.copy(c);
    b.cap.material.opacity = b.s.status === 'stopped' ? .3 : .88;
    b.beam.material.color.set(tok('--accent'));
  }
  function updateLabel(b) {
    const html = `<span class="dot" style="background:var(${stInfo(b.s).t})"></span>${esc(b.s.name)}`;
    if (b.lab._h !== html) { b.lab.innerHTML = html; b.lab._h = html; }
  }
  function applySceneColors() {
    if (L.renderer) {
      L.slabMat.color.set(tok('--panel')); L.edgeMat.color.set(tok('--line')); L.gridMat.color.set(tok('--line'));
      L.dotMat.color.set(tok('--ink'));
      L.blocks.forEach(tintBlock);
    }
    if (KI.core) KI.core.set(KI.core.state);
    if (AuthBg) AuthBg.set('idle');
    drawSparks();
  }
  function targetH(s) {
    switch (s.status) {
      case 'running': return .9 + load(s) * 3.2;
      case 'starting': return .7;
      case 'installing': return .25 + (s.progress || 0) * 1.8;
      case 'preparing': return .25;
      default: return .3;
    }
  }
  function pick(e) {
    const r = $('#land').getBoundingClientRect();
    L.ndc.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
    L.ray.setFromCamera(L.ndc, L.cam);
    const hits = L.ray.intersectObjects([...L.blocks.values()].map(b => b.mesh));
    return hits.length ? hits[0].object.userData.id : null;
  }
  function bindLandInput(cv) {
    let down = null;
    cv.addEventListener('pointerdown', e => { down = { x: e.clientX, y: e.clientY, moved: false }; cv.setPointerCapture(e.pointerId); L.drag = true; });
    cv.addEventListener('pointermove', e => {
      if (down) {
        const dx = e.clientX - down.x, dy = e.clientY - down.y;
        if (Math.abs(dx) + Math.abs(dy) > 2) { down.moved = true; cv.style.cursor = 'grabbing'; }
        L.theta -= dx * .008; L.phi = clamp(L.phi - dy * .006, .42, 1.32); down.x = e.clientX; down.y = e.clientY; return;
      }
      L.hover = pick(e); cv.style.cursor = L.hover ? 'pointer' : 'grab';
    });
    cv.addEventListener('pointerup', e => {
      if (down && !down.moved) { const id = pick(e); if (id) select(id); }
      down = null; L.drag = false; cv.style.cursor = 'grab';
    });
    cv.addEventListener('pointercancel', () => { down = null; L.drag = false; });
    cv.addEventListener('pointerleave', () => { if (!down) L.hover = null; });
    cv.addEventListener('wheel', e => { if (!(e.ctrlKey || e.metaKey)) return; e.preventDefault(); L.tr = clamp(L.tr + e.deltaY * .03, 6, 60); }, { passive: false });
  }
  function tickLand(t, dt) {
    if (!L.renderer || R.page !== 'home') return;
    const k = motion ? 1 - Math.exp(-dt * 3) : 1;
    if (motion && !L.drag && !S.sel) L.theta += dt * .06;
    L.r += (L.tr - L.r) * k; L.target.lerp(L.tt, k);
    const tg = L.target, r = L.r;
    L.cam.position.set(tg.x + r * Math.sin(L.phi) * Math.sin(L.theta), tg.y + r * Math.cos(L.phi), tg.z + r * Math.sin(L.phi) * Math.cos(L.theta));
    L.cam.lookAt(tg);
    L.world.position.y = motion ? Math.sin(t * .7) * .08 : 0;
    const hk = motion ? 1 - Math.exp(-dt * 2.2) : 1;
    let busy = false;
    L.blocks.forEach(b => {
      const s = b.s;
      b.g.position.x += (b.tx - b.g.position.x) * hk; b.g.position.z += (b.tz - b.g.position.z) * hk;
      b.h += (targetH(s) - b.h) * hk; const h = Math.max(.02, b.h);
      b.mesh.scale.y = h; b.mesh.position.y = h / 2; b.edges.scale.y = h; b.edges.position.y = h / 2; b.cap.position.y = h + .004;
      const work = ['preparing', 'installing', 'starting'].includes(s.status); if (work) busy = true;
      let ei = s.status === 'stopped' ? 0 : .12;
      if (work) ei = .18 + (motion ? .14 * Math.sin(t * 5) : 0);
      if (s.status === 'error') ei = .25;
      if (L.hover === s.id || S.sel === s.id) ei += .22;
      b.mesh.material.emissiveIntensity = ei;
      const bo = (s.status === 'installing' || s.status === 'preparing') ? .4 + (motion ? .2 * Math.sin(t * 7) : 0) : 0;
      b.beam.material.opacity += (bo - b.beam.material.opacity) * .08;
      if (s.status === 'running' && motion) {
        const ph = ((t + b.ph) % 2.6) / 2.6; b.ring.scale.setScalar(1 + ph * 1.4); b.ring.material.opacity = (1 - ph) * .55;
      } else b.ring.material.opacity = 0;
      const want = hasPl(s) ? Math.min(s.players, 24) : 0;
      while (b.dots.length < want) {
        const m = new THREE.Mesh(L.dotGeo, L.dotMat); b.g.add(m);
        b.dots.push({ m, x: (Math.random() - .5) * 2.2, z: (Math.random() - .5) * 2.2, tx: (Math.random() - .5) * 2.2, tz: (Math.random() - .5) * 2.2, sp: .4 + Math.random() * .5, ph: Math.random() * 6 });
      }
      while (b.dots.length > want) b.g.remove(b.dots.pop().m);
      b.dots.forEach(d => {
        if (motion) {
          const dx = d.tx - d.x, dz = d.tz - d.z, dist = Math.hypot(dx, dz);
          if (dist < .05) { d.tx = (Math.random() - .5) * 2.2; d.tz = (Math.random() - .5) * 2.2; }
          else { d.x += dx / dist * dt * d.sp; d.z += dz / dist * dt * d.sp; }
        }
        d.m.position.set(d.x, h + .1 + (motion ? Math.abs(Math.sin(t * 6 + d.ph)) * .07 : 0), d.z);
      });
      b.g.getWorldPosition(L.v); L.v.y += h + .6; L.v.project(L.cam);
      const x = (L.v.x * .5 + .5) * L.w, y = (-L.v.y * .5 + .5) * L.h;
      b.lab.style.transform = `translate(${x.toFixed(1)}px,${y.toFixed(1)}px) translate(-50%,-100%)`;
      b.lab.style.opacity = L.v.z > 1 ? 0 : 1;
      b.lab.classList.toggle('hot', L.hover === s.id || S.sel === s.id);
    });
    L.glow.intensity = .5 + ((busy || KI.core && KI.core.state === 'executing') ? (motion ? .7 + .4 * Math.sin(t * 8) : 1) : 0);
    L.renderer.render(L.scene, L.cam);
  }
  function select(id, tab) { if (byId(id)) go('#/server/' + encodeURIComponent(id) + (tab && tab !== 'console' ? '/' + tab : '')); }
  $('#zIn').onclick = () => { L.tr = clamp(L.tr - 3, 6, 60); };
  $('#zOut').onclick = () => { L.tr = clamp(L.tr + 3, 6, 60); };

  /* ---------------- Server-Seite: Kopf + Infos ---------------- */
  function renderServerPage() {
    const s = byId(R.id);
    if (!s) { if (S.loaded) { toast('Diesen Server gibt es nicht (mehr).', 'bad'); go('#/server'); } return; }
    const on = isOn(s), busy = !!S.pending[s.id] || s.status === 'preparing';
    const set = (el, html) => { if (el._h !== html) { el.innerHTML = html; el._h = html; } };
    $('#svGame').textContent = s.template_name + (s.category ? ' · ' + s.category : '');
    $('#svName').textContent = s.name;
    const port = s.ports && s.ports[0] ? s.ports[0].host : '';
    set($('#svMeta'), `<span class="pill ${esc(s.status)}"><span class="dot" style="background:var(${stInfo(s).t})"></span>${esc(statusLabel(s))}</span>
      ${port ? `<button type="button" class="addr" data-copy="${esc(addr() + ':' + port)}" title="Adresse kopieren">${esc(addr())}:${port}</button>` : ''}
      ${hasPl(s) ? `<span>${s.players} / ${s.max_players} Spieler</span>` : ''}
      ${s.status === 'running' ? `<span>CPU ${Math.round(s.cpu)} %</span><span>RAM ${gb(s.mem_mb)} / ${gb(s.memory_mb)} GB</span>` : `<span>RAM ${gb(s.memory_mb)} GB</span>`}`);
    const pw = $('[data-sv="power"]'); pw.textContent = on ? 'Stoppen' : 'Starten'; pw.disabled = busy;
    pw.classList.toggle('btn-accent', !on); pw.classList.toggle('btn-line', on);
    $('[data-sv="restart"]').disabled = !on || busy;
    const m = $('#svMsg'); m.hidden = !s.message; m.textContent = s.message || ''; m.classList.toggle('bad', s.status === 'error');
    $('#svRouterBtn').textContent = s.no_router ? 'Ports im Router öffnen' : 'Ports nicht im Router öffnen'; $('#svRouterBtn').hidden = !S.routerOn;
    const tpl = S.tplById[s.template];
    $('#tabMods').hidden = !(tpl && tpl.mods);
    // settings that wait for the first start reload once the status moves on
    if (MG.tab === 'settings' && GS.data && GS.status !== s.status && GS.data.files.some(f => !f.editable) && !document.activeElement.closest('#stForm')) loadSettings();
    set($('#svInfo'), `<h2>Details</h2><dl class="kv one">
        <div><dt>Adresse für Spieler</dt><dd>${port ? esc(addr()) + ':' + port : '–'}</dd></div>
        <div><dt>Ports</dt><dd>${esc(portList(s)) || '–'}</dd></div>
        <div><dt>Arbeitsspeicher</dt><dd>${s.status === 'running' ? gb(s.mem_mb) + ' von ' : ''}${gb(s.memory_mb)} GB</dd></div>
        <div><dt>CPU</dt><dd>${s.status === 'running' ? Math.round(s.cpu) + ' %' : '–'}</dd></div>
        <div><dt>Spieler</dt><dd>${hasPl(s) ? s.players + ' von ' + s.max_players : '–'}</dd></div>
        <div><dt>Angelegt</dt><dd>${new Date(s.created).toLocaleDateString('de-DE')}${s.created_by === 'ai' ? ' mit KI' : ''}</dd></div>
        <div><dt>ID</dt><dd class="mono">${esc(s.id)}</dd></div>
        ${routerRow(s)}
      </dl>${s.notes ? `<p class="note">${esc(s.notes)}</p>` : ''}`);
  }
  $('#svActs').addEventListener('click', e => {
    const b = e.target.closest('[data-sv]'); if (!b) return;
    const s = byId(R.id); if (!s) return;
    const a = b.dataset.sv;
    $('#svMore').open = false;
    if (a === 'settings-tab') select(s.id, 'settings');
    else if (a === 'router') setServerRouter(s, !!s.no_router);
    else if (a === 'power') togglePower(s);
    else if (a === 'restart') act(s, 'restart', 'Neustart ausgelöst');
    else if (a === 'edit') openEdit(s);
    else if (a === 'delete') openDelete(s);
  });
  document.addEventListener('click', async e => {
    const c = e.target.closest('[data-copy]'); if (!c) return;
    try { await navigator.clipboard.writeText(c.dataset.copy); toast('Adresse kopiert: ' + c.dataset.copy, 'ok'); } catch (err) { toast(c.dataset.copy); }
  });

  /* ---------------- Aktionen ---------------- */
  async function act(s, action, okMsg) {
    S.pending[s.id] = true; renderAll();
    try { await api('POST', `/api/servers/${s.id}/${action}`); if (okMsg) toast(okMsg); }
    catch (e) { toast(e.message, 'bad'); }
    finally { delete S.pending[s.id]; await loadServers(); }
  }
  function togglePower(s) {
    if (!can('operator')) return;
    if (S.pending[s.id] || s.status === 'preparing') return;
    if (isOn(s)) act(s, 'stop', s.name + ' wird gestoppt');
    else act(s, 'start', s.name + ' wird gestartet');
  }

  /* ---------------- Karten ---------------- */
  function cardHTML(s, i) {
    const on = isOn(s), busy = !!S.pending[s.id] || s.status === 'preparing';
    let body;
    if (s.status === 'installing' || s.status === 'preparing') {
      const pct = Math.round((s.progress || 0) * 100);
      body = `<div class="mono muted">${esc(s.message || 'Bitte warten')}</div><div class="bar ${pct ? '' : 'indet'}"><b style="width:${pct}%"></b></div>`;
    } else {
      const first = hasPl(s)
        ? `<div><span class="k">Spieler</span><span class="v">${pad2(s.players)}<small>/${pad2(s.max_players)}</small></span></div>`
        : `<div><span class="k">CPU</span><span class="v">${s.status === 'running' ? Math.round(s.cpu) : '–'}<small> %</small></span></div>`;
      body = `<div class="mod-foot">${first}
        <div><span class="k">RAM</span><span class="v">${s.status === 'running' ? gb(s.mem_mb) : '–'}<small> / ${gb(s.memory_mb)} GB</small></span></div>
        <button class="power" type="button" aria-pressed="${on}" aria-label="${esc(s.name)} ${on ? 'ausschalten' : 'einschalten'}" ${busy ? 'disabled' : ''}><span></span></button></div>
        ${s.message ? `<p class="msg-line ${s.status === 'error' ? 'bad' : ''}">${esc(s.message)}</p>` : ''}`;
    }
    return `<div class="mod-top"><span class="mono game">${s.created_by === 'selbsttest' ? 'Selbsttest · ' : ''}${esc(s.template_name)} · ${esc(portList(s))}</span><span class="st mono"><span class="dot" style="background:var(${stInfo(s).t})"></span>${esc(statusLabel(s))}</span></div>
      <h3>${esc(s.name)}</h3>${body}`;
  }
  const SF = { q: '', f: '' };
  const matches = s => (!SF.q || (s.name + ' ' + s.template_name).toLowerCase().includes(SF.q)) && (!SF.f || (SF.f === 'on' ? isOn(s) : !isOn(s)));
  function renderCards() {
    const box = $('#mods');
    const list = S.servers.filter(matches);
    if (S.servers.length && !list.length) {
      $$('.mod', box).forEach(el => el.remove());
      if (!$('.empty', box)) box.insertAdjacentHTML('beforeend', '<div class="empty"></div>');
      $('.empty', box).textContent = 'Kein Server passt zur Suche.';
    } else if (!S.servers.length) {
      box.innerHTML = '<div class="empty">' + (can('admin') ? 'Hier erscheinen deine Gameserver. Leg oben rechts den ersten an.' : 'Hier erscheinen die Server, die dir freigegeben sind.') + '</div>';
    } else {
      const empty = $('.empty', box); if (empty) empty.remove();
      const seen = new Set();
      list.forEach((s, i) => {
        seen.add(s.id);
        let el = box.querySelector(`.mod[data-id="${s.id}"]`);
        if (!el) { el = document.createElement('article'); el.dataset.id = s.id; el.style.animation = 'rise .5s both'; box.appendChild(el); }
        const cls = `mod ${s.status}`;
        if (el.className !== cls) el.className = cls;
        const html = cardHTML(s, i);
        if (el._h !== html) { el.innerHTML = html; el._h = html; }
      });
      $$('.mod', box).forEach(el => { if (!seen.has(el.dataset.id)) el.remove(); });
    }
    const run = S.servers.filter(s => s.status === 'running').length;
    $('#modCount').textContent = S.servers.length === 1 ? '1 Server' + (run ? ', läuft' : '') : S.servers.length + ' Server, ' + run + ' davon laufen';
    $('#navCount').textContent = S.servers.length || '';
  }
  function renderHomeList() {
    const box = $('#homeList');
    const html = S.servers.length ? S.servers.slice(0, 8).map(s => {
      const on = isOn(s), busy = !!S.pending[s.id] || s.status === 'preparing';
      return `<li data-id="${esc(s.id)}"><a class="row" href="#/server/${encodeURIComponent(s.id)}"><span class="dot" style="background:var(${stInfo(s).t})"></span>
        <span class="nm"><b>${esc(s.name)}</b><small>${esc(s.template_name)}</small></span>
        <span class="rt">${hasPl(s) ? s.players + ' / ' + s.max_players + ' Spieler' : esc(statusLabel(s))}</span></a>
        <button class="power op-only" type="button" aria-pressed="${on}" aria-label="${esc(s.name)} ${on ? 'ausschalten' : 'einschalten'}" ${busy ? 'disabled' : ''}><span></span></button></li>`;
    }).join('') + (S.servers.length > 8 ? `<li class="more"><a href="#/server">und ${S.servers.length - 8} weitere</a></li>` : '')
      : `<li class="none">${can('admin') ? 'Noch kein Server angelegt.' : 'Dir ist noch kein Server freigegeben.'}</li>`;
    if (box._h !== html) { box.innerHTML = html; box._h = html; }
    const run = S.servers.filter(x => x.status === 'running').length;
    $('#homeSub').textContent = S.servers.length ? `${run} von ${S.servers.length} Servern laufen` : 'Willkommen im Panel';
  }
  $('#homeList').addEventListener('click', e => {
    const p = e.target.closest('.power'); if (!p) return;
    e.preventDefault(); const s = byId(p.closest('li').dataset.id); if (s) togglePower(s);
  });
  $('#srvSearch').addEventListener('input', e => { SF.q = e.target.value.trim().toLowerCase(); renderCards(); });
  $('#srvFilter').addEventListener('click', e => {
    const b = e.target.closest('button'); if (!b) return;
    SF.f = b.dataset.f; $$('#srvFilter button').forEach(x => x.setAttribute('aria-pressed', String(x === b))); renderCards();
  });
  const mods = $('#mods');
  mods.addEventListener('click', e => {
    const c = e.target.closest('.mod'); if (!c) return;
    const s = byId(c.dataset.id); if (!s) return;
    if (e.target.closest('.power')) { togglePower(s); return; }
    select(s.id);
  });
  mods.addEventListener('pointermove', e => {
    if (!motion || e.pointerType !== 'mouse') return;
    const c = e.target.closest('.mod'); if (!c) return;
    const r = c.getBoundingClientRect(), px = (e.clientX - r.left) / r.width - .5, py = (e.clientY - r.top) / r.height - .5;
    c.style.transform = `perspective(800px) rotateX(${(-py * 6).toFixed(2)}deg) rotateY(${(px * 8).toFixed(2)}deg) translateY(-2px)`;
    c.style.setProperty('--gx', ((px + .5) * 100).toFixed(1) + '%'); c.style.setProperty('--gy', ((py + .5) * 100).toFixed(1) + '%');
  });
  mods.addEventListener('pointerout', e => { const c = e.target.closest('.mod'); if (c && !c.contains(e.relatedTarget)) c.style.transform = ''; });

  function renderAll() { renderCards(); renderHomeList(); syncLand(); if (R.page === 'server') renderServerPage(); trackAI(); }

  /* ---------------- Konsole ---------------- */
  function addLine(text, cls) {
    const box = $('#lines');
    const nearBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
    const d = document.createElement('div');
    d.textContent = text;
    if (cls) d.className = cls;
    else if (/\[panel\]/i.test(text)) d.className = 'g';
    else if (/warn|error|exception|fehl/i.test(text)) d.className = 'w';
    box.appendChild(d);
    while (box.children.length > 600) box.firstChild.remove();
    if (nearBottom) box.scrollTop = box.scrollHeight;
  }
  function closeConsole() { if (S.es) { S.es.close(); S.es = null; } $('#conLive').hidden = true; S.consoleId = null; }
  function openConsole(id) {
    closeConsole();
    const s = byId(id); if (!s) return;
    S.consoleId = id;
    $('#conName').textContent = 'Konsole';
    $('#lines').innerHTML = isOn(s) ? '' : '<div class="dim">Der Server ist gestoppt. Nach dem Start erscheint hier seine Ausgabe.</div>';
    $('#cmdInput').disabled = false;
    $('#cmdInput').placeholder = s.console === 'exec' ? 'Befehl senden (z. B. list)' : 'Befehl senden';
    connectConsole(id);
  }
  function connectConsole(id) {
    if (S.consoleId !== id) return;
    const es = new EventSource(`/api/servers/${id}/console`);
    S.es = es;
    es.onopen = () => { $('#conLive').hidden = false; };
    es.onmessage = e => addLine(e.data);
    es.addEventListener('end', e => { addLine(e.data, 'dim'); es.close(); $('#conLive').hidden = true; retry(); });
    es.onerror = () => { es.close(); $('#conLive').hidden = true; retry(); };
    function retry() {
      if (S.es !== es) return;
      S.es = null;
      setTimeout(() => { const s = byId(id); if (S.consoleId === id && !S.es && s && isOn(s)) connectConsole(id); else if (S.consoleId === id && !S.es) S.reconnect = id; }, 4000);
    }
  }
  $('#cmdForm').addEventListener('submit', async e => {
    e.preventDefault();
    const inp = $('#cmdInput'), cmd = inp.value.trim(), id = S.consoleId;
    if (!cmd || !id) return;
    inp.value = '';
    addLine('› ' + cmd, 'me');
    try {
      const r = await api('POST', `/api/servers/${id}/command`, { command: cmd });
      if (r && r.output) r.output.split('\n').filter(Boolean).forEach(l => addLine(l));
    } catch (err) { addLine(err.message, 'w'); }
  });

  /* ---------------- Werte + Verläufe ---------------- */
  function pushHist(k, v) { const h = S.hist[k]; h.push(v); if (h.length > 45) h.shift(); }
  function setV(id, html) { const el = document.getElementById(id); if (el._h !== html) { el.innerHTML = html; el._h = html; } }
  function renderHost() {
    const h = S.host; if (!h) return;
    const run = S.servers.filter(s => s.status === 'running').length;
    const pl = S.servers.reduce((a, s) => a + (hasPl(s) ? s.players : 0), 0);
    pushHist('cpu', h.cpu_percent); pushHist('ram', h.mem_used_mb / 1024); pushHist('disk', h.disk_used_gb); pushHist('run', pl);
    $('#hostName').textContent = 'CPU · ' + (h.hostname || 'Rechner');
    setV('vCpu', Math.round(h.cpu_percent) + '<small>% · ' + h.cores + ' Kerne</small>');
    setV('vRam', (h.mem_used_mb / 1024).toFixed(1).replace('.', ',') + '<small> / ' + (h.mem_total_mb / 1024).toFixed(0) + ' GB</small>');
    setV('vDisk', Math.round(h.disk_used_gb) + '<small> / ' + Math.round(h.disk_total_gb) + ' GB</small>');
    setV('vRun', pl + '<small> · ' + run + ' von ' + S.servers.length + ' Servern aktiv</small>');
    drawSparks();
  }
  function drawSpark(id, arr, lo, hi) {
    const c = document.getElementById(id); if (!c || arr.length < 2) return;
    const dpr = Math.min(devicePixelRatio, 2), w = c.clientWidth, h = c.clientHeight; if (!w) return;
    c.width = w * dpr; c.height = h * dpr;
    const x = c.getContext('2d'); x.scale(dpr, dpr); x.clearRect(0, 0, w, h);
    const mn = lo != null ? lo : Math.min(...arr) - 1, mx = hi != null ? hi : Math.max(...arr) + 1;
    const X = i => i / (arr.length - 1) * (w - 6) + 1, Y = v => h - 3 - ((v - mn) / (mx - mn || 1)) * (h - 8);
    x.strokeStyle = tok('--line'); x.lineWidth = 1; x.beginPath(); x.moveTo(0, h - .5); x.lineTo(w, h - .5); x.stroke();
    x.beginPath(); arr.forEach((v, i) => i ? x.lineTo(X(i), Y(v)) : x.moveTo(X(i), Y(v)));
    const ink = tok('--ink'); x.strokeStyle = ink; x.lineWidth = 1.5; x.lineJoin = 'round'; x.stroke();
    x.lineTo(X(arr.length - 1), h); x.lineTo(X(0), h); x.closePath(); x.globalAlpha = .1; x.fillStyle = ink; x.fill(); x.globalAlpha = 1;
    x.fillStyle = tok('--accent'); x.beginPath(); x.arc(X(arr.length - 1), Y(arr[arr.length - 1]), 3, 0, Math.PI * 2); x.fill();
  }
  function drawSparks() {
    const h = S.host;
    drawSpark('sCpu', S.hist.cpu, 0, 100);
    drawSpark('sRam', S.hist.ram, 0, h ? h.mem_total_mb / 1024 : null);
    drawSpark('sDisk', S.hist.disk, null, null);
    drawSpark('sRun', S.hist.run, 0, Math.max(4, ...S.hist.run) + 1);
  }

  /* ---------------- Formular-Bausteine ---------------- */
  function varField(v, val, forEdit, pre) {
    const id = (pre || 'v_') + v.key, req = v.required ? ' *' : '';
    const help = v.help ? `<small>${esc(v.help)}</small>` : '';
    const full = (v.type === 'command' || (v.help && v.help.length > 50)) ? ' full' : '';
    if (v.type === 'bool') {
      return `<label class="check${full}"><input type="checkbox" data-var="${esc(v.key)}" ${val === 'true' ? 'checked' : ''}> ${esc(v.label)}</label>`;
    }
    let input;
    if (v.type === 'select') {
      input = `<select id="${id}" data-var="${esc(v.key)}">${(v.options || []).map(o => `<option ${o === val ? 'selected' : ''}>${esc(o)}</option>`).join('')}</select>`;
    } else {
      const type = v.type === 'secret' ? 'password' : v.type === 'number' ? 'number' : 'text';
      const secretKeep = forEdit && v.type === 'secret' && val === '••••••';
      input = `<input id="${id}" data-var="${esc(v.key)}" type="${type}" value="${secretKeep ? '' : esc(val)}" ${secretKeep ? 'placeholder="unverändert"' : ''} autocomplete="off" ${v.required && !secretKeep ? 'required' : ''}>`;
    }
    return `<label class="field${full}"><span>${esc(v.label)}${req}</span>${input}${help}</label>`;
  }
  function collectVars(box, forEdit) {
    const out = {};
    $$('[data-var]', box).forEach(el => {
      const k = el.dataset.var;
      if (el.type === 'checkbox') out[k] = el.checked ? 'true' : 'false';
      else if (forEdit && el.type === 'password' && el.value === '') { /* unverändert */ }
      else out[k] = el.value;
    });
    return out;
  }
  function memRange(input, label, min, val) {
    const hostMax = S.host ? Math.floor(S.host.mem_total_mb / 256) * 256 : 32768;
    input.min = min; input.max = Math.max(min, hostMax); input.value = clamp(val, min, input.max);
    const show = () => { label.textContent = (input.value / 1024).toFixed(input.value % 1024 ? 2 : 0).replace('.', ',') + ' GB'; };
    input.oninput = show; show();
  }
  function showErr(el, msg) { el.textContent = msg || ''; el.hidden = !msg; }
  document.addEventListener('click', e => { const c = e.target.closest('[data-close]'); if (c) c.closest('dialog').close(); });

  /* ---------------- Server anlegen ---------------- */
  let createPrefill = null;
  const CAT_ORDER = ['Minecraft', 'Steam', 'Steam (Windows, Wine)', 'Weitere Spiele', 'Sprachserver', 'Steam (Community)'];
  const STATUS_TAG = { beta: ' · beta', experimentell: ' · experimentell' };
  const loginTag = t => t.steam_login ? ' · Steam-Konto' : '';
  const usable = () => S.tpls.filter(t => t.status !== 'defekt');
  function fillPicker(keep) {
    const q = $('#cSearch').value.trim().toLowerCase();
    const sel = $('#cTpl'), cur = keep || sel.value;
    const list = usable().filter(t => !q || t.name.toLowerCase().includes(q) || t.id.includes(q) || String(t.steam_app_id || '') === q);
    const cats = {};
    list.forEach(t => { (cats[t.category] = cats[t.category] || []).push(t); });
    const order = Object.keys(cats).sort((a, b) => (CAT_ORDER.indexOf(a) + 99) % 99 - (CAT_ORDER.indexOf(b) + 99) % 99 || a.localeCompare(b));
    sel.innerHTML = order.map(c => `<optgroup label="${esc(c)}">${cats[c].sort((a, b) => a.name.localeCompare(b.name)).map(t => `<option value="${esc(t.id)}">${esc(t.name)}${STATUS_TAG[t.status] || ''}${loginTag(t)}</option>`).join('')}</optgroup>`).join('');
    $('#cCount').textContent = list.length === usable().length ? list.length + ' Spiele' : list.length + ' von ' + usable().length;
    if (list.some(t => t.id === cur)) sel.value = cur; else if (list.length) sel.value = list[0].id;
    return list.length;
  }
  $('#cSearch').addEventListener('input', () => { if (fillPicker()) fillCreate(); });
  $('#cSearch').addEventListener('keydown', e => { if (e.key === 'ArrowDown') { e.preventDefault(); $('#cTpl').focus(); } if (e.key === 'Enter') e.preventDefault(); });
  function openCreate(prefill) {
    createPrefill = prefill || null;
    $('#cSearch').value = '';
    fillPicker(prefill && prefill.template ? prefill.template : (S.tplById['minecraft-java'] ? 'minecraft-java' : S.tpls[0].id));
    $('#createTitle').textContent = prefill ? 'KI-Vorschlag prüfen' : 'Server anlegen';
    fillCreate();
    showErr($('#cErr'));
    $('#dlgCreate').showModal();
  }
  function fillCreate() {
    const t = S.tplById[$('#cTpl').value]; if (!t) return;
    const p = createPrefill && createPrefill.template === t.id ? createPrefill : null;
    $('#cName').value = p ? p.name : t.name;
    $('#cNotes').hidden = !t.notes; $('#cNotes').textContent = t.notes || '';
    memRange($('#cMem'), $('#cMemV'), t.min_memory_mb, p && p.memory_mb ? p.memory_mb : t.memory_mb);
    $('#cVars').innerHTML = (t.vars || []).map(v => varField(v, p && p.vars && p.vars[v.key] != null && p.vars[v.key] !== '' ? String(p.vars[v.key]) : v.default, false)).join('');
  }
  $('#cTpl').addEventListener('change', fillCreate);
  $('#createForm').addEventListener('submit', async e => {
    e.preventDefault();
    const btn = $('#cSubmit'); btn.disabled = true; showErr($('#cErr'));
    const body = { template: $('#cTpl').value, name: $('#cName').value, memory_mb: +$('#cMem').value, vars: collectVars($('#cVars')), start: $('#cStart').checked, by_ai: !!createPrefill };
    try {
      const s = await api('POST', '/api/servers', body);
      $('#dlgCreate').close();
      toast(s.name + ' wird angelegt', 'ok');
      if (createPrefill) startAITrack(s.id);
      await loadServers(); select(s.id);
    } catch (err) { showErr($('#cErr'), err.message); }
    finally { btn.disabled = false; }
  });

  /* ---------------- Bearbeiten / Löschen ---------------- */
  let editId = null, delId = null;
  function openEdit(s) {
    editId = s.id; const t = S.tplById[s.template];
    $('#eName').value = s.name;
    memRange($('#eMem'), $('#eMemV'), t ? t.min_memory_mb : 512, s.memory_mb);
    $('#eVars').innerHTML = t ? (t.vars || []).map(v => varField(v, s.vars[v.key] != null ? s.vars[v.key] : v.default, true)).join('') : '';
    $('#eUpdWrap').hidden = !t || t.kind !== 'steam'; $('#eUpd').checked = !!s.update_on_start;
    showErr($('#eErr'));
    $('#dlgEdit').showModal();
  }
  $('#editForm').addEventListener('submit', async e => {
    e.preventDefault();
    const s = byId(editId); if (!s) return;
    const body = { name: $('#eName').value, memory_mb: +$('#eMem').value, vars: collectVars($('#eVars'), true) };
    if (!$('#eUpdWrap').hidden) body.update_on_start = $('#eUpd').checked;
    try { await api('PATCH', '/api/servers/' + editId, body); $('#dlgEdit').close(); toast('Gespeichert. Der Container wird neu angelegt.', 'ok'); await loadServers(); }
    catch (err) { showErr($('#eErr'), err.message); }
  });
  $('#eToSettings').addEventListener('click', () => { const id = editId; $('#dlgEdit').close(); select(id, 'settings'); });
  function openDelete(s) {
    delId = s.id; $('#delWipe').checked = false;
    $('#delText').textContent = `„${s.name}“ wird gestoppt und aus dem Panel entfernt.`;
    $('#dlgDelete').showModal();
  }
  $('#deleteForm').addEventListener('submit', async e => {
    e.preventDefault();
    const s = byId(delId);
    try {
      await api('DELETE', '/api/servers/' + delId + ($('#delWipe').checked ? '?wipe=1' : ''));
      $('#dlgDelete').close(); toast((s ? s.name : 'Server') + ' gelöscht');
      if (S.consoleId === delId) closeConsole();
      if (R.page === 'server' && R.id === delId) go('#/server');
      await loadServers();
    } catch (err) { toast(err.message, 'bad'); }
  });

  /* ---------------- Einstellungen + Lizenz ---------------- */
  function openSettings(sec) { go('#/einstellungen' + (typeof sec === 'string' && sec ? '/' + sec : '')); }
  const SEC = ['konto', 'https', 'router', 'ki', 'backups', 'steam', 'lizenz'];
  async function enterSettings(sec) {
    if (!can('admin') || !SEC.includes(sec)) sec = 'konto';
    $$('#pageSettings [data-sec]').forEach(el => {
      if (el.tagName === 'A') el.setAttribute('aria-current', el.dataset.sec === sec ? 'page' : 'false');
      else el.hidden = el.dataset.sec !== sec;
    });
    $('#verLine').textContent = 'Version ' + S.version;
    clearTimeout(stmTimer);
    if (sec === 'konto') return loadMe();
    if (sec === 'https') return loadTLS();
    if (sec === 'router') return loadRouter(false);
    if (sec === 'steam') { $('#stmPass').value = ''; $('#stmCode').value = ''; return loadSteam(); }
    if (sec === 'lizenz') { showErr($('#licErr')); $('#licKey').value = ''; return renderLicense(); }
    if (sec === 'backups') {
      try {
        const rc = await api('GET', '/api/remote');
        $('#rmType').value = rc.type || ''; $('#rmDir').value = rc.dir || ''; $('#rmEp').value = rc.endpoint || '';
        $('#rmBucket').value = rc.bucket || ''; $('#rmRegion').value = rc.region || ''; $('#rmAk').value = rc.access_key || '';
        $('#rmSk').value = rc.secret_key || ''; $('#rmPrefix').value = rc.prefix || ''; $('#rmPath').checked = rc.type ? !!rc.path_style : true;
        $('#rmKeep').value = rc.keep || 14;
      } catch (e) { /* egal */ }
      rmFields(); showErr($('#rmErr')); return;
    }
    try { const r = await api('GET', '/api/settings'); S.settings = r.settings; S.defaultModel = r.default_model; } catch (e) { toast(e.message, 'bad'); return; }
    const s = S.settings;
    $('#sProv').value = s.ai_provider || '';
    $('#sAnt').value = s.anthropic_key || ''; $('#sOai').value = s.openai_key || '';
    $('#sOll').value = s.ollama_url || ''; $('#sModel').value = s.ai_model || ''; $('#sHost').value = s.public_host || '';
    provFields(); showErr($('#sErr'));
  }
  function provFields() {
    const p = $('#sProv').value;
    $$('[data-prov]').forEach(el => { el.hidden = el.dataset.prov === 'any' ? !p : el.dataset.prov !== p; });
    const dm = { anthropic: 'claude-sonnet-5-5', openai: 'gpt-4o-mini', ollama: 'llama3.1' }[p] || '';
    $('#sModelHint').textContent = dm ? 'Standard: ' + dm : '';
  }
  $('#sProv').addEventListener('change', provFields);
  function settingsBody() {
    return { ai_provider: $('#sProv').value, anthropic_key: $('#sAnt').value, openai_key: $('#sOai').value, ollama_url: $('#sOll').value, ai_model: $('#sModel').value, public_host: $('#sHost').value };
  }
  $('#aiForm').addEventListener('submit', async e => {
    e.preventDefault(); showErr($('#sErr'));
    try { const r = await api('PUT', '/api/settings', settingsBody()); S.settings = r.settings; toast('Einstellungen gespeichert', 'ok'); renderKI(); }
    catch (err) { showErr($('#sErr'), err.message); }
  });
  $('#sTest').addEventListener('click', async () => {
    showErr($('#sErr'));
    const b = $('#sTest'); b.disabled = true; b.textContent = 'Teste …';
    try { const r = await api('PUT', '/api/settings', settingsBody()); S.settings = r.settings; await api('POST', '/api/ai/test'); toast('Verbindung zur KI klappt', 'ok'); renderKI(); }
    catch (err) { showErr($('#sErr'), err.message); }
    finally { b.disabled = false; b.textContent = 'Verbindung testen'; }
  });
  function renderLicense() {
    const l = S.license; if (!l) return;
    const e = l.edition;
    $('#edChip').textContent = e.name + (l.updates_until ? ' · Updates bis ' + new Date(l.updates_until).toLocaleDateString('de-DE') : '');
    let txt = `<b>${esc(e.name)}</b> · ${e.max_servers < 0 ? 'unbegrenzt viele Server' : 'bis zu ' + e.max_servers + ' Server'} · KI ${e.ai ? 'enthalten' : 'nicht enthalten'}`;
    if (l.licensee) txt += `<br><span class="muted small">Lizenziert für ${esc(l.licensee)}${l.updates_until ? ', Updates bis ' + new Date(l.updates_until).toLocaleDateString('de-DE') + (l.updates_ok ? '' : ' (abgelaufen)') : ''}</span>`;
    if (!l.valid) txt += '<br><span class="muted small">Ohne Lizenz läuft das Panel im Testmodus mit einem Server.</span>';
    if (l.error) txt += `<br><span class="err">${esc(l.error)}</span>`;
    $('#licStatus').innerHTML = txt;
  }
  function rmFields() {
    const t = $('#rmType').value;
    $$('[data-rm]').forEach(el => { el.hidden = el.dataset.rm === 'any' ? !t : el.dataset.rm !== t; });
  }
  $('#rmType').addEventListener('change', rmFields);
  const rmBody = () => ({ type: $('#rmType').value, dir: $('#rmDir').value, endpoint: $('#rmEp').value, bucket: $('#rmBucket').value, region: $('#rmRegion').value,
    access_key: $('#rmAk').value, secret_key: $('#rmSk').value, prefix: $('#rmPrefix').value, path_style: $('#rmPath').checked, keep: +$('#rmKeep').value || 14 });
  $('#rmForm').addEventListener('submit', async e => {
    e.preventDefault(); showErr($('#rmErr'));
    try { await api('PUT', '/api/remote', rmBody()); toast($('#rmType').value ? 'Externes Backup-Ziel gespeichert' : 'Externe Backups ausgeschaltet', 'ok'); }
    catch (err) { showErr($('#rmErr'), err.message); }
  });
  $('#rmTest').addEventListener('click', async () => {
    showErr($('#rmErr'));
    const b = $('#rmTest'); b.disabled = true; b.textContent = 'Teste …';
    try { const r = await api('POST', '/api/remote/test', rmBody()); toast('Verbindung klappt: ' + r.ok, 'ok'); }
    catch (err) { showErr($('#rmErr'), err.message); }
    finally { b.disabled = false; b.textContent = 'Verbindung testen'; }
  });
  // Steam-Konto
  let stmTimer = null;
  async function loadSteam() {
    clearTimeout(stmTimer);
    let st;
    try { st = await api('GET', '/api/steam'); } catch (e) { return; }
    renderSteam(st);
    if ((st.state === 'working' || st.state === 'need_app') && R.page === 'settings') stmTimer = setTimeout(loadSteam, 2000);
  }
  function renderSteam(st) {
    const busy = st.state === 'working' || st.state === 'need_app';
    const msg = { none: 'Nicht angemeldet.', ok: st.user ? `Angemeldet als <b>${esc(st.user)}</b>.` : '' }[st.state];
    $('#stmStatus').innerHTML = busy ? `<span class="spin" aria-hidden="true"></span> ${esc(st.message || 'Anmeldung läuft …')}` : (st.state === 'need_code' ? esc(st.message) : msg || '');
    if (st.state === 'ok' && st.user && st.message && !msg) $('#stmStatus').innerHTML = esc(st.message);
    showErr($('#stmErr'), st.state === 'error' ? st.message : '');
    $('#stmCodeWrap').hidden = st.state !== 'need_code';
    $('#stmLogWrap').hidden = !(st.log && st.log.length) || st.state === 'ok';
    $('#stmLog').textContent = (st.log || []).join('\n');
    $('#stmCodeLabel').textContent = st.code_kind === 'app' ? 'Code aus der Steam-App' : 'Steam-Guard-Code (E-Mail oder Steam-App)';
    if (st.pending && !$('#stmUser').value) $('#stmUser').value = st.pending;
    if (st.user && !$('#stmUser').value) $('#stmUser').value = st.user;
    $('#stmLogout').hidden = !st.user || busy;
    $('#stmGo').disabled = busy;
    $('#stmGo').textContent = st.state === 'need_code' ? 'Code senden' : st.user ? 'Neu anmelden' : 'Anmelden';
    if (st.state === 'need_code') setTimeout(() => $('#stmCode').focus(), 50);
    if (st.state === 'ok') { $('#stmPass').value = ''; $('#stmCode').value = ''; }
  }
  $('#stmForm').addEventListener('submit', async e => {
    e.preventDefault(); showErr($('#stmErr'));
    try {
      const st = await api('POST', '/api/steam/login', { user: $('#stmUser').value.trim(), password: $('#stmPass').value, code: $('#stmCodeWrap').hidden ? '' : $('#stmCode').value.trim() });
      renderSteam(st); stmTimer = setTimeout(loadSteam, 1500);
    } catch (err) { showErr($('#stmErr'), err.message); }
  });
  $('#stmLogout').addEventListener('click', async () => {
    if (!await confirmBox('Steam-Konto abmelden?', 'Server, die ein Steam-Konto brauchen, können danach nicht mehr aktualisieren.', 'Abmelden')) return;
    try { renderSteam(await api('DELETE', '/api/steam')); $('#stmUser').value = ''; } catch (err) { showErr($('#stmErr'), err.message); }
  });

  $('#licForm').addEventListener('submit', async e => {
    e.preventDefault(); showErr($('#licErr'));
    try { S.license = await api('PUT', '/api/license', { key: $('#licKey').value }); renderLicense(); renderKI(); toast('Lizenz aktiviert: ' + S.license.edition.name, 'ok'); $('#licKey').value = ''; }
    catch (err) { showErr($('#licErr'), err.message); }
  });
  /* ---------------- Mein Konto ---------------- */
  const fmtLogin = d => d && !String(d).startsWith('0001') ? fmtDate(d) : 'noch nie';
  async function loadMe() {
    showErr($('#pwErr')); $('#pwOld').value = ''; $('#pwNew').value = ''; $('#pwNew2').value = ''; $('#pwUser').value = S.user;
    let me;
    try { me = await api('GET', '/api/me'); } catch (e) { return; }
    S.twoFactor = me.two_factor;
    $('#meLine').innerHTML = `Angemeldet als <b>${esc(me.name)}</b> · <span class="role ${esc(me.role)}">${esc(ROLE[me.role] || me.role)}</span>` +
      (me.role !== 'admin' && me.servers.length ? ` · ${me.servers.length} Server freigegeben` : '');
    $('#tfStatus').innerHTML = me.two_factor
      ? `<span class="on-ok">Eingeschaltet.</span> Noch ${me.recovery_left} Wiederherstellungscode${me.recovery_left === 1 ? '' : 's'} übrig.${me.recovery_left < 3 ? ' <b>Bitte neue erstellen.</b>' : ''}`
      : 'Ausgeschaltet.';
    $('#tfOn').hidden = me.two_factor; $('#tfOff').hidden = !me.two_factor; $('#tfCodes').hidden = !me.two_factor;
    $('#sessLine').textContent = me.sessions === 1 ? 'Du bist nur auf diesem Gerät angemeldet.' : `Du bist auf ${me.sessions} Geräten bzw. Browsern angemeldet.`;
    $('#logoutAll').hidden = me.sessions < 2;
  }
  $('#pwForm').addEventListener('submit', async e => {
    e.preventDefault(); showErr($('#pwErr'));
    if ($('#pwNew').value !== $('#pwNew2').value) { showErr($('#pwErr'), 'Die neuen Passwörter stimmen nicht überein.'); return; }
    try {
      const r = await api('PUT', '/api/me/password', { old: $('#pwOld').value, new: $('#pwNew').value });
      toast('Passwort geändert' + (r.ended ? ', ' + r.ended + ' andere Geräte abgemeldet' : ''), 'ok'); loadMe();
    } catch (err) { showErr($('#pwErr'), err.message); }
  });
  $('#logoutAll').addEventListener('click', async () => {
    try { const r = await api('POST', '/api/me/logout-all'); toast(r.ended + ' andere Sitzungen beendet', 'ok'); loadMe(); } catch (err) { toast(err.message, 'bad'); }
  });

  // Zwei-Faktor einrichten
  let tfCodesText = '';
  function showCodes(codes) {
    tfCodesText = 'Gamepanel – Wiederherstellungscodes für ' + S.user + ' (' + location.host + ')\n' + codes.join('\n') + '\n';
    $('#tfList').innerHTML = codes.map(c => `<li>${esc(c)}</li>`).join('');
    $('#tfStep1').hidden = true; $('#tfAct1').hidden = true; $('#tfStep2').hidden = false; $('#tfAct2').hidden = false;
  }
  $('#tfOn').addEventListener('click', async () => {
    let r;
    try { r = await api('POST', '/api/me/2fa/setup'); } catch (err) { toast(err.message, 'bad'); return; }
    $('#tfStep1').hidden = false; $('#tfAct1').hidden = false; $('#tfStep2').hidden = true; $('#tfAct2').hidden = true;
    showErr($('#tfErr')); $('#tfCode').value = '';
    $('#tfSecret').textContent = r.secret.replace(/(.{4})/g, '$1 ').trim();
    try { const q = qrcode(0, 'M'); q.addData(r.uri); q.make(); $('#tfQR').innerHTML = q.createSvgTag({ cellSize: 4, margin: 2, scalable: true, alt: 'QR-Code' }); }
    catch (e) { $('#tfQR').textContent = 'QR-Code nicht verfügbar, bitte den Schlüssel eintippen.'; }
    $('#dlg2FA').showModal(); setTimeout(() => $('#tfCode').focus(), 60);
  });
  $('#tfForm').addEventListener('submit', async e => {
    e.preventDefault(); showErr($('#tfErr'));
    if ($('#tfStep1').hidden) { $('#dlg2FA').close(); return; }
    try { const r = await api('POST', '/api/me/2fa/enable', { code: $('#tfCode').value }); showCodes(r.recovery); toast('Zwei-Faktor-Anmeldung ist an', 'ok'); loadMe(); }
    catch (err) { showErr($('#tfErr'), err.message + (/stimmt nicht/.test(err.message) ? ' Stimmt die Uhrzeit auf Handy und Server?' : '')); }
  });
  $('#tfCodes').addEventListener('click', async () => {
    const pw = await passwordBox('Neue Wiederherstellungscodes', 'Die alten Codes werden ungültig. Zur Bestätigung dein Passwort:');
    if (pw == null) return;
    try { const r = await api('POST', '/api/me/2fa/recovery', { password: pw }); showCodes(r.recovery); $('#dlg2FA').showModal(); loadMe(); }
    catch (err) { toast(err.message, 'bad'); }
  });
  $('#tfOff').addEventListener('click', async () => {
    const pw = await passwordBox('Zwei-Faktor ausschalten?', 'Danach reicht wieder das Passwort allein. Zur Bestätigung dein Passwort:');
    if (pw == null) return;
    try { await api('POST', '/api/me/2fa/disable', { password: pw }); toast('Zwei-Faktor-Anmeldung ist aus'); loadMe(); }
    catch (err) { toast(err.message, 'bad'); }
  });
  $('#tfDl').addEventListener('click', () => {
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([tfCodesText], { type: 'text/plain' }));
    a.download = 'gamepanel-wiederherstellungscodes.txt'; a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  });
  $('#tfCopy').addEventListener('click', async () => {
    try { await navigator.clipboard.writeText(tfCodesText); toast('Kopiert', 'ok'); } catch (e) { toast('Kopieren geht hier nicht – bitte als Datei speichern', 'bad'); }
  });
  function passwordBox(title, label) {
    return new Promise(res => {
      const d = $('#dlgPrompt'), inp = $('#prInput');
      $('#prTitle').textContent = title; $('#prLabel').textContent = label; $('#prOk').textContent = 'Bestätigen';
      inp.type = 'password'; inp.value = ''; inp.autocomplete = 'current-password';
      const onClose = () => { d.removeEventListener('close', onClose); inp.type = 'text'; inp.autocomplete = 'off'; res(d.returnValue === 'ok' ? inp.value : null); inp.value = ''; };
      d.returnValue = '';
      d.addEventListener('close', onClose);
      $('#prForm').onsubmit = e => { e.preventDefault(); d.close('ok'); };
      d.showModal(); setTimeout(() => inp.focus(), 30);
    });
  }

  /* ---------------- Benutzer ---------------- */
  const US = { list: [], servers: [], me: '', edit: null };
  function openUsers() { go('#/benutzer'); }
  async function enterUsers() { await loadUsers(); usReset(); }
  async function loadUsers() {
    try { const r = await api('GET', '/api/users'); US.list = r.users; US.servers = r.servers; US.me = r.me; } catch (err) { toast(err.message, 'bad'); return; }
    const srvName = id => (US.servers.find(s => s.id === id) || { name: id }).name;
    $('#usRows').innerHTML = US.list.map(u => `<tr class="${u.disabled ? 'off' : ''}">
        <td><b>${esc(u.name)}</b>${u.name === US.me ? ' <span class="muted small">(du)</span>' : ''}${u.disabled ? ' <span class="muted small">gesperrt</span>' : ''}</td>
        <td><span class="role ${esc(u.role)}">${esc(ROLE[u.role] || u.role)}</span></td>
        <td class="wrap small">${u.role === 'admin' || !u.servers.length ? '<span class="muted">alle</span>' : u.servers.map(id => esc(srvName(id))).join(', ')}</td>
        <td>${u.two_factor ? '<span class="on-ok">an</span>' : '<span class="muted">aus</span>'}</td>
        <td class="small">${esc(fmtLogin(u.last_login))}${u.sessions ? ` <span class="muted">· ${u.sessions} aktiv</span>` : ''}</td>
        <td><div class="acts"><button type="button" class="btn-line sm" data-us-edit="${esc(u.name)}">Bearbeiten</button>${u.name === US.me ? '' : `<button type="button" class="btn-line sm" data-us-del="${esc(u.name)}">Löschen</button>`}</div></td>
      </tr>`).join('');
  }
  function usServers(sel) {
    $('#usSrv').innerHTML = US.servers.length ? US.servers.map(s => `<label class="check"><input type="checkbox" value="${esc(s.id)}" ${sel.includes(s.id) ? 'checked' : ''}> ${esc(s.name)}</label>`).join('')
      : '<span class="muted small">Noch keine Server angelegt.</span>';
    $('#usAll').checked = !sel.length; $('#usSrv').hidden = !sel.length;
  }
  function usRoleFields() { $('#usSrvWrap').hidden = $('#usRole').value === 'admin'; }
  function usReset() {
    US.edit = null; showErr($('#usErr'));
    $('#usFormTitle').textContent = 'Neuer Benutzer'; $('#usSave').textContent = 'Anlegen'; $('#usCancel').hidden = true; $('#usExtra').hidden = true;
    $('#usName').value = ''; $('#usName').disabled = false; $('#usPw').value = ''; $('#usPwLabel').textContent = 'Passwort';
    $('#usPw').placeholder = 'mindestens 10 Zeichen'; $('#usRole').value = 'viewer';
    $('#usDis').checked = false; $('#us2fa').checked = false; $('#usEnd').checked = false;
    usServers([]); usRoleFields();
  }
  function usEdit(name) {
    const u = US.list.find(x => x.name === name); if (!u) return;
    US.edit = u; showErr($('#usErr'));
    $('#usFormTitle').textContent = u.name + ' bearbeiten'; $('#usSave').textContent = 'Speichern'; $('#usCancel').hidden = false; $('#usExtra').hidden = false;
    $('#usName').value = u.name; $('#usName').disabled = true; $('#usPw').value = ''; $('#usPwLabel').textContent = 'Neues Passwort';
    $('#usPw').placeholder = 'leer lassen = unverändert'; $('#usRole').value = u.role;
    $('#usDis').checked = u.disabled; $('#us2fa').checked = false; $('#us2faWrap').hidden = !u.two_factor; $('#usEnd').checked = false;
    const self = u.name === US.me;
    $('#usRole').disabled = self; $('#usDis').disabled = self;
    usServers(u.servers); usRoleFields();
    $('#usForm').scrollIntoView({ behavior: motion ? 'smooth' : 'auto', block: 'nearest' }); $('#usPw').focus();
  }
  $('#usRole').addEventListener('change', usRoleFields);
  $('#usAll').addEventListener('change', () => { $('#usSrv').hidden = $('#usAll').checked; });
  $('#usCancel').addEventListener('click', () => { $('#usRole').disabled = false; $('#usDis').disabled = false; usReset(); });
  $('#usRows').addEventListener('click', async e => {
    const ed = e.target.closest('[data-us-edit]'), del = e.target.closest('[data-us-del]');
    if (ed) usEdit(ed.dataset.usEdit);
    if (del) {
      const n = del.dataset.usDel;
      if (!await confirmBox(n + ' löschen?', 'Das Konto wird entfernt und überall abgemeldet. Server und Protokoll bleiben.', 'Löschen')) return;
      try { await api('DELETE', '/api/users/' + encodeURIComponent(n)); toast(n + ' gelöscht'); await loadUsers(); usReset(); } catch (err) { toast(err.message, 'bad'); }
    }
  });
  $('#usForm').addEventListener('submit', async e => {
    e.preventDefault(); showErr($('#usErr'));
    const servers = $('#usAll').checked ? [] : $$('#usSrv input:checked').map(i => i.value);
    if (!$('#usAll').checked && !servers.length && $('#usRole').value !== 'admin') { showErr($('#usErr'), 'Bitte mindestens einen Server wählen oder „Alle Server“.'); return; }
    const body = { servers };
    if (!$('#usRole').disabled) body.role = $('#usRole').value;
    if ($('#usPw').value) body.password = $('#usPw').value;
    try {
      if (US.edit) {
        if (!$('#usDis').disabled) body.disabled = $('#usDis').checked;
        body.reset_2fa = $('#us2fa').checked; body.end_sessions = $('#usEnd').checked;
        await api('PATCH', '/api/users/' + encodeURIComponent(US.edit.name), body);
        toast(US.edit.name + ' gespeichert', 'ok');
      } else {
        body.name = $('#usName').value.trim();
        if (!body.password) { showErr($('#usErr'), 'Bitte ein Passwort vergeben.'); return; }
        await api('POST', '/api/users', body);
        toast(body.name + ' angelegt. Gib ihm Benutzername und Passwort weiter.', 'ok');
      }
      $('#usRole').disabled = false; $('#usDis').disabled = false;
      await loadUsers(); usReset();
    } catch (err) { showErr($('#usErr'), err.message); }
  });

  /* ---------------- Protokoll ---------------- */
  let auTimer = null;
  function openAudit() { go('#/protokoll'); }
  async function enterAudit() {
    try { const r = await api('GET', '/api/users'); $('#auUser').innerHTML = '<option value="">Alle</option>' + r.users.map(u => `<option>${esc(u.name)}</option>`).join(''); } catch (e) { /* egal */ }
    loadAudit();
  }
  async function loadAudit() {
    const q = new URLSearchParams({ q: $('#auQ').value, user: $('#auUser').value, days: $('#auDays').value, failed: $('#auFail').checked ? '1' : '', limit: '500' });
    let list;
    try { list = await api('GET', '/api/audit?' + q); } catch (err) { toast(err.message, 'bad'); return; }
    $('#auRows').innerHTML = list.length ? list.map(e => `<tr>
        <td class="mono small">${esc(fmtDate(e.time))}</td><td>${esc(e.user || '–')}</td>
        <td class="${e.ok ? '' : 'bad'}">${esc(e.action)}${e.ok ? '' : ' ✕'}</td><td>${esc(e.target || '')}</td>
        <td class="wrap small">${esc(e.detail || '')}${e.error ? `<span class="bad">${e.detail ? ' · ' : ''}${esc(e.error)}</span>` : ''}</td>
        <td class="mono small muted">${esc(e.ip || '')}</td></tr>`).join('')
      : '<tr class="empty"><td colspan="6">Keine Einträge.</td></tr>';
    $('#auCount').textContent = list.length >= 500 ? 'Die neuesten 500 Einträge. Filter eingrenzen für ältere.' : list.length + ' Einträge';
  }
  $('#auForm').addEventListener('input', () => { clearTimeout(auTimer); auTimer = setTimeout(loadAudit, 250); });
  $('#auForm').addEventListener('submit', e => { e.preventDefault(); loadAudit(); });

  /* ---------------- HTTPS ---------------- */
  async function loadTLS() {
    let r;
    try { r = await api('GET', '/api/https'); } catch (e) { return; }
    renderTLS(r);
    $('#tlsMode').value = r.status.mode || ''; $('#tlsDomain').value = r.status.domain || ''; $('#tlsEmail').value = r.status.email || '';
    $('#tlsCert').value = ''; $('#tlsKey').value = ''; tlsFields(); showErr($('#tlsErr'));
    $('#tlsPort').textContent = location.port || (location.protocol === 'https:' ? '443' : '80');
  }
  function renderTLS(r) {
    const st = r.status, parts = [];
    parts.push(r.secure ? '<span class="on-ok">Diese Verbindung ist verschlüsselt.</span>' : '<b>Diese Verbindung ist unverschlüsselt.</b>');
    if (st.off) parts.push('<span class="err">Die Umleitung auf HTTPS ist per GP_NO_TLS abgeschaltet.</span>');
    if (st.subject) parts.push(`Zertifikat: <b>${esc(st.issuer && st.issuer !== st.subject ? st.issuer : st.subject === 'Gamepanel' ? 'selbst signiert' : st.subject)}</b>, gültig bis ${esc(new Date(st.not_after).toLocaleDateString('de-DE'))}` +
      (st.names && st.names.length ? `<br><span class="muted small">für ${esc(st.names.slice(0, 8).join(', '))}${st.names.length > 8 ? ' …' : ''}</span>` : ''));
    $('#tlsStatus').innerHTML = parts.join('<br>');
    showErr($('#tlsErr'), st.error || '');
    $('#tlsRenew').hidden = st.mode !== 'acme';
  }
  function tlsFields() { const m = $('#tlsMode').value; $$('[data-tls]').forEach(el => { el.hidden = el.dataset.tls !== m; }); }
  $('#tlsMode').addEventListener('change', tlsFields);
  $('#tlsForm').addEventListener('submit', async e => {
    e.preventDefault(); showErr($('#tlsErr'));
    const mode = $('#tlsMode').value;
    try {
      const r = await api('PUT', '/api/https', { mode, domain: $('#tlsDomain').value, email: $('#tlsEmail').value, cert: $('#tlsCert').value, key: $('#tlsKey').value });
      renderTLS(r); $('#tlsCert').value = ''; $('#tlsKey').value = '';
      if (mode && location.protocol === 'http:' && !/^(localhost|127\.|\[::1\])/.test(location.hostname)) {
        const host = mode === 'acme' && $('#tlsDomain').value ? $('#tlsDomain').value.trim() : location.host;
        toast('HTTPS ist an. Du wirst gleich umgeleitet …', 'ok');
        setTimeout(() => { location.href = 'https://' + host + (mode === 'acme' ? '/' : location.pathname); }, 1800);
      } else toast(mode ? 'HTTPS gespeichert' : 'HTTPS ausgeschaltet, nur noch HTTP', 'ok');
    } catch (err) { showErr($('#tlsErr'), err.message); }
  });
  $('#tlsRenew').addEventListener('click', async () => {
    const b = $('#tlsRenew'); b.disabled = true; b.textContent = 'Frage Let\'s Encrypt …';
    try { renderTLS(await api('POST', '/api/https/renew')); toast('Zertifikat ist da', 'ok'); }
    catch (err) { showErr($('#tlsErr'), err.message); }
    finally { b.disabled = false; b.textContent = 'Zertifikat jetzt holen'; }
  });

  /* ---------------- Router & Ports ---------------- */
  function routerRow(s) {
    const r = s.router || [];
    if (s.no_router) return S.routerOn ? '<div><dt>Von außen</dt><dd class="muted">Ports werden für diesen Server nicht automatisch geöffnet</dd></div>' : '';
    if (!r.length) return '';
    const bad = r.filter(p => !p.ok);
    const list = r.map(p => p.proto + ' ' + p.port).join(', ');
    if (!bad.length) return `<div><dt>Von außen</dt><dd><span class="on-ok">Ports offen</span><br><span class="small muted">${esc(list)}</span></dd></div>`;
    return `<div><dt>Von außen</dt><dd><span class="bad-txt">${bad.length} von ${r.length} Ports nicht offen</span><br><span class="small">${esc(bad[0].error || '')}</span></dd></div>`;
  }
  async function setServerRouter(s, open) {
    try { await api('PUT', `/api/servers/${encodeURIComponent(s.id)}/router`, { open }); toast(open ? 'Ports werden im Router geöffnet' : 'Ports werden nicht mehr im Router geöffnet', 'ok'); await loadServers(); }
    catch (err) { toast(err.message, 'bad'); }
  }
  async function loadRouter(check) {
    showErr($('#rtErr'));
    const b = $('#rtCheck');
    if (check) { b.disabled = true; b.textContent = 'Prüfe …'; }
    try { renderRouter(await api(check ? 'POST' : 'GET', check ? '/api/router/check' : '/api/router')); }
    catch (err) { showErr($('#rtErr'), err.message); }
    finally { b.disabled = false; b.textContent = 'Jetzt prüfen'; }
  }
  function renderRouter(r) {
    S.routerOn = r.enabled;
    $('#rtOn').checked = r.enabled;
    const when = r.checked && !String(r.checked).startsWith('0001') ? ' · geprüft ' + fmtWhen(r.checked) : '';
    let txt = '';
    if (!r.enabled) txt = 'Ausgeschaltet. Ports musst du selbst im Router freigeben.';
    else if (r.found) txt = `Router: <b>${esc(r.name || 'gefunden')}</b> · dieser Rechner ${esc(r.local_ip || '?')}${r.external_ip ? ' · öffentliche IP ' + esc(r.external_ip) : ''}${when}`;
    else if (!r.error) txt = '<span class="spin" aria-hidden="true"></span> Router wird gesucht …';
    $('#rtStatus').innerHTML = txt;
    showErr($('#rtErr'), r.enabled ? r.error || '' : '');
    const ports = r.ports || [];
    $('#rtWrap').hidden = !r.enabled;
    $('#rtRows').innerHTML = ports.length ? ports.map(p => `<tr><td>${esc(p.name)}</td><td class="mono">${esc(p.proto)} ${p.port}</td>
        <td class="wrap">${p.ok ? '<span class="on-ok">offen</span>' + (p.manual ? ' <span class="muted small">(von Hand freigegeben)</span>' : '') : `<span class="bad-txt">${esc(p.error || 'nicht offen')}</span>`}</td></tr>`).join('')
      : '<tr class="empty"><td colspan="3">Gerade läuft kein Server, also ist auch nichts freigegeben.</td></tr>';
  }
  $('#rtOn').addEventListener('change', async e => {
    const on = e.target.checked; e.target.disabled = true;
    $('#rtStatus').innerHTML = '<span class="spin" aria-hidden="true"></span> ' + (on ? 'Suche den Router und öffne die Ports …' : 'Schließe die Ports …');
    try { renderRouter(await api('PUT', '/api/router', { enabled: on })); toast(on ? 'Automatische Portfreigabe ist an' : 'Automatische Portfreigabe ist aus', 'ok'); loadServers(); }
    catch (err) { showErr($('#rtErr'), err.message); e.target.checked = !on; }
    finally { e.target.disabled = false; }
  });
  $('#rtCheck').addEventListener('click', () => loadRouter(true));

  /* ---------------- Selbsttest ---------------- */
  const TS = { timer: null, open: {} };
  const fmtDur = sec => sec >= 3600 ? Math.floor(sec / 3600) + ' h ' + pad2(Math.floor(sec % 3600 / 60)) + ' min' : Math.max(1, Math.round(sec / 60)) + ' min';
  function openTest() { go('#/selbsttest'); }
  async function pollTest() {
    clearTimeout(TS.timer);
    let r;
    try { r = await api('GET', '/api/selftest'); } catch (e) { toast(e.message, 'bad'); return; }
    renderTest(r);
    $('#navTest').hidden = !r.running;
    if (R.page === 'test') TS.timer = setTimeout(pollTest, r.running ? 3000 : 15000);
  }
  function renderTest(r) {
    r.queue = r.queue || []; r.results = r.results || [];
    $('#tsForm').hidden = r.running; $('#tsHint').hidden = r.running; $('#tsRun').hidden = !r.running;
    $('#tsGo').disabled = false;
    if (r.running) {
      const done = r.total - r.queue.length - (r.current ? 1 : 0);
      $('#tsCount').textContent = `${done + 1} von ${r.total} · läuft seit ${fmtDur((Date.now() - new Date(r.started)) / 1000)} · ${Math.round(r.free_gb)} GB frei`;
      const t = S.tplById[r.current];
      $('#tsCur').textContent = r.current ? (t ? t.name : r.current) : 'Wird vorbereitet';
      $('#tsStep').textContent = r.step ? r.step + (r.step_since ? ' · seit ' + fmtDur((Date.now() - new Date(r.step_since)) / 1000) : '') : '';
      $('#tsBar').style.width = Math.round(100 * done / Math.max(1, r.total)) + '%';
    }
    const c = { ok: 0, unsicher: 0, fehler: 0, 'übersprungen': 0 };
    (r.results || []).forEach(x => { c[x.result] = (c[x.result] || 0) + 1; });
    let sum = Object.entries(c).map(([k, v]) => `<span class="ts-res ${k}">${v} ${k}</span>`).join('');
    if (r.queue && r.queue.length && !r.running) sum += `<span>${r.queue.length} offen</span>`;
    if (r.stopped) sum += `<span>${esc(r.stopped)}</span>`;
    $('#tsSum').innerHTML = (r.results || []).length || r.running ? sum : '';
    $('#tsReport').hidden = !(r.results || []).length;
    const mk = (ok, has) => !has ? '<span class="muted">–</span>' : ok ? '<span class="ts-yes">✓</span>' : '<span class="ts-no">✗</span>';
    const order = { fehler: 0, unsicher: 1, 'übersprungen': 2, ok: 3 };
    const rows = (r.results || []).slice().sort((a, b) => order[a.result] - order[b.result] || a.name.localeCompare(b.name, 'de'));
    $('#tsRows').innerHTML = rows.length ? rows.map(x => {
      const det = x.error || x.note || (x.log && x.log.length);
      const main = `<tr class="${det ? 'has-d' : ''}" data-t="${esc(x.template)}"><td>${esc(x.name)}</td><td><span class="ts-res ${esc(x.result)}">${esc(x.result)}</span></td>
        <td>${mk(x.download, true)}</td><td>${mk(x.start, true)}</td><td>${mk(x.ready, x.has_ready)}</td><td>${mk(x.query, x.has_query)}${x.players ? ' <span class="mono small muted">' + esc(x.players) + '</span>' : ''}</td>
        <td class="num">${x.disk_mb ? gb(x.disk_mb) + ' GB' : '–'}</td><td class="num">${fmtDur(x.seconds)}</td></tr>`;
      const d = det && TS.open[x.template] ? `<tr class="ts-detail"><td colspan="8">${x.error ? '<b>' + esc(x.error) + '</b><br>' : ''}${x.note ? esc(x.note) : ''}${x.log && x.log.length ? '<pre>' + esc(x.log.join('\n')) + '</pre>' : ''}</td></tr>` : '';
      return main + d;
    }).join('') : '<tr class="empty"><td colspan="8">Noch keine Ergebnisse.</td></tr>';
  }
  $('#tsRows').addEventListener('click', e => {
    const tr = e.target.closest('tr.has-d'); if (!tr) return;
    TS.open[tr.dataset.t] = !TS.open[tr.dataset.t]; pollTest();
  });
  $('#tsForm').addEventListener('submit', async e => {
    e.preventDefault(); $('#tsGo').disabled = true;
    try { renderTest(await api('POST', '/api/selftest', { which: $('#tsWhich').value })); toast('Selbsttest gestartet', 'ok'); pollTest(); loadServers(); }
    catch (err) { toast(err.message, 'bad'); $('#tsGo').disabled = false; }
  });
  $('#tsStop').addEventListener('click', async () => {
    if (!await confirmBox('Selbsttest abbrechen?', 'Der laufende Testserver wird gelöscht. Bisherige Ergebnisse bleiben erhalten.', 'Test abbrechen')) return;
    try { await api('DELETE', '/api/selftest'); toast('Wird abgebrochen …'); setTimeout(pollTest, 1500); } catch (err) { toast(err.message, 'bad'); }
  });
  $('#edChip').onclick = () => openSettings(can('admin') ? 'lizenz' : 'konto');
  $('#edChip').onkeydown = e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); $('#edChip').click(); } };

  /* ---------------- KI ---------------- */
  const KI = { core: null, busy: false, plan: null, tpl: null, track: null, order: 0 };
  function setCore(st) {
    const labels = { idle: 'Bereit', listening: 'Hört zu', thinking: 'Denkt nach', executing: 'Führt aus', done: 'Fertig' };
    $('#coreState').textContent = labels[st]; $('#ki').dataset.state = st;
    if (KI.core) KI.core.set(st); else if (KI.fake) KI.fake.state = st;
  }
  function addMsg(who, text, cls) {
    const chat = $('#chat'), d = document.createElement('div');
    d.className = 'msg' + (who === 'Du' ? ' du' : '') + (cls ? ' ' + cls : '');
    d.innerHTML = '<span class="mono muted who"></span><p></p>';
    d.firstChild.textContent = who; d.lastChild.textContent = text;
    chat.appendChild(d); while (chat.children.length > 4) chat.firstChild.remove();
  }
  function renderKI() {
    const body = $('#coreBody'); let lock = $('.locked', body);
    const l = S.license, s = S.settings;
    let msg = '';
    if (l && !l.edition.ai) msg = 'Der KI-Assistent ist in deiner Edition nicht enthalten. Er gehört zu Pro und Ultimate.';
    else if (s && !s.ai_provider) msg = 'Richte zuerst einen KI-Anbieter ein: Anthropic, OpenAI oder Ollama auf deinem eigenen Rechner.';
    $('#askForm').hidden = !!msg; $('#chips').hidden = !!msg;
    if (msg) {
      if (!lock) { lock = document.createElement('div'); lock.className = 'locked'; body.prepend(lock); }
      lock.innerHTML = `<span>${esc(msg)}</span><button class="btn-line" type="button">Einstellungen öffnen</button>`;
      lock.querySelector('button').onclick = () => openSettings(l && !l.edition.ai ? 'lizenz' : 'ki');
    } else if (lock) lock.remove();
    if (!msg && !$('#chat').children.length) addMsg('KI', 'Sag mir, was für einen Server du willst. Ich erstelle einen Plan, und du gibst ihn frei.');
  }
  async function ask(text) {
    text = text.trim(); if (!text || KI.busy) return;
    KI.busy = true; $('#askInput').value = ''; $('#plan').hidden = true;
    addMsg('Du', text); setCore('thinking');
    try {
      const r = await api('POST', '/api/ai/plan', { prompt: text });
      if (r.plan.question) { addMsg('KI', r.plan.question); }
      else { addMsg('KI', r.plan.reply || 'Hier ist mein Vorschlag.'); showPlan(r.plan, r.template, r.warning); }
    } catch (e) { addMsg('KI', e.message, 'bad'); }
    finally { KI.busy = false; setCore('idle'); }
  }
  function showPlan(plan, tpl, warning) {
    KI.plan = plan; KI.tpl = tpl; KI.track = null;
    const vars = Object.entries(plan.vars || {}).filter(([, v]) => v !== '' && v != null);
    const steps = (plan.steps && plan.steps.length ? plan.steps : ['Container anlegen', 'Serverdateien laden', 'Einstellungen setzen', 'Server starten']);
    const p = $('#plan'); p.hidden = false; KI.order++;
    p.innerHTML = `<div class="plan-head"><span class="mono muted">Auftrag Nr. ${String(KI.order).padStart(4, '0')}</span><span class="stamp" id="stamp">Wartet auf Freigabe</span></div>
      <h3>${esc(plan.name)}</h3>
      <dl class="kv"><div><dt>Spiel</dt><dd>${esc(tpl ? tpl.name : plan.template)}</dd></div><div><dt>RAM</dt><dd>${gb(plan.memory_mb || (tpl && tpl.memory_mb) || 0)} GB</dd></div>
      ${vars.slice(0, 6).map(([k, v]) => `<div><dt>${esc(k)}</dt><dd>${esc(v)}</dd></div>`).join('')}</dl>
      <ol class="steps">${steps.map((s, i) => `<li style="--d:${150 + i * 130}ms"><span class="box"></span><span>${esc(s)}</span></li>`).join('')}</ol>
      ${warning ? `<p class="msg-line bad">${esc(warning)}</p>` : ''}
      <div class="plan-actions"><button class="btn-line" type="button" id="editPlan">Ändern</button><button class="btn-accent" type="button" id="runPlan">${warning ? 'Im Formular ergänzen' : 'Ausführen'}</button></div>`;
    $('#editPlan').onclick = () => openCreate(plan);
    $('#runPlan').onclick = () => warning ? openCreate(plan) : runPlan();
  }
  async function runPlan() {
    const plan = KI.plan; if (!plan || KI.busy) return;
    KI.busy = true; $('#runPlan').disabled = true; $('#editPlan').disabled = true;
    $('#stamp').textContent = 'Wird ausgeführt'; setCore('executing');
    try {
      const s = await api('POST', '/api/servers', { template: plan.template, name: plan.name, vars: plan.vars, memory_mb: plan.memory_mb, start: true, by_ai: true });
      startAITrack(s.id); await loadServers();
      $('.plan-actions').insertAdjacentHTML('beforeend', `<a class="btn-line" href="#/server/${encodeURIComponent(s.id)}">Zum Server</a>`);
    } catch (e) {
      addMsg('KI', e.message, 'bad'); $('#stamp').textContent = 'Abgebrochen'; $('#stamp').classList.add('bad');
      setCore('idle'); KI.busy = false; $('#runPlan').disabled = false; $('#editPlan').disabled = false;
    }
  }
  function startAITrack(id) { KI.track = { id, done: false }; KI.busy = true; setCore('executing'); }
  function trackAI() {
    const t = KI.track; if (!t || t.done) return;
    const s = byId(t.id); const lis = $$('#plan .steps li');
    if (!s) return;
    const n = lis.length;
    const idx = { preparing: 1, installing: Math.max(2, n - 2), starting: n - 1, running: n }[s.status];
    if (idx != null) lis.forEach((li, i) => { li.classList.toggle('done', i < idx); li.classList.toggle('run', i === idx); });
    if (s.status === 'running') {
      t.done = true; KI.busy = false;
      if ($('#stamp')) { $('#stamp').textContent = 'Erledigt'; $('#stamp').classList.add('ok'); }
      setCore('done'); toast(s.name + ' läuft auf Port ' + (s.ports[0] ? s.ports[0].host : ''), 'ok');
      setTimeout(() => { if (!KI.busy) setCore('idle'); }, 2200);
    } else if (s.status === 'error' || (s.status === 'stopped' && s.message)) {
      t.done = true; KI.busy = false;
      if ($('#stamp')) { $('#stamp').textContent = 'Fehler'; $('#stamp').classList.add('bad'); }
      addMsg('KI', 'Das hat nicht geklappt: ' + (s.message || 'unbekannter Fehler') + ' Die Details stehen in der Konsole.', 'bad');
      setCore('idle');
    }
  }
  $('#askForm').addEventListener('submit', e => { e.preventDefault(); ask($('#askInput').value); });
  $('#askInput').addEventListener('input', e => { if (!KI.busy) setCore(e.target.value.trim() ? 'listening' : 'idle'); });
  $('#chips').addEventListener('click', e => { const b = e.target.closest('button'); if (b) ask(b.textContent); });

  /* ---------------- Bestätigen / Name abfragen ---------------- */
  function confirmBox(title, text, okLabel) {
    return new Promise(res => {
      const d = $('#dlgConfirm');
      $('#cfTitle').textContent = title; $('#cfText').textContent = text; $('#cfOk').textContent = okLabel || 'OK';
      const done = v => { d.removeEventListener('close', onClose); res(v); };
      const onClose = () => done(d.returnValue === 'ok');
      d.returnValue = '';
      d.addEventListener('close', onClose);
      $('#cfForm').onsubmit = e => { e.preventDefault(); d.close('ok'); };
      d.showModal();
    });
  }
  function promptBox(title, label, value, okLabel) {
    return new Promise(res => {
      const d = $('#dlgPrompt');
      $('#prTitle').textContent = title; $('#prLabel').textContent = label; $('#prOk').textContent = okLabel || 'OK';
      const inp = $('#prInput'); inp.value = value || '';
      const onClose = () => { d.removeEventListener('close', onClose); res(d.returnValue === 'ok' ? inp.value.trim() : null); };
      d.returnValue = '';
      d.addEventListener('close', onClose);
      $('#prForm').onsubmit = e => { e.preventDefault(); d.close('ok'); };
      d.showModal(); setTimeout(() => { inp.focus(); inp.select(); }, 30);
    });
  }
  const fmtSize = b => b < 1024 ? b + ' B' : b < 1048576 ? (b / 1024).toFixed(0) + ' KB' : b < 1073741824 ? (b / 1048576).toFixed(1).replace('.', ',') + ' MB' : (b / 1073741824).toFixed(2).replace('.', ',') + ' GB';
  const fmtDate = d => new Date(d).toLocaleString('de-DE', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' });
  const ICON = {
    dir: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round"><path d="M3 6.5A1.5 1.5 0 014.5 5H9l2 2.5h8.5A1.5 1.5 0 0121 9v9.5a1.5 1.5 0 01-1.5 1.5h-15A1.5 1.5 0 013 18.5z"/></svg>',
    file: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round"><path d="M6 3h8l4 4v14H6z"/><path d="M14 3v4h4"/></svg>',
    dl: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4v11M7 10l5 5 5-5M5 20h14"/></svg>',
    ren: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 20h4L19 9l-4-4L4 16z"/></svg>',
    up: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M7 18a4 4 0 01-.5-8 6 6 0 0111.5 1.5A3.5 3.5 0 0117.5 18"/><path d="M12 12v8M9 15l3-3 3 3"/></svg>',
    play: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round"><path d="M7 5l12 7-12 7z"/></svg>',
    del: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/></svg>'
  };

  /* ---------------- Server verwalten: Backups + Dateien ---------------- */
  const MG = { id: null, tab: 'backups', path: '', edit: null, dirty: false, busy: false };
  const KIND = { manuell: 'Manuell', auto: 'Automatisch', 'vor-wiederherstellung': 'Vor Wiederherstellung' };
  $('#bkAt').innerHTML = Array.from({ length: 24 }, (_, h) => `<option value="${h}">${String(h).padStart(2, '0')}:00 Uhr</option>`).join('');

  function openManage(id, tab) { select(id, tab); }
  function mgTab(tab) {
    const s = byId(MG.id), tpl = s && S.tplById[s.template];
    if (tab === 'mods' && !(tpl && tpl.mods)) tab = 'settings';
    if (!can('operator') && ['settings', 'mods', 'files'].includes(tab)) tab = 'console';
    if (!['console', 'settings', 'mods', 'backups', 'files', 'tasks', 'events'].includes(tab)) tab = 'console';
    MG.tab = tab;
    $$('#svTabs button').forEach(b => b.setAttribute('aria-selected', String(b.dataset.tab === tab)));
    const panes = { console: 'paneConsole', backups: 'paneBackups', files: 'paneFiles', tasks: 'paneTasks', events: 'paneEvents', settings: 'paneSettings', mods: 'paneMods' };
    Object.entries(panes).forEach(([k, id]) => { $('#' + id).hidden = k !== tab; });
    clearTimeout(TK.timer);
    if (tab !== 'files') closeEditor(true);
    if (tab === 'console') { if (S.consoleId !== MG.id) openConsole(MG.id); setTimeout(() => { const b = $('#lines'); b.scrollTop = b.scrollHeight; }, 0); }
    else if (tab === 'settings') loadSettings();
    else if (tab === 'mods') loadMods();
    else if (tab === 'backups') loadBackups();
    else if (tab === 'files') loadFiles(MG.path);
    else loadTasks();
  }
  $('#svTabs').addEventListener('click', e => { const b = e.target.closest('button[data-tab]'); if (b) select(MG.id, b.dataset.tab); });

  // Spiel-Einstellungen
  const GS = { data: null, orig: {} };
  function cfgField(f, s) {
    const id = `st_${f}_${s.key.replace(/[^A-Za-z0-9_-]/g, '_')}`, attrs = `data-cf="${f}" data-k="${esc(s.key)}"`;
    const help = s.help ? `<small>${esc(s.help)}</small>` : '';
    if (s.type === 'bool') return `<label class="check"><input type="checkbox" ${attrs} ${s.value === 'true' ? 'checked' : ''}> ${esc(s.label)}</label>`;
    let input;
    if (s.type === 'select') {
      const opts = s.options.map((o, i) => `<option value="${esc(o)}" ${o === s.value ? 'selected' : ''}>${esc((s.labels && s.labels[i]) || o)}</option>`);
      if (s.value && !s.options.includes(s.value)) opts.unshift(`<option value="${esc(s.value)}" selected>${esc(s.value)}</option>`);
      input = `<select id="${id}" ${attrs}>${opts.join('')}</select>`;
    } else if (s.type === 'secret') {
      const set = s.value === '••••••';
      input = `<input id="${id}" ${attrs} type="password" value="" placeholder="${set ? 'gesetzt, unverändert' : 'leer'}" autocomplete="new-password">`;
    } else if (s.type === 'number') {
      input = `<input id="${id}" ${attrs} type="number" step="any" value="${esc(s.value)}"${s.min != null ? ` min="${s.min}"` : ''}${s.max != null ? ` max="${s.max}"` : ''}>`;
    } else {
      input = `<input id="${id}" ${attrs} type="text" value="${esc(s.value)}" maxlength="300" autocomplete="off">`;
    }
    return `<label class="field"><span>${esc(s.label)}</span>${input}${help}</label>`;
  }
  async function loadSettings() {
    const id = MG.id;
    let d;
    try { d = await api('GET', `/api/servers/${id}/settings`); } catch (e) { toast(e.message, 'bad'); return; }
    if (MG.id !== id) return;
    const sv = byId(id); GS.status = sv ? sv.status : '';
    GS.data = d; renderSettings();
  }
  function renderSettings() {
    const d = GS.data;
    let h = '';
    if (d.vars.length) {
      h += `<fieldset class="st-sec"><legend>Start-Einstellungen</legend><p class="small muted st-info">Werden dem Spiel beim Start mitgegeben. Ändern legt den Container neu an, Spielstände bleiben erhalten.</p>
        <div class="vars" id="stVars">${d.vars.map(v => varField(v, v.value, true, 'stv_')).join('')}</div></fieldset>`;
    }
    d.files.forEach((f, i) => {
      const shown = f.settings.filter(s => s.present);
      h += `<fieldset class="st-sec"><legend>${esc(f.title)}</legend><p class="st-file">${esc(f.file)}</p>`;
      if (f.note) h += `<p class="small muted st-info">${esc(f.note)}</p>`;
      if (f.error) h += `<p class="err">${esc(f.error)}</p>`;
      if (!f.editable && !f.error) {
        const sv = byId(MG.id), st = sv ? sv.status : '';
        h += `<p class="st-wait">${st === 'installing' || st === 'preparing' ? '<span class="spin" aria-hidden="true"></span> Das Spiel wird gerade installiert. Diese Datei gibt es erst nach der Installation oder dem ersten Start – sobald sie da ist, erscheinen die Einstellungen hier von selbst.'
          : st === 'starting' ? '<span class="spin" aria-hidden="true"></span> Der Server startet zum ersten Mal und legt die Datei gerade an. Die Einstellungen erscheinen hier gleich von selbst.'
          : st === 'running' ? 'Das Spiel hat die Datei noch nicht angelegt. Manche Spiele tun das erst nach dem ersten Speichern oder Beenden – stoppe den Server einmal, dann erscheinen die Einstellungen.'
          : 'Diese Datei legt das Spiel beim ersten Start an. Starte den Server einmal, danach kannst du die Werte hier ändern.'}</p>`;
      }
      else if (f.editable) h += `<div class="vars">${shown.map(s => cfgField(i, s)).join('')}</div>`;
      if (f.editable && shown.length < f.settings.length) { const n = f.settings.length - shown.length; h += `<p class="small muted">${n === 1 ? 'Eine weitere Einstellung gibt' : n + ' weitere Einstellungen gibt'} es in dieser Spielversion nicht.</p>`; }
      h += '</fieldset>';
    });
    if (!d.vars.length && !d.files.length) h = '<p class="muted">Für dieses Spiel gibt es noch kein Einstellungs-Formular. Die Konfigurationsdateien kannst du unter „Dateien“ bearbeiten.</p>';
    $('#stBody').innerHTML = h;
    $('#stSave').hidden = !d.vars.length && !d.files.some(f => f.editable);
    $('#stNote').textContent = d.running ? 'Der Server läuft. Zum Speichern wird er kurz gestoppt und wieder gestartet, die Spieler werden dabei getrennt.' : '';
    $('.st-foot').hidden = $('#stSave').hidden;
    GS.orig = collectSettings();
  }
  function collectSettings() {
    const files = {};
    $$('#stBody [data-cf]').forEach(el => {
      const f = el.dataset.cf, k = el.dataset.k;
      let v = el.type === 'checkbox' ? String(el.checked) : el.value;
      if (el.type === 'password' && v === '') v = '••••••';
      (files[f] = files[f] || {})[k] = v;
    });
    return { vars: $('#stVars') ? collectVars($('#stVars'), true) : {}, files };
  }
  $('#stForm').addEventListener('submit', async e => {
    e.preventDefault();
    const now = collectSettings(), body = { vars: {}, files: {} };
    for (const [k, v] of Object.entries(now.vars)) if (GS.orig.vars[k] !== v) body.vars[k] = v;
    for (const [f, kv] of Object.entries(now.files)) for (const [k, v] of Object.entries(kv)) {
      if (!GS.orig.files[f] || GS.orig.files[f][k] !== v) (body.files[f] = body.files[f] || {})[k] = v;
    }
    if (!Object.keys(body.vars).length && !Object.keys(body.files).length) { toast('Nichts geändert'); return; }
    const b = $('#stSave'); b.disabled = true; b.textContent = GS.data.running ? 'Stoppt, speichert, startet …' : 'Speichert …';
    try {
      const r = await api('PUT', `/api/servers/${MG.id}/settings`, body);
      GS.data = r.settings; renderSettings();
      toast(r.changed && r.changed.length ? 'Gespeichert: ' + r.changed.join(', ') : 'Nichts geändert', 'ok');
      loadServers();
    } catch (err) { toast(err.message, 'bad'); }
    finally { b.disabled = false; b.textContent = 'Speichern'; }
  });

  // Mods (Modrinth)
  const MD = { st: null, want: [], info: {}, q: '', off: 0, hits: [] };
  const fmtNum = n => n >= 1e6 ? (n / 1e6).toFixed(1).replace('.', ',') + ' Mio.' : n >= 1e3 ? Math.round(n / 1e3) + ' Tsd.' : String(n);
  async function loadMods() {
    const id = MG.id;
    let d;
    try { d = await api('GET', `/api/servers/${id}/mods`); } catch (e) { toast(e.message, 'bad'); return; }
    if (MG.id !== id) return;
    d.slugs = d.slugs || []; d.installed = d.installed || [];
    MD.st = d; MD.want = d.slugs.slice(); MD.hits = [];
    (d.installed || []).forEach(p => { MD.info[p.slug] = p; MD.info[p.id] = p; });
    const what = d.project_type === 'plugin' ? 'Plugins' : 'Mods';
    const loader = { fabric: 'Fabric', forge: 'Forge', neoforge: 'NeoForge', quilt: 'Quilt', paper: 'Paper', purpur: 'Purpur', spigot: 'Spigot', bukkit: 'Bukkit', folia: 'Folia' }[d.loader] || d.loader;
    $('#mdInfo').textContent = d.supported
      ? `${loader}-${what} für Minecraft ${d.game_version || '(neueste Version)'}. Nur Projekte, die auf dem Server laufen. Abhängigkeiten lädt der Server beim Start automatisch mit.${d.lookup_error ? ' Hinweis: ' + d.lookup_error : ''}`
      : d.reason;
    $('#mdSearchForm').hidden = !d.supported;
    renderMods();
    if (d.supported && !MD.hits.length) searchMods('', 0);
  }
  function modCard(p, mode) {
    const icon = p.icon ? `<img src="${esc(p.icon)}" alt="" loading="lazy" referrerpolicy="no-referrer">` : '<span class="md-ph" aria-hidden="true"></span>';
    const btn = mode === 'installed' || mode === 'adding' ? `<button type="button" class="btn-line" data-md-del="${esc(p.slug)}">Entfernen</button>`
      : mode === 'removing' ? `<button type="button" class="btn-line" data-md-add="${esc(p.slug)}">Behalten</button>`
      : mode === 'have' ? '<span class="mono muted">installiert</span>'
      : `<button type="button" class="btn-line" data-md-add="${esc(p.slug)}">Hinzufügen</button>`;
    const sub = p.downloads ? `<span class="mono muted">${fmtNum(p.downloads)} Downloads${p.author ? ' · ' + esc(p.author) : ''}</span>` : '';
    return `<div class="md-item ${mode === 'adding' ? 'add' : mode === 'removing' ? 'del' : ''}">${icon}<div class="md-t"><b title="${esc(p.title || p.slug)}">${esc(p.title || p.slug)}</b>${sub}<p>${esc(p.description || '')}</p></div>${btn}</div>`;
  }
  function renderMods() {
    const d = MD.st, lc = s => s.toLowerCase(), want = MD.want.map(lc), had = d.slugs.map(lc);
    const shown = [];
    MD.want.forEach(sl => shown.push([MD.info[sl] || { slug: sl, title: sl }, had.includes(lc(sl)) ? 'installed' : 'adding']));
    d.slugs.forEach(sl => { if (!want.includes(lc(sl))) shown.push([MD.info[sl] || { slug: sl, title: sl }, 'removing']); });
    $('#mdInstalled').innerHTML = shown.length
      ? shown.map(([p, m]) => modCard(p, m)).join('')
      : `<p class="muted">Noch keine ${d.project_type === 'plugin' ? 'Plugins' : 'Mods'} installiert.</p>`;
    const dirty = want.length !== had.length || want.some(x => !had.includes(x));
    $('#mdBar').hidden = !dirty;
    if (dirty) {
      const add = want.filter(x => !had.includes(x)).length, del = had.filter(x => !want.includes(x)).length;
      $('#mdBarText').textContent = [add ? add + ' hinzufügen' : '', del ? del + ' entfernen' : ''].filter(Boolean).join(', ') + '. ' + (d.running ? 'Der Server startet zum Übernehmen neu.' : 'Wird beim nächsten Start installiert.');
    }
    $('#mdResults').innerHTML = MD.hits.map(p => modCard(p, want.includes(lc(p.slug)) ? 'have' : 'search')).join('');
  }
  async function searchMods(q, off) {
    const id = MG.id;
    try {
      const r = await api('GET', `/api/servers/${id}/mods/search?q=${encodeURIComponent(q)}&offset=${off}`);
      if (MG.id !== id) return;
      MD.q = q; MD.off = off + r.hits.length;
      r.hits = r.hits || [];
      MD.hits = off ? MD.hits.concat(r.hits) : r.hits;
      r.hits.forEach(p => { MD.info[p.slug] = p; });
      $('#mdMore').hidden = MD.off >= r.total;
      if (!MD.hits.length) $('#mdResults').innerHTML = '<p class="muted">Nichts gefunden.</p>'; else renderMods();
    } catch (e) { $('#mdResults').innerHTML = `<p class="err">${esc(e.message)}</p>`; $('#mdMore').hidden = true; }
  }
  $('#mdSearchForm').addEventListener('submit', e => { e.preventDefault(); searchMods($('#mdQ').value.trim(), 0); });
  $('#mdMore').addEventListener('click', () => searchMods(MD.q, MD.off));
  $('#paneMods').addEventListener('click', e => {
    const a = e.target.closest('[data-md-add]'), d = e.target.closest('[data-md-del]');
    if (a && !MD.want.some(x => x.toLowerCase() === a.dataset.mdAdd.toLowerCase())) { MD.want.push(a.dataset.mdAdd); renderMods(); }
    if (d) { MD.want = MD.want.filter(x => x.toLowerCase() !== d.dataset.mdDel.toLowerCase()); renderMods(); }
  });
  $('#mdReset').addEventListener('click', () => { MD.want = MD.st.slugs.slice(); renderMods(); });
  $('#mdApply').addEventListener('click', async () => {
    const b = $('#mdApply'); b.disabled = true; b.textContent = 'Übernimmt …';
    try {
      const d = await api('PUT', `/api/servers/${MG.id}/mods`, { slugs: MD.want });
      d.slugs = d.slugs || []; MD.st = d; MD.want = d.slugs.slice(); (d.installed || []).forEach(p => { MD.info[p.slug] = p; });
      renderMods(); toast(d.running ? 'Übernommen, der Server startet neu und lädt die Mods' : 'Übernommen, wird beim nächsten Start installiert', 'ok'); loadServers();
    } catch (err) { toast(err.message, 'bad'); }
    finally { b.disabled = false; b.textContent = 'Übernehmen'; }
  });

  // Aufgaben, Wächter, Verlauf
  const TK = { data: null, timer: null };
  const DAYS = ['So', 'Mo', 'Di', 'Mi', 'Do', 'Fr', 'Sa'];
  $('#tkDays').innerHTML = [1, 2, 3, 4, 5, 6, 0].map(d => `<label><input type="checkbox" value="${d}">${DAYS[d]}</label>`).join('');
  const fmtWhen = d => {
    if (!d || d.startsWith('0001')) return '–';
    const t = new Date(d), now = new Date(), tom = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1);
    const hm = pad2(t.getHours()) + ':' + pad2(t.getMinutes());
    if (t.toDateString() === now.toDateString()) return 'heute ' + hm;
    if (t.toDateString() === tom.toDateString()) return 'morgen ' + hm;
    return t.toLocaleDateString('de-DE', { weekday: 'short', day: '2-digit', month: '2-digit' }) + ' ' + hm;
  };
  const schedText = t => t.every_hours ? (t.every_hours === 1 ? 'Stündlich' : `Alle ${t.every_hours} Stunden`)
    : (t.days && t.days.length ? t.days.slice().sort((a, b) => ((a + 6) % 7) - ((b + 6) % 7)).map(d => DAYS[d]).join(', ') : 'Täglich') + ' ' + t.at;
  async function loadTasks() {
    const id = MG.id, tab = MG.tab;
    let d;
    try { d = await api('GET', `/api/servers/${id}/tasks`); } catch (e) { toast(e.message, 'bad'); return; }
    if (MG.id !== id || MG.tab !== tab || R.page !== 'server') return;
    TK.data = d;
    if (tab === 'tasks') renderTasks(); else renderEvents();
    TK.timer = setTimeout(loadTasks, 10000);
  }
  function renderEvents() {
    const ev = TK.data.events || [];
    $('#evList').innerHTML = ev.length ? ev.map(e => `<li class="${esc(e.level)}"><span class="dot" aria-hidden="true"></span><time datetime="${esc(e.time)}">${esc(fmtWhen(e.time))}</time><span>${esc(e.text)}</span></li>`).join('')
      : '<li class="muted" style="display:block;padding:20px 4px">Noch keine Ereignisse. Hier erscheinen Starts, Stopps, Abstürze, Updates und ausgeführte Aufgaben.</li>';
  }
  function renderTasks() {
    const d = TK.data, s = byId(MG.id);
    if (document.activeElement && document.activeElement.closest && document.activeElement.closest('.tk-top')) { /* nicht unter dem Finger umschalten */ }
    else {
      $('#wAuto').checked = !d.watch.no_auto_restart; $('#wHang').checked = !!d.watch.hang_restart && d.can_query;
      $('#wHangMin').value = String(d.watch.hang_minutes || 5);
    }
    $('#wHang').disabled = $('#wHangMin').disabled = !d.can_query; $('#wHangNote').hidden = d.can_query;
    $('#tkUpdWrap').hidden = !d.can_update;
    $('#tkUpdText').textContent = s && s.category && s.category.startsWith('Steam') ? 'SteamCMD lädt die neueste Version. Ein laufender Server startet dafür neu, ein gestoppter aktualisiert beim nächsten Start.' : 'Lädt die neueste Version des Spiels. Ein laufender Server startet dafür neu.';
    $('#tkAction').querySelector('[value=update]').disabled = !d.can_update;
    const tasks = d.tasks || [];
    $('#tkRows').innerHTML = tasks.length ? tasks.map((t, i) => {
      const what = esc(t.label) + (t.command ? ` <span class="tk-cmdtxt">${esc(t.command)}</span>` : '') + (t.warn_min ? ` <span class="muted small">· Warnung ${t.warn_min} Min.</span>` : '');
      const res = t.last_result ? `<span class="tk-res small ${/^Fehler/.test(t.last_result) ? 'bad' : 'muted'}" title="${esc(t.last_result)}">${esc(t.last_result)}</span>` : '';
      return `<tr data-i="${i}"><td><input type="checkbox" class="tk-on" ${t.enabled ? 'checked' : ''} aria-label="Aufgabe aktiv"></td><td>${esc(schedText(t))}</td><td>${what}</td>
        <td class="mono small">${t.enabled ? esc(fmtWhen(t.next_run)) : 'aus'}</td><td><span class="mono small">${esc(fmtWhen(t.last_run))}</span>${res}</td>
        <td><div class="acts row"><button type="button" class="icon-btn tk-run" title="Jetzt ausführen" aria-label="Jetzt ausführen">${ICON.play}</button><button type="button" class="icon-btn tk-del" title="Löschen" aria-label="Löschen">${ICON.del}</button></div></td></tr>`;
    }).join('') : '<tr class="empty"><td colspan="6">Noch keine Aufgaben. Zum Beispiel: täglich um 4 Uhr neu starten, vorher die Spieler warnen.</td></tr>';
    tkFormFields();
  }
  function tkFormFields() {
    const a = $('#tkAction').value, k = $('#tkKind').value, d = TK.data || {};
    $('#tkCmdWrap').hidden = a !== 'command';
    $('#tkAtWrap').hidden = k === 'every'; $('#tkEveryWrap').hidden = k !== 'every'; $('#tkDays').hidden = k !== 'days';
    const warnable = ['restart', 'update', 'stop'].includes(a);
    $('#tkWarnWrap').hidden = !warnable || !d.can_broadcast;
    $('#tkNote').textContent = warnable && !d.can_broadcast ? 'Dieses Spiel kann vom Panel aus keine Nachricht an die Spieler schicken, deshalb gibt es keine Vorwarnung.' : (a === 'backup' && S.license && !S.license.edition.auto_backup ? 'Geplante Backups gehören nicht zu deiner Edition.' : '');
  }
  ['tkAction', 'tkKind'].forEach(x => $('#' + x).addEventListener('change', tkFormFields));
  async function saveTasks(tasks, okMsg) {
    try { TK.data = await api('PUT', `/api/servers/${MG.id}/tasks`, { tasks }); renderTasks(); if (okMsg) toast(okMsg, 'ok'); return true; }
    catch (e) { toast(e.message, 'bad'); loadTasks(); return false; }
  }
  const plainTasks = () => (TK.data.tasks || []).map(t => ({ id: t.id, action: t.action, command: t.command, at: t.at, days: t.days, every_hours: t.every_hours, warn_min: t.warn_min, enabled: t.enabled }));
  $('#tkForm').addEventListener('submit', async e => {
    e.preventDefault();
    const k = $('#tkKind').value, a = $('#tkAction').value;
    const t = { action: a, command: $('#tkCmd').value, enabled: true, warn_min: $('#tkWarnWrap').hidden ? 0 : +$('#tkWarn').value };
    if (k === 'every') t.every_hours = +$('#tkEvery').value; else t.at = $('#tkAt').value;
    if (k === 'days') {
      t.days = $$('#tkDays input:checked').map(x => +x.value);
      if (!t.days.length) { toast('Bitte mindestens einen Tag wählen', 'bad'); return; }
    }
    if (await saveTasks(plainTasks().concat([t]), 'Aufgabe angelegt')) $('#tkCmd').value = '';
  });
  $('#tkRows').addEventListener('change', e => {
    const tr = e.target.closest('tr[data-i]'); if (!tr || !e.target.classList.contains('tk-on')) return;
    const list = plainTasks(); list[+tr.dataset.i].enabled = e.target.checked; saveTasks(list);
  });
  $('#tkRows').addEventListener('click', async e => {
    const tr = e.target.closest('tr[data-i]'); if (!tr) return;
    const t = TK.data.tasks[+tr.dataset.i];
    if (e.target.closest('.tk-del')) {
      if (!await confirmBox('Aufgabe löschen?', schedText(t) + ': ' + t.label, 'Löschen')) return;
      const list = plainTasks(); list.splice(+tr.dataset.i, 1); saveTasks(list, 'Aufgabe gelöscht');
    } else if (e.target.closest('.tk-run')) {
      if (['restart', 'stop', 'update'].includes(t.action) && !await confirmBox(t.label + ' jetzt ausführen?', 'Ohne Vorwarnung an die Spieler.', 'Ausführen')) return;
      try { await api('POST', `/api/servers/${MG.id}/tasks/${t.id}/run`); toast(t.label + ' wird ausgeführt', 'ok'); setTimeout(loadTasks, 2500); } catch (err) { toast(err.message, 'bad'); }
    }
  });
  async function saveWatch() {
    try { TK.data = await api('PUT', `/api/servers/${MG.id}/watch`, { no_auto_restart: !$('#wAuto').checked, hang_restart: $('#wHang').checked, hang_minutes: +$('#wHangMin').value }); renderTasks(); toast('Wächter gespeichert', 'ok'); }
    catch (e) { toast(e.message, 'bad'); loadTasks(); }
  }
  ['wAuto', 'wHang', 'wHangMin'].forEach(x => $('#' + x).addEventListener('change', saveWatch));
  $('#tkUpdate').addEventListener('click', async () => {
    const s = byId(MG.id);
    if (isOn(s) && !await confirmBox('Jetzt aktualisieren?', s.name + ' startet dafür neu. Spieler werden getrennt.', 'Aktualisieren')) return;
    try { await api('POST', `/api/servers/${MG.id}/update`); toast('Update gestartet', 'ok'); loadServers(); setTimeout(loadTasks, 2000); } catch (e) { toast(e.message, 'bad'); }
  });

  // Backups
  async function loadBackups() {
    const id = MG.id;
    let d;
    try { d = await api('GET', `/api/servers/${id}/backups`); } catch (e) { toast(e.message, 'bad'); return; }
    if (MG.id !== id) return;
    $('#bkScope').textContent = d.scope && d.scope.length
      ? 'Gesichert werden: ' + d.scope.join(', ') + '. Die Spieldateien selbst lädt das Panel bei Bedarf neu.'
      : 'Noch keine Spieldaten vorhanden. Starte den Server einmal, dann gibt es etwas zu sichern.';
    const p = d.plan || {};
    $('#bkEvery').value = String(p.every_hours || 0); $('#bkAt').value = String(p.at_hour || 4); $('#bkKeep').value = p.keep || 7;
    $('#bkEvery').disabled = !d.auto_allowed; $('#bkAtWrap').hidden = +$('#bkEvery').value !== 24;
    const notes = [];
    if (!d.auto_allowed) notes.push('Geplante Backups gehören zu Pro und Ultimate. Manuelle Backups gehen immer.');
    if (p.last_auto && !p.last_auto.startsWith('0001')) notes.push('Letztes automatisches Backup: ' + fmtDate(p.last_auto));
    if (p.last_error) notes.push('Fehler beim letzten automatischen Backup: ' + p.last_error);
    $('#bkPlanNote').hidden = !notes.length; $('#bkPlanNote').textContent = notes.join(' · ');
    const hasRemote = !!d.remote;
    $('#bkRemote').innerHTML = hasRemote
      ? 'Externe Kopie: ' + esc(d.remote) + (d.remote_error ? ` · <span class="err">${esc(d.remote_error)}</span>` : '')
      : 'Backups liegen nur auf diesem Server. <button type="button" class="linkbtn" id="bkSetup">Externes Ziel einrichten</button>, damit sie einen Plattendefekt überstehen.';
    const su = $('#bkSetup'); if (su) su.onclick = () => openSettings('backups');
    const loc = b => {
      const parts = [];
      if (b.local) parts.push('<span>Lokal</span>');
      if (b.remote) parts.push('<span class="ext">Extern</span>');
      else if (b.upload === 'läuft') parts.push('<span class="busy">Wird kopiert</span>');
      else if (b.upload) parts.push(`<span class="bad" title="${esc(b.upload)}">Kopie fehlgeschlagen</span>`);
      return '<div class="loc">' + parts.join('') + '</div>';
    };
    MG.uploading = d.backups.some(b => b.upload === 'läuft');
    if (MG.uploading) { clearTimeout(MG.upT); MG.upT = setTimeout(() => { if (R.page === 'server' && MG.tab === 'backups' && MG.id === id) loadBackups(); }, 3000); }
    $('#bkRows').innerHTML = d.backups.length ? d.backups.map(b => `<tr>
        <td class="num" style="text-align:left">${fmtDate(b.created)}</td>
        <td><span class="kind ${esc(b.kind)}">${esc(KIND[b.kind] || b.kind)}</span></td>
        <td>${loc(b)}</td>
        <td class="num">${fmtSize(b.size)}</td>
        <td><div class="acts">
          ${hasRemote && b.local && !b.remote && b.upload !== 'läuft' ? `<button class="icon-btn" type="button" data-bk="upload" data-name="${esc(b.name)}" aria-label="Extern kopieren" title="Extern kopieren">${ICON.up}</button>` : ''}
          <a class="icon-btn" href="/api/servers/${esc(id)}/backups/${esc(b.name)}" download aria-label="Herunterladen" title="Herunterladen">${ICON.dl}</a>
          <button class="btn-line sm" type="button" data-bk="restore" data-name="${esc(b.name)}">Wiederherstellen</button>
          <button class="icon-btn" type="button" data-bk="delete" data-name="${esc(b.name)}" aria-label="Backup löschen">${ICON.del}</button>
        </div></td></tr>`).join('')
      : '<tr class="empty"><td colspan="5">Noch keine Backups. Erstelle oben das erste.</td></tr>';
    $('#bkTotal').textContent = d.backups.length ? d.backups.length + ' Backups · ' + fmtSize(d.total_size) + ' lokal belegt' : '';
  }
  $('#bkEvery').addEventListener('change', () => { $('#bkAtWrap').hidden = +$('#bkEvery').value !== 24; });
  $('#bkPlan').addEventListener('submit', async e => {
    e.preventDefault();
    try {
      await api('PUT', `/api/servers/${MG.id}/backup-plan`, { every_hours: +$('#bkEvery').value, at_hour: +$('#bkAt').value, keep: +$('#bkKeep').value });
      toast('Backup-Plan gespeichert', 'ok'); loadBackups(); loadServers();
    } catch (err) { toast(err.message, 'bad'); }
  });
  $('#bkNow').addEventListener('click', async () => {
    const b = $('#bkNow'); if (b.disabled) return;
    b.disabled = true; b.textContent = 'Sichert …';
    try { const r = await api('POST', `/api/servers/${MG.id}/backups`); toast('Backup erstellt · ' + fmtSize(r.size), 'ok'); }
    catch (err) { toast(err.message, 'bad'); }
    finally { b.disabled = false; b.textContent = 'Backup jetzt erstellen'; loadBackups(); }
  });
  $('#bkRows').addEventListener('click', async e => {
    const b = e.target.closest('[data-bk]'); if (!b) return;
    const name = b.dataset.name, id = MG.id, s = byId(id);
    if (b.dataset.bk === 'upload') {
      try { await api('POST', `/api/servers/${id}/backups/${encodeURIComponent(name)}/upload`); toast('Externe Kopie läuft'); } catch (err) { toast(err.message, 'bad'); }
      loadBackups(); return;
    }
    if (b.dataset.bk === 'delete') {
      if (!(await confirmBox('Backup löschen?', 'Das Backup vom ' + name.slice(0, 10).split('-').reverse().join('.') + ' wird endgültig gelöscht, lokal und extern.', 'Löschen'))) return;
      try { await api('DELETE', `/api/servers/${id}/backups/${encodeURIComponent(name)}`); toast('Backup gelöscht'); } catch (err) { toast(err.message, 'bad'); }
      loadBackups();
    } else {
      const txt = (s && isOn(s) ? 'Der Server wird dafür kurz gestoppt. ' : '') + 'Der aktuelle Stand wird vorher automatisch gesichert, du kannst also zurück.';
      if (!(await confirmBox('Backup wiederherstellen?', txt, 'Wiederherstellen'))) return;
      try { await api('POST', `/api/servers/${id}/backups/${encodeURIComponent(name)}/restore`); toast('Wiederherstellung läuft'); await loadServers(); select(id); }
      catch (err) { toast(err.message, 'bad'); }
    }
  });

  // Dateien
  const joinPath = (a, b) => (a ? a + '/' : '') + b;
  async function loadFiles(path) {
    const id = MG.id;
    let list;
    try { list = await api('GET', `/api/servers/${id}/files?path=${encodeURIComponent(path)}`); }
    catch (e) { toast(e.message, 'bad'); if (path) { MG.path = ''; return loadFiles(''); } return; }
    if (MG.id !== id) return;
    MG.path = path;
    const parts = path ? path.split('/') : [];
    $('#fmCrumbs').innerHTML = `<button type="button" data-p="">Serverordner</button>` +
      parts.map((p, i) => `<span>/</span><button type="button" data-p="${esc(parts.slice(0, i + 1).join('/'))}">${esc(p)}</button>`).join('');
    $('#fmList').innerHTML = list.length ? list.map(f => `<div class="fm-row">
        <button type="button" class="nm ${f.dir ? 'dir' : ''}" data-f="${f.dir ? 'open' : 'edit'}" data-n="${esc(f.name)}" data-size="${f.size}">${f.dir ? ICON.dir : ICON.file}<span>${esc(f.name)}</span></button>
        <span class="sz">${f.dir ? '' : fmtSize(f.size)}</span>
        <span class="dt">${fmtDate(f.mod_time)}</span>
        <div class="acts">
          ${f.dir ? '' : `<a class="icon-btn" href="/api/servers/${esc(id)}/files/download?path=${encodeURIComponent(joinPath(path, f.name))}" download aria-label="${esc(f.name)} herunterladen">${ICON.dl}</a>`}
          <button type="button" class="icon-btn" data-f="rename" data-n="${esc(f.name)}" aria-label="${esc(f.name)} umbenennen">${ICON.ren}</button>
          <button type="button" class="icon-btn" data-f="delete" data-n="${esc(f.name)}" data-dir="${f.dir}" aria-label="${esc(f.name)} löschen">${ICON.del}</button>
        </div></div>`).join('')
      : `<div class="fm-empty">${path ? 'Dieser Ordner ist leer.' : 'Noch keine Dateien. Starte den Server einmal oder lade Dateien hoch.'} Dateien kannst du auch hierher ziehen.</div>`;
  }
  $('#fmCrumbs').addEventListener('click', async e => {
    const b = e.target.closest('[data-p]'); if (!b) return;
    if (MG.dirty && !(await confirmBox('Änderungen verwerfen?', 'Die Datei hat ungespeicherte Änderungen.', 'Verwerfen'))) return;
    closeEditor(true); loadFiles(b.dataset.p);
  });
  $('#fmList').addEventListener('click', async e => {
    const b = e.target.closest('[data-f]'); if (!b) return;
    const n = b.dataset.n, p = joinPath(MG.path, n), id = MG.id;
    const act = b.dataset.f;
    if (act === 'open') loadFiles(p);
    else if (act === 'edit') {
      if (+b.dataset.size > 2 * 1048576) { location.href = `/api/servers/${id}/files/download?path=${encodeURIComponent(p)}`; return; }
      openEditor(p);
    } else if (act === 'rename') {
      const nn = await promptBox('Umbenennen', 'Neuer Name', n, 'Umbenennen'); if (!nn || nn === n) return;
      try { await api('POST', `/api/servers/${id}/files/rename?path=${encodeURIComponent(p)}`, { name: nn }); toast('Umbenannt', 'ok'); } catch (err) { toast(err.message, 'bad'); }
      loadFiles(MG.path);
    } else if (act === 'delete') {
      const dir = b.dataset.dir === 'true';
      if (!(await confirmBox(dir ? 'Ordner löschen?' : 'Datei löschen?', `„${n}“${dir ? ' und alles darin' : ''} wird endgültig gelöscht. Ein Backup vorher schadet nicht.`, 'Löschen'))) return;
      try { await api('DELETE', `/api/servers/${id}/files?path=${encodeURIComponent(p)}`); toast('Gelöscht'); } catch (err) { toast(err.message, 'bad'); }
      loadFiles(MG.path);
    }
  });
  $('#fmMkdir').addEventListener('click', async () => {
    const n = await promptBox('Neuer Ordner', 'Ordnername', '', 'Anlegen'); if (!n) return;
    try { await api('POST', `/api/servers/${MG.id}/files/mkdir?path=${encodeURIComponent(MG.path)}`, { name: n }); } catch (err) { toast(err.message, 'bad'); }
    loadFiles(MG.path);
  });
  function upload(fileList) {
    const filesArr = [...fileList]; if (!filesArr.length) return;
    const fd = new FormData(); filesArr.forEach(f => fd.append('file', f, f.name));
    const xhr = new XMLHttpRequest(), prog = $('#fmProgress');
    xhr.open('POST', `/api/servers/${MG.id}/files/upload?path=${encodeURIComponent(MG.path)}`);
    xhr.setRequestHeader('X-Panel', '1');
    xhr.upload.onprogress = e => { if (e.lengthComputable) prog.textContent = 'Hochladen ' + Math.round(e.loaded / e.total * 100) + ' %'; };
    xhr.onload = () => {
      prog.textContent = '';
      let d = null; try { d = JSON.parse(xhr.responseText); } catch (e) { /* egal */ }
      if (xhr.status < 300) toast(filesArr.length === 1 ? filesArr[0].name + ' hochgeladen' : filesArr.length + ' Dateien hochgeladen', 'ok');
      else toast((d && d.error) || 'Hochladen fehlgeschlagen', 'bad');
      loadFiles(MG.path);
    };
    xhr.onerror = () => { prog.textContent = ''; toast('Hochladen fehlgeschlagen. Verbindung prüfen.', 'bad'); };
    xhr.send(fd);
  }
  $('#fmUpload').addEventListener('change', e => { upload(e.target.files); e.target.value = ''; });
  const fl = $('#fmList');
  fl.addEventListener('dragover', e => { if (e.dataTransfer && [...e.dataTransfer.types].includes('Files')) { e.preventDefault(); fl.classList.add('drop'); } });
  fl.addEventListener('dragleave', () => fl.classList.remove('drop'));
  fl.addEventListener('drop', e => { e.preventDefault(); fl.classList.remove('drop'); upload(e.dataTransfer.files); });

  async function openEditor(p) {
    let r;
    try { r = await api('GET', `/api/servers/${MG.id}/files/content?path=${encodeURIComponent(p)}`); }
    catch (e) { toast(e.message, 'bad'); return; }
    MG.edit = p; MG.dirty = false;
    $('#fmEdPath').textContent = p; $('#fmEdState').textContent = '';
    $('#fmText').value = r.content;
    $('#fmList').hidden = true; $('#fmEditor').hidden = false; $('#fmMkdir').hidden = true; $('.upl').hidden = true;
    $('#fmText').focus(); $('#fmText').setSelectionRange(0, 0); $('#fmText').scrollTop = 0;
  }
  function closeEditor(force) {
    if (!force && MG.dirty) return;
    MG.edit = null; MG.dirty = false;
    $('#fmEditor').hidden = true; $('#fmList').hidden = false; $('#fmMkdir').hidden = false; $('.upl').hidden = false;
  }
  async function saveEditor() {
    if (!MG.edit) return;
    const b = $('#fmEdSave'); b.disabled = true;
    try {
      await api('PUT', `/api/servers/${MG.id}/files/content?path=${encodeURIComponent(MG.edit)}`, { content: $('#fmText').value });
      MG.dirty = false; $('#fmEdState').textContent = 'Gespeichert';
      const s = byId(MG.id);
      toast(s && isOn(s) ? 'Gespeichert. Starte den Server neu, damit es wirkt.' : 'Gespeichert', 'ok');
    } catch (err) { toast(err.message, 'bad'); }
    finally { b.disabled = false; }
  }
  $('#fmText').addEventListener('input', () => { if (!MG.dirty) { MG.dirty = true; $('#fmEdState').textContent = '· ungespeichert'; } });
  $('#fmText').addEventListener('keydown', e => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') { e.preventDefault(); saveEditor(); }
    if (e.key === 'Tab' && !e.shiftKey && !e.ctrlKey) {
      e.preventDefault();
      const t = e.target, a = t.selectionStart;
      t.setRangeText('\t', a, t.selectionEnd, 'end'); t.dispatchEvent(new Event('input'));
    }
  });
  $('#fmEdSave').addEventListener('click', saveEditor);
  $('#fmEdClose').addEventListener('click', async () => {
    if (MG.dirty && !(await confirmBox('Änderungen verwerfen?', 'Die Datei hat ungespeicherte Änderungen.', 'Verwerfen'))) return;
    closeEditor(true); loadFiles(MG.path);
  });

  /* ---------------- Befehlssuche ---------------- */
  let palItems = [], palIdx = 0;
  function actions() {
    const a = can('admin') ? [
      { label: 'Server anlegen', hint: 'Neu', run: () => openCreate() },
      { label: 'Server mit KI planen', hint: 'KI', run: () => { go('#/ki'); setTimeout(() => $('#askInput').focus(), 50); } }
    ] : [];
    const op = can('operator');
    S.servers.forEach(s => {
      a.push({ label: s.name + ' öffnen', hint: 'Server', run: () => select(s.id) });
      if (op && s.status !== 'preparing') a.push({ label: s.name + (isOn(s) ? ' stoppen' : ' starten'), hint: 'Server', run: () => togglePower(s) });
      if (op) a.push({ label: 'Einstellungen von ' + s.name, hint: 'Einstellungen', run: () => openManage(s.id, 'settings') });
      if (op && (S.tplById[s.template] || {}).mods) a.push({ label: 'Mods von ' + s.name, hint: 'Mods', run: () => openManage(s.id, 'mods') });
      a.push({ label: 'Backups von ' + s.name, hint: 'Backup', run: () => openManage(s.id, 'backups') });
      if (op) a.push({ label: 'Dateien von ' + s.name, hint: 'Dateien', run: () => openManage(s.id, 'files') });
      a.push({ label: 'Aufgaben von ' + s.name, hint: 'Aufgaben', run: () => openManage(s.id, 'tasks') });
      a.push({ label: 'Verlauf von ' + s.name, hint: 'Verlauf', run: () => openManage(s.id, 'events') });
      a.push({ label: 'Konsole von ' + s.name, hint: 'Konsole', run: () => select(s.id, 'console') });
    });
    a.push({ label: 'Übersicht', hint: 'Seite', run: () => go('#/') });
    a.push({ label: 'Alle Server', hint: 'Seite', run: () => go('#/server') });
    a.push({ label: 'Mein Konto, Passwort, Zwei-Faktor', hint: 'Konto', run: () => openSettings('konto') });
    if (can('admin')) {
      a.push({ label: 'Selbsttest der Spielvorlagen', hint: 'Panel', run: openTest });
      a.push({ label: 'Benutzer verwalten', hint: 'Panel', run: openUsers });
      a.push({ label: 'Protokoll: wer hat was gemacht', hint: 'Panel', run: openAudit });
      a.push({ label: 'HTTPS einrichten', hint: 'Panel', run: () => openSettings('https') });
      a.push({ label: 'Allgemein: Spieler-Adresse und KI-Anbieter', hint: 'Panel', run: () => openSettings('ki') });
      a.push({ label: 'Externe Backups', hint: 'Panel', run: () => openSettings('backups') });
      a.push({ label: 'Steam-Konto', hint: 'Panel', run: () => openSettings('steam') });
      a.push({ label: 'Lizenz', hint: 'Panel', run: () => openSettings('lizenz') });
    }
    a.push({ label: isDark() ? 'Helles Design' : 'Dunkles Design', hint: 'Ansicht', run: toggleTheme });
    a.push({ label: motion ? 'Animationen reduzieren' : 'Animationen einschalten', hint: 'Ansicht', run: toggleMotion });
    return a;
  }
  function renderPal() {
    const q = $('#palInput').value.trim().toLowerCase();
    palItems = actions().filter(a => !q || a.label.toLowerCase().includes(q));
    palIdx = clamp(palIdx, 0, Math.max(0, palItems.length - 1));
    $('#palList').innerHTML = palItems.length ? palItems.map((a, i) => `<li><button type="button" data-i="${i}" class="${i === palIdx ? 'act' : ''}">${esc(a.label)}<span class="mono">${esc(a.hint)}</span></button></li>`).join('') : '<li class="muted" style="padding:14px">Nichts gefunden.</li>';
  }
  function openPal() { if ($('#app').hidden) return; $('#pal').hidden = false; $('#palInput').value = ''; palIdx = 0; renderPal(); $('#palInput').focus(); }
  function closePal() { $('#pal').hidden = true; }
  function runPal(i) { const a = palItems[i]; closePal(); if (a) a.run(); }
  $('#openPal').onclick = openPal; $('#openPal2').onclick = openPal;
  $('#palInput').addEventListener('input', () => { palIdx = 0; renderPal(); });
  $('#palList').addEventListener('click', e => { const b = e.target.closest('button'); if (b) runPal(+b.dataset.i); });
  $('#pal').addEventListener('click', e => { if (e.target.id === 'pal') closePal(); });
  document.addEventListener('keydown', e => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); $('#pal').hidden ? openPal() : closePal(); return; }
    if (!$('#pal').hidden) {
      if (e.key === 'Escape') closePal();
      else if (e.key === 'ArrowDown') { e.preventDefault(); palIdx = Math.min(palItems.length - 1, palIdx + 1); renderPal(); }
      else if (e.key === 'ArrowUp') { e.preventDefault(); palIdx = Math.max(0, palIdx - 1); renderPal(); }
      else if (e.key === 'Enter') { e.preventDefault(); runPal(palIdx); }
      return;
    }
    if (e.key === 'Escape' && document.body.classList.contains('side-open')) sideToggle(false);
    if (e.key === 'Escape' && $('#svMore').open) $('#svMore').open = false;
  });
  document.addEventListener('click', e => {
    const b = e.target.closest('[data-act]'); if (!b) return;
    if (b.dataset.act === 'create') openCreate();
    if (b.dataset.act === 'ai') { go('#/ki'); setTimeout(() => $('#askInput').focus(), 50); }
  });

  /* ---------------- Design + Bewegung ---------------- */
  function syncSwitches() {
    $('#themeBtn').setAttribute('aria-pressed', String(isDark()));
    $('#motionBtn').setAttribute('aria-pressed', String(motion));
    document.body.classList.toggle('still', !motion);
  }
  function toggleTheme() { const n = isDark() ? 'light' : 'dark'; root.dataset.theme = n; store.set('gp-theme', n); syncSwitches(); requestAnimationFrame(applySceneColors); }
  function toggleMotion() { motion = !motion; store.set('gp-motion', motion ? '1' : '0'); syncSwitches(); toast(motion ? 'Animationen an' : 'Animationen reduziert'); }
  $('#themeBtn').onclick = toggleTheme; $('#motionBtn').onclick = toggleMotion;
  lightMQ.addEventListener('change', () => { if (!root.dataset.theme) { syncSwitches(); applySceneColors(); } });

  /* ---------------- Anmeldung ---------------- */
  let authMode = 'login', AuthBg = null, authTicket = '';
  function showAuth(mode) {
    authMode = mode;
    $('#app').hidden = true; $('#auth').hidden = false;
    const setup = mode === 'setup', code = mode === 'code';
    $('#authTitle').textContent = setup ? 'Willkommen' : code ? 'Bestätigen' : 'Anmelden';
    $('#authLead').textContent = setup ? 'Lege dein Admin-Konto an. Damit meldest du dich künftig im Panel an.'
      : code ? 'Gib den 6-stelligen Code aus deiner Authenticator-App ein. Handy weg? Dann einen deiner Wiederherstellungscodes.'
      : 'Melde dich mit deinem Konto an.';
    $('#authCreds').hidden = code; $('#authCodeWrap').hidden = !code; $('#authBack').hidden = !code;
    $('#authName').required = !code; $('#authPass').required = !code;
    $('#authPass2Wrap').hidden = !setup;
    $('#authPass').autocomplete = setup ? 'new-password' : 'current-password';
    $('#authBtn').textContent = setup ? 'Konto anlegen' : code ? 'Bestätigen' : 'Anmelden';
    showErr($('#authErr'));
    if (hasGL && !AuthBg) { try { AuthBg = makeCore($('#authBg'), 9); } catch (e) { AuthBg = null; } }
    setTimeout(() => (code ? $('#authCode') : $('#authName')).focus(), 50);
  }
  $('#authForm').addEventListener('submit', async e => {
    e.preventDefault(); showErr($('#authErr'));
    const name = $('#authName').value, pw = $('#authPass').value;
    if (authMode === 'setup' && pw !== $('#authPass2').value) { showErr($('#authErr'), 'Die Passwörter stimmen nicht überein.'); return; }
    const btn = $('#authBtn'); btn.disabled = true;
    try {
      if (authMode === 'code') {
        const r = await api('POST', '/api/login/2fa', { ticket: authTicket, code: $('#authCode').value });
        $('#authCode').value = ''; authTicket = '';
        if (r.used_recovery) toast(r.recovery_left ? 'Wiederherstellungscode verbraucht. Noch ' + r.recovery_left + ' übrig.' : 'Das war dein letzter Wiederherstellungscode. Bitte neue erstellen.', r.recovery_left > 2 ? '' : 'bad');
      } else {
        const r = await api('POST', authMode === 'setup' ? '/api/setup' : '/api/login', { name, password: pw });
        $('#authPass').value = ''; $('#authPass2').value = '';
        if (r && r.need_code) { authTicket = r.ticket; showAuth('code'); return; }
      }
      await boot();
    } catch (err) {
      showErr($('#authErr'), err.message);
      if (authMode === 'code' && /abgelaufen/.test(err.message)) { authTicket = ''; setTimeout(() => { showAuth('login'); showErr($('#authErr'), err.message); }, 50); }
    }
    finally { btn.disabled = false; }
  });
  $('#authBack').onclick = () => { authTicket = ''; $('#authCode').value = ''; showAuth('login'); };
  $('#logoutBtn').onclick = async () => { try { await api('POST', '/api/logout'); } catch (e) { /* egal */ } stopApp(); closeDialogs(); history.replaceState(null, '', '#/'); showAuth('login'); };

  /* ---------------- Seiten ---------------- */
  const R = { page: null, id: null, tab: null, sec: null, hash: '' };
  const PAGES = { home: 'pageHome', servers: 'pageServers', server: 'pageServer', ki: 'pageKI', users: 'pageUsers', audit: 'pageAudit', test: 'pageTest', settings: 'pageSettings' };
  const ADMIN_PAGES = ['ki', 'users', 'audit', 'test'];
  const TITLES = { home: 'Übersicht', servers: 'Server', ki: 'KI-Assistent', users: 'Benutzer', audit: 'Protokoll', test: 'Selbsttest', settings: 'Einstellungen' };
  function go(hash) { if (location.hash !== hash) location.hash = hash; else route(); }
  function parseHash() {
    const p = location.hash.replace(/^#\/?/, '').split('/').map(x => { try { return decodeURIComponent(x); } catch (e) { return x; } });
    const m = { '': 'home', server: 'servers', ki: 'ki', benutzer: 'users', protokoll: 'audit', selbsttest: 'test', einstellungen: 'settings' };
    let page = m[p[0]] || 'home', id = null, tab = null, sec = null;
    if (page === 'servers' && p[1]) { page = 'server'; id = p[1]; tab = p[2] || 'console'; }
    if (page === 'settings') sec = p[1] || 'konto';
    if (ADMIN_PAGES.includes(page) && !can('admin')) page = 'home';
    return { page, id, tab, sec };
  }
  let routing = false;
  async function route() {
    if ($('#app').hidden || routing) return;
    const n = parseHash();
    const leavingFiles = R.page === 'server' && MG.tab === 'files' && MG.dirty && !(n.page === 'server' && n.id === R.id && n.tab === 'files');
    if (leavingFiles) {
      routing = true;
      const ok = await confirmBox('Änderungen verwerfen?', 'Die Datei hat ungespeicherte Änderungen.', 'Verwerfen');
      routing = false;
      if (!ok) { history.replaceState(null, '', R.hash || '#/'); return; }
      closeEditor(true);
    }
    const prev = R.page, prevId = R.id;
    Object.assign(R, n, { hash: location.hash || '#/' });
    if (prev !== n.page) {
      Object.entries(PAGES).forEach(([k, id]) => { $('#' + id).hidden = k !== n.page; });
      window.scrollTo(0, 0);
    }
    $$('.side-nav a').forEach(a => a.setAttribute('aria-current', a.dataset.nav === (n.page === 'server' ? 'servers' : n.page) ? 'page' : 'false'));
    sideToggle(false);
    clearTimeout(TS.timer); clearTimeout(stmTimer); clearTimeout(TK.timer);
    if (n.page !== 'server' && S.consoleId) closeConsole();
    const s = n.page === 'server' ? byId(n.id) : null;
    document.title = (s ? s.name : TITLES[n.page] || 'Panel') + ' · [Panel]';
    if (n.page === 'home') { requestAnimationFrame(() => { sizeLand(); drawSparks(); }); }
    else if (n.page === 'server') {
      if (!s) { if (S.loaded) { toast('Diesen Server gibt es nicht (mehr).', 'bad'); go('#/server'); } return; }
      if (prevId !== n.id || prev !== 'server') { MG.id = n.id; MG.path = ''; closeEditor(true); if (S.consoleId && S.consoleId !== n.id) closeConsole(); }
      renderServerPage(); mgTab(n.tab);
    }
    else if (n.page === 'ki') { renderKI(); }
    else if (n.page === 'users') enterUsers();
    else if (n.page === 'audit') enterAudit();
    else if (n.page === 'test') pollTest();
    else if (n.page === 'settings') enterSettings(n.sec);
  }
  window.addEventListener('hashchange', route);
  function sideToggle(open) {
    document.body.classList.toggle('side-open', open);
    $('#sideScrim').hidden = !open;
  }
  document.addEventListener('click', e => { const m = $('#svMore'); if (m.open && !m.contains(e.target)) m.open = false; });
  $('#sideOpen').onclick = () => sideToggle(true);
  $('#sideClose').onclick = () => sideToggle(false);
  $('#sideScrim').onclick = () => sideToggle(false);

  /* ---------------- Start + Abfragen ---------------- */
  let timers = [];
  async function loadServers() {
    try {
      S.servers = await api('GET', '/api/servers'); S.loaded = true; renderAll();
      if (S.reconnect && S.consoleId === S.reconnect && !S.es) { const s = byId(S.reconnect); if (s && isOn(s)) { S.reconnect = null; connectConsole(s.id); } }
    } catch (e) { /* nächste Runde */ }
  }
  async function loadHost() {
    try { S.host = await api('GET', '/api/host'); renderHost(); } catch (e) { /* nächste Runde */ }
  }
  async function loadState() {
    try {
      const st = await api('GET', '/api/state');
      S.license = st.license; renderLicense();
      if (st.role && st.role !== S.role) { S.role = st.role; document.body.dataset.role = S.role; renderAll(); }
      const ban = $('#dockerBanner');
      ban.hidden = !st.docker_error;
      if (st.docker_error) ban.textContent = 'Docker ist nicht erreichbar. Läuft der Docker-Dienst? Prüfen mit: sudo systemctl status docker. (' + st.docker_error + ')';
    } catch (e) { /* egal */ }
  }
  function stopApp() { timers.forEach(clearInterval); timers = []; closeConsole(); R.page = null; }
  async function startApp() {
    $('#auth').hidden = true; $('#app').hidden = false;
    const [tpls, set] = await Promise.all([api('GET', '/api/templates'), can('admin') ? api('GET', '/api/settings') : Promise.resolve({ settings: { public_host: S.publicHost } })]);
    S.tpls = tpls; S.tplById = Object.fromEntries(tpls.map(t => [t.id, t])); S.settings = set.settings;
    if (hasGL && !L.renderer) { try { initLand(); } catch (e) { console.error(e); } }
    if (!L.renderer && !$('.nogl')) $('#stage').insertAdjacentHTML('beforeend', '<div class="nogl">Die 3D-Ansicht braucht WebGL. Die Liste daneben funktioniert trotzdem.</div>');
    if (hasGL && !KI.core) { try { KI.core = makeCore($('#core')); } catch (e) { KI.core = null; } }
    if (!KI.core) $('#core').style.height = '24px';
    renderLicense(); renderKI();
    await Promise.all([loadServers(), loadHost()]);
    const vis = fn => () => { if (!document.hidden) fn(); };
    timers.push(setInterval(vis(loadServers), 2500), setInterval(vis(loadHost), 2000), setInterval(vis(loadState), 30000));
    route();
    if (can('admin')) { pollTest(); api('GET', '/api/router').then(r => { S.routerOn = r.enabled; }).catch(() => {}); }
  }
  async function boot() {
    let st;
    try { st = await api('GET', '/api/state'); }
    catch (e) { document.body.innerHTML = '<p style="padding:24px">Das Panel antwortet nicht. Bitte Seite neu laden.</p>'; return; }
    S.version = st.version; S.license = st.license; S.user = st.user;
    S.role = st.role || ''; S.twoFactor = !!st.two_factor; S.publicHost = st.public_host || '';
    document.body.dataset.role = S.role;
    $('#whoAmI').textContent = st.user || ''; $('#whoRole').textContent = ROLE[S.role] || '';
    $('#meAvatar').textContent = (st.user || '?').slice(0, 1).toUpperCase();
    if (st.setup_needed) { showAuth('setup'); return; }
    if (!st.user) { showAuth('login'); return; }
    const ban = $('#dockerBanner'); ban.hidden = !st.docker_error;
    if (st.docker_error) ban.textContent = 'Docker ist nicht erreichbar. Läuft der Docker-Dienst? Prüfen mit: sudo systemctl status docker. (' + st.docker_error + ')';
    await startApp();
  }

  let last = performance.now();
  function loop(now) {
    const dt = Math.min(.05, (now - last) / 1000); last = now; const t = now / 1000;
    try {
      if (!$('#app').hidden) { tickLand(t, dt); if (KI.core) KI.core.tick(t, dt); }
      else if (AuthBg) AuthBg.tick(t, dt);
    } catch (e) { console.error(e); }
    requestAnimationFrame(loop);
  }
  syncSwitches();
  requestAnimationFrame(loop);
  window.addEventListener('resize', drawSparks);
  boot();
})();
