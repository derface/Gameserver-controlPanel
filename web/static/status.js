/* Öffentliche Statusseite: zeigt die freigegebenen Server, ohne Anmeldung. */
(() => {
  const LOC = (window.I18N && I18N.locale) || 'de-DE', DEC = (window.I18N && I18N.dec) || ',';
  const $ = s => document.querySelector(s);
  const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const LABEL = { online: 'Online', startet: 'Startet', aus: 'Aus', fehler: 'Gestört', wartung: 'Wartung' };
  async function load() {
    let d;
    try {
      const r = await fetch('/api/public/status', { cache: 'no-store' });
      if (!r.ok) throw new Error(r.status === 404 ? 'Die Statusseite ist nicht eingeschaltet.' : 'Gerade nicht erreichbar.');
      d = await r.json();
    } catch (e) { $('#msg').textContent = e.message; return; }
    document.title = d.title + ' · Status';
    $('#title').textContent = d.title;
    if (window.gpWishes) window.gpWishes(!!d.wishes);
    $('#note').hidden = !d.note; $('#note').textContent = d.note || '';
    $('#msg').hidden = d.servers.length > 0;
    $('#msg').textContent = 'Gerade ist kein Server freigegeben.';
    $('#list').innerHTML = d.servers.map(s => `<article class="card">
      <h2>${esc(s.name)}</h2><span class="game">${esc(s.game)}</span>
      <span class="state ${s.state}"><i></i>${LABEL[s.state] || s.state}${s.until ? ' bis ' + esc(new Date(s.until).toLocaleString(LOC, { weekday: 'short', hour: '2-digit', minute: '2-digit' })) : ''}${s.state === 'online' ? ` · ${s.players}${s.max ? ' / ' + s.max : ''} Spieler` : ''}</span>
      ${s.names && s.names.length ? `<ul class="names">${s.names.map(n => `<li>${esc(n)}</li>`).join('')}</ul>` : ''}
      ${s.address ? `<div class="join"><code>${esc(s.address)}</code><button type="button" data-copy="${esc(s.address)}">Kopieren</button>${s.steam_link ? `<a class="btn primary" href="${esc(s.steam_link)}">Beitreten</a>` : ''}</div>` : ''}
      ${s.lan_address ? `<div class="join lan"><span class="lanl">Du bist im Heimnetz, nimm diese Adresse:</span><code>${esc(s.lan_address)}</code><button type="button" data-copy="${esc(s.lan_address)}">Kopieren</button></div>` : ''}
      ${s.hint ? `<p class="hint">${esc(s.hint)}</p>` : ''}
      ${s.note ? `<p class="snote">${esc(s.note)}</p>` : ''}
      ${s.season ? `<p class="season">🏆 ${esc(s.season.name)} · seit ${esc(new Date(s.season.since).toLocaleDateString(LOC))}${s.season.next ? ` · neue Saison ab ${esc(new Date(s.season.next).toLocaleString(LOC, { weekday: 'short', day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' }))}` : ''}</p>` : ''}
      ${s.season && s.season.fame && s.season.fame.length ? `<details class="mods fame"><summary>Ruhmeshalle</summary><ul class="modl">${s.season.fame.map(f => `<li><b>${esc(f.name)}</b> <span class="muted">${esc(new Date(f.start).toLocaleDateString(LOC))}–${esc(new Date(f.end).toLocaleDateString(LOC))}</span>${f.top && f.top.length ? '<br>' + f.top.map((t, i) => ['🥇', '🥈', '🥉'][i] + ' ' + esc(t)).join(' ') : ''}</li>`).join('')}</ul></details>` : ''}
      ${s.mods ? `<details class="mods" data-key="${esc(s.key)}"><summary>Mods (${s.mods})</summary><div class="mods-body muted small">Lädt …</div></details>` : ''}
      ${s.uptime ? uptimeBars(s.uptime, s.uptime_pct) : ''}
    </article>`).join('');
    const evs = d.events || [];
    $('#events').hidden = !evs.length && !d.calendar;
    const fmt = t => new Date(t).toLocaleString(LOC, { weekday: 'short', day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' });
    $('#evList').innerHTML = evs.length ? evs.map(e => `<li class="${e.active ? 'now' : ''}"><b>${esc(e.name)}</b> <span class="muted">· ${esc(e.server)}</span><br>
      <span class="when">${e.active ? 'läuft gerade, bis ' + esc(fmt(e.end)) : esc(fmt(e.start)) + ' bis ' + esc(fmt(e.end))}${e.weekly ? ' · jede Woche' : ''}</span>${e.what ? `<br><span class="muted small">${esc(e.what)}</span>` : ''}</li>`).join('')
      : '<li class="muted">Gerade ist nichts geplant.</li>';
    if (d.calendar) { const c = $('#cal'); c.hidden = false; c.href = 'webcal://' + location.host + d.calendar; c.title = 'Oder diese Adresse im Kalender hinzufügen: ' + location.origin + d.calendar; }
    const pl = d.players || [];
    $('#players').hidden = !pl.length;
    const dur = s => s >= 3600 ? Math.round(s / 3600) + ' Std.' : Math.max(1, Math.round(s / 60)) + ' Min.';
    const ago = t => { const h = (Date.now() - new Date(t)) / 3600e3; return h < 0.1 ? 'gerade eben' : h < 24 ? 'heute' : h < 48 ? 'gestern' : 'vor ' + Math.round(h / 24) + ' Tagen'; };
    $('#plSum').textContent = pl.length ? pl.length + ' Spieler · zusammen ' + dur(pl.reduce((a, p) => a + p.seconds, 0)) : '';
    $('#plRows').innerHTML = pl.map(p => `<tr><td>${p.online ? '<i class="on" title="gerade online"></i>' : ''}<b>${esc(p.name)}</b></td><td>${dur(p.seconds)}</td><td>${esc(p.fav)}${p.servers > 1 ? ` <span class="muted">+${p.servers - 1}</span>` : ''}</td>
      <td>${new Date(p.first).toLocaleDateString(LOC, { day: '2-digit', month: '2-digit', year: 'numeric' })}</td><td>${p.online ? 'jetzt' : ago(p.last)}</td></tr>`).join('');
    document.querySelectorAll('details.mods[data-key]').forEach(d => { if (openMods.has(d.dataset.key)) { d.open = true; } });
    $('#upd').textContent = 'Stand ' + new Date(d.updated).toLocaleTimeString(LOC, { hour: '2-digit', minute: '2-digit' });
  }
  function uptimeBars(days, pct) {
    const start = new Date(); start.setHours(12, 0, 0, 0); start.setDate(start.getDate() - (days.length - 1));
    const bars = days.map(([up, cr], i) => {
      const d = new Date(start); d.setDate(start.getDate() + i);
      const day = d.toLocaleDateString(LOC, { weekday: 'short', day: '2-digit', month: '2-digit' });
      const t = up < 0 ? `${day}: keine Daten` : `${day}: ${up} % online${cr ? `, ${cr} ${cr === 1 ? 'Absturz' : 'Abstürze'}` : ''}`;
      return `<i class="${up < 0 ? 'none' : cr ? 'crash' : ''}" style="--h:${Math.max(up, 0)}%" title="${esc(t)}"></i>`;
    }).join('');
    return `<div class="uptime"><div class="ubars" role="img" aria-label="Online in den letzten 30 Tagen">${bars}</div>
      <span class="ulabel">${pct != null ? String(pct).replace('.', DEC) + ' % online' : ''} · letzte 30 Tage</span></div>`;
  }
  // open mod lists stay open across the 20-second refresh
  const openMods = new Set();
  document.addEventListener('toggle', async e => {
    const d = e.target; if (!d.matches || !d.matches('details.mods[data-key]')) return;
    if (!d.open) { openMods.delete(d.dataset.key); return; }
    openMods.add(d.dataset.key);
    await fillMods(d);
  }, true);
  async function fillMods(d) {
    const body = d.querySelector('.mods-body');
    try {
      const r = await fetch('/api/public/mods/' + encodeURIComponent(d.dataset.key)); const m = await r.json();
      if (!r.ok) throw new Error(m.error || 'Nicht verfügbar');
      body.classList.remove('muted');
      body.innerHTML = modList(m);
    } catch (err) { body.textContent = err.message; }
  }
  window.modList = function modList(m) {
    const items = (m.mods || []).map(x => `<li>${x.url ? `<a href="${esc(x.url)}" target="_blank" rel="noopener">${esc(x.name)}</a>` : esc(x.name)}${x.version ? ` <span class="muted">${esc(x.version)}</span>` : ''}${x.need === 'optional' ? ' <span class="muted">(optional)</span>' : ''}</li>`).join('');
    return `${m.note ? `<p class="hint">${esc(m.note)}</p>` : ''}<ul class="modl">${items}</ul>${m.download ? `<a class="btn primary" href="${esc(m.download)}">Mod-Paket herunterladen (.zip)</a>` : ''}`;
  };
  document.addEventListener('click', async e => {
    const b = e.target.closest('[data-copy]'); if (!b) return;
    try { await navigator.clipboard.writeText(b.dataset.copy); b.textContent = 'Kopiert'; setTimeout(() => { b.textContent = 'Kopieren'; }, 1500); } catch { /* ältere Browser: Text ist markierbar */ }
  });
  // wish list
  const WKIND = { spiel: 'Spiel', mod: 'Mod', sonstiges: 'Sonstiges' };
  const WSTATE = { offen: '', geplant: 'Geplant', umgesetzt: 'Umgesetzt', abgelehnt: 'Abgelehnt' };
  let wishesOn = false;
  async function loadWishes() {
    try {
      const r = await fetch('/api/public/wishes', { cache: 'no-store' }); if (!r.ok) return;
      const d = await r.json();
      $('#wiList').innerHTML = (d.wishes || []).map(w => {
        const closed = w.status === 'umgesetzt' || w.status === 'abgelehnt';
        return `<li class="wi ${esc(w.status)}"><button type="button" class="vote${w.voted ? ' on' : ''}" data-vote="${esc(w.id)}" ${closed ? 'disabled' : ''} aria-pressed="${w.voted ? 'true' : 'false'}" aria-label="Dafür stimmen">▲<b>${w.votes}</b></button>
          <div><span class="wt">${esc(w.title)}</span> <span class="muted small">${esc(WKIND[w.kind] || '')}${w.by ? ' · ' + esc(w.by) : ''}</span>${WSTATE[w.status] ? ` <span class="badge ${esc(w.status)}">${WSTATE[w.status]}</span>` : ''}
          ${w.answer ? `<p class="small answer">${esc(w.answer)}</p>` : ''}</div></li>`;
      }).join('') || '<li class="muted small">Noch keine Wünsche. Fang an!</li>';
    } catch (e) { /* nächste Runde */ }
  }
  const wiMsg = (t, bad) => { const m = $('#wiMsg'); m.hidden = !t; m.textContent = t || ''; m.className = 'small' + (bad ? ' bad' : ''); };
  $('#wiForm').addEventListener('submit', async e => {
    e.preventDefault(); wiMsg('');
    const b = e.target.querySelector('button'); b.disabled = true;
    try {
      const r = await fetch('/api/public/wishes', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: $('#wiTitle').value, kind: $('#wiKind').value, by: $('#wiBy').value, website: $('#wiWeb').value }) });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(d.error || 'Hat nicht geklappt');
      $('#wiTitle').value = ''; wiMsg('Danke! Dein Wunsch steht jetzt auf der Liste.'); loadWishes();
    } catch (err) { wiMsg(err.message, true); }
    b.disabled = false;
  });
  $('#wiList').addEventListener('click', async e => {
    const b = e.target.closest('[data-vote]'); if (!b) return;
    b.disabled = true;
    try {
      const r = await fetch('/api/public/wishes/' + encodeURIComponent(b.dataset.vote) + '/vote', { method: 'POST' });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(d.error || 'Hat nicht geklappt');
      b.classList.toggle('on', d.voted); b.setAttribute('aria-pressed', d.voted ? 'true' : 'false'); b.querySelector('b').textContent = d.votes;
    } catch (err) { wiMsg(err.message, true); }
    b.disabled = false;
  });
  window.gpWishes = on => { $('#wishes').hidden = !on; if (on && !wishesOn) loadWishes(); wishesOn = on; };

  load(); setInterval(() => { if (!document.hidden) { load(); if (wishesOn) loadWishes(); } }, 20000);
})();
