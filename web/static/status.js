/* Öffentliche Statusseite: zeigt die freigegebenen Server, ohne Anmeldung. */
(() => {
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
    $('#note').hidden = !d.note; $('#note').textContent = d.note || '';
    $('#msg').hidden = d.servers.length > 0;
    $('#msg').textContent = 'Gerade ist kein Server freigegeben.';
    $('#list').innerHTML = d.servers.map(s => `<article class="card">
      <h2>${esc(s.name)}</h2><span class="game">${esc(s.game)}</span>
      <span class="state ${s.state}"><i></i>${LABEL[s.state] || s.state}${s.until ? ' bis ' + esc(new Date(s.until).toLocaleString('de-DE', { weekday: 'short', hour: '2-digit', minute: '2-digit' })) : ''}${s.state === 'online' ? ` · ${s.players}${s.max ? ' / ' + s.max : ''} Spieler` : ''}</span>
      ${s.names && s.names.length ? `<ul class="names">${s.names.map(n => `<li>${esc(n)}</li>`).join('')}</ul>` : ''}
      ${s.address ? `<div class="join"><code>${esc(s.address)}</code><button type="button" data-copy="${esc(s.address)}">Kopieren</button>${s.steam_link ? `<a class="btn primary" href="${esc(s.steam_link)}">Beitreten</a>` : ''}</div>` : ''}
      ${s.lan_address ? `<div class="join lan"><span class="lanl">Du bist im Heimnetz, nimm diese Adresse:</span><code>${esc(s.lan_address)}</code><button type="button" data-copy="${esc(s.lan_address)}">Kopieren</button></div>` : ''}
      ${s.hint ? `<p class="hint">${esc(s.hint)}</p>` : ''}
      ${s.note ? `<p class="snote">${esc(s.note)}</p>` : ''}
      ${s.uptime ? uptimeBars(s.uptime, s.uptime_pct) : ''}
    </article>`).join('');
    const evs = d.events || [];
    $('#events').hidden = !evs.length && !d.calendar;
    const fmt = t => new Date(t).toLocaleString('de-DE', { weekday: 'short', day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' });
    $('#evList').innerHTML = evs.length ? evs.map(e => `<li class="${e.active ? 'now' : ''}"><b>${esc(e.name)}</b> <span class="muted">· ${esc(e.server)}</span><br>
      <span class="when">${e.active ? 'läuft gerade, bis ' + esc(fmt(e.end)) : esc(fmt(e.start)) + ' bis ' + esc(fmt(e.end))}${e.weekly ? ' · jede Woche' : ''}</span>${e.what ? `<br><span class="muted small">${esc(e.what)}</span>` : ''}</li>`).join('')
      : '<li class="muted">Gerade ist nichts geplant.</li>';
    if (d.calendar) { const c = $('#cal'); c.hidden = false; c.href = 'webcal://' + location.host + d.calendar; c.title = 'Oder diese Adresse im Kalender hinzufügen: ' + location.origin + d.calendar; }
    $('#upd').textContent = 'Stand ' + new Date(d.updated).toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' });
  }
  function uptimeBars(days, pct) {
    const start = new Date(); start.setHours(12, 0, 0, 0); start.setDate(start.getDate() - (days.length - 1));
    const bars = days.map(([up, cr], i) => {
      const d = new Date(start); d.setDate(start.getDate() + i);
      const day = d.toLocaleDateString('de-DE', { weekday: 'short', day: '2-digit', month: '2-digit' });
      const t = up < 0 ? `${day}: keine Daten` : `${day}: ${up} % online${cr ? `, ${cr} ${cr === 1 ? 'Absturz' : 'Abstürze'}` : ''}`;
      return `<i class="${up < 0 ? 'none' : cr ? 'crash' : ''}" style="--h:${Math.max(up, 0)}%" title="${esc(t)}"></i>`;
    }).join('');
    return `<div class="uptime"><div class="ubars" role="img" aria-label="Online in den letzten 30 Tagen">${bars}</div>
      <span class="ulabel">${pct != null ? String(pct).replace('.', ',') + ' % online' : ''} · letzte 30 Tage</span></div>`;
  }
  document.addEventListener('click', async e => {
    const b = e.target.closest('[data-copy]'); if (!b) return;
    try { await navigator.clipboard.writeText(b.dataset.copy); b.textContent = 'Kopiert'; setTimeout(() => { b.textContent = 'Kopieren'; }, 1500); } catch { /* ältere Browser: Text ist markierbar */ }
  });
  load(); setInterval(() => { if (!document.hidden) load(); }, 20000);
})();
