/* Öffentliche Statusseite: zeigt die freigegebenen Server, ohne Anmeldung. */
(() => {
  const $ = s => document.querySelector(s);
  const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const LABEL = { online: 'Online', startet: 'Startet', aus: 'Aus', fehler: 'Gestört' };
  async function load() {
    let d;
    try {
      const r = await fetch('/api/public/status', { cache: 'no-store' });
      if (!r.ok) throw new Error(r.status === 404 ? 'Die Statusseite ist nicht eingeschaltet.' : 'Gerade nicht erreichbar.');
      d = await r.json();
    } catch (e) { $('#msg').textContent = e.message; return; }
    document.title = d.title + ' · Status';
    $('#title').textContent = d.title;
    $('#note').hidden = !d.note; $('#note').textContent = d.note || '';
    $('#msg').hidden = d.servers.length > 0;
    $('#msg').textContent = 'Gerade ist kein Server freigegeben.';
    $('#list').innerHTML = d.servers.map(s => `<article class="card">
      <h2>${esc(s.name)}</h2><span class="game">${esc(s.game)}</span>
      <span class="state ${s.state}"><i></i>${LABEL[s.state] || s.state}${s.state === 'online' ? ` · ${s.players}${s.max ? ' / ' + s.max : ''} Spieler` : ''}</span>
      ${s.names && s.names.length ? `<ul class="names">${s.names.map(n => `<li>${esc(n)}</li>`).join('')}</ul>` : ''}
      ${s.address ? `<div class="join"><code>${esc(s.address)}</code><button type="button" data-copy="${esc(s.address)}">Kopieren</button>${s.steam_link ? `<a class="btn primary" href="${esc(s.steam_link)}">Beitreten</a>` : ''}</div>` : ''}
      ${s.hint ? `<p class="hint">${esc(s.hint)}</p>` : ''}
      ${s.note ? `<p class="snote">${esc(s.note)}</p>` : ''}
    </article>`).join('');
    $('#upd').textContent = 'Stand ' + new Date(d.updated).toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' });
  }
  document.addEventListener('click', async e => {
    const b = e.target.closest('[data-copy]'); if (!b) return;
    try { await navigator.clipboard.writeText(b.dataset.copy); b.textContent = 'Kopiert'; setTimeout(() => { b.textContent = 'Kopieren'; }, 1500); } catch { /* ältere Browser: Text ist markierbar */ }
  });
  load(); setInterval(() => { if (!document.hidden) load(); }, 20000);
})();
