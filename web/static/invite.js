/* Einladungsseite: alles, was ein Freund zum Beitreten braucht. */
(() => {
  const LOC = (window.I18N && I18N.locale) || 'de-DE', DEC = (window.I18N && I18N.dec) || ',';
  const $ = s => document.querySelector(s);
  const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const LABEL = { online: 'Läuft gerade', startet: 'Startet gerade', aus: 'Gerade aus', fehler: 'Gestört', wartung: 'Wartung' };
  const tok = location.pathname.split('/').filter(Boolean).pop() || '';
  async function load() {
    let d;
    try {
      const r = await fetch('/api/public/invite/' + encodeURIComponent(tok), { cache: 'no-store' });
      d = await r.json();
      if (!r.ok) throw new Error(d.error || 'Diesen Einladungslink gibt es nicht (mehr).');
    } catch (e) { $('#msg').textContent = e.message; return; }
    const s = d.server;
    document.title = 'Einladung: ' + s.name;
    $('#kicker').textContent = d.title; $('#title').textContent = s.name;
    $('#msg').hidden = true; $('#card').hidden = false;
    $('#card').innerHTML = `
      <span class="game">${esc(s.game)}</span>
      <span class="state ${s.state}"><i></i>${LABEL[s.state] || s.state}${s.until ? ' bis ' + esc(new Date(s.until).toLocaleString(LOC, { weekday: 'short', hour: '2-digit', minute: '2-digit' })) : ''}${s.state === 'online' ? ` · ${s.players}${s.max ? ' / ' + s.max : ''} Spieler` : ''}</span>
      ${s.names && s.names.length ? `<ul class="names">${s.names.map(n => `<li>${esc(n)}</li>`).join('')}</ul>` : ''}
      ${s.note ? `<p class="inv-note">${esc(s.note)}</p>` : ''}
      <h2 class="inv-h">So kommst du drauf</h2>
      ${s.address ? `<div class="join"><code>${esc(s.address)}</code><button type="button" data-copy="${esc(s.address)}">Kopieren</button>${s.steam_link ? `<a class="btn primary" href="${esc(s.steam_link)}">Beitreten</a>` : ''}</div>` : ''}
      ${s.lan_address ? `<div class="join lan"><span class="lanl">Du bist im selben Heimnetz, nimm diese Adresse:</span><code>${esc(s.lan_address)}</code><button type="button" data-copy="${esc(s.lan_address)}">Kopieren</button></div>` : ''}
      ${s.hint ? `<p class="hint">${esc(s.hint)}</p>` : ''}
      ${s.state === 'aus' ? '<p class="hint">Der Server ist gerade aus. Frag in eurer Gruppe, wer ihn starten kann, oder schau später nochmal vorbei.</p>' : ''}`;
    try {
      const r = await fetch('/api/public/mods/' + encodeURIComponent(tok)); const m = await r.json();
      if (r.ok && (m.mods || []).length) {
        $('#card').insertAdjacentHTML('beforeend', `<h2 class="inv-h">Mods (${m.mods.length})</h2>` + modList(m));
      }
    } catch (e) { /* ohne Mods */ }
    if (d.status) { $('#statusLink').href = d.status; $('#statusLink').hidden = false; $('#sep').hidden = false; }
  }
  function modList(m) {
    const items = (m.mods || []).map(x => `<li>${x.url ? `<a href="${esc(x.url)}" target="_blank" rel="noopener">${esc(x.name)}</a>` : esc(x.name)}${x.version ? ` <span class="muted">${esc(x.version)}</span>` : ''}${x.need === 'optional' ? ' <span class="muted">(optional)</span>' : ''}</li>`).join('');
    return `${m.note ? `<p class="hint">${esc(m.note)}</p>` : ''}<ul class="modl">${items}</ul>${m.download ? `<a class="btn primary" href="${esc(m.download)}">Mod-Paket herunterladen (.zip)</a>` : ''}`;
  }
  document.addEventListener('click', async e => {
    const b = e.target.closest('[data-copy]'); if (!b) return;
    try { await navigator.clipboard.writeText(b.dataset.copy); b.textContent = 'Kopiert'; setTimeout(() => { b.textContent = 'Kopieren'; }, 1500); } catch { /* Text ist markierbar */ }
  });
  load(); setInterval(() => { if (!document.hidden) load(); }, 30000);
})();
