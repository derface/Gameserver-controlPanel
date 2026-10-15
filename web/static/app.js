/* Bytewise Panel Web-Oberfläche. Kein Build-Schritt: reines JavaScript + three.js (r128). */
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
    user: '', role: '', twoFactor: false, update: null, version: '', settings: null,
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

  /* ============ 3D: Server-Racks ============ */
  // Every server is a rack cabinet in a dark room. Height follows the RAM it
  // gets, the LEDs show the state: cyan = running (more lit units = more load),
  // orange chasing upwards = starting/installing, red blinking = error,
  // dark with one standby light = stopped.
  const L = { blocks: new Map(), theta: 0, phi: 1.32, r: 14, tr: 14, base: 14, hover: null, drag: false, w: 1, h: 1, span: 0, user: 0 };
  const RW = 1.5, RD = 1.3, UNIT = .26, GAP = 1.05, PER_ROW = 6;
  const LED = { run: 0x37e8f0, dim: 0x0d3a3e, work: 0xff7a2f, err: 0xff4b3e, off: 0x15171a, standby: 0x5a3a12 };
  function initLand() {
    const cv = $('#land');
    L.renderer = new THREE.WebGLRenderer({ canvas: cv, antialias: true, alpha: true });
    L.renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
    L.scene = new THREE.Scene();
    L.cam = new THREE.PerspectiveCamera(32, 1, .1, 200);
    L.target = new THREE.Vector3(0, 1.4, 0);
    L.amb = new THREE.AmbientLight(0xffffff, .35); L.scene.add(L.amb);
    L.key = new THREE.DirectionalLight(0xffffff, .75); L.key.position.set(-8, 10, 7); L.scene.add(L.key);
    L.glow = new THREE.PointLight(0x37e8f0, .5, 6); L.glow.position.set(0, 3.4, -.4); L.scene.add(L.glow);
    L.fill = new THREE.DirectionalLight(0xdfe8ff, .35); L.fill.position.set(4, 3, 10); L.scene.add(L.fill);
    L.world = new THREE.Group(); L.scene.add(L.world);
    // room: back wall with a diagonal light wedge, glossy floor
    L.wallTex = new THREE.CanvasTexture(document.createElement('canvas'));
    L.wall = new THREE.Mesh(new THREE.PlaneGeometry(60, 22), new THREE.MeshBasicMaterial({ map: L.wallTex }));
    L.wall.position.set(0, 9, -6); L.scene.add(L.wall);
    L.floorMat = new THREE.MeshStandardMaterial({ roughness: .55, metalness: .25 });
    L.floor = new THREE.Mesh(new THREE.PlaneGeometry(60, 30), L.floorMat);
    L.floor.rotation.x = -Math.PI / 2; L.floor.position.z = 6; L.scene.add(L.floor);
    L.poolTex = poolTexture();
    L.bodyMat = new THREE.MeshStandardMaterial({ roughness: .5, metalness: .55 });
    L.plateMat = new THREE.MeshStandardMaterial({ roughness: .6, metalness: .4 });
    L.ventMat = new THREE.MeshBasicMaterial();
    L.plateGeo = new THREE.BoxGeometry(RW - .22, UNIT - .045, .05);
    L.ledGeo = new THREE.BoxGeometry(.09, UNIT - .11, .02);
    L.actGeo = new THREE.BoxGeometry(.035, .035, .02);
    L.ventGeo = new THREE.BoxGeometry(.5, .012, .01);
    L.ray = new THREE.Raycaster(); L.ndc = new THREE.Vector2(); L.v = new THREE.Vector3();
    bindLandInput(cv);
    new ResizeObserver(sizeLand).observe($('#stage')); sizeLand();
    applySceneColors();
  }
  function poolTexture() {
    const c = document.createElement('canvas'); c.width = c.height = 128;
    const g = c.getContext('2d'), gr = g.createRadialGradient(64, 64, 4, 64, 64, 64);
    gr.addColorStop(0, 'rgba(255,255,255,.9)'); gr.addColorStop(.45, 'rgba(255,255,255,.25)'); gr.addColorStop(1, 'rgba(255,255,255,0)');
    g.fillStyle = gr; g.fillRect(0, 0, 128, 128);
    return new THREE.CanvasTexture(c);
  }
  function paintWall() {
    const c = L.wallTex.image; c.width = 1024; c.height = 384;
    const g = c.getContext('2d'), dark = isDark();
    const base = dark ? '#232528' : '#cfcbc3', lit = dark ? '#4b4d51' : '#f1ede6';
    g.fillStyle = base; g.fillRect(0, 0, c.width, c.height);
    // the wedge of light falling in from the left, like light through a door
    const gr = g.createLinearGradient(0, 0, c.width * .55, c.height);
    gr.addColorStop(0, lit); gr.addColorStop(1, base);
    g.fillStyle = gr; g.beginPath(); g.moveTo(0, 0); g.lineTo(c.width * .62, 0); g.lineTo(c.width * .2, c.height); g.lineTo(0, c.height); g.closePath(); g.fill();
    L.wallTex.needsUpdate = true;
  }
  function sizeLand() {
    const r = $('#stage').getBoundingClientRect(); L.w = r.width; L.h = r.height;
    if (!L.renderer || !L.w) return;
    L.renderer.setSize(L.w, L.h, false); L.cam.aspect = L.w / L.h; L.cam.updateProjectionMatrix();
    fitBase(); if (!S.sel) L.tr = L.base;
  }
  function fitBase() {
    // frame the tallest rack plus its name tag, centred in the view
    const top = (L.maxH || 3) + .75;
    L.target.y = top / 2 - .2;
    const span = Math.max(L.span, 6), fov = L.cam ? L.cam.fov * Math.PI / 180 : .55, aspect = L.w / Math.max(1, L.h);
    const byW = (span / 2 + 1.7) / (Math.tan(fov / 2) * aspect), byH = (top / 2 + .9) / Math.tan(fov / 2);
    L.base = Math.max(byW, byH);
  }
  const rackUnits = s => clamp(Math.round(5 + (s.memory_mb || 4096) / 1024), 6, 16);
  function layout(n) {
    const rows = Math.max(1, Math.ceil(n / PER_ROW)), cols = Math.min(PER_ROW, Math.max(1, Math.ceil(n / rows)));
    return { cols, rows, pos: i => [((i % cols) - (cols - 1) / 2) * (RW + GAP), -Math.floor(i / cols) * 3] };
  }
  function syncLand() {
    if (!L.renderer) return;
    const lay = layout(S.servers.length);
    const span = lay.cols * (RW + GAP);
    const maxH = Math.max(0, ...S.servers.map(s => rackUnits(s) * UNIT + .34));
    if (span !== L.span || maxH !== L.maxH) { L.span = span; L.maxH = maxH; fitBase(); if (!S.sel) L.tr = L.base; }
    L.target.z = -(lay.rows - 1) * 1.5;
    const ids = new Set(S.servers.map(s => s.id));
    L.blocks.forEach((b, id) => { if (!ids.has(id)) { L.world.remove(b.g); L.scene.remove(b.pool); b.lab.remove(); L.blocks.delete(id); } });
    S.servers.forEach((s, i) => {
      let b = L.blocks.get(s.id);
      if (b && b.units.length !== rackUnits(s)) { L.world.remove(b.g); L.scene.remove(b.pool); b.lab.remove(); L.blocks.delete(s.id); b = null; }
      if (!b) b = addRack(s);
      b.s = s; const [x, z] = lay.pos(i); b.tx = x; b.tz = z;
      updateLabel(b);
    });
    $('#stageEmpty').hidden = S.servers.length > 0;
  }
  function addRack(s) {
    const n = rackUnits(s), H = n * UNIT + .34;
    const g = new THREE.Group();
    const body = new THREE.Mesh(new THREE.BoxGeometry(RW, H, RD), L.bodyMat);
    body.position.y = H / 2; body.userData.id = s.id;
    const outline = new THREE.LineSegments(new THREE.EdgesGeometry(body.geometry), new THREE.LineBasicMaterial({ transparent: true, opacity: 0 }));
    outline.position.copy(body.position);
    // little feet and a vented top panel
    const top = new THREE.Mesh(new THREE.BoxGeometry(RW - .1, .02, RD - .1), L.plateMat); top.position.y = H + .01;
    g.add(body, outline, top);
    for (let k = 0; k < 4; k++) {
      const v = new THREE.Mesh(L.ventGeo, L.ventMat); v.position.set(-.35, H - .08 - k * .035, RD / 2 + .006); g.add(v);
    }
    const units = [];
    for (let u = 0; u < n; u++) {
      const y = .17 + u * UNIT + UNIT / 2;
      const plate = new THREE.Mesh(L.plateGeo, L.plateMat); plate.position.set(0, y, RD / 2 + .02);
      const led = new THREE.Mesh(L.ledGeo, new THREE.MeshBasicMaterial({ color: LED.off })); led.position.set(RW / 2 - .23, y, RD / 2 + .055);
      const act = new THREE.Mesh(L.actGeo, new THREE.MeshBasicMaterial({ color: LED.off })); act.position.set(-RW / 2 + .22, y, RD / 2 + .055);
      g.add(plate, led, act);
      units.push({ led, act, ph: Math.random() * 10, sp: 3 + Math.random() * 5 });
    }
    L.world.add(g);
    const pool = new THREE.Mesh(new THREE.PlaneGeometry(RW * 2.2, RD * 3), new THREE.MeshBasicMaterial({ map: L.poolTex, transparent: true, opacity: 0, depthWrite: false, blending: THREE.AdditiveBlending, color: LED.run }));
    pool.rotation.x = -Math.PI / 2; pool.position.y = .005; L.scene.add(pool);
    const lab = document.createElement('div'); lab.className = 'lab'; $('#labels').appendChild(lab);
    const b = { s, g, body, outline, units, pool, lab, H, tx: 0, tz: 0, lit: 0 };
    L.blocks.set(s.id, b);
    return b;
  }
  function updateLabel(b) {
    const html = `<span class="dot" style="background:var(${stInfo(b.s).t})"></span><span class="nm">${esc(b.s.name)}</span>`;
    if (b.lab._h !== html) { b.lab.innerHTML = html; b.lab._h = html; }
  }
  function applySceneColors() {
    if (L.renderer) {
      const dark = isDark();
      L.bodyMat.color.set(dark ? 0x4a4f58 : 0x555a63); L.plateMat.color.set(dark ? 0x2b2f36 : 0x30343b);
      L.ventMat.color.set(dark ? 0x17191c : 0x24272c);
      L.floorMat.color.set(dark ? 0x141516 : 0x8f8b84);
      L.amb.intensity = dark ? .35 : .55;
      L.blocks.forEach(b => b.outline.material.color.set(tok('--accent')));
      paintWall();
    }
    if (KI.core) KI.core.set(KI.core.state);
    if (AuthBg) AuthBg.set('idle');
    drawSparks();
  }
  function pick(e) {
    const r = $('#land').getBoundingClientRect();
    L.ndc.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
    L.ray.setFromCamera(L.ndc, L.cam);
    const hits = L.ray.intersectObjects([...L.blocks.values()].map(b => b.body));
    return hits.length ? hits[0].object.userData.id : null;
  }
  function bindLandInput(cv) {
    let down = null;
    cv.addEventListener('pointerdown', e => { down = { x: e.clientX, y: e.clientY, moved: false }; cv.setPointerCapture(e.pointerId); L.drag = true; });
    cv.addEventListener('pointermove', e => {
      if (down) {
        const dx = e.clientX - down.x, dy = e.clientY - down.y;
        if (Math.abs(dx) + Math.abs(dy) > 2) { down.moved = true; cv.style.cursor = 'grabbing'; }
        L.user = clamp(L.user - dx * .006, -.75, .75); L.phi = clamp(L.phi - dy * .004, 1.05, 1.5); down.x = e.clientX; down.y = e.clientY; return;
      }
      L.hover = pick(e); cv.style.cursor = L.hover ? 'pointer' : 'grab';
    });
    cv.addEventListener('pointerup', e => {
      if (down && !down.moved) { const id = pick(e); if (id) select(id); }
      down = null; L.drag = false; cv.style.cursor = 'grab';
    });
    cv.addEventListener('pointercancel', () => { down = null; L.drag = false; });
    cv.addEventListener('pointerleave', () => { if (!down) L.hover = null; });
    cv.addEventListener('wheel', e => { if (!(e.ctrlKey || e.metaKey)) return; e.preventDefault(); L.tr = clamp(L.tr + e.deltaY * .03, 5, 60); }, { passive: false });
  }
  // how many units light up and in which colour
  function ledState(s, u, n, t, unit) {
    const blink = motion ? Math.sin(t * 7) > 0 : true;
    switch (s.status) {
      case 'running': {
        const lit = Math.max(1, Math.ceil(n * (.3 + .7 * load(s))));
        return u < lit ? LED.run : LED.dim;
      }
      case 'starting': {
        const head = motion ? Math.floor(t * 9) % (n + 3) : n;
        return u <= head && u > head - 3 ? LED.work : LED.off;
      }
      case 'installing': case 'preparing': {
        const done = Math.floor(n * (s.progress || 0));
        if (u < done) return LED.work;
        return u === done && blink ? LED.work : LED.off;
      }
      case 'error': return blink ? LED.err : LED.off;
      default: return u === 0 ? LED.standby : LED.off;
    }
  }
  function tickLand(t, dt) {
    if (!L.renderer || R.page !== 'home') return;
    const k = motion ? 1 - Math.exp(-dt * 3) : 1;
    // a slow, gentle sway instead of spinning around
    L.theta = L.user + (motion && !L.drag ? Math.sin(t * .12) * .16 : 0);
    L.r += (L.tr - L.r) * k;
    const tg = L.target, r = L.r;
    L.cam.position.set(tg.x + r * Math.sin(L.phi) * Math.sin(L.theta), tg.y + r * Math.cos(L.phi) + .6, tg.z + r * Math.sin(L.phi) * Math.cos(L.theta));
    L.cam.lookAt(tg);
    const hk = motion ? 1 - Math.exp(-dt * 2.2) : 1;
    let busy = false, running = 0;
    L.blocks.forEach(b => {
      const s = b.s, n = b.units.length;
      b.g.position.x += (b.tx - b.g.position.x) * hk; b.g.position.z += (b.tz - b.g.position.z) * hk;
      b.pool.position.x = b.g.position.x; b.pool.position.z = b.g.position.z + RD;
      if (['preparing', 'installing', 'starting'].includes(s.status)) busy = true;
      if (s.status === 'running') running++;
      const l = load(s);
      b.units.forEach((x, u) => {
        const c = ledState(s, u, n, t, x);
        x.led.material.color.setHex(c);
        // activity LEDs flicker faster with more load
        let a = LED.off;
        if (s.status === 'running') a = (!motion || Math.sin(t * x.sp * (1 + l * 3) + x.ph) > .2) ? (u % 3 ? LED.run : 0x7dff9a) : LED.dim;
        else if (c !== LED.off && c !== LED.standby) a = c;
        x.act.material.color.setHex(a);
      });
      const pool = s.status === 'running' ? .22 + l * .25 : s.status === 'error' ? .18 : ['preparing', 'installing', 'starting'].includes(s.status) ? .14 : 0;
      b.pool.material.opacity += (pool - b.pool.material.opacity) * .06;
      b.pool.material.color.setHex(s.status === 'error' ? LED.err : s.status === 'running' ? LED.run : LED.work);
      const hot = L.hover === s.id || S.sel === s.id;
      b.outline.material.opacity += ((hot ? .9 : 0) - b.outline.material.opacity) * .2;
      b.g.getWorldPosition(L.v); L.v.y += b.H + .35; L.v.project(L.cam);
      const x = (L.v.x * .5 + .5) * L.w, y = (-L.v.y * .5 + .5) * L.h;
      b.lab.style.transform = `translate(${x.toFixed(1)}px,${y.toFixed(1)}px) translate(-50%,-100%)`;
      b.lab.style.opacity = L.v.z > 1 ? 0 : 1;
      b.lab.classList.toggle('hot', hot);
    });
    L.glow.color.setHex(running ? LED.run : LED.work);
    L.glow.intensity = (running ? .3 : .05) + (busy ? (motion ? .12 + .08 * Math.sin(t * 6) : .15) : 0) + (KI.core && KI.core.state === 'executing' ? .2 : 0);
    L.renderer.render(L.scene, L.cam);
  }
  function select(id, tab) { if (byId(id)) go('#/server/' + encodeURIComponent(id) + (tab && tab !== 'console' ? '/' + tab : '')); }
  $('#zIn').onclick = () => { L.tr = clamp(L.tr - 2, 5, 60); };
  $('#zOut').onclick = () => { L.tr = clamp(L.tr + 2, 5, 60); };

  /* ---------------- Server-Seite: Kopf + Infos ---------------- */
  function renderServerPage() {
    const s = byId(R.id);
    if (!s) { if (S.loaded) { toast('Diesen Server gibt es nicht (mehr).', 'bad'); go('#/server'); } return; }
    const on = isOn(s), busy = !!S.pending[s.id] || s.status === 'preparing';
    const set = (el, html) => { if (el._h !== html) { el.innerHTML = html; el._h = html; } };
    $('#svGame').textContent = s.template_name + (s.category ? ' · ' + s.category : '');
    $('#svName').textContent = s.name;
    const port = s.join_port || (s.ports && s.ports[0] ? s.ports[0].host : '');
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
    $('#tabMods').hidden = false;
    // settings that wait for the first start reload once the status moves on
    if (MG.tab === 'settings' && GS.data && GS.status !== s.status && GS.data.files.some(f => !f.editable) && !document.activeElement.closest('#stForm')) loadSettings();
    set($('#svInfo'), `<h2>Details</h2><dl class="kv one">
        <div><dt>Adresse für Spieler</dt><dd>${port ? esc(addr()) + ':' + port : '–'}${s.join_hint ? `<span class="small muted join-hint">${esc(s.join_hint)}</span>` : ''}${S.lanIP && port && S.lanIP !== addr() ? `<span class="small muted join-hint">Aus dem Heimnetz: ${esc(S.lanIP)}:${port}</span>` : ''}</dd></div>
        <div><dt>Ports</dt><dd>${esc(portList(s)) || '–'}</dd></div>
        <div><dt>Arbeitsspeicher</dt><dd>${s.status === 'running' ? gb(s.mem_mb) + ' von ' : ''}${gb(s.memory_mb)} GB</dd></div>
        <div><dt>CPU</dt><dd>${s.status === 'running' ? Math.round(s.cpu) + ' %' : '–'}</dd></div>
        <div><dt>Spieler</dt><dd>${hasPl(s) ? s.players + ' von ' + s.max_players : '–'}</dd></div>
        <div><dt>Angelegt</dt><dd>${new Date(s.created).toLocaleDateString('de-DE')}${s.created_by === 'ai' ? ' mit KI' : ''}</dd></div>
        <div><dt>ID</dt><dd class="mono">${esc(s.id)}</dd></div>
        ${routerRow(s)}
      </dl><div class="sv-checks">${can('operator') ? '<button type="button" class="btn-line" data-diag>Mit KI prüfen</button>' : ''}${can('operator') && port ? `<button type="button" class="btn-line" data-reach>Von außen erreichbar? Prüfen</button>` : ''}</div>${s.notes ? `<p class="note">${esc(s.notes)}</p>` : ''}`);
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
      input = `<select id="${id}" data-var="${esc(v.key)}">${(v.options || []).map((o, i) => `<option value="${esc(o)}" ${o === val ? 'selected' : ''}>${esc((v.labels && v.labels[i]) || o)}</option>`).join('')}</select>`;
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

  /* ---------------- Übernahme aus AMP ---------------- */
  let ampPoll = null;
  async function openAmp() {
    if ($('#dlgCreate').open) $('#dlgCreate').close();
    $('#ampCmd').textContent = '…';
    $('#dlgAmp').showModal();
    try {
      const t = await api('POST', '/api/import/amp/token');
      const o = location.origin;
      $('#ampCmd').textContent = `curl -fsSk ${o}/import/amp.py | python3 - --panel ${o} --token ${t.token}`;
    } catch (err) { $('#ampCmd').textContent = err.message; }
    loadAmp();
    clearInterval(ampPoll); ampPoll = setInterval(() => { if ($('#dlgAmp').open) loadAmp(); else clearInterval(ampPoll); }, 3000);
  }
  async function loadAmp() {
    let list;
    try { list = await api('GET', '/api/import/amp'); } catch (err) { return; }
    const box = $('#ampList');
    const have = new Set([...box.querySelectorAll('[data-amp]')].map(e => e.dataset.amp));
    if (list.length === have.size && list.every(x => have.has(x.id))) return; // keep typed names
    box.innerHTML = list.length ? list.map(x => `<div class="amp-item" data-amp="${esc(x.id)}">
        <div class="amp-top"><b>${esc(x.game || 'Unbekanntes Spiel')}</b> <span class="muted small">AMP-Instanz ${esc(x.instance)} · ${x.files} Dateien · ${fmtSize(x.bytes)} · ${x.settings} Einstellungen</span></div>
        ${x.problem ? `<p class="err">${esc(x.problem)}</p>` : `<p class="small">Wird zu: <b>${esc(x.template_name)}</b></p>
        <label class="field"><span>Name im Panel</span><input data-amp-name maxlength="40" value="${esc(x.name)}"></label>`}
        <div class="dlg-actions"><button type="button" class="btn-line" data-amp-del>Verwerfen</button>${x.problem ? '' : '<button type="button" class="btn-accent" data-amp-go>Übernehmen</button>'}</div>
      </div>`).join('') : '<p class="muted small">Noch nichts empfangen. Sobald der Befehl auf dem AMP-Rechner läuft, erscheinen die Instanzen hier.</p>';
  }
  $('#cAmp').addEventListener('click', openAmp);
  $('#ampCopy').addEventListener('click', async () => {
    try { await navigator.clipboard.writeText($('#ampCmd').textContent); toast('Befehl kopiert', 'ok'); }
    catch (e) { const r = document.createRange(); r.selectNodeContents($('#ampCmd')); getSelection().removeAllRanges(); getSelection().addRange(r); toast('Bitte mit Strg+C kopieren'); }
  });
  $('#ampList').addEventListener('click', async e => {
    const item = e.target.closest('[data-amp]'); if (!item) return;
    const id = item.dataset.amp;
    if (e.target.closest('[data-amp-del]')) {
      if (!await confirmBox('Export verwerfen?', 'Die übertragenen Spielstände werden im Panel gelöscht. In AMP bleibt alles erhalten.', 'Verwerfen')) return;
      try { await api('DELETE', '/api/import/amp/' + id); item.remove(); loadAmp(); } catch (err) { toast(err.message, 'bad'); }
    } else if (e.target.closest('[data-amp-go]')) {
      const btn = e.target.closest('[data-amp-go]'); btn.disabled = true;
      try {
        const s = await api('POST', `/api/import/amp/${id}/create`, { name: item.querySelector('[data-amp-name]').value });
        toast(s.name + ' wird übernommen', 'ok'); item.remove();
        await loadServers();
        if (!$('#ampList [data-amp]')) { $('#dlgAmp').close(); select(s.id); } else loadAmp();
      } catch (err) { toast(err.message, 'bad'); btn.disabled = false; }
    }
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

  /* ---------------- Einstellungen ---------------- */
  function openSettings(sec) { go('#/einstellungen' + (typeof sec === 'string' && sec ? '/' + sec : '')); }
  const SEC = ['konto', 'https', 'router', 'ki', 'benachrichtigungen', 'statusseite', 'discord', 'backups', 'steam', 'panel'];
  async function enterSettings(sec) {
    if (!can('admin') || !SEC.includes(sec)) sec = 'konto';
    $$('#pageSettings [data-sec]').forEach(el => {
      if (el.tagName === 'A') el.setAttribute('aria-current', el.dataset.sec === sec ? 'page' : 'false');
      else el.hidden = el.dataset.sec !== sec;
    });
    $('#verLine').textContent = 'Version ' + S.version;
    clearTimeout(stmTimer);
    if (sec === 'konto') return loadMe();
    if (sec === 'https') { loadProxy(); return loadTLS(); }
    if (sec === 'router') return loadRouter(false);
    if (sec === 'steam') { $('#stmPass').value = ''; $('#stmCode').value = ''; api('GET', '/api/settings').then(r => { $('#stmApiKey').value = r.settings.steam_api_key || ''; }).catch(() => {}); return loadSteam(); }
    if (sec === 'panel') return loadPanelUpdate(false);
    if (sec === 'benachrichtigungen') return loadNotify();
    if (sec === 'statusseite') return loadStatusPage();
    if (sec === 'discord') return loadDiscord();
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
    $('#sAuto').checked = !!s.ai_auto_diagnose;
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
    return { ai_provider: $('#sProv').value, anthropic_key: $('#sAnt').value, openai_key: $('#sOai').value, ollama_url: $('#sOll').value, ai_model: $('#sModel').value, public_host: $('#sHost').value, ai_auto_diagnose: $('#sAuto').checked };
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
  async function loadStatusPage() {
    showErr($('#spErr'));
    let r; try { r = await api('GET', '/api/status-page'); } catch (e) { showErr($('#spErr'), e.message); return; }
    const c = r.config || {};
    $('#spOn').checked = !!c.enabled; $('#spTitle').value = c.title || ''; $('#spNote').value = c.note || ''; $('#spHost').value = c.host || ''; $('#spNames').checked = !!c.show_names;
    $('#spLink').href = c.host ? 'https://' + c.host + '/' : '/status'; $('#spLink').textContent = c.host ? c.host : location.host + '/status';
    $('#spRows').innerHTML = r.servers.length ? r.servers.map(x => `<tr data-id="${esc(x.id)}"><td><input type="checkbox" class="sp-show" ${x.show ? 'checked' : ''} aria-label="${esc(x.name)} zeigen"></td>
      <td><b>${esc(x.name)}</b><br><span class="small muted">${esc(x.game)}</span></td><td><input class="sp-note" maxlength="200" value="${esc(x.note || '')}" placeholder="z. B. Passwort im Discord"></td></tr>`).join('')
      : '<tr class="empty"><td colspan="3">Noch keine Server.</td></tr>';
  }
  $('#spForm').addEventListener('submit', async e => {
    e.preventDefault(); showErr($('#spErr'));
    const servers = {};
    $$('#spRows tr[data-id]').forEach(tr => { servers[tr.dataset.id] = { show: $('.sp-show', tr).checked, note: $('.sp-note', tr).value }; });
    try {
      await api('PUT', '/api/status-page', { enabled: $('#spOn').checked, title: $('#spTitle').value, note: $('#spNote').value, host: $('#spHost').value, show_names: $('#spNames').checked, servers });
      toast('Statusseite gespeichert', 'ok'); loadStatusPage();
    } catch (err) { showErr($('#spErr'), err.message); }
  });
  async function loadDiscord() {
    showErr($('#dcErr'));
    let r; try { r = await api('GET', '/api/discord'); } catch (e) { showErr($('#dcErr'), e.message); return; }
    const c = r.config || {};
    $('#dcApp').value = c.app_id || ''; $('#dcKey').value = c.public_key || ''; $('#dcToken').value = c.token || '';
    $('#dcGuild').value = c.guild_id || ''; $('#dcChannel').value = c.channel || '';
    $('#dcRoles').value = (c.roles || []).join(', '); $('#dcUsers').value = (c.users || []).join(', ');
    $('#dcEndpoint').textContent = r.endpoint;
    $('#dcServers').innerHTML = r.servers.length ? r.servers.map(x => `<label class="check"><input type="checkbox" value="${esc(x.id)}" ${x.allowed ? 'checked' : ''}> ${esc(x.name)} <span class="muted small">${esc(x.game)}</span></label>`).join('')
      : '<p class="small muted">Noch keine Server.</p>';
    const st = r.state || {}, ok = st.last_update && !String(st.last_update).startsWith('0001');
    $('#dcState').innerHTML = st.last_error ? `<span class="bad">Statusnachricht: ${esc(st.last_error)}</span>`
      : ok ? 'Statusnachricht zuletzt aktualisiert: ' + esc(fmtDate(st.last_update)) : c.channel ? '' : '<span class="muted">Ohne Kanal gibt es keine Statusnachricht, die Befehle gehen trotzdem.</span>';
  }
  const idList = v => v.split(/[\s,;]+/).map(x => x.trim()).filter(Boolean);
  $('#dcForm').addEventListener('submit', async e => {
    e.preventDefault(); showErr($('#dcErr'));
    try {
      const r = await api('PUT', '/api/discord', { app_id: $('#dcApp').value, public_key: $('#dcKey').value, token: $('#dcToken').value, guild_id: $('#dcGuild').value, channel: $('#dcChannel').value,
        roles: idList($('#dcRoles').value), users: idList($('#dcUsers').value), servers: $$('#dcServers input:checked').map(x => x.value) });
      toast(r.note || 'Discord-Bot gespeichert', /aber/.test(r.note || '') ? 'bad' : 'ok'); setTimeout(loadDiscord, 1500);
    } catch (err) { showErr($('#dcErr'), err.message); }
  });
  $('#dcTest').addEventListener('click', async () => {
    const b = $('#dcTest'); b.disabled = true; showErr($('#dcErr'));
    try { await api('POST', '/api/discord/test'); toast('Statusnachricht ist in Discord', 'ok'); loadDiscord(); } catch (err) { showErr($('#dcErr'), err.message); }
    b.disabled = false;
  });
  async function loadNotify() {
    showErr($('#ntErr'));
    let r; try { r = await api('GET', '/api/notify'); } catch (e) { showErr($('#ntErr'), e.message); return; }
    const c = r.config || {}, on = c.kinds || r.defaults;
    $('#ntDiscord').value = c.discord || ''; $('#ntNtfy').value = c.ntfy_url || ''; $('#ntToken').value = c.ntfy_token || '';
    $('#ntKinds').innerHTML = r.kinds.map(k => `<label class="check"><input type="checkbox" value="${esc(k.Key)}" ${on.includes(k.Key) ? 'checked' : ''}> ${esc(k.Label)}</label>`).join('');
  }
  $('#ntForm').addEventListener('submit', async e => {
    e.preventDefault(); showErr($('#ntErr'));
    try {
      await api('PUT', '/api/notify', { discord: $('#ntDiscord').value, ntfy_url: $('#ntNtfy').value, ntfy_token: $('#ntToken').value, kinds: $$('#ntKinds input:checked').map(x => x.value) });
      toast('Benachrichtigungen gespeichert', 'ok'); loadNotify();
    } catch (err) { showErr($('#ntErr'), err.message); }
  });
  $('#ntTest').addEventListener('click', async () => {
    const b = $('#ntTest'); b.disabled = true; showErr($('#ntErr'));
    try { await api('POST', '/api/notify/test'); toast('Testnachricht verschickt', 'ok'); } catch (err) { showErr($('#ntErr'), err.message); }
    b.disabled = false;
  });
  async function loadPanelUpdate(refresh) {
    showErr($('#upErr'));
    $('#upStatus').textContent = 'Frage GitHub …'; $('#upGo').hidden = true; $('#upNotes').hidden = true;
    let r; try { r = await api('GET', '/api/panel/update' + (refresh ? '?refresh=1' : '')); } catch (e) { $('#upStatus').textContent = ''; showErr($('#upErr'), e.message); return; }
    const u = r.info; S.update = { available: u.available, latest: u.latest }; renderVersion();
    if (u.source) $('#upSource').href = u.source;
    let txt = `Installiert: <b>${esc(u.current)}</b>`;
    if (u.error) { $('#upStatus').innerHTML = txt; showErr($('#upErr'), u.error); return; }
    if (u.available) txt += ` · <b class="on-ok">Neu: ${esc(u.latest)}</b>` + (u.published && !u.published.startsWith('0001') ? ` vom ${new Date(u.published).toLocaleDateString('de-DE')}` : '');
    else if (u.dev_build) txt += ` · neueste Veröffentlichung: ${esc(u.latest)} (du nutzt einen eigenen Entwicklungsstand)`;
    else txt += ' · das ist die neueste Version';
    txt += `<br><span class="muted small">Zuletzt geprüft ${new Date(u.checked).toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' })}${u.url ? ` · <a href="${esc(u.url)}" target="_blank" rel="noopener">Release ansehen</a>` : ''}</span>`;
    $('#upStatus').innerHTML = txt;
    if (u.available && u.notes) { $('#upNotes').hidden = false; $('#upNotes').textContent = u.notes; }
    $('#upGo').hidden = !(u.available || u.dev_build); $('#upGo').disabled = !!r.busy;
    $('#upGo').textContent = u.available ? 'Auf ' + u.latest + ' aktualisieren' : u.latest + ' installieren';
  }
  $('#upCheck').addEventListener('click', () => loadPanelUpdate(true));
  $('#upGo').addEventListener('click', async () => {
    if (!await confirmBox('Panel aktualisieren?', 'Das Panel lädt die neue Version von GitHub, prüft die Prüfsumme und startet neu. Deine Spieleserver laufen dabei weiter, die Seite ist ein paar Sekunden weg.', 'Aktualisieren')) return;
    const b = $('#upGo'); b.disabled = true; b.textContent = 'Lädt …'; showErr($('#upErr'));
    try {
      const r = await api('POST', '/api/panel/update');
      toast(r.message, 'ok'); b.textContent = 'Startet neu …';
      const was = S.version;
      for (let i = 0; i < 60; i++) {
        await new Promise(res => setTimeout(res, 2000));
        try { const st = await api('GET', '/api/state'); if (st.version && st.version !== was) { location.reload(); return; } } catch (e) { /* Panel startet noch */ }
      }
      location.reload();
    } catch (err) { showErr($('#upErr'), err.message); b.disabled = false; b.textContent = 'Erneut versuchen'; }
  });
  function renderVersion() {
    const u = S.update;
    $('#edChip').textContent = 'v' + String(S.version || '?').replace(/^v/, '') + (u && u.available ? ' · Update da' : '');
    $('#edChip').classList.toggle('upd', !!(u && u.available));
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
    tfCodesText = 'Bytewise Panel – Wiederherstellungscodes für ' + S.user + ' (' + location.host + ')\n' + codes.join('\n') + '\n';
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
  async function loadProxy() {
    let r;
    try { r = await api('GET', '/api/proxy'); } catch (e) { return; }
    renderProxy(r); $('#pxList').value = r.trusted.join(', ');
  }
  function renderProxy(r) {
    const p = [];
    if (r.via_proxy) p.push(`<span class="on-ok">Du bist gerade über den Proxy hier.</span> Das Panel sieht dich als <span class="mono">${esc(r.client)}</span>.`);
    else if (r.trusted.length) p.push(`Du bist gerade direkt verbunden (<span class="mono">${esc(r.client)}</span>).`);
    if (r.seen) p.push(`Von <span class="mono">${esc(r.seen)}</span> kamen Anfragen mit Proxy-Angaben. Ist das dein Reverse Proxy, trag die Adresse ein.`);
    $('#pxStatus').innerHTML = p.join('<br>');
    const b = $('#pxUse'); b.hidden = !r.seen; b.dataset.ip = r.seen || ''; b.textContent = r.seen ? r.seen + ' übernehmen' : '';
  }
  $('#pxUse').addEventListener('click', () => { const v = $('#pxList').value.trim(), ip = $('#pxUse').dataset.ip; $('#pxList').value = v ? v + ', ' + ip : ip; });
  $('#pxForm').addEventListener('submit', async e => {
    e.preventDefault(); showErr($('#pxErr'));
    try { const r = await api('PUT', '/api/proxy', { trusted: [$('#pxList').value] }); renderProxy(r); $('#pxList').value = r.trusted.join(', '); toast('Gespeichert', 'ok'); }
    catch (err) { showErr($('#pxErr'), err.message); }
  });
  function renderTLS(r) {
    const st = r.status, parts = [];
    parts.push(r.secure ? '<span class="on-ok">Diese Verbindung ist verschlüsselt.</span>' : '<b>Diese Verbindung ist unverschlüsselt.</b>');
    if (st.off) parts.push('<span class="err">Die Umleitung auf HTTPS ist per GP_NO_TLS abgeschaltet.</span>');
    if (st.subject) parts.push(`Zertifikat: <b>${esc(st.issuer && st.issuer !== st.subject ? st.issuer : (st.subject === 'Gamepanel' || st.subject === 'Bytewise Panel') ? 'selbst signiert' : st.subject)}</b>, gültig bis ${esc(new Date(st.not_after).toLocaleDateString('de-DE'))}` +
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
  /* ---------------- Erreichbarkeit von außen ---------------- */
  const RC = { id: null, busy: false };
  const rcIcon = { ok: '✓', warn: '!', fail: '✕', skip: '–' };
  $('#svInfo').addEventListener('click', e => { if (e.target.closest('[data-reach]')) checkReach(R.id); if (e.target.closest('[data-diag]')) openDiag(R.id); });
  $('#rcAgain').onclick = () => checkReach(RC.id);
  async function checkReach(id) {
    if (!id || RC.busy) return;
    RC.id = id; RC.busy = true;
    const d = $('#dlgReach'); if (!d.open) d.showModal();
    $('#rcAgain').disabled = true;
    $('#rcBody').innerHTML = '<p class="rc-wait"><span class="spin" aria-hidden="true"></span> Prüfe den Weg vom Internet zum Server … (bis zu 30 Sekunden)</p>';
    try { renderReach(await api('POST', `/api/servers/${encodeURIComponent(id)}/reach`)); }
    catch (err) { $('#rcBody').innerHTML = `<p class="err">${esc(err.message)}</p>`; }
    finally { RC.busy = false; $('#rcAgain').disabled = false; }
  }
  function renderReach(r) {
    const head = { ok: 'Erreichbar', likely: 'Wahrscheinlich erreichbar', fail: 'Nicht erreichbar' }[r.verdict] || '';
    const row = (st, title, text) => `<li class="rc-${st}"><span class="rc-ic" aria-hidden="true">${rcIcon[st] || '–'}</span><div><b>${esc(title)}</b><span>${esc(text)}</span></div></li>`;
    const ports = r.ports.map(p => `<tr class="${p.internal ? 'rc-int' : ''}"><th scope="row"><span class="mono">${p.port} ${esc(p.proto)}</span>${p.name ? `<br><span class="small muted">${esc(p.name)}</span>` : ''}</th>
      <td class="rc-${p.router}" data-l="Router"><span class="rc-ic" aria-hidden="true">${rcIcon[p.router]}</span> ${esc(p.router_text)}</td>
      <td class="rc-${p.outside}" data-l="Von außen"><span class="rc-ic" aria-hidden="true">${rcIcon[p.outside]}</span> ${esc(p.outside_text)}</td></tr>`).join('');
    $('#rcBody').innerHTML = `<div class="rc-verdict rc-${r.verdict === 'likely' ? 'warn' : r.verdict}">
        <span class="rc-ic big" aria-hidden="true">${rcIcon[r.verdict === 'likely' ? 'ok' : r.verdict]}</span>
        <div><b>${esc(head)}</b><span>${esc(r.summary)}</span>
        ${r.verdict !== 'fail' && r.address ? `<button type="button" class="addr" data-copy="${esc(r.address)}" title="Adresse kopieren">${esc(r.address)}</button>` : ''}</div></div>
      <ul class="rc-steps">${r.steps.map(x => row(x.state, x.title, x.detail)).join('')}</ul>
      ${ports ? `<div class="rc-scroll"><table class="rc-ports"><thead><tr><th scope="col">Port</th><th scope="col">Router</th><th scope="col">Von außen</th></tr></thead><tbody>${ports}</tbody></table></div>` : ''}
      <p class="small muted">Geprüft ${new Date(r.checked).toLocaleTimeString('de-DE')}${r.services && r.services.length ? ' · Für den Test von außen wurden ' + esc(r.services.join(' und ')) + ' gefragt (sie sehen nur deine öffentliche IP und den Port).' : ''}</p>`;
  }
  /* ---------------- KI-Diagnose ---------------- */
  const DG = { id: null, busy: false, list: [] };
  const SEV = { ok: ['ok', '✓', 'Alles in Ordnung'], hinweis: ['warn', 'i', 'Hinweis'], warnung: ['warn', '!', 'Warnung'], kritisch: ['fail', '✕', 'Kritisch'] };
  async function openDiag(id) {
    DG.id = id; const d = $('#dlgDiag'); if (!d.open) d.showModal();
    $('#dgBody').innerHTML = '<p class="rc-wait"><span class="spin" aria-hidden="true"></span> Lade …</p>';
    try { const r = await api('GET', `/api/servers/${encodeURIComponent(id)}/diagnose`); DG.list = r.diagnoses || []; DG.ai = r.ai; }
    catch (err) { $('#dgBody').innerHTML = `<p class="err">${esc(err.message)}</p>`; return; }
    $('#dgRun').hidden = false;
    if (DG.list.length) renderDiag(); else runDiag();
  }
  async function runDiag() {
    if (!DG.id || DG.busy) return;
    DG.busy = true; $('#dgRun').disabled = true;
    $('#dgBody').innerHTML = '<p class="rc-wait"><span class="spin" aria-hidden="true"></span> Die KI liest Konsole, Verlauf und Auslastung … (bis zu einer Minute)</p>';
    try { DG.list.unshift(await api('POST', `/api/servers/${encodeURIComponent(DG.id)}/diagnose`)); renderDiag(); }
    catch (err) {
      const setup = /Kein KI-Anbieter|API-Key/.test(err.message);
      $('#dgBody').innerHTML = `<p class="err">${esc(err.message)}</p>${setup ? '<button type="button" class="btn-line" data-dg-setup>Zu den KI-Einstellungen</button>' : ''}`;
    }
    finally { DG.busy = false; $('#dgRun').disabled = false; }
  }
  function actionBtn(a, i) {
    switch (a.kind) {
      case 'memory': return `<button type="button" class="btn-accent" data-dg-act="${i}">RAM auf ${gb(a.memory_mb)} GB setzen</button>`;
      case 'var': return `<button type="button" class="btn-accent" data-dg-act="${i}">Übernehmen</button>`;
      case 'restart': return `<button type="button" class="btn-line" data-dg-act="${i}">Neu starten</button>`;
      case 'update': return `<button type="button" class="btn-line" data-dg-act="${i}">Aktualisieren</button>`;
    }
    return '';
  }
  function renderDiag() {
    const d = DG.list[0]; if (!d) return;
    const [cls, ic, head] = SEV[d.severity] || SEV.hinweis;
    const when = new Date(d.time).toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'short' });
    const finds = (d.findings || []).map(f => `<li class="rc-${cls === 'ok' ? 'ok' : 'warn'}"><span class="rc-ic" aria-hidden="true">${cls === 'ok' ? '✓' : '!'}</span><div><b>${esc(f.title)}</b><span>${esc(f.detail)}</span>${f.evidence ? `<code class="mono dg-ev">${esc(f.evidence)}</code>` : ''}</div></li>`).join('');
    const acts = (d.actions || []).map((a, i) => `<li><div><b>${esc(a.title)}</b><span>${esc(a.why)}${a.kind === 'var' ? ` <span class="mono">${esc(a.key)} = ${esc(a.value)}</span>` : ''}</span></div>${actionBtn(a, i)}</li>`).join('');
    const older = DG.list.slice(1).map(o => `<li><span class="muted small">${esc(new Date(o.time).toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'short' }))}${o.trigger ? ' · ' + esc(o.trigger) : ''}</span><br>${esc((SEV[o.severity] || SEV.hinweis)[2])}: ${esc(o.summary)}</li>`).join('');
    $('#dgBody').innerHTML = `<div class="rc-verdict rc-${cls}"><span class="rc-ic big" aria-hidden="true">${ic}</span><div><b>${esc(head)}</b><span>${esc(d.summary)}</span></div></div>
      ${finds ? `<ul class="rc-steps">${finds}</ul>` : ''}
      ${acts ? `<h3 class="dg-h">Vorschläge</h3><ul class="dg-acts">${acts}</ul><p class="small muted">Nichts passiert ohne deinen Klick. RAM-Änderungen legen den Container neu an, Spielstände bleiben erhalten.</p>` : ''}
      <p class="small muted">Geprüft ${esc(when)}${d.trigger ? ' · automatisch nach: ' + esc(d.trigger) : ''} · An die KI gingen Fakten, Verlauf und ein Konsolenauszug, ohne Passwörter, IP-Adressen und Steam-IDs.</p>
      ${older ? `<details class="dg-old"><summary>Frühere Prüfungen</summary><ul>${older}</ul></details>` : ''}`;
  }
  $('#dgRun').onclick = runDiag;
  $('#dgBody').addEventListener('click', async e => {
    if (e.target.closest('[data-dg-setup]')) { $('#dlgDiag').close(); openSettings('ki'); return; }
    const b = e.target.closest('[data-dg-act]'); if (!b) return;
    const a = DG.list[0].actions[+b.dataset.dgAct], id = encodeURIComponent(DG.id);
    const s = byId(DG.id);
    const ask = { memory: ['RAM ändern?', `${s ? s.name : 'Der Server'} bekommt ${gb(a.memory_mb)} GB statt ${s ? gb(s.memory_mb) : '?'} GB. Läuft er, wird er dafür kurz neu gestartet.`],
      var: ['Wert ändern?', `${a.key} wird auf „${a.value}“ gesetzt. Läuft der Server, wird er dafür kurz neu gestartet.`],
      restart: ['Neu starten?', 'Spieler werden dabei getrennt.'], update: ['Aktualisieren?', 'Der Server wird gestoppt, aktualisiert und wieder gestartet.'] }[a.kind];
    if (!ask || !await confirmBox(ask[0], ask[1], a.title)) return;
    b.disabled = true;
    try {
      if (a.kind === 'memory') await api('PATCH', `/api/servers/${id}`, { memory_mb: a.memory_mb });
      else if (a.kind === 'var') await api('PATCH', `/api/servers/${id}`, { vars: { [a.key]: a.value } });
      else await api('POST', `/api/servers/${id}/${a.kind}`);
      toast(a.title + ': erledigt', 'ok'); b.textContent = 'Erledigt'; loadServers();
    } catch (err) { toast(err.message, 'bad'); b.disabled = false; }
  });
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
      const det = x.error || x.note || x.settings_note || (x.log && x.log.length);
      const stg = !x.settings ? '<span class="muted">–</span>' : x.settings === 'keine' ? '<span class="muted" title="' + esc(x.settings_note || '') + '">keine</span>'
        : (x.settings === 'ok' ? '<span class="ts-yes">✓</span>' : '<span class="ts-no">✗</span>') + (x.settings_count ? ' <span class="mono small muted">' + x.settings_count + '</span>' : '');
      const main = `<tr class="${det ? 'has-d' : ''}" data-t="${esc(x.template)}"><td>${esc(x.name)}</td><td><span class="ts-res ${esc(x.result)}">${esc(x.result)}</span></td>
        <td>${mk(x.download, true)}</td><td>${mk(x.start, true)}</td><td>${mk(x.ready, x.has_ready)}</td><td>${mk(x.query, x.has_query)}${x.players ? ' <span class="mono small muted">' + esc(x.players) + '</span>' : ''}</td><td>${stg}</td>
        <td class="num">${x.disk_mb ? gb(x.disk_mb) + ' GB' : '–'}</td><td class="num">${fmtDur(x.seconds)}</td></tr>`;
      const d = det && TS.open[x.template] ? `<tr class="ts-detail"><td colspan="9">${x.error ? '<b>' + esc(x.error) + '</b><br>' : ''}${x.note ? esc(x.note) : ''}${x.settings_note ? (x.note ? '<br>' : '') + 'Einstellungen: ' + esc(x.settings_note) : ''}${x.log && x.log.length ? '<pre>' + esc(x.log.join('\n')) + '</pre>' : ''}</td></tr>` : '';
      return main + d;
    }).join('') : '<tr class="empty"><td colspan="9">Noch keine Ergebnisse.</td></tr>';
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
  $('#edChip').onclick = () => openSettings(can('admin') ? 'panel' : 'konto');
  $('#edChip').onkeydown = e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); $('#edChip').click(); } };

  /* ---------------- KI ---------------- */
  const KI = { core: null, busy: false, plan: null, tpl: null, track: null, order: 0, hist: [] };
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
    chat.appendChild(d); while (chat.children.length > 6) chat.firstChild.remove();
    if (cls !== 'bad' && !(who === 'KI' && !KI.hist.length)) { KI.hist.push(who + ': ' + text); if (KI.hist.length > 12) KI.hist.shift(); }
  }
  function renderKI() {
    const body = $('#coreBody'); let lock = $('.locked', body);
    const s = S.settings;
    let msg = '';
    if (s && !s.ai_provider) msg = 'Richte zuerst einen KI-Anbieter ein: Anthropic, OpenAI oder Ollama auf deinem eigenen Rechner.';
    $('#askForm').hidden = !!msg; $('#chips').hidden = !!msg;
    if (msg) {
      if (!lock) { lock = document.createElement('div'); lock.className = 'locked'; body.prepend(lock); }
      lock.innerHTML = `<span>${esc(msg)}</span><button class="btn-line" type="button">Einstellungen öffnen</button>`;
      lock.querySelector('button').onclick = () => openSettings('ki');
    } else if (lock) lock.remove();
    if (!msg && !$('#chat').children.length) addMsg('KI', 'Sag mir, was für einen Server du willst oder was ich an einem vorhandenen ändern soll. Ich mache einen Vorschlag, und du gibst ihn frei.');
    const first = (S.servers || [])[0], chip = $('#chipChange');
    if (first && !chip) $('#chips').insertAdjacentHTML('afterbegin', `<button type="button" id="chipChange">${esc(first.name)}: mehr Spieler und schneller leveln</button>`);
  }
  async function ask(text) {
    text = text.trim(); if (!text || KI.busy) return;
    KI.busy = true; $('#askInput').value = ''; $('#plan').hidden = true;
    const history = KI.hist.slice();
    addMsg('Du', text); setCore('thinking');
    try {
      const r = await api('POST', '/api/ai/plan', { prompt: text, history, tz: -new Date().getTimezoneOffset() });
      if (r.plan.question) { addMsg('KI', r.plan.question); }
      else if (r.plan.action === 'event') { addMsg('KI', r.plan.reply || 'So würde ich das Event planen.'); showChange(r.plan, r.changes || [], r.notes || [], r.server, r.event); }
      else if (r.plan.action === 'change') { addMsg('KI', r.plan.reply || 'Das würde ich ändern.'); showChange(r.plan, r.changes || [], r.notes || [], r.server); }
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
  function showChange(plan, changes, notes, sv, ev) {
    KI.plan = plan; KI.change = { changes, sv, ev }; KI.track = null;
    const p = $('#plan'); p.hidden = false; KI.order++;
    const fmtV = v => v === '' || v == null ? '–' : v;
    const running = sv && (sv.status === 'running' || sv.status === 'starting');
    p.innerHTML = `<div class="plan-head"><span class="mono muted">Änderung Nr. ${String(KI.order).padStart(4, '0')}</span><span class="stamp" id="stamp">Wartet auf Freigabe</span></div>
      <h3>${esc(sv ? sv.name : plan.server)}${ev ? ' · ' + esc(ev.name) : ''}</h3>
      ${ev ? `<p class="small"><b>Zeitraum:</b> ${esc(fmtSpan(ev.start, ev.end))}${ev.weekly ? ', jede Woche' : ''}. Danach stellt das Panel die bisherigen Werte von selbst wieder her.</p>` : ''}
      ${changes.length ? `<table class="tbl chg"><thead><tr><th>Einstellung</th><th>Bisher</th><th>Neu</th></tr></thead><tbody>${changes.map(c =>
        `<tr><td>${esc(c.label)}</td><td class="mono muted">${esc(fmtV(c.old))}</td><td class="mono"><b>${esc(fmtV(c.new))}</b></td></tr>`).join('')}</tbody></table>`
        : '<p class="muted">Die Werte stehen schon so da, es gibt nichts zu ändern.</p>'}
      ${notes.length ? `<p class="small muted">${notes.map(esc).join('<br>')}</p>` : ''}
      ${changes.length && running && !ev ? '<p class="small">Der Server läuft und wird dafür neu gestartet. Spieler werden kurz getrennt.</p>' : ''}
      <div class="plan-actions"><a class="btn-line" href="#/server/${encodeURIComponent(plan.server)}/${ev ? 'tasks' : 'settings'}">${ev ? 'Events ansehen' : 'Einstellungen öffnen'}</a>${changes.length ? `<button class="btn-accent" type="button" id="runChange">${ev ? 'Event anlegen' : 'Übernehmen'}</button>` : ''}</div>`;
    if (changes.length) $('#runChange').onclick = ev ? runEventFromAI : runChange;
  }
  function runEventFromAI() {
    const { changes, ev } = KI.change || {}; if (!changes || !ev) return;
    const vars = {}, files = {};
    for (const c of changes) {
      if (c.where === 'vars') vars[c.key] = c.new;
      else if (c.where !== 'memory') (files[c.where] = files[c.where] || {})[c.key] = c.new;
    }
    const list = changes.filter(c => c.where !== 'memory').map(c => `<li><b>${esc(c.label)}</b> <span class="muted">${esc(c.old ?? '')}</span> → <span class="mono">${esc(c.new)}</span></li>`).join('');
    const id = KI.plan.server;
    KI.onEvent = () => {
      KI.onEvent = null;
      $('#stamp').textContent = 'Geplant'; $('#stamp').classList.add('ok'); $('#runChange').disabled = true; $('#runChange').textContent = 'Angelegt';
    };
    GE_SERVER = id;
    openEventDialog({ vars, files }, list, ev);
  }
  async function runChange() {
    const { changes, sv } = KI.change || {}; if (!changes || KI.busy) return;
    const id = KI.plan.server, vars = {}, files = {};
    let mem = 0;
    for (const c of changes) {
      if (c.where === 'vars') vars[c.key] = c.new;
      else if (c.where === 'memory') mem = +c.new;
      else (files[c.where] = files[c.where] || {})[c.key] = c.new;
    }
    KI.busy = true; $('#runChange').disabled = true; $('#stamp').textContent = 'Wird übernommen'; setCore('executing');
    try {
      if (Object.keys(vars).length || Object.keys(files).length) await api('PUT', `/api/servers/${id}/settings`, { vars, files });
      if (mem) await api('PATCH', `/api/servers/${id}`, { memory_mb: mem });
      $('#stamp').textContent = 'Erledigt'; $('#stamp').classList.add('ok'); setCore('done'); $('#runChange').textContent = 'Übernommen';
      toast('Einstellungen von ' + (sv ? sv.name : 'dem Server') + ' geändert', 'ok'); loadServers();
      setTimeout(() => { if (!KI.busy) setCore('idle'); }, 2200);
    } catch (e) {
      addMsg('KI', e.message, 'bad'); $('#stamp').textContent = 'Fehler'; $('#stamp').classList.add('bad'); $('#runChange').disabled = false; setCore('idle');
    } finally { KI.busy = false; }
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
    if (!can('operator') && ['settings', 'mods', 'files'].includes(tab)) tab = 'console';
    if (!['console', 'players', 'settings', 'mods', 'backups', 'files', 'tasks', 'events'].includes(tab)) tab = 'console';
    MG.tab = tab;
    $$('#svTabs button').forEach(b => b.setAttribute('aria-selected', String(b.dataset.tab === tab)));
    const panes = { console: 'paneConsole', players: 'panePlayers', backups: 'paneBackups', files: 'paneFiles', tasks: 'paneTasks', events: 'paneEvents', settings: 'paneSettings', mods: 'paneMods' };
    Object.entries(panes).forEach(([k, id]) => { $('#' + id).hidden = k !== tab; });
    clearTimeout(TK.timer);
    if (tab !== 'files') closeEditor(true);
    if (tab === 'console') { if (S.consoleId !== MG.id) openConsole(MG.id); setTimeout(() => { const b = $('#lines'); b.scrollTop = b.scrollHeight; }, 0); }
    else if (tab === 'settings') loadSettings();
    else if (tab === 'mods') {
      const mr = !!(tpl && tpl.mods);
      ['mdTitle', 'mdInfo', 'mdInstalled', 'mdSearchForm', 'mdResults'].forEach(x => { $('#' + x).hidden = !mr; });
      if (!mr) { $('#mdBar').hidden = true; $('#mdMore').hidden = true; }
      $('#wsBox').hidden = !(WS.info[MG.id] && WS.info[MG.id].available);
      loadModList(); if (mr) loadMods(); loadWorkshopInfo();
    }
    else if (tab === 'backups') loadBackups();
    else if (tab === 'players') loadPlayers();
    else if (tab === 'files') loadFiles(MG.path);
    else loadTasks();
  }
  $('#svTabs').addEventListener('click', e => { const b = e.target.closest('button[data-tab]'); if (b) select(MG.id, b.dataset.tab); });

  /* ---------------- Spieler ---------------- */
  const PL = { data: null, timer: null };
  const PL_ACT = { kick: 'Rauswerfen', ban: 'Bannen', unban: 'Entbannen', whitelist_add: 'Whitelist +', whitelist_remove: 'Whitelist −', op: 'Admin machen', deop: 'Admin entziehen' };
  const PL_ONLINE = ['kick', 'ban', 'op'];
  const plDur = s => s >= 3600 ? Math.floor(s / 3600) + ' Std. ' + Math.floor(s % 3600 / 60) + ' Min.' : Math.floor(s / 60) + ' Min.';
  async function loadPlayers() {
    const id = MG.id; clearTimeout(PL.timer);
    let d; try { d = await api('GET', `/api/servers/${id}/players`); } catch (e) { $('#plInfo').textContent = e.message; return; }
    if (MG.id !== id || MG.tab !== 'players') return;
    PL.data = d; renderPlayers(); loadStats(id); loadGeo(id);
    PL.timer = setTimeout(() => { if (MG.tab === 'players' && !document.hidden) loadPlayers(); }, 15000);
  }
  function renderPlayers() {
    const d = PL.data, op = can('operator');
    $('#plInfo').textContent = !d.running ? 'Server läuft nicht' : d.online_error ? d.online_error
      : d.online_from ? `${d.online.length} online · ${d.online_from === 'console' ? 'über die Server-Konsole' : 'über die Steam-Abfrage'}` : 'Dieses Spiel meldet keine Spielerliste';
    const acts = d.actions || [];
    $('#plOnline').innerHTML = d.online.length ? `<table class="tbl"><thead><tr><th>Name</th><th>Online seit</th><th><span class="sr">Aktionen</span></th></tr></thead><tbody>${d.online.map(p =>
      `<tr><td><b>${esc(p.name)}</b>${p.score ? ` <span class="muted small">· ${p.score} Punkte</span>` : ''}</td><td class="mono small">${p.seconds ? plDur(p.seconds) : '–'}</td><td><div class="row pl-acts">${op && d.online_ids ? PL_ONLINE.filter(a => acts.includes(a)).map(a =>
        `<button type="button" class="btn-line small" data-pl="${a}" data-name="${esc(p.name)}">${PL_ACT[a]}</button>`).join('') : ''}</div></td></tr>`).join('')}</tbody></table>`
      : `<p class="muted small">${d.running ? 'Gerade ist niemand online.' : 'Startet der Server, siehst du hier, wer spielt.'}</p>`;
    $('#plIdLabel').textContent = d.id_label || 'Spielername';
    $('#plName').placeholder = d.id_label === 'Steam-ID' ? 'z. B. 76561198012345678' : 'z. B. Steve';
    const listActs = (d.lists || []).filter(l => l.editable).map(l => ({ key: 'list_add', list: l.key, label: l.label + ' +' }));
    // with lists the add buttons belong to them (remove sits in the list); without lists all actions by name
    const order = ['kick', 'ban', 'unban', 'op', 'deop', 'whitelist_add', 'whitelist_remove'];
    const btns = listActs.length ? listActs : order.filter(a => acts.includes(a)).map(a => ({ key: a, label: PL_ACT[a] }));
    $('#plForm').hidden = !op || !btns.length;
    $('#plBtns').innerHTML = btns.map(b => `<button type="button" class="btn-line" data-pla="${b.key}" data-list="${b.list || ''}">${esc(b.label)}</button>`).join('');
    $('#plLists').innerHTML = (d.lists || []).map(l => `<div class="pl-list"><h4>${esc(l.label)} <span class="muted small">${l.entries.length}</span></h4>
      ${l.entries.length ? `<ul>${l.entries.map(x => `<li><span>${esc(x)}</span>${op && l.editable ? `<button type="button" class="linkbtn small" data-plrm="${esc(l.key)}" data-name="${esc(x)}">Entfernen</button>` : ''}</li>`).join('')}</ul>` : '<p class="muted small">leer</p>'}
      ${l.hint ? `<p class="small muted">${esc(l.hint)}</p>` : ''}</div>`).join('');
    $('#plNote').textContent = !d.supported ? 'Kicken, Bannen und Admins verwalten gibt es für dieses Spiel noch nicht. Die Online-Liste kommt, wenn das Spiel sie über die Steam-Abfrage verrät.' : (d.note || '');
  }
  const WD = ['Mo', 'Di', 'Mi', 'Do', 'Fr', 'Sa', 'So'];
  async function loadStats(id) {
    let v; try { v = await api('GET', `/api/servers/${id}/stats`); } catch (e) { return; }
    if (MG.id !== id) return;
    const since = v.since && !v.since.startsWith('0001') ? new Date(v.since) : null;
    $('#stSince').textContent = since ? `seit ${since.toLocaleDateString('de-DE')}` + (v.last_played && !v.last_played.startsWith('0001') ? ` · zuletzt gespielt ${fmtWhen(v.last_played)}` : '') : 'sammelt ab jetzt jede Minute';
    $('#stInsights').innerHTML = (v.insights || []).map(x => `<li>${esc(x)}</li>`).join('');
    // Mo … So as rows, 0–23 as columns
    const cells = [];
    cells.push('<span></span>' + Array.from({ length: 24 }, (_, h) => `<span class="hh">${h % 3 === 0 ? h : ''}</span>`).join(''));
    [1, 2, 3, 4, 5, 6, 0].forEach((d, i) => {
      cells.push(`<span class="wd">${WD[i]}</span>` + v.heat[d].map((x, h) => {
        const a = x < 0 ? 0 : v.heat_max > 0 ? Math.max(.08, x / v.heat_max) : .08;
        return `<span class="hc${x < 0 ? ' none' : ''}" style="--a:${a.toFixed(2)}" title="${WD[i]} ${h}:00 – ${x < 0 ? 'keine Daten' : x.toFixed(1).replace('.', ',') + ' Spieler im Schnitt'}"></span>`;
      }).join(''));
    });
    $('#stHeat').innerHTML = cells.join('');
    const ps = v.players || [];
    $('#stPlayers').innerHTML = ps.length ? `<table class="tbl"><thead><tr><th>Spieler</th><th>Spielzeit</th><th>Sitzungen</th><th>Zuletzt</th></tr></thead><tbody>${ps.slice(0, 20).map(p =>
      `<tr><td>${p.online ? '<span class="dot on" aria-label="online"></span> ' : ''}<b>${esc(p.name)}</b></td><td class="mono small">${plDur(p.seconds)}</td><td class="mono small">${p.sessions}</td><td class="mono small">${esc(fmtWhen(p.last))}</td></tr>`).join('')}</tbody></table>`
      : `<p class="small muted">${v.has_names ? '' : 'Spielzeiten je Spieler gibt es, sobald das Spiel Namen verrät (Steam-Abfrage oder Minecraft).'}</p>`;
  }
  /* ---------------- Herkunftskarte ---------------- */
  const GEO = { svg: null, vb: null, data: null, forId: null, scope: 'one', fitted: false };
  const MAP_W = 1000, mercY = lat => Math.log(Math.tan(Math.PI / 4 + Math.max(-85, Math.min(85, lat)) * Math.PI / 360));
  const MAP_H = (mercY(84) - mercY(-58)) * MAP_W / (2 * Math.PI);
  const proj = (lat, lon) => [(lon + 180) / 360 * MAP_W, (mercY(84) - mercY(lat)) * MAP_W / (2 * Math.PI)];
  const GEO_VIEW = { world: [0, 0, MAP_W, MAP_H], eu: [455, 165, 160, 215] };
  let worldSVG = null;
  async function worldMap() {
    if (!worldSVG) worldSVG = fetch('/img/world.svg').then(r => { if (!r.ok) throw new Error('Karte fehlt'); return r.text(); });
    return worldSVG;
  }
  async function loadGeo(id) {
    const scope = $('#geoScope').value;
    let v; try { v = await api('GET', scope === 'all' ? '/api/geo' : `/api/servers/${id}/geo`); } catch (e) { return; }
    if (MG.id !== id || MG.tab !== 'players') return;
    if (GEO.forId !== id + scope) { GEO.forId = id + scope; GEO.fitted = false; }
    GEO.data = v;
    $('#geoOff').hidden = v.enabled; $('#geoBody').hidden = !v.enabled;
    $('#geoAskAdmin').hidden = can('admin');
    $('#geoSub').textContent = v.enabled && v.total ? `${v.total} ${v.total === 1 ? 'Besuch' : 'Besuche'} erfasst` : '';
    if (!v.enabled) return;
    const notes = [];
    if (v.db.loading) notes.push('Die Ortsdatenbank wird gerade geladen (gut 100 MB). Das dauert ein paar Minuten, bis dahin zählt das Panel noch keine Orte.');
    else if (!v.db.ready) notes.push('Noch keine Ortsdatenbank.' + (v.db.error ? ' Fehler: ' + v.db.error : '') + (can('admin') ? ' <button type="button" class="linkbtn" id="geoReload">Jetzt laden</button>' : ''));
    if (v.source_error) notes.push(esc(v.source_error));
    if (v.db.ready && !v.source_error && !v.total && !v.live.length) notes.push('Noch keine Verbindungen erfasst. Sobald jemand ein paar Minuten auf einem laufenden Server spielt, erscheint er hier.');
    $('#geoNote').hidden = !notes.length; $('#geoNote').innerHTML = notes.join('<br>');
    const rl = $('#geoReload'); if (rl) rl.onclick = async () => { rl.disabled = true; try { await api('POST', '/api/geo/update'); toast('Ortsdatenbank wird geladen', 'ok'); setTimeout(() => loadGeo(MG.id), 3000); } catch (e) { toast(e.message, 'bad'); } };
    await renderGeo();
  }
  const cityName = p => p.kind === 'lan' ? 'Heimnetz' : p.kind === 'relay' ? 'über Steam-Relay' : p.kind === 'unknown' ? 'unbekannt' : [p.city, p.country_name].filter(Boolean).join(', ') || p.country || '?';
  const flag = cc => cc && cc.length === 2 ? String.fromCodePoint(...[...cc.toUpperCase()].map(c => 0x1f1a5 + c.charCodeAt(0))) : '';
  async function renderGeo() {
    const v = GEO.data, box = $('#geoMap');
    if (!GEO.svg || !box.contains(GEO.svg)) {
      let txt; try { txt = await worldMap(); } catch (e) { box.insertAdjacentHTML('beforeend', `<p class="err">${esc(e.message)}</p>`); return; }
      const tmp = document.createElement('div'); tmp.innerHTML = txt;
      GEO.svg = tmp.querySelector('svg'); GEO.svg.classList.add('geo-svg');
      GEO.svg.insertAdjacentHTML('beforeend', '<g class="geo-dots"></g>');
      box.prepend(GEO.svg); geoInteract();
    }
    // countries shaded by visits
    const max = Math.max(1, ...v.countries.map(c => c.sessions));
    const byCC = Object.fromEntries(v.countries.map(c => [c.country, c]));
    $$('path[data-cc]', GEO.svg).forEach(p => {
      const c = byCC[p.dataset.cc];
      p.classList.toggle('has', !!c);
      p.style.setProperty('--a', c ? (0.18 + 0.6 * Math.sqrt(c.sessions / max)).toFixed(2) : 0);
    });
    const pmax = Math.max(1, ...v.points.map(p => p.sessions));
    const dots = v.points.map(p => { const [x, y] = proj(p.lat, p.lon); return `<circle class="geo-dot" cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" data-r="${(3 + 9 * Math.sqrt(p.sessions / pmax)).toFixed(1)}"
      data-tip="${esc(cityName(p))}|${p.sessions} ${p.sessions === 1 ? 'Besuch' : 'Besuche'} · ${esc(plDur(p.seconds))}|zuletzt ${esc(fmtWhen(p.last))}"/>`; });
    const live = v.live.filter(l => !l.kind && (l.lat || l.lon)).map(l => { const [x, y] = proj(l.lat, l.lon); return `<circle class="geo-live-dot" cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" data-r="5" data-tip="${esc(cityName(l))}|spielt gerade (seit ${esc(fmtWhen(l.since))})"/>`; });
    $('.geo-dots', GEO.svg).innerHTML = dots.join('') + live.join('');
    if (!GEO.fitted) { GEO.fitted = true; geoZoom(v.points.length ? 'fit' : 'eu'); } else geoApply();
    const srv = id => { const s = byId(id); return s ? s.name : id; };
    $('#geoLive').innerHTML = v.live.length ? v.live.map(l => `<li>${flag(l.country)} ${esc(cityName(l))}${$('#geoScope').value === 'all' ? ` <span class="muted small">· ${esc(srv(l.server))}</span>` : ''} <span class="muted small">seit ${esc(fmtWhen(l.since))}</span></li>`).join('') : '<li class="muted small">Gerade niemand.</li>';
    $('#geoCountries').innerHTML = v.countries.length ? v.countries.slice(0, 12).map(c => {
      const share = v.total ? Math.round(c.sessions / v.total * 100) : 0;
      return `<li><span>${flag(c.country)} ${esc(c.name || c.country)}</span><span class="mono small">${c.sessions} · ${share} %</span><i style="--w:${share}%"></i></li>`;
    }).join('') : '<li class="muted small">Noch keine.</li>';
    const extra = [];
    if (v.lan.sessions) extra.push(`${v.lan.sessions}× aus dem Heimnetz`);
    if (v.relay.sessions) extra.push(`${v.relay.sessions}× über Steam-Relay (Ort verborgen, das Spiel leitet über Valve-Server)`);
    if (v.unknown.sessions) extra.push(`${v.unknown.sessions}× Ort unbekannt`);
    $('#geoExtra').textContent = extra.join(' · ');
  }
  function geoApply() {
    if (!GEO.svg || !GEO.vb) return;
    const [x, y, w, h] = GEO.vb;
    GEO.svg.setAttribute('viewBox', `${x} ${y} ${w} ${h}`);
    const px = w / Math.max(1, GEO.svg.clientWidth || 800); // map units per screen pixel
    $$('circle', GEO.svg).forEach(c => c.setAttribute('r', (+c.dataset.r * px).toFixed(2)));
    GEO.svg.style.setProperty('--sw', (px * 0.8).toFixed(3));
  }
  function geoClamp(vb) {
    let [x, y, w, h] = vb;
    w = Math.min(MAP_W, Math.max(20, w)); h = w * (GEO.vb ? GEO.vb[3] / GEO.vb[2] : MAP_H / MAP_W);
    x = Math.min(MAP_W - w, Math.max(0, x)); y = Math.min(Math.max(0, MAP_H - h), Math.max(Math.min(0, MAP_H - h), y));
    return [x, y, w, h];
  }
  function geoZoom(kind) {
    const box = $('#geoMap'), ratio = (box.clientHeight || 400) / (box.clientWidth || 800);
    let [x, y, w, h] = kind === 'keep' && GEO.vb ? GEO.vb : (GEO_VIEW[kind] || GEO_VIEW.world);
    if (kind === 'fit') {
      const pts = (GEO.data.points || []).map(p => proj(p.lat, p.lon));
      if (!pts.length) return geoZoom('eu');
      const xs = pts.map(p => p[0]), ys = pts.map(p => p[1]);
      x = Math.min(...xs); y = Math.min(...ys); w = Math.max(...xs) - x; h = Math.max(...ys) - y;
      const pad = Math.max(25, w * 0.15, h * 0.15); x -= pad; y -= pad; w += 2 * pad; h += 2 * pad;
    }
    // keep the box's aspect ratio, centered on the wanted area
    const cx = x + w / 2, cy = y + h / 2;
    if (h / w > ratio) w = h / ratio; else h = w * ratio;
    GEO.vb = [cx - w / 2, cy - h / 2, w, h];
    GEO.vb = geoClamp(GEO.vb);
    geoApply();
  }
  function geoInteract() {
    const svg = GEO.svg, tip = $('#geoTip');
    const toMap = (ev) => { const r = svg.getBoundingClientRect(), [x, y, w, h] = GEO.vb; return [x + (ev.clientX - r.left) / r.width * w, y + (ev.clientY - r.top) / r.height * h]; };
    const zoomAt = (f, pt) => {
      const [x, y, w, h] = GEO.vb, nw = w * f, nh = h * f;
      const [px, py] = pt || [x + w / 2, y + h / 2];
      GEO.vb = geoClamp([px - (px - x) * f, py - (py - y) * f, nw, nh]); geoApply();
    };
    svg.addEventListener('wheel', e => { e.preventDefault(); zoomAt(e.deltaY > 0 ? 1.25 : 0.8, toMap(e)); }, { passive: false });
    let drag = null;
    svg.addEventListener('pointerdown', e => { drag = { x: e.clientX, y: e.clientY, vb: GEO.vb.slice() }; svg.setPointerCapture(e.pointerId); });
    svg.addEventListener('pointermove', e => {
      if (drag) {
        const r = svg.getBoundingClientRect();
        GEO.vb = geoClamp([drag.vb[0] - (e.clientX - drag.x) / r.width * drag.vb[2], drag.vb[1] - (e.clientY - drag.y) / r.height * drag.vb[3], drag.vb[2], drag.vb[3]]);
        geoApply(); tip.hidden = true; return;
      }
      const c = e.target.closest('circle');
      if (!c) { tip.hidden = true; return; }
      const [a, b, d] = c.dataset.tip.split('|');
      tip.innerHTML = `<b>${a}</b><br>${b}${d ? '<br><span class="muted">' + d + '</span>' : ''}`;
      const r = $('#geoMap').getBoundingClientRect();
      tip.style.left = (e.clientX - r.left + 12) + 'px'; tip.style.top = (e.clientY - r.top + 12) + 'px'; tip.hidden = false;
    });
    const end = () => { drag = null; };
    svg.addEventListener('pointerup', end); svg.addEventListener('pointercancel', end);
    svg.addEventListener('pointerleave', () => { tip.hidden = true; });
    $('.geo-zoom').onclick = e => {
      const b = e.target.closest('[data-gz]'); if (!b) return;
      if (b.dataset.gz === 'in') zoomAt(0.67); else if (b.dataset.gz === 'out') zoomAt(1.5); else geoZoom(b.dataset.gz);
    };
    window.addEventListener('resize', () => { if (GEO.vb && document.body.contains(svg)) geoZoom('keep'); });
  }
  $('#geoScope').addEventListener('change', () => loadGeo(MG.id));
  $('#geoEnable').addEventListener('click', async () => {
    const b = $('#geoEnable'); b.disabled = true;
    try { await api('PUT', '/api/geo', { enabled: true }); toast('Herkunftskarte ist an. Die Ortsdatenbank wird jetzt geladen.', 'ok'); GEO.forId = null; await loadGeo(MG.id); }
    catch (e) { toast(e.message, 'bad'); }
    b.disabled = false;
  });
  $('#geoDisable').addEventListener('click', async () => {
    if (!await confirmBox('Herkunftskarte ausschalten?', 'Das Panel erfasst dann keine Orte mehr. Was schon gezählt ist, bleibt in der Statistik.', 'Ausschalten')) return;
    try { await api('PUT', '/api/geo', { enabled: false }); loadGeo(MG.id); } catch (e) { toast(e.message, 'bad'); }
  });
  async function loadHints() {
    let l; try { l = await api('GET', '/api/insights'); } catch (e) { return; }
    $('#homeHints').hidden = !l.length;
    $('#homeHintList').innerHTML = l.slice(0, 6).map(x => `<li><a href="#/server/${encodeURIComponent(x.server)}/players">${esc(x.name)}</a>: ${esc(x.text)}</li>`).join('');
  }
  async function plDo(action, name, list, confirmText) {
    if (!name) { toast('Bitte einen Namen eintragen', 'bad'); return; }
    if (confirmText && !await confirmBox(confirmText, name, 'Ja')) return;
    try { const r = await api('POST', `/api/servers/${MG.id}/players`, { action, name, list }); toast(r.message || 'Erledigt', 'ok'); setTimeout(loadPlayers, 800); }
    catch (e) { toast(e.message, 'bad'); }
  }
  $('#plReload').addEventListener('click', loadPlayers);
  $('#plOnline').addEventListener('click', e => {
    const b = e.target.closest('[data-pl]'); if (!b) return;
    const a = b.dataset.pl; plDo(a, b.dataset.name, '', a === 'ban' ? 'Spieler bannen?' : a === 'kick' ? 'Spieler rauswerfen?' : '');
  });
  $('#plBtns').addEventListener('click', e => {
    const b = e.target.closest('[data-pla]'); if (!b) return;
    plDo(b.dataset.pla, $('#plName').value.trim(), b.dataset.list, b.dataset.pla === 'ban' ? 'Spieler bannen?' : '').then(() => { $('#plName').value = ''; });
  });
  $('#plForm').addEventListener('submit', e => e.preventDefault());
  $('#plLists').addEventListener('click', e => {
    const b = e.target.closest('[data-plrm]'); if (!b) return;
    plDo('list_remove', b.dataset.name, b.dataset.plrm, 'Aus der Liste entfernen?');
  });

  // Spiel-Einstellungen
  const GS = { data: null, orig: {}, group: null };
  /* Einstellungen im Kachel-Layout: Reiter je Gruppe, eine Kachel je Wert */
  const numDe = n => String(n).replace('.', ',');
  function stCard(o) {
    // o: {id, attrs, label, help, type, value, options, labels, unit, min, max, group, file, key, when}
    const help = o.help ? `<p class="st-help">${esc(o.help)}</p>` : '';
    const range = o.type === 'number' && o.min != null && o.max != null ? `<p class="st-range">${numDe(o.min)} bis ${numDe(o.max)}${o.unit ? ' ' + esc(o.unit) : ''}</p>` : '';
    const when = o.when ? ` data-when="${esc(o.when)}"` : '';
    const q = esc((o.label + ' ' + (o.help || '') + ' ' + o.key).toLowerCase());
    const tag = `<span class="st-gtag">${esc(o.group)}</span>`;
    if (o.type === 'bool') {
      return `<div class="st-card st-bool" data-g="${esc(o.group)}" data-q="${q}" data-f="${o.file}"${when}>${tag}
        <div class="st-head"><label for="${o.id}">${esc(o.label)}</label><input type="checkbox" role="switch" class="tgl" id="${o.id}" ${o.attrs} ${o.value === 'true' ? 'checked' : ''}></div>${help}<p class="st-cond" hidden></p></div>`;
    }
    let input;
    if (o.type === 'select') {
      const opts = o.options.map((x, i) => `<option value="${esc(x)}" ${x === o.value ? 'selected' : ''}>${esc((o.labels && o.labels[i]) || x)}</option>`);
      if (o.value && !o.options.includes(o.value)) opts.unshift(`<option value="${esc(o.value)}" selected>${esc(o.value)}</option>`);
      input = `<select id="${o.id}" ${o.attrs}>${opts.join('')}</select>`;
    } else if (o.type === 'secret') {
      const set = o.value === '••••••';
      input = `<input id="${o.id}" ${o.attrs} type="password" value="" placeholder="${set ? 'gesetzt, bleibt unverändert' : 'leer'}" autocomplete="new-password">`;
    } else if (o.type === 'number') {
      input = `<input id="${o.id}" ${o.attrs} type="number" step="any" inputmode="decimal" value="${esc(o.value)}"${o.min != null ? ` min="${o.min}"` : ''}${o.max != null ? ` max="${o.max}"` : ''}>`;
    } else {
      input = `<input id="${o.id}" ${o.attrs} type="text" value="${esc(o.value)}" maxlength="300" autocomplete="off"${o.type === 'list' ? ' placeholder="Durch Komma getrennt"' : ''}>`;
    }
    const unit = o.unit ? `<span class="st-unit" aria-hidden="true">${esc(o.unit)}</span>` : '';
    return `<div class="st-card" data-g="${esc(o.group)}" data-q="${q}" data-f="${o.file}"${when}>${tag}
      <label class="st-title" for="${o.id}">${esc(o.label)}</label>${help}
      <div class="st-in${o.unit ? ' has-unit' : ''}">${input}${unit}</div>${range}<p class="st-cond" hidden></p></div>`;
  }
  function cfgField(f, s, group) {
    const id = `st_${f}_${s.key.replace(/[^A-Za-z0-9_-]/g, '_')}`;
    return stCard({ ...s, id, attrs: `data-cf="${f}" data-k="${esc(s.key)}"`, group, file: f });
  }
  function varCard(v) {
    const id = 'stv_' + v.key, type = v.type === 'bool' ? 'bool' : v.type === 'select' ? 'select' : v.type === 'secret' ? 'secret' : v.type === 'number' ? 'number' : 'text';
    return stCard({ id, attrs: `data-var="${esc(v.key)}"`, label: v.label + (v.required ? ' *' : ''), help: v.help, type, value: v.value, options: v.options || [], labels: v.labels, key: v.key, group: v.group || 'Start', file: 'v' });
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
    const groups = [], cards = [], msgs = [];
    const addG = g => { if (!groups.includes(g)) groups.push(g); return g; };
    d.vars.forEach(v => { addG(v.group || 'Start'); cards.push(varCard(v)); });
    if (d.vars.length) msgs.push(`<p class="small muted">${d.files.length ? 'Die Werte unter „' + esc(d.vars[0].group || 'Start') + '“ ' : 'Diese Werte '}bekommt das Spiel beim Start mit. Wer sie ändert, bekommt einen neuen Container; Spielstände bleiben erhalten.</p>`);
    d.files.forEach((f, i) => {
      const fallback = f.title.replace(/\s*\(.*\)\s*$/, '') || 'Einstellungen';
      if (f.note) msgs.push(`<p class="small muted"${f.note_group ? ` data-mg="${esc(f.note_group)}"` : ''}>${esc(f.note)}</p>`);
      if (f.error) msgs.push(`<p class="err">${esc(f.error)}</p>`);
      if (!f.editable && !f.error) {
        const sv = byId(MG.id), st = sv ? sv.status : '';
        msgs.push(`<p class="st-wait">${st === 'installing' || st === 'preparing' ? '<span class="spin" aria-hidden="true"></span> Das Spiel wird gerade installiert. Die Datei ' + esc(f.file) + ' gibt es erst nach der Installation oder dem ersten Start. Sobald sie da ist, erscheinen die Einstellungen hier von selbst.'
          : st === 'starting' ? '<span class="spin" aria-hidden="true"></span> Der Server startet zum ersten Mal und legt ' + esc(f.file) + ' gerade an. Die Einstellungen erscheinen hier gleich von selbst.'
          : st === 'running' ? 'Das Spiel hat ' + esc(f.file) + ' noch nicht angelegt. Manche Spiele tun das erst nach dem ersten Speichern oder Beenden. Stoppe den Server einmal, dann erscheinen die Einstellungen.'
          : 'Die Datei ' + esc(f.file) + ' legt das Spiel beim ersten Start an. Starte den Server einmal, danach kannst du die Werte hier ändern.'}</p>`);
        return;
      }
      if (!f.editable) return;
      f.settings.filter(s => s.present).forEach(s => cards.push(cfgField(i, s, addG(s.group || fallback))));
    });
    if (!cards.length && !msgs.length) msgs.push('<p class="muted">Für dieses Spiel gibt es noch kein Einstellungs-Formular. Die Konfigurationsdateien kannst du unter „Dateien“ bearbeiten.</p>');
    if (!groups.includes(GS.group)) GS.group = groups[0];
    $('#stBody').innerHTML = `<div class="st-top">${groups.length > 1 ? `<nav class="st-tabs" aria-label="Bereiche">${groups.map(g => `<button type="button" data-st-g="${esc(g)}">${esc(g)}</button>`).join('')}</nav>` : '<span></span>'}
        ${cards.length > 8 ? '<input type="search" id="stFind" placeholder="Einstellung suchen" aria-label="Einstellung suchen" autocomplete="off">' : ''}</div>
      ${msgs.length ? `<div class="st-msgs">${msgs.join('')}</div>` : ''}
      <p class="st-banner" id="stBanner" hidden></p>
      <div class="st-grid" id="stGrid">${cards.join('')}</div>
      <p class="muted st-none" id="stNone" hidden>Keine Einstellung gefunden.</p>`;
    $('#stSave').hidden = !cards.length;
    $('#stNote').textContent = d.running ? 'Der Server läuft. Zum Speichern wird er kurz gestoppt und wieder gestartet, die Spieler werden dabei getrennt.' : '';
    $('.st-foot').hidden = $('#stSave').hidden;
    GS.orig = collectSettings();
    showStGroup();
  }
  // which cards are visible: current tab, or all matches while searching
  function showStGroup() {
    const find = $('#stFind'), q = find ? find.value.trim().toLowerCase() : '';
    $$('#stBody [data-st-g]').forEach(b => { const on = !q && b.dataset.stG === GS.group; b.classList.toggle('on', on); b.setAttribute('aria-current', on ? 'true' : 'false'); });
    let n = 0;
    $$('#stGrid .st-card').forEach(c => { const show = q ? c.dataset.q.includes(q) : c.dataset.g === GS.group; c.hidden = !show; if (show) n++; });
    $('#stGrid').classList.toggle('searching', !!q);
    $$('#stBody [data-mg]').forEach(m => { m.hidden = !!q || m.dataset.mg !== GS.group; });
    const box = $('#stBody .st-msgs'); if (box) box.hidden = ![...box.children].some(m => !m.hidden);
    $('#stNone').hidden = n > 0;
    updateWhen();
  }
  // settings that only count when another one has a certain value
  function whenInfo(card) {
    const [k, v] = card.dataset.when.split('='), f = card.dataset.f;
    const el = $$(`#stBody [data-cf="${f}"]`).find(x => x.dataset.k === k);
    if (!el) return null;
    const cur = el.type === 'checkbox' ? String(el.checked) : el.value;
    const label = el.closest('.st-card').querySelector('.st-title, label').textContent.trim();
    let what;
    if (el.type === 'checkbox') what = `„${label}“ eingeschaltet ist`;
    else { const o = [...(el.options || [])].find(o => o.value === v); what = `„${label}“ auf „${o ? o.textContent : v}“ steht`; }
    return { ok: cur === v, el, v, text: 'Wirkt nur, wenn ' + what + '.' };
  }
  function updateWhen() {
    const vis = $$('#stGrid .st-card:not([hidden])');
    let shared = null, all = vis.length > 0;
    $$('#stGrid .st-card[data-when]').forEach(c => {
      const w = whenInfo(c), off = !!w && !w.ok;
      c.classList.toggle('st-dim', off);
      const p = $('.st-cond', c); p.hidden = !off; if (w) p.textContent = w.text;
    });
    vis.forEach(c => {
      if (!c.classList.contains('st-dim')) { all = false; return; }
      const w = whenInfo(c);
      if (shared === null) shared = w; else if (!w || !shared || w.text !== shared.text) all = false;
    });
    const ban = $('#stBanner');
    ban.hidden = !(all && shared);
    $('#stGrid').classList.toggle('shared-cond', !ban.hidden);
    if (!ban.hidden) {
      ban.innerHTML = `${esc(shared.text)} <button type="button" class="btn-line" id="stWhenFix">Jetzt umstellen</button>`;
      $('#stWhenFix').onclick = () => { const el = shared.el; if (el.type === 'checkbox') el.checked = shared.v === 'true'; else el.value = shared.v; updateWhen(); };
    }
  }
  $('#stUndo').onclick = async () => {
    const d = $('#dlgUndo'), ul = $('#undoList');
    ul.innerHTML = '<li class="muted">Lädt …</li>'; d.showModal();
    try {
      const list = await api('GET', `/api/servers/${MG.id}/settings/backups`);
      ul.innerHTML = list.length ? list.map(b => `<li><div><b>${esc(new Date(b.when).toLocaleString('de-DE', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' }))}</b>
          <span class="small muted">Stand vor: ${esc((b.changed || []).slice(0, 6).join(', '))}${(b.changed || []).length > 6 ? ' …' : ''}</span></div>
          <button type="button" class="btn-line" data-undo="${esc(b.id)}">Zurückholen</button></li>`).join('')
        : '<li class="muted">Noch keine Stände. Sie entstehen beim ersten Speichern.</li>';
    } catch (err) { ul.innerHTML = `<li class="err">${esc(err.message)}</li>`; }
  };
  $('#undoList').addEventListener('click', async e => {
    const b = e.target.closest('[data-undo]'); if (!b) return;
    const sv = byId(MG.id);
    if (!await confirmBox('Stand zurückholen?', 'Die Konfigurationsdateien werden auf diesen Stand gesetzt.' + (sv && isOn(sv) ? ' Der Server wird dafür kurz gestoppt und wieder gestartet.' : ''), 'Zurückholen')) return;
    b.disabled = true; b.textContent = 'Holt zurück …';
    try {
      const r = await api('POST', `/api/servers/${MG.id}/settings/restore`, { id: b.dataset.undo });
      $('#dlgUndo').close(); GS.data = r.settings; renderSettings(); toast('Stand zurückgeholt', 'ok'); loadServers();
    } catch (err) { toast(err.message, 'bad'); b.disabled = false; b.textContent = 'Zurückholen'; }
  });
  $('#stBody').addEventListener('click', e => {
    const b = e.target.closest('[data-st-g]'); if (!b) return;
    GS.group = b.dataset.stG; const f = $('#stFind'); if (f) f.value = ''; showStGroup();
  });
  $('#stBody').addEventListener('input', e => { if (e.target.id === 'stFind') showStGroup(); });
  $('#stBody').addEventListener('change', e => { if (e.target.dataset.cf != null) updateWhen(); });
  function collectSettings() {
    const files = {};
    $$('#stBody [data-cf]').forEach(el => {
      const f = el.dataset.cf, k = el.dataset.k;
      let v = el.type === 'checkbox' ? String(el.checked) : el.value;
      if (el.type === 'password' && v === '') v = '••••••';
      (files[f] = files[f] || {})[k] = v;
    });
    return { vars: collectVars($('#stBody'), true), files };
  }
  function settingsDiff() {
    const now = collectSettings(), body = { vars: {}, files: {} };
    for (const [k, v] of Object.entries(now.vars)) if (GS.orig.vars[k] !== v) body.vars[k] = v;
    for (const [f, kv] of Object.entries(now.files)) for (const [k, v] of Object.entries(kv)) {
      if (!GS.orig.files[f] || GS.orig.files[f][k] !== v) (body.files[f] = body.files[f] || {})[k] = v;
    }
    body.empty = !Object.keys(body.vars).length && !Object.keys(body.files).length;
    return body;
  }
  // label shown next to a changed field, for the event dialog
  function fieldLabel(el) {
    const l = el.id && document.querySelector(`label[for="${CSS.escape(el.id)}"]`);
    return (l && l.textContent.replace(/\s*\*$/, '').trim()) || el.dataset.var || el.dataset.k || '';
  }
  $('#stEvent').addEventListener('click', () => {
    const body = settingsDiff();
    if (body.empty) { toast('Erst Werte ändern, die während des Events gelten sollen. Gespeichert wird dabei nichts.'); return; }
    const items = [];
    $$('#stBody [data-var], #stBody [data-cf]').forEach(el => {
      const isVar = !!el.dataset.var, k = isVar ? el.dataset.var : el.dataset.k;
      const v = isVar ? body.vars[k] : (body.files[el.dataset.cf] || {})[k];
      if (v === undefined) return;
      const old = isVar ? GS.orig.vars[k] : (GS.orig.files[el.dataset.cf] || {})[k];
      items.push(`<li><b>${esc(fieldLabel(el))}</b> <span class="muted">${esc(old ?? '')}</span> → <span class="mono">${esc(el.type === 'password' ? '••••' : v)}</span></li>`);
    });
    openEventDialog({ vars: body.vars, files: body.files }, items.join(''));
  });
  const toLocalInput = d => { const x = new Date(d.getTime() - d.getTimezoneOffset() * 60000); return x.toISOString().slice(0, 16); };
  let GE_PENDING = null, GE_SERVER = null, GE_FROM_ST = false;
  function openEventDialog(changes, listHTML, preset) {
    GE_PENDING = changes;
    GE_FROM_ST = !preset; if (!preset) GE_SERVER = MG.id;
    const p = preset || {}, now = new Date();
    // default: next Friday 18:00 to Sunday 23:00
    const fri = new Date(now.getFullYear(), now.getMonth(), now.getDate() + ((5 - now.getDay() + 7) % 7 || 7), 18, 0);
    const sun = new Date(fri.getFullYear(), fri.getMonth(), fri.getDate() + 2, 23, 0);
    $('#geChanges').innerHTML = listHTML;
    $('#geName').value = p.name || '';
    $('#geStart').value = toLocalInput(p.start ? new Date(p.start) : fri);
    $('#geEnd').value = toLocalInput(p.end ? new Date(p.end) : sun);
    $('#geWeekly').checked = !!p.weekly;
    $('#geErr').hidden = true;
    $('#dlgEvent').showModal(); setTimeout(() => $('#geName').focus(), 30);
  }
  $('#geForm').addEventListener('submit', async e => {
    e.preventDefault();
    const err = t => { $('#geErr').textContent = t; $('#geErr').hidden = false; };
    const name = $('#geName').value.trim(), st = new Date($('#geStart').value), en = new Date($('#geEnd').value);
    if (!name) return err('Bitte einen Namen eingeben.');
    if (isNaN(st) || isNaN(en)) return err('Bitte Beginn und Ende angeben.');
    if (en <= st) return err('Das Ende muss nach dem Beginn liegen.');
    const b = $('#geOk'); b.disabled = true;
    try {
      const r = await api('POST', `/api/servers/${GE_SERVER}/game-events`, { name, start: st.toISOString(), end: en.toISOString(), weekly: $('#geWeekly').checked, vars: GE_PENDING.vars || {}, files: GE_PENDING.files || {} });
      $('#dlgEvent').close();
      toast('Event „' + r.event.name + '“ geplant' + ((r.notes || []).length ? ' · ' + r.notes.join(' ') : ''), 'ok');
      if (GE_FROM_ST && R.page === 'server' && MG.tab === 'settings') renderSettings(); // discard the unsaved edits, they belong to the event
      if (KI.onEvent) KI.onEvent(r.event);
    } catch (x) { err(x.message); }
    finally { b.disabled = false; }
  });
  $('#stForm').addEventListener('submit', async e => {
    e.preventDefault();
    const body = settingsDiff(); delete body.empty;
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
  /* ---------------- Installierte Mods ---------------- */
  const ML_STATE = { geladen: ['ok', 'Geladen'], fehler: ['bad', 'Fehler'], fehlt: ['bad', 'Fehlt'], nicht_geladen: ['warn', 'Nicht heruntergeladen'],
    aktiv: ['on', 'Aktiv'], installiert: ['muted', 'Installiert'], inaktiv: ['muted', 'Aus'] };
  async function loadModList() {
    const id = MG.id;
    $('#mlInfo').textContent = 'Lese Mods und Konsole …';
    let d;
    try { d = await api('GET', `/api/servers/${id}/modlist`); } catch (e) { $('#mlInfo').textContent = e.message; return; }
    if (MG.id !== id) return;
    const mods = d.mods || [], cnt = k => mods.filter(x => x.state === k).length;
    const parts = [mods.length + (mods.length === 1 ? ' Mod' : ' Mods')];
    [['geladen', 'geladen'], ['fehler', 'mit Fehler'], ['fehlt', 'fehlen'], ['nicht_geladen', 'nicht heruntergeladen'], ['inaktiv', 'aus']].forEach(([k, t]) => { if (cnt(k)) parts.push(cnt(k) + ' ' + t); });
    const since = d.log_since && !String(d.log_since).startsWith('0001') ? new Date(d.log_since).toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'short' }) : '';
    $('#mlInfo').textContent = mods.length
      ? parts.join(' · ') + (since ? ` · Konsole ${d.running ? 'seit dem Start' : 'des letzten Laufs'} (${since}) ausgewertet` : ' · noch kein Start, deshalb ist unbekannt, was geladen wird') + ((d.notes || []).length ? ' · ' + d.notes.join(' ') : '')
      : 'Keine Mods gefunden. Das Panel erkennt Steam-Workshop-Inhalte, Project-Zomboid-Mods, BepInEx-Plugins (Valheim), Pak- und UE4SS-Mods (Unreal-Spiele wie Palworld) sowie Minecraft-Mods und -Plugins.';
    $('#mlList').innerHTML = mods.map(m => {
      const [cls, lab] = ML_STATE[m.state] || ML_STATE.installiert;
      const sub = [m.source, m.version ? 'Version ' + m.version : '', m.workshop && m.name !== m.workshop ? 'Workshop ' + m.workshop : '', m.size ? fmtSize(m.size) : '',
        m.modified && !String(m.modified).startsWith('0001') ? 'Stand ' + new Date(m.modified).toLocaleDateString('de-DE') : ''].filter(Boolean).map(esc).join(' · ');
      const name = m.url ? `<a href="${esc(m.url)}" target="_blank" rel="noopener">${esc(m.name)}</a>` : esc(m.name);
      const note = m.note ? (m.state === 'fehler' ? `<code class="mono dg-ev">${esc(m.note)}</code>` : `<p class="small">${esc(m.note)}</p>`) : '';
      const upd = m.update_hint ? '<p class="small ml-upd">Im Workshop gibt es eine neuere Version. Der Server holt sie beim nächsten Start.</p>' : '';
      const sw = m.key && m.enabled != null && can('operator') ? `<input type="checkbox" role="switch" class="tgl" aria-label="${esc(m.name)} an oder aus" data-ml="${esc(m.key)}" ${m.enabled ? 'checked' : ''}>` : '';
      const wi = WS.info[MG.id];
      const rm = m.workshop && wi && wi.available && can('operator') && (wi.mode === 'zomboid' || m.enabled) ? `<button type="button" class="linkbtn small" data-ws-rm="${esc(m.workshop)}" data-name="${esc(m.name)}">Entfernen</button>` : '';
      const ki = m.advice && m.workshop && WS.ai && can('operator') ? `<button type="button" class="linkbtn small" data-ws-ki="${esc(m.workshop)}">KI fragen</button><div class="ws-ki" id="wsKi-${esc(m.workshop)}"></div>` : '';
      return `<div class="ml-item"><span class="ml-st ml-${cls}">${esc(lab)}</span><div class="ml-t"><b>${name}</b><span class="small muted">${sub}</span>${note}${upd}${rm} ${ki}</div>${sw}</div>`;
    }).join('');
  }
  $('#mlReload').addEventListener('click', loadModList);
  /* ---------------- Steam Workshop ---------------- */
  const WS = { loaded: {}, info: {}, ai: false, q: '', page: 1, items: [], b42: null, busy: false };
  const wsGeneric = () => { const i = WS.info[MG.id]; return !!(i && i.mode !== 'zomboid'); };
  async function loadWorkshopInfo() {
    const id = MG.id;
    if (!WS.info[id]) {
      try { const r = await api('GET', `/api/servers/${id}/workshop/info`); WS.info[id] = r.info; WS.ai = !!r.ai; }
      catch { return; }
      if (MG.id !== id) return;
      loadModList();
    }
    const i = WS.info[id];
    $('#wsBox').hidden = !i.available;
    if (!i.available) return;
    $('#wsB42').parentElement.hidden = !i.build42_filter;
    $('#wsQ').placeholder = 'Im Workshop von ' + (i.game || 'diesem Spiel') + ' suchen …';
    $('#wsInfo').textContent = i.mode === 'zomboid'
      ? 'Installieren trägt die Mod in die Server-INI ein, der Server lädt sie beim nächsten Start selbst herunter. Läuft er, wird er dafür neu gestartet.'
      : (i.note || '') + (i.login === 'anonym' ? ' Manche Spiele geben Workshop-Inhalte nur an ein Steam-Konto heraus, das das Spiel besitzt. Ist unter Einstellungen → Steam-Konto eines angemeldet, versucht das Panel es damit, wenn Steam den anonymen Download ablehnt.' : ' Lehnt Steam den anonymen Download ab, versucht das Panel es mit dem angemeldeten Steam-Konto.');
    if (!WS.loaded[id]) { WS.loaded[id] = true; WS.b42 = null; searchWorkshop('', 1); }
  }
  async function searchWorkshop(q, page) {
    const id = MG.id; if (WS.busy) return;
    WS.busy = true; $('#wsMore').hidden = true;
    if (page === 1) $('#wsResults').innerHTML = '<p class="rc-wait"><span class="spin" aria-hidden="true"></span> Frage Steam …</p>';
    try {
      const b = WS.b42 == null ? '' : '&b42=' + (WS.b42 ? 1 : 0);
      const r = await api('GET', `/api/servers/${id}/workshop?q=${encodeURIComponent(q)}&page=${page}${b}`);
      if (MG.id !== id) return;
      WS.q = q; WS.page = page; WS.items = page === 1 ? r.items : WS.items.concat(r.items);
      if (WS.b42 == null) WS.b42 = r.build42;
      $('#wsB42').checked = !!WS.b42;
      renderWorkshop(); $('#wsMore').hidden = !r.more;
    } catch (e) { $('#wsResults').innerHTML = `<p class="err">${esc(e.message)}</p>`; }
    finally { WS.busy = false; }
  }
  function renderWorkshop() {
    $('#wsResults').innerHTML = WS.items.length ? WS.items.map(it => {
      const img = it.preview ? `<img src="${esc(it.preview)}" alt="" loading="lazy" referrerpolicy="no-referrer">` : '<span class="md-ph" aria-hidden="true"></span>';
      const ids = wsGeneric() ? (it.size ? fmtSize(it.size) : '') : it.mod_ids && it.mod_ids.length ? 'Mod-ID ' + it.mod_ids.join(', ') : 'Mod-ID nicht in der Beschreibung';
      const sub = `<span class="mono muted">${fmtNum(it.subs)} Abos · ${esc(ids)}${it.maps && it.maps.length ? ' · Karte ' + esc(it.maps.join(', ')) : ''}</span>`;
      const gi = WS.info[MG.id] || {}, verb = gi.mode === 'zomboid' || gi.recipe ? 'Installieren' : 'Herunterladen';
      const btn = it.installed ? `<span class="mono muted">${wsGeneric() ? 'vorgemerkt' : 'eingetragen'}</span>` : `<button type="button" class="btn-line" data-ws-add="${esc(it.id)}">${verb}</button>`;
      return `<div class="md-item"><a href="https://steamcommunity.com/sharedfiles/filedetails/?id=${esc(it.id)}" target="_blank" rel="noopener">${img}</a><div class="md-t"><b title="${esc(it.title)}">${esc(it.title)}</b>${sub}<p>${esc(it.description)}</p></div>${btn}</div>`;
    }).join('') : '<p class="muted">Nichts gefunden.</p>';
  }
  $('#wsForm').addEventListener('submit', e => { e.preventDefault(); searchWorkshop($('#wsQ').value.trim(), 1); });
  $('#wsB42').addEventListener('change', () => { WS.b42 = $('#wsB42').checked; searchWorkshop(WS.q, 1); });
  $('#wsMore').addEventListener('click', () => searchWorkshop(WS.q, WS.page + 1));
  $('#wsResults').addEventListener('click', async e => {
    const b = e.target.closest('[data-ws-add]'); if (!b) return;
    const it = WS.items.find(x => x.id === b.dataset.wsAdd), s = byId(MG.id);
    if (!wsGeneric() && s && isOn(s) && !await confirmBox('Mod installieren?', `„${it.title}“ wird eingetragen. Dafür startet der Server neu, Spieler werden kurz getrennt. Beim Start lädt er die Mod herunter.`, 'Installieren')) return;
    b.disabled = true; b.textContent = 'Trage ein …';
    try { const r = await api('POST', `/api/servers/${MG.id}/workshop/install`, { id: it.id }); it.installed = true; renderWorkshop(); toast(r.message, 'ok'); loadModList(); }
    catch (err) { toast(err.message, 'bad'); b.disabled = false; b.textContent = wsGeneric() && !(WS.info[MG.id] || {}).recipe ? 'Herunterladen' : 'Installieren'; }
  });
  $('#mlList').addEventListener('click', async e => {
    const k = e.target.closest('[data-ws-ki]');
    if (k) {
      const box = document.getElementById('wsKi-' + k.dataset.wsKi);
      k.disabled = true; box.innerHTML = '<p class="rc-wait small"><span class="spin" aria-hidden="true"></span> Die KI sieht sich die Dateien der Mod an …</p>';
      try {
        const r = await api('POST', `/api/servers/${MG.id}/workshop/advice`, { id: k.dataset.wsKi });
        box.innerHTML = `<p class="small">${esc(r.summary || '')}${r.sure ? '' : ' <span class="muted">(Die KI ist sich nicht sicher.)</span>'}</p>` +
          (r.steps && r.steps.length ? `<ol class="small">${r.steps.map(x => `<li>${esc(x)}</li>`).join('')}</ol>` : '');
      } catch (err) { box.innerHTML = `<p class="err small">${esc(err.message)}</p>`; }
      k.disabled = false;
      return;
    }
    const b = e.target.closest('[data-ws-rm]'); if (!b) return;
    const s = byId(MG.id);
    const txt = wsGeneric()
      ? `„${b.dataset.name}“ wird ab dem nächsten Start nicht mehr geladen. Die Dateien bleiben liegen.`
      : `„${b.dataset.name}“ wird aus WorkshopItems und der Mod-Liste genommen.${s && isOn(s) ? ' Der Server startet dafür neu.' : ''} Spielstände mit Gegenständen aus der Mod können danach Fehler zeigen.`;
    if (!await confirmBox('Mod entfernen?', txt, 'Entfernen')) return;
    b.disabled = true;
    try { const r = await api('POST', `/api/servers/${MG.id}/workshop/remove`, { id: b.dataset.wsRm }); toast(r.message, 'ok'); loadModList(); const it = WS.items.find(x => x.id === b.dataset.wsRm); if (it) { it.installed = false; renderWorkshop(); } }
    catch (err) { toast(err.message, 'bad'); b.disabled = false; }
  });
  $('#stmApiSave').addEventListener('click', async () => {
    try { const r = await api('PUT', '/api/settings/steam-api-key', { key: $('#stmApiKey').value }); $('#stmApiKey').value = r.key; toast(r.key ? 'Schlüssel gespeichert' : 'Schlüssel entfernt', 'ok'); }
    catch (err) { toast(err.message, 'bad'); }
  });
  $('#mlList').addEventListener('change', async e => {
    const c = e.target.closest('[data-ml]'); if (!c) return;
    const key = c.dataset.ml, on = c.checked, s = byId(MG.id);
    if (key.startsWith('pz:') && s && isOn(s) && !await confirmBox(on ? 'Mod einschalten?' : 'Mod ausschalten?', 'Dafür wird die Mod-Liste gespeichert und der Server neu gestartet. Spieler werden kurz getrennt.', on ? 'Einschalten' : 'Ausschalten')) { c.checked = !on; return; }
    c.disabled = true;
    try { const r = await api('POST', `/api/servers/${MG.id}/modlist/toggle`, { key, on }); toast(r.message, 'ok'); loadModList(); }
    catch (err) { toast(err.message, 'bad'); c.checked = !on; c.disabled = false; }
  });

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
  const TK = { data: null, timer: null, checked: false };
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
    if (TK.forId !== id) { TK.checked = false; TK.forId = id; }
    TK.data = d;
    if (tab === 'tasks') { renderTasks(); loadGameEvents(); } else renderEvents();
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
    $('#tkCheck').hidden = !d.can_check_update;
    $('#tkVer').hidden = !d.can_check_update || !d.installed_build;
    if (d.installed_build && !TK.checked) $('#tkVer').textContent = 'Installiert: Build ' + d.installed_build;
    const tasks = d.tasks || [];
    renderSuggestions(tasks);
    $('#tkRows').innerHTML = tasks.length ? tasks.map((t, i) => {
      const what = esc(t.label) + (t.command ? ` <span class="tk-cmdtxt">${esc(t.command)}</span>` : '') + tkTags(t);
      const res = t.last_result ? `<span class="tk-res small ${/^Fehler/.test(t.last_result) ? 'bad' : 'muted'}" title="${esc(t.last_result)}">${esc(t.last_result)}</span>` : '';
      return `<tr data-i="${i}"><td><input type="checkbox" class="tk-on" ${t.enabled ? 'checked' : ''} aria-label="Aufgabe aktiv"></td><td>${esc(schedText(t))}</td><td>${what}</td>
        <td class="mono small">${t.enabled ? esc(fmtWhen(t.next_run)) : 'aus'}</td><td><span class="mono small">${esc(fmtWhen(t.last_run))}</span>${res}</td>
        <td><div class="acts row"><button type="button" class="icon-btn tk-run" title="Jetzt ausführen" aria-label="Jetzt ausführen">${ICON.play}</button><button type="button" class="icon-btn tk-del" title="Löschen" aria-label="Löschen">${ICON.del}</button></div></td></tr>`;
    }).join('') : '<tr class="empty"><td colspan="6">Noch keine Aufgaben. Zum Beispiel: täglich um 4 Uhr neu starten, vorher die Spieler warnen.</td></tr>';
    tkFormFields();
  }
  // zeitlich begrenzte Events
  const GE_STATE = { geplant: ['muted', 'Geplant'], aktiv: ['ok', 'Läuft'], beendet: ['muted', 'Beendet'] };
  const fmtSpan = (a, b) => {
    const A = new Date(a), B = new Date(b), o = { weekday: 'short', day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' };
    return A.toLocaleString('de-DE', o) + ' bis ' + (A.toDateString() === B.toDateString() ? B.toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' }) : B.toLocaleString('de-DE', o));
  };
  async function loadGameEvents() {
    const id = MG.id;
    let d; try { d = await api('GET', `/api/servers/${id}/game-events`); } catch (e) { return; }
    if (MG.id !== id) return;
    const evs = (d.events || []).slice().sort((a, b) => new Date(a.start) - new Date(b.start));
    $('#geList').innerHTML = evs.map(ev => {
      const [cls, lab] = GE_STATE[ev.state] || GE_STATE.geplant;
      const chg = (ev.changes || []).map(c => `<span class="tk-tag">${esc(c.label || c.key)}: ${esc(c.value)}</span>`).join('');
      const btn = can('operator') ? `<button type="button" class="linkbtn small" data-ge-del="${esc(ev.id)}" data-name="${esc(ev.name)}" data-state="${esc(ev.state)}">${ev.state === 'aktiv' ? 'Jetzt beenden' : 'Löschen'}</button>` : '';
      return `<div class="ge-item"><span class="ml-st ml-${cls}">${esc(lab)}</span><div class="ml-t"><b>${esc(ev.name)}</b>
        <span class="small muted">${esc(fmtSpan(ev.start, ev.end))}${ev.weekly ? ' · jede Woche' : ''}</span>
        <div class="tk-tags">${chg}</div>${ev.last_error ? `<p class="small bad">${esc(ev.last_error)}</p>` : ''}</div>${btn}</div>`;
    }).join('');
    $('#geHint').hidden = evs.length > 0 && !can('operator');
  }
  $('#geList').addEventListener('click', async e => {
    const b = e.target.closest('[data-ge-del]'); if (!b) return;
    const active = b.dataset.state === 'aktiv';
    if (!await confirmBox(active ? 'Event jetzt beenden?' : 'Event löschen?', active ? 'Die alten Werte werden wiederhergestellt, der Server startet dafür neu.' : '„' + b.dataset.name + '“ wird gelöscht.', active ? 'Beenden' : 'Löschen')) return;
    b.disabled = true;
    try { await api('DELETE', `/api/servers/${MG.id}/game-events/${b.dataset.geDel}`); toast(active ? 'Event beendet, alte Werte zurück' : 'Event gelöscht', 'ok'); loadGameEvents(); }
    catch (x) { toast(x.message, 'bad'); b.disabled = false; }
  });
  const waitText = m => m >= 60 ? (m / 60) + ' Std.' : m + ' Min.';
  function tkTags(t) {
    const d = TK.data || {}, tags = [];
    if (t.action === 'restart' && d.can_check_update && d.update_on_run) tags.push(t.only_if_update ? 'nur bei neuem Update' : 'mit Update-Prüfung');
    else if (t.only_if_update) tags.push('nur bei neuem Update');
    if (t.backup_first) tags.push('vorher Backup');
    if (t.when_empty) tags.push('nur wenn leer (max. ' + waitText(t.max_wait_min || 120) + ')');
    if (t.warn_min) tags.push('Warnung ' + t.warn_min + ' Min.');
    return tags.length ? `<div class="tk-tags">${tags.map(x => `<span class="tk-tag">${esc(x)}</span>`).join('')}</div>` : '';
  }
  // ready-made tasks that fit this game; hidden once a similar one exists
  function suggestions() {
    const d = TK.data || {}, q = d.can_query, b = d.can_backup, out = [];
    if (d.can_update) out.push({ key: 'nightly', text: 'Nachts neu starten' + (d.can_check_update && d.update_on_run ? ' mit Update' : '') + (q ? ', nur wenn leer' : '') + (b ? ', vorher Backup' : ''),
      task: { action: 'restart', at: '04:00', enabled: true, when_empty: !!q, max_wait_min: q ? 120 : 0, backup_first: !!b, warn_min: d.can_broadcast ? 5 : 0 },
      has: t => t.action === 'restart' && !t.every_hours });
    if (d.can_check_update) out.push({ key: 'upd', text: 'Updates einspielen, sobald es welche gibt' + (q ? ' (wenn niemand spielt)' : ''),
      task: { action: 'update', every_hours: 1, enabled: true, only_if_update: true, when_empty: !!q, max_wait_min: q ? 240 : 0, backup_first: !!b, warn_min: d.can_broadcast ? 5 : 0 },
      has: t => t.action === 'update' && t.only_if_update });
    if (b) out.push({ key: 'bk', text: 'Alle 6 Stunden sichern', task: { action: 'backup', every_hours: 6, enabled: true }, has: t => t.action === 'backup' });
    return out;
  }
  function renderSuggestions(tasks) {
    const list = can('operator') ? suggestions().filter(x => !tasks.some(x.has)) : [];
    $('#tkSugg').hidden = !list.length;
    $('#tkSuggList').innerHTML = list.map(x => `<button type="button" data-sugg="${x.key}">+ ${esc(x.text)}</button>`).join('');
  }
  $('#tkSuggList').addEventListener('click', async e => {
    const b = e.target.closest('[data-sugg]'); if (!b) return;
    const x = suggestions().find(y => y.key === b.dataset.sugg); if (!x) return;
    b.disabled = true;
    if (!await saveTasks(plainTasks().concat([x.task]), 'Aufgabe angelegt')) b.disabled = false;
  });
  $('#tkCheck').addEventListener('click', async () => {
    const b = $('#tkCheck'), id = MG.id; b.disabled = true; b.textContent = 'Frage Steam …';
    try {
      const r = await api('GET', `/api/servers/${id}/update/check`);
      if (MG.id !== id) return;
      TK.checked = true;
      $('#tkVer').hidden = false;
      $('#tkVer').innerHTML = r.error ? `<span class="muted">${esc(r.error)}</span>` + (r.installed ? ` · Installiert: Build ${esc(r.installed)}` : '')
        : r.available ? `<b class="on-ok">Neue Version verfügbar:</b> Build ${esc(r.latest)} (installiert ${esc(r.installed)})`
        : `Aktuell: Build ${esc(r.installed)} ist die neueste Version.`;
    } catch (err) { toast(err.message, 'bad'); }
    b.disabled = false; b.textContent = 'Auf Updates prüfen';
  });
  function tkFormFields() {
    const a = $('#tkAction').value, k = $('#tkKind').value, d = TK.data || {};
    $('#tkCmdWrap').hidden = a !== 'command';
    $('#tkAtWrap').hidden = k === 'every'; $('#tkEveryWrap').hidden = k !== 'every'; $('#tkDays').hidden = k !== 'days';
    const warnable = ['restart', 'update', 'stop'].includes(a);
    $('#tkEmptyWrap').hidden = !warnable || !d.can_query;
    $('#tkIfUpdWrap').hidden = !['restart', 'update'].includes(a) || !d.can_check_update;
    $('#tkBackupWrap').hidden = !warnable || !d.can_backup;
    $('#tkCond').hidden = $('#tkEmptyWrap').hidden && $('#tkIfUpdWrap').hidden && $('#tkBackupWrap').hidden;
    $('#tkWarnWrap').hidden = !warnable || !d.can_broadcast;
    $('#tkNote').textContent = warnable && !d.can_broadcast ? 'Dieses Spiel kann vom Panel aus keine Nachricht an die Spieler schicken, deshalb gibt es keine Vorwarnung.' : '';
  }
  ['tkAction', 'tkKind'].forEach(x => $('#' + x).addEventListener('change', tkFormFields));
  async function saveTasks(tasks, okMsg) {
    try { TK.data = await api('PUT', `/api/servers/${MG.id}/tasks`, { tasks }); renderTasks(); if (okMsg) toast(okMsg, 'ok'); return true; }
    catch (e) { toast(e.message, 'bad'); loadTasks(); return false; }
  }
  const plainTasks = () => (TK.data.tasks || []).map(t => ({ id: t.id, action: t.action, command: t.command, at: t.at, days: t.days, every_hours: t.every_hours, warn_min: t.warn_min, enabled: t.enabled,
    when_empty: t.when_empty, max_wait_min: t.max_wait_min, only_if_update: t.only_if_update, backup_first: t.backup_first }));
  $('#tkForm').addEventListener('submit', async e => {
    e.preventDefault();
    const k = $('#tkKind').value, a = $('#tkAction').value;
    const t = { action: a, command: $('#tkCmd').value, enabled: true, warn_min: $('#tkWarnWrap').hidden ? 0 : +$('#tkWarn').value,
      when_empty: !$('#tkEmptyWrap').hidden && $('#tkEmpty').checked, only_if_update: !$('#tkIfUpdWrap').hidden && $('#tkIfUpd').checked,
      backup_first: !$('#tkBackupWrap').hidden && $('#tkBackup').checked };
    if (t.when_empty) t.max_wait_min = +$('#tkMaxWait').value;
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
      a.push({ label: 'Benachrichtigungen (Discord, ntfy)', hint: 'Panel', run: () => openSettings('benachrichtigungen') });
      a.push({ label: 'Statusseite für Mitspieler', hint: 'Panel', run: () => openSettings('statusseite') });
      a.push({ label: 'Discord-Bot einrichten', hint: 'Panel', run: () => openSettings('discord') });
      a.push({ label: 'Steam-Konto', hint: 'Panel', run: () => openSettings('steam') });
      a.push({ label: 'Panel aktualisieren', hint: 'Panel', run: () => openSettings('panel') });
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
    document.title = (s ? s.name : TITLES[n.page] || 'Panel') + ' · Bytewise Panel';
    if (n.page === 'home') { requestAnimationFrame(() => { sizeLand(); drawSparks(); }); loadHints(); }
    else if (n.page === 'server') {
      if (!s) { if (S.loaded) { toast('Diesen Server gibt es nicht (mehr).', 'bad'); go('#/server'); } return; }
      if (prevId !== n.id || prev !== 'server') { MG.id = n.id; MG.path = ''; GS.group = null; closeEditor(true); if (S.consoleId && S.consoleId !== n.id) closeConsole(); }
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
      if (st.update) { S.update = st.update; } renderVersion();
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
    renderVersion(); renderKI();
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
    S.version = st.version; S.update = st.update || null; S.user = st.user;
    S.role = st.role || ''; S.twoFactor = !!st.two_factor; S.publicHost = st.public_host || ''; S.lanIP = st.lan_ip || '';
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
