/* Sprache / language.
 * The panel is written in German. For English, a dictionary (i18n/en.json)
 * is applied to the page as it renders: exact texts first, then patterns
 * for texts with numbers or names, then known phrases inside longer texts.
 * Elements with data-noi18n (consoles, file contents, names) stay as they are.
 */
(() => {
  const KEY = 'gp-lang';
  let pick = '';
  try { pick = localStorage.getItem(KEY) || ''; } catch (e) { /* private mode */ }
  const q = new URLSearchParams(location.search).get('lang');
  if (q === 'de' || q === 'en') pick = q;
  const auto = (navigator.languages || [navigator.language || 'de']).some(l => /^de\b/i.test(l)) ? 'de' : 'en';
  const lang = pick === 'de' || pick === 'en' ? pick : auto;
  const I = window.I18N = {
    lang, choice: pick || 'auto',
    locale: lang === 'en' ? 'en-GB' : 'de-DE',
    dec: lang === 'en' ? '.' : ',',
    ready: Promise.resolve(),
    t: s => s,
    set(choice) {
      try { if (choice === 'auto') localStorage.removeItem(KEY); else localStorage.setItem(KEY, choice); } catch (e) { /* egal */ }
      location.reload();
    }
  };
  document.documentElement.lang = lang;
  if (lang !== 'en') return;

  let exact = new Map(), html = new Map(), patterns = [], generic = [], inline = [], frag = null, fragMap = new Map();
  const norm = s => s.replace(/\s+/g, ' ').trim();
  function load(d) {
    for (const [k, v] of Object.entries(d.exact || {})) exact.set(k, v);
    // paragraphs with inline markup (<b>, <code>, links) as a whole
    for (const [k, v] of Object.entries(d.html || {})) html.set(k, v);
    const pat = l => (l || []).map(([re, to]) => [new RegExp('^' + re + '$', 'su'), to]);
    patterns = pat(d.patterns); generic = pat(d.generic);
    // short pieces with numbers anywhere in a text („3 Std.“)
    inline = (d.inline || []).map(([re, to]) => [new RegExp(re, 'gu'), to]);
    // phrases inside longer texts: longest first, whole words only
    // (single short words like „Läuft“ only as whole texts: inside sentences they mislead)
    const keys = [...exact.keys()].filter(k => !/^\d/.test(k) && (k.length >= 9 || (k.length >= 5 && /\s/.test(k)))).sort((a, b) => b.length - a.length);
    keys.forEach(k => fragMap.set(k, exact.get(k)));
    if (keys.length) {
      const esc = s => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
      frag = new RegExp('(?<![\\p{L}\\p{N}])(?:' + keys.map(esc).join('|') + ')(?![\\p{L}\\p{N}])', 'gu');
    }
  }
  const cache = new Map();
  function tr(s) {
    const n = norm(s);
    if (!n || !/[A-Za-zÄÖÜäöüß]/.test(n)) return s;
    if (cache.has(n)) return keep(s, cache.get(n));
    let out = exact.get(n);
    const byPattern = list => {
      for (const [re, to] of list) {
        if (re.test(n)) return n.replace(re, (...m) => to.replace(/\{(\d)\}/g, (_, i) => tr(m[+i + 1] || '')));
      }
    };
    let lead;
    if (out === undefined && (lead = /^([:·+,–] )(.+)$/su.exec(n))) out = lead[1] + tr(lead[2]);
    if (out === undefined) out = byPattern(patterns);
    // „a · b · c“: each part on its own
    if (out === undefined && n.includes(' · ')) out = n.split(' · ').map(tr).join(' · ');
    if (out === undefined) {
      let r = frag ? n.replace(frag, m => fragMap.get(m) ?? m) : n;
      for (const [re, to] of inline) r = r.replace(re, to);
      if (r !== n) out = r;
    }
    if (out === undefined) out = byPattern(generic);
    if (out === undefined) out = n;
    if (cache.size > 20000) cache.clear();
    cache.set(n, out);
    return keep(s, out);
  }
  // keep the surrounding spaces of the original text node
  const keep = (orig, t) => (/^\s/.test(orig) ? ' ' : '') + t + (/\s$/.test(orig) && orig.trim() ? ' ' : '');
  I.t = s => (typeof s === 'string' ? tr(s) : s);

  const ATTRS = ['placeholder', 'title', 'aria-label', 'alt', 'label'];
  const skip = el => el.closest && el.closest('[data-noi18n],script,style,textarea,code,pre,.mono-raw');
  function node(n) {
    if (n.nodeType === 3) {
      const p = n.parentElement;
      if (!p || skip(p)) return;
      const v = n.nodeValue, t = tr(v);
      if (t !== v) n.nodeValue = t;
      return;
    }
    if (n.nodeType !== 1 || skip(n)) return;
    const whole = el => { if (!el.firstElementChild || !html.size) return; const h = html.get(norm(el.innerHTML)); if (h !== undefined) el.innerHTML = h; };
    whole(n);
    for (const a of ATTRS) {
      const v = n.getAttribute(a);
      if (v) { const t = tr(v); if (t !== v) n.setAttribute(a, t); }
    }
    if (n.tagName === 'INPUT' && (n.type === 'submit' || n.type === 'button') && n.value) n.value = tr(n.value);
    const w = document.createTreeWalker(n, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
    let c;
    while ((c = w.nextNode())) {
      if (c.nodeType === 3) { const p = c.parentElement; if (p && !skip(p)) { const v = c.nodeValue, t = tr(v); if (t !== v) c.nodeValue = t; } }
      else if (!skip(c)) { whole(c); for (const a of ATTRS) { const v = c.getAttribute(a); if (v) { const t = tr(v); if (t !== v) c.setAttribute(a, t); } } }
    }
  }
  const base = (document.currentScript && document.currentScript.src) ? new URL('.', document.currentScript.src).href : '/';
  // hide the page until the dictionary is there, so German does not flash
  const st = document.createElement('style'); st.textContent = 'body{visibility:hidden}';
  document.head.appendChild(st);
  I.ready = fetch(base + 'i18n/en.json').then(r => r.json()).then(load).catch(() => {}).then(() => {
    const go = () => {
      node(document.body);
      document.title = tr(document.title);
      new MutationObserver(ms => {
        for (const m of ms) {
          if (m.type === 'characterData') { node(m.target); continue; }
          if (m.type === 'attributes') { const el = m.target, v = el.getAttribute(m.attributeName); if (v && !skip(el)) { const t = tr(v); if (t !== v) el.setAttribute(m.attributeName, t); } continue; }
          m.addedNodes.forEach(node);
        }
      }).observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ATTRS });
      new MutationObserver(() => { const t = tr(document.title); if (t !== document.title) document.title = t; }).observe(document.querySelector('title') || document.head, { childList: true, characterData: true, subtree: true });
      st.remove();
    };
    if (document.body) go(); else document.addEventListener('DOMContentLoaded', go);
  });
  // safety: never keep the page hidden
  setTimeout(() => st.remove(), 3000);
})();
