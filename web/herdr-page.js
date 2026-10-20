// herdr-page.js: the /herdr page (web/herdr.html). Page B's shape: one pane
// fills the page, the strip keeps every pane in a fixed place, and after you
// send you move to the next pane that needs you, with a link back. A Claude
// pane reads from its transcript (GET /api/herdr/transcript, drawn through
// herdr-transcript-ctl.js); any other pane shows what the sweep read. One
// send per pane: Finish review shows the exact text, then POST
// /api/herdr/send. Contract: docs/herdr-api.md.
// Dependencies: window.crit.shared, sse, herdrModel, herdrPageModel,
// herdrPageView, herdrPageEnd, herdrPageChildren, herdrTranscriptParse, herdrTranscriptView,
// herdrTranscriptCtl.
(function () {
  'use strict';
  var C = window.crit, SH = C.shared, HM = C.herdrModel, PM = C.herdrPageModel, PV = C.herdrPageView, KC = C.herdrPageChildren;
  var TP = C.herdrTranscriptParse, TC = C.herdrTranscriptCtl, TM = C.herdrTranscriptMd, F = C.herdrTranscriptView.fmt;
  var STORE = 'crit-herdr:v1:';
  var main = document.getElementById('hpMain');
  function $(id) { return document.getElementById(id); }
  var S = {
    raw: null, snap: null, byPath: {}, machines: {}, order: [], states: {}, pass: null,
    current: null, previous: null, resume: null, confirming: null, confirmTimer: null,
    tx: {}, docs: {}, dots: {}, stripKey: '', refreshing: null, again: false, restore: null,
    closed: {}, // panes this page exited or closed: their leaving the sweep needs no toast
    child: null, // the open sub-agent of the current pane (herdr-page.children.js), or null
  };

  // ---------- the browser's own records: watch list, reply drafts, sent copies ----------
  function readJSON(key, dflt) {
    try { var v = JSON.parse(localStorage.getItem(STORE + key)); return v === null ? dflt : v; } catch (_) { return dflt; }
  }
  function writeJSON(key, v) { try { localStorage.setItem(STORE + key, JSON.stringify(v)); } catch (_) { /* storage full */ } }
  function watched(path) { return readJSON('watch', []).indexOf(path) >= 0; }
  function draft(path) { return readJSON('draft:' + path, ''); }
  function setDraft(path, text) {
    if (text) writeJSON('draft:' + path, text);
    else { try { localStorage.removeItem(STORE + 'draft:' + path); } catch (_) { /* ignore */ } }
  }
  function lastSent(path) { var l = readJSON('sent:' + path, []); return l.length ? l[l.length - 1] : null; }
  function addSent(path, rec) { var l = readJSON('sent:' + path, []); l.push(rec); writeJSON('sent:' + path, l.slice(-20)); }
  function toast(msg, kind) { SH.showToast(msg, { kind: kind || 'info', timeout: kind === 'error' ? 8000 : 3000 }); }

  // ---------- a pane as the views need it ----------
  function nameOf(path) {
    var t = S.tx[path], titles = t && t.model ? { customTitle: t.model.info.customTitle, aiTitle: t.model.info.aiTitle } : null;
    return PM.paneName(S.byPath[path], titles);
  }
  function pane(path) {
    var it = S.byPath[path];
    if (!it) return null;
    var m = S.machines[it.machine] || null, hasTx = PM.wantsTranscript(it);
    return {
      path: path, item: it, machine: m, name: nameOf(path), state: S.states[path] || 'read', came: PM.cameBack(it, lastSent(path), S.tx[path] && S.tx[path].model),
      watched: watched(path), hasTx: hasTx, why: hasTx ? '' : PM.whyNoTranscript(it, m), sent: lastSent(path),
    };
  }
  function storeKey(path) { var t = S.tx[path]; return path + '|' + (t && t.session ? t.session : 'none'); }
  // A transcript key is a pane path, or "<pane path>~<child id>" for one of the
  // pane's sub-agents; pane paths and child ids never hold a "~".
  function txKey() { return S.child ? S.current + '~' + S.child : S.current; }
  function paneOf(key) { var i = key.indexOf('~'); return i < 0 ? key : key.slice(0, i); }
  function childOf(key) { var i = key.indexOf('~'); return i < 0 ? '' : key.slice(i + 1); }
  // the sub-agent lists, one per lead pane, read when the pane is shown
  var kids = KC.create({ changed: function (path) { if (path === S.current && !S.confirming) { patchStrip(); renderMain(); } } });
  function kid(key) { var c = childOf(key); return c ? kids.child(paneOf(key), c) : null; }

  // ---------- the transcript area (shared controller) ----------
  var ctl = TC.create({
    root: main,
    current: function () {
      if (S.confirming || !S.current || !S.byPath[S.current]) return null;
      var key = txKey(), t = S.tx[key], it = S.byPath[S.current];
      return { pane: { id: key, store: storeKey(key) }, model: (S.child || PM.wantsTranscript(it)) && t && t.status === 'ok' ? t.shown : null };
    },
    title: function (p) { var ch = kid(p.id); return ch ? 'Ask ' + nameOf(paneOf(p.id)) + ' about ' + ch.name : PM.previewTitle(S.byPath[p.id]); },
    // a question about a sub-agent goes to its lead, saying which one it is about
    compose: function (list, extra) {
      var text = PM.compose(list, extra), ch = S.child && kids.child(S.current, S.child);
      return ch && text ? KC.prefix(ch) + '\n\n' + text : text;
    },
    receiptTo: function (p) { return PM.receiptTo(S.byPath[paneOf(p.id)]); },
    deliver: deliver,
    changed: function () { renderAll(); },
    sent: afterSend,
    toast: toast,
  });

  // the seq of what you were reading: a transcript pane's settled view, else the item
  function readSeq(path) {
    var t = S.tx[path], it = S.byPath[path];
    return t && t.status === 'ok' && typeof t.readSeq === 'number' ? t.readSeq : it.state_change_seq;
  }
  function deliver(p, text) {
    var it = S.byPath[paneOf(p.id)];
    if (!it || !HM.canSend(it)) return Promise.resolve({ ok: false, message: 'This pane cannot take text: it has no herdr state to send to.' });
    var seq = readSeq(paneOf(p.id));
    return fetch('/api/herdr/send', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ path: it.path, expect_state_change_seq: seq, text: text }) })
      .then(function (res) {
        return res.json().catch(function () { return null; }).then(function (body) {
          if (!res.ok) return { ok: false, status: res.status, message: HM.sendErrorMessage(res.status, body) };
          return { ok: true, at: Date.now(), body: body, completion_seq: it.completion_seq, state_change_seq: seq };
        });
      }, function (err) { return { ok: false, message: 'Send failed: ' + (err && err.message ? err.message : String(err)) }; });
  }
  function afterSend(p, info) {
    var path = paneOf(p.id);
    addSent(path, { at: info.at, text: info.text, completion_seq: info.res.completion_seq, state_change_seq: info.res.state_change_seq });
    // asking a lead about a sub-agent keeps you on the sub-agent
    if (info.tag === 'finish' && path === p.id) { setDraft(p.id, ''); handled(p.id, 'send'); }
    else {
      if (info.tag === 'finish') setDraft(p.id, '');
      toast('Sent to ' + info.to + ' at ' + F.hhmm(info.at) + '.', 'success'); renderAll();
    }
    refreshSnapshot();
  }

  // ---------- transcripts: read once, then only what was added ----------
  // the parser for each kind GET /api/herdr/transcript sends; a Claude transcript otherwise
  var PARSERS = {
    codex_rollout: C.herdrTranscriptCodex.parseRollout, pi_jsonl: C.herdrTranscriptPi.parseSession, opencode_sqlite: C.herdrTranscriptOpencode.parseSession,
    grok_jsonl: C.herdrTranscriptGrok.parseSession, cursor_jsonl: C.herdrTranscriptCursor.parseSession,
  };
  function seqsMoved(t, it) { return !!t.seen && (t.seen.c !== it.completion_seq || t.seen.s !== it.state_change_seq); }
  function ensureTranscript(key) {
    var it = S.byPath[paneOf(key)];
    if (!it || !(childOf(key) || PM.wantsTranscript(it))) return;
    var t = S.tx[key];
    if (!t) { S.tx[key] = { status: 'loading' }; loadTranscript(key, false); return; }
    if (t.status === 'error' && !t.inflight) { t.status = 'loading'; loadTranscript(key, false); return; }
    if (t.status === 'ok') {
      if (t.newer) settle(t);
      // lines added while you were elsewhere: fetch them and show them, no banner.
      // A sub-agent moves no seq of its pane's, so it is asked every time.
      if (childOf(key) || seqsMoved(t, it)) { t.settleNext = true; loadTranscript(key, true); }
    }
  }
  function settle(t) { t.shown = t.model; t.newer = false; t.readSeq = t.seen.s; }
  function loadTranscript(key, incremental) {
    var t = S.tx[key], it = S.byPath[paneOf(key)], child = childOf(key), path = key;
    if (!t || !it) return;
    if (t.inflight) { t.again = true; return; }
    var seqs = { c: it.completion_seq, s: it.state_change_seq }, t0 = performance.now();
    var url = '/api/herdr/transcript?path=' + encodeURIComponent(paneOf(key)) + (child ? '&child=' + encodeURIComponent(child) : '') +
      (incremental && t.version ? '&since=' + encodeURIComponent(t.version) : '');
    t.inflight = true;
    fetch(url).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (body) { return { res: res, body: body || {} }; });
    }).then(function (x) {
      t.inflight = false;
      if (!x.res.ok) {
        if (t.status !== 'ok') {
          t.status = 'error';
          t.error = x.res.status === 404 ? (x.body.reason || x.body.error || 'No transcript for this pane') + '.'
            : 'Could not read the transcript: ' + (x.body.error || 'HTTP ' + x.res.status) + '.';
        }
        return finishLoad(path);
      }
      var b = x.body, first = t.status !== 'ok', grew = false;
      if (b.from > 0 && !first) { if (b.jsonl) { t.text += b.jsonl; grew = true; } }
      else { grew = !first && t.text !== (b.jsonl || ''); t.text = b.jsonl || ''; }
      t.version = b.version; t.session = b.session_id; t.kind = b.kind; t.bytes = b.bytes; t.dropped = b.dropped_bytes || 0; t.fetchedAt = Date.parse(b.fetched_at) || Date.now(); t.ms = performance.now() - t0;
      if (first || grew) { var p0 = performance.now(); t.model = (PARSERS[t.kind] || TP.parseTranscript)(t.text); t.parseMs = performance.now() - p0; }
      t.status = 'ok';
      t.seen = seqs;
      // on screen, new lines wait for "Show them"; anywhere else (or right as
      // you open the pane) they just land
      if (!first && grew && txKey() === path && !t.settleNext) t.newer = true; else settle(t);
      t.settleNext = false;
      finishLoad(path);
    }, function (err) {
      t.inflight = false;
      if (t.status !== 'ok') { t.status = 'error'; t.error = 'Could not read the transcript: ' + (err && err.message ? err.message : String(err)) + '.'; }
      finishLoad(path);
    });
  }
  function finishLoad(path) {
    var t = S.tx[path];
    if (t && t.again) { t.again = false; loadTranscript(path, t.status === 'ok'); return; }
    if (S.current && txKey() === path && !S.confirming) { patchStrip(); renderTop(); renderMain(); }
    else patchStrip();
  }

  // ---------- the pass and moving between panes (page B) ----------
  function setCurrent(path) {
    S.current = path;
    if (path && S.pass && S.pass.ids.indexOf(path) >= 0) S.resume = path;
    if (path) ensureTranscript(path);
    var it = path && S.byPath[path];
    if (it && it.children && it.children.count) kids.load(path);
  }
  function cancelConfirm() {
    if (S.confirmTimer) clearTimeout(S.confirmTimer);
    S.confirmTimer = null;
    if (S.confirming) { S.current = S.confirming.from; S.confirming = null; }
  }
  function open(path, opts) {
    if (!S.byPath[path]) return;
    cancelConfirm();
    if (opts && opts.back) S.previous = null;
    else if (S.current && S.current !== path) S.previous = S.current;
    S.child = null;
    setCurrent(path);
    renderAll();
  }
  // one of the current pane's sub-agents, and back to the pane
  function openChild(id) {
    if (!S.current || S.confirming || !id) return;
    S.child = id;
    if (!kids.get(S.current)) kids.load(S.current);
    ensureTranscript(txKey());
    renderAll();
    main.scrollTop = 0;
  }
  function leadView() {
    if (!S.child) return;
    S.child = null;
    ensureTranscript(S.current);
    renderAll();
  }
  function copyText(text) {
    navigator.clipboard.writeText(text).then(function () { toast('Copied.', 'success'); }, function (err) { toast('Copy failed: ' + err.message, 'error'); });
  }
  function goBack() { if (S.previous) open(S.previous, { back: true }); }
  function step(d) {
    var here = S.confirming ? S.confirming.from : S.current, ids = S.order;
    var i = here ? ids.indexOf(here) : (d > 0 ? -1 : ids.length);
    var j = Math.max(0, Math.min(ids.length - 1, i + d));
    if (ids[j] && ids[j] !== here) open(ids[j]);
  }
  // "Sent to X ✓" for a moment, then the next pane that needs you, with Back to X
  function handled(path, kind) {
    cancelConfirm();
    PM.markHandled(S.pass, path);
    S.confirming = { kind: kind, from: path, name: nameOf(path), next: PM.queue(S.pass, path, S.resume)[0] || null };
    renderAll();
    S.confirmTimer = setTimeout(function () {
      var c = S.confirming;
      S.confirming = null; S.confirmTimer = null;
      if (!c) return;
      S.previous = c.from;
      setCurrent(c.next && S.byPath[c.next] ? c.next : null);
      renderAll();
    }, 1000);
  }
  function openFinish() {
    var it = S.current && S.byPath[S.current];
    if (!it || S.confirming) return;
    if (!HM.canSend(it)) { toast('This pane cannot take text right now.', 'error'); return; }
    ctl.preview(ctl.unresolved(), ($('replyText') || {}).value || '', 'finish');
  }
  function post(url, body) {
    return fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (b) { return { ok: res.ok, status: res.status, body: b || {} }; });
    });
  }
  function markRead() {
    var path = S.current, it = path && S.byPath[path];
    if (!it || S.confirming) return;
    if (it.viewed) { toast(nameOf(path) + ' is already read. N moves on.'); return; }
    post('/api/herdr/viewed', { path: path, viewed: true }).then(function (r) {
      if (!r.ok) { toast('Mark read failed: ' + (r.body.error || 'HTTP ' + r.status), 'error'); return; }
      it.viewed = true;
      S.states[path] = PM.stateOf(it);
      handled(path, 'read');
    }, function (err) { toast('Mark read failed: ' + err.message, 'error'); });
  }
  function focusPane() {
    var it = S.current && S.byPath[S.current];
    if (!it) return;
    post('/api/herdr/focus', { path: it.path }).then(function (r) {
      toast(r.ok ? 'Focused ' + PM.receiptTo(it) + ' in herdr.' : 'Focus failed: ' + (r.body.error || 'HTTP ' + r.status), r.ok ? 'success' : 'error');
    }, function (err) { toast('Focus failed: ' + err.message, 'error'); });
  }
  // Rename: the box shows the name herdr will get (its agent-name rule) before Save.
  var renaming = null;
  function openRename() {
    var it = S.current && S.byPath[S.current];
    if (!it || S.confirming) return;
    renaming = it.path;
    $('rn-title').textContent = 'Rename ' + PM.receiptTo(it) + ' in herdr';
    $('rn-input').value = it.name || PM.herdrName(nameOf(it.path));
    $('rn-clear').hidden = !it.name;
    showRenameResult();
    $('renameDlg').showModal();
    $('rn-input').select();
  }
  function showRenameResult() {
    var name = PM.herdrName($('rn-input').value);
    $('rn-result').textContent = name || '(nothing left: use a letter first)';
    $('rn-save').disabled = !name;
    $('rn-error').hidden = true;
  }
  function saveRename(clear) {
    var path = renaming, it = path && S.byPath[path], name = PM.herdrName($('rn-input').value);
    if (!it || (!clear && !name)) return;
    $('rn-save').disabled = true;
    post('/api/herdr/rename', clear ? { path: path, clear: true } : { path: path, name: name }).then(function (r) {
      if (!r.ok) { $('rn-error').textContent = 'herdr refused it: ' + (r.body.error || 'HTTP ' + r.status); $('rn-error').hidden = false; $('rn-save').disabled = false; return; }
      $('renameDlg').close();
      toast(clear ? 'Name cleared. It shows from the next sweep.' : 'Renamed to ' + name + '. It shows from the next sweep.', 'success');
    }, function (err) { $('rn-error').textContent = 'Rename failed: ' + err.message; $('rn-error').hidden = false; $('rn-save').disabled = false; });
  }
  // Exit and Close pane (herdr-page.end.js): a dialog, then the receipt.
  var endDlg = C.herdrPageEnd.create({
    item: function (path) { return S.byPath[path]; }, name: nameOf, post: post, toast: toast,
    ended: function (path) { S.closed[path] = true; }, refresh: refreshSnapshot,
  });
  function openEnd(kind) {
    var it = S.current && S.byPath[S.current];
    if (it && !S.confirming) endDlg.open(kind, it);
  }
  function toggleWatch() {
    var path = S.current;
    if (!path || S.confirming) return;
    var list = readJSON('watch', []), i = list.indexOf(path);
    if (i >= 0) list.splice(i, 1); else list.push(path);
    writeJSON('watch', list);
    toast(i >= 0 ? 'Stopped watching ' + nameOf(path) : 'Watching ' + nameOf(path) + ': its dot keeps a ★ in the strip');
    patchStrip(); renderMain();
  }
  function skip() {
    if (S.confirming) return;
    var nx = PM.queue(S.pass, S.current, S.resume)[0];
    if (!nx) { toast(S.current ? 'Nothing else in this pass to skip to.' : 'This pass is empty.'); return; }
    open(nx);
  }
  function startPass() {
    cancelConfirm();
    S.pass = PM.startPass(S.order, S.states);
    S.previous = null; S.resume = null;
    setCurrent(PM.firstToOpen(S.pass));
    renderAll();
  }
  function openArrival() {
    var info = PM.passInfo(S.pass);
    if (!info.arrivals.length) return;
    open(info.arrivals.filter(function (p) { return p !== S.current; })[0] || info.arrivals[0]);
  }
  function toggleDoc() {
    var path = S.current, d = S.docs[path] || (S.docs[path] = { open: false });
    d.open = !d.open;
    if (d.open && !d.html && !d.loading) {
      d.loading = true;
      fetch('/api/file?path=' + encodeURIComponent(path)).then(function (res) {
        if (!res.ok) throw new Error('HTTP ' + res.status);
        return res.json();
      }).then(function (b) { d.html = TM.md(b.content || ''); }, function (err) { d.error = 'Could not read the pane: ' + err.message; })
        .then(function () { d.loading = false; if (S.current === path) renderMain(); });
    }
    renderMain();
  }

  // ---------- where you were: the pane in the URL, the pass for this tab ----------
  // The URL names the pane (#local/wSJ:pD), so a reload, a link or a second
  // tab opens it. sessionStorage keeps the pass, Back, and how far down the
  // pane you had scrolled, for this tab only.
  var WHERE = STORE + 'where', rememberTimer = null;
  // the hash is "<pane>" or "<pane>~<sub-agent id>" (#local/wSJ:pR~tm:reader-a)
  function hashParts() {
    var h;
    try { h = decodeURI(location.hash.slice(1)); } catch (_) { return ['', '']; }
    var i = h.indexOf('~');
    return i < 0 ? [h, ''] : [h.slice(0, i), h.slice(i + 1)];
  }
  function hashPath() {
    var h = hashParts()[0];
    return !h ? '' : S.byPath[h + '.md'] ? h + '.md' : h;
  }
  function remember() {
    var here = S.confirming ? S.confirming.from : S.current;
    var url = location.pathname + location.search + (here ? '#' + encodeURI(here.replace(/\.md$/, '') + (S.child && !S.confirming ? '~' + S.child : '')) : '');
    if (url !== location.pathname + location.search + location.hash) history.replaceState(null, '', url);
    try {
      sessionStorage.setItem(WHERE, JSON.stringify({ current: here, previous: S.previous, resume: S.resume, pass: S.pass,
        scroll: S.restore || (here && main.getAttribute('data-view') === here ? { path: here, top: main.scrollTop } : null) }));
    } catch (_) { /* storage off */ }
  }
  function restore() {
    var saved = null;
    try { saved = JSON.parse(sessionStorage.getItem(WHERE)); } catch (_) { /* nothing saved */ }
    var keep = function (l) { return (Array.isArray(l) ? l : []).filter(function (p) { return !!S.byPath[p]; }); };
    if (saved && saved.pass && Array.isArray(saved.pass.ids)) S.pass = { ids: keep(saved.pass.ids), handled: keep(saved.pass.handled), arrivals: keep(saved.pass.arrivals) };
    if (saved && S.byPath[saved.previous]) S.previous = saved.previous;
    if (saved && S.byPath[saved.resume]) S.resume = saved.resume;
    var want = hashPath();
    var cur = S.byPath[want] ? want : saved && S.byPath[saved.current] ? saved.current : PM.firstToOpen(S.pass);
    if (want && !S.byPath[want]) toast(want + ' is not in the sweep any more.');
    setCurrent(cur);
    if (cur === want && hashParts()[1]) {
      S.child = hashParts()[1];
      if (!kids.get(cur)) kids.load(cur);
      ensureTranscript(txKey());
    }
    S.restore = saved && saved.scroll && saved.scroll.path === cur && saved.scroll.top > 0 ? saved.scroll : null;
  }
  // the scroll you had, once the pane is drawn in full (its transcript read)
  function restoreScroll() {
    var r = S.restore;
    if (!r || S.confirming || r.path !== S.current) { if (r && r.path !== S.current) S.restore = null; return; }
    var t = S.tx[S.current];
    if (PM.wantsTranscript(S.byPath[S.current]) && (!t || t.status === 'loading')) return;
    main.scrollTop = r.top;
    S.restore = null;
  }
  main.addEventListener('scroll', function () {
    if (!rememberTimer) rememberTimer = setTimeout(function () { rememberTimer = null; remember(); }, 250);
  });
  window.addEventListener('pagehide', function () { remember(); });
  window.addEventListener('hashchange', function () {
    var p = hashPath(), c = hashParts()[1];
    if (!p || !S.byPath[p] || (p === S.current && c === (S.child || ''))) return;
    open(p);
    if (c) openChild(c);
  });

  // ---------- finding a pane: / (or ⌘K, or Find), then type ----------
  var finding = { results: [], sel: 0 };
  function openFind() {
    if ($('findDlg').open) return;
    $('findInput').value = '';
    finding.sel = 0;
    renderFind();
    $('findDlg').showModal();
    $('findInput').focus();
  }
  function renderFind() {
    finding.results = PM.findPanes($('findInput').value, S.order.map(pane).filter(Boolean)).slice(0, 14);
    finding.sel = Math.max(0, Math.min(finding.sel, finding.results.length - 1));
    $('findList').innerHTML = PV.findHTML(finding.results, finding.sel, S.confirming ? S.confirming.from : S.current);
    $('findInput').setAttribute('aria-activedescendant', finding.results.length ? 'find-' + finding.sel : '');
    var on = $('findList').querySelector('[aria-selected="true"]');
    if (on) on.scrollIntoView({ block: 'nearest' });
  }
  function pickFind(path) { $('findDlg').close(); if (path && path !== S.current) open(path); }

  // ---------- drawing ----------
  function groups() {
    var labels = S.snap.machines.map(function (m) { return m.label; });
    S.order.forEach(function (p) { var m = S.byPath[p].machine; if (labels.indexOf(m) < 0) labels.push(m); });
    return labels.map(function (label) {
      return { machine: S.machines[label] || { label: label }, paths: S.order.filter(function (p) { return S.byPath[p].machine === label; }) };
    });
  }
  function buildStrip() {
    var key = S.order.join('|') + '#' + S.snap.machines.map(function (m) { return m.label + ':' + m.state; }).join(',');
    if (key === S.stripKey) return;
    S.stripKey = key;
    var strip = $('hpStrip');
    strip.innerHTML = PV.strip(groups(), HM.machineNotices(S.raw), S.byPath);
    S.dots = {};
    strip.querySelectorAll('.hp-fdot').forEach(function (b) { S.dots[b.getAttribute('data-path')] = b; });
  }
  function patchStrip() {
    var here = S.confirming ? S.confirming.from : S.current;
    S.order.forEach(function (path) {
      var b = S.dots[path], p = pane(path);
      if (!b || !p) return;
      var cls = 'hp-fdot' + (path === here ? ' hp-current' : '') + (p.watched ? ' hp-pinned' : '') + (p.came ? ' hp-came' : '');
      if (b.className !== cls) b.className = cls;
      var dc = 'hp-dot hp-' + p.state + ' hp-adot', mk = PM.agentMark(p.item.agent);
      if (b.firstChild.className !== dc) b.firstChild.className = dc;
      if (b.firstChild.textContent !== mk) b.firstChild.firstChild.textContent = mk;
      var bd = KC.badge(p.item.children), kb = b.querySelector('.hp-kidn');
      if (kb) {
        kb.hidden = !bd;
        if (bd && kb.textContent !== bd.text) kb.textContent = bd.text;
        if (bd) kb.className = 'hp-kidn' + (bd.warn ? ' hp-kidn-warn' : '');
      }
      var tip = (p.watched ? '★ ' : '') + p.name + ' · ' + (p.item.agent || 'no agent') + ' · ' + p.item.machine + ' ' + p.item.pane_id + ' · ' + PM.STATE_LABEL[p.state] +
        (p.came ? ' · came back, see New' : '') + (bd ? ' · ' + bd.tip : '') + (path === here ? ' · you are here' : '');
      b.setAttribute('data-tip', tip);
      b.setAttribute('aria-label', p.item.machine + ': ' + tip);
      if (path === here) b.setAttribute('aria-current', 'true'); else b.removeAttribute('aria-current');
    });
  }
  function nameButton(path) { var p = pane(path); return p ? PV.nameButton(path, p.name, p.state, p.item.machine) : ''; }
  function renderTop() {
    var info = PM.passInfo(S.pass), here = S.confirming ? S.confirming.from : S.current, p = here && pane(here), pos;
    var inPass = p && info.ids.indexOf(here) >= 0, arrived = p && info.arrivals.indexOf(here) >= 0;
    if (!p) pos = info.total ? 'This pass is empty' : 'Nothing in this pass';
    else if (arrived) pos = inPass ? 'Came back since you answered' : 'New since you started';
    else if (inPass) pos = 'Pane ' + (info.ids.indexOf(here) + 1) + ' of ' + info.total + ' in this pass' + (S.pass.handled.indexOf(here) >= 0 ? ' · done' : '');
    else pos = 'Not in this pass';
    $('hpPos').textContent = pos;
    $('hpFill').style.width = (info.total ? Math.round(100 * info.done / info.total) : 0) + '%';
    $('hpBar').setAttribute('aria-valuemax', String(info.total));
    $('hpBar').setAttribute('aria-valuenow', String(info.done));
    $('hpDone').textContent = info.done + ' of ' + info.total + ' done';
    var nx = PM.queue(S.pass, here, S.resume).slice(0, 2);
    $('hpUpNext').innerHTML = nx.length ? '<span>Up next:</span>' + nx.map(nameButton).join('<span class="hp-muted">,</span>') : (p ? '<span class="hp-muted">Up next: nothing, this is the last one</span>' : '');
    $('hpArrivals').hidden = info.arrivals.length === 0;
    $('hpArrivals').textContent = info.arrivals.length + ' new since you started';
    $('hpSweep').textContent = S.snap && S.snap.collectedAt ? 'sweep ' + F.dayTime(Date.parse(S.snap.collectedAt)) : '';
  }
  function txView(path) {
    var t = S.tx[path];
    if (!t || t.status !== 'ok') return t || null;
    return { status: 'ok', model: t.shown, kind: t.kind, bytes: t.bytes, sent: Math.max(0, (t.bytes || 0) - (t.dropped || 0)), fetchedAt: t.fetchedAt, ms: t.ms, newer: t.newer };
  }
  function renderMain() {
    var view = S.confirming ? 'confirm:' + S.confirming.from : (S.current ? txKey() : 'done');
    var same = main.getAttribute('data-view') === view;
    var ta = $('replyText'), active = document.activeElement;
    var taFocus = same && ta && active === ta, sel = ta ? [ta.selectionStart, ta.selectionEnd] : [0, 0];
    var focusId = same && !taFocus && active && active !== document.body && main.contains(active) ? active.id : '';
    // a comment form you are typing in keeps focus and caret across a redraw
    var formEl = same && !taFocus && active && active.tagName === 'TEXTAREA' && main.contains(active) ? active.closest('.ht-form') : null;
    var formFk = formEl ? formEl.getAttribute('data-fk') : '', formSel = formEl ? [active.selectionStart, active.selectionEnd] : null;
    var scroll = same ? main.scrollTop : 0, html;
    if (S.confirming) {
      var nx = S.confirming.next && pane(S.confirming.next);
      html = PV.confirmHTML(S.confirming, nx ? { name: nx.name, machine: nx.item.machine } : null);
    } else if (!S.current || !S.byPath[S.current]) {
      var info = PM.passInfo(S.pass), need = S.order.filter(function (p) { return PM.needsAttention(S.states[p]); }).length;
      html = PV.doneHTML(S.previous ? pane(S.previous) : null, info, need, info.arrivals.map(nameButton).join(', '));
    } else if (S.child) {
      var lead = pane(S.current), ch = kids.child(S.current, S.child), list = kids.get(S.current), key = txKey();
      html = ch ? KC.childHTML({ lead: lead, child: ch, tx: txView(key), stream: ctl.stream, draft: draft(key), open: TC.openCount(storeKey(key)) })
        : list && list.list ? KC.missingHTML(lead, S.child) : PV.messageHTML('Reading the sub-agents of ' + lead.name + '…', 'It takes a moment.', false);
    } else {
      var p = pane(S.current);
      html = PV.paneHTML(p, {
        prev: S.previous && S.previous !== S.current ? pane(S.previous) : null, open: TC.openCount(storeKey(S.current)), draft: draft(S.current),
        tx: p.hasTx ? txView(S.current) : null, stream: ctl.stream, docState: S.docs[S.current],
        kids: p.item.children && p.item.children.count ? KC.sectionHTML(kids.get(S.current) || { status: 'loading' }, p.name) : '',
      });
    }
    ctl.keepFrames(function () { main.innerHTML = html; }); // live preview frames survive the redraw
    main.setAttribute('data-view', view);
    main.scrollTop = scroll;
    var nta = $('replyText');
    if (nta && taFocus) { nta.focus({ preventScroll: true }); try { nta.setSelectionRange(sel[0], sel[1]); } catch (_) { /* ignore */ } }
    if (focusId && $(focusId)) $(focusId).focus({ preventScroll: true });
    var fta = formFk && main.querySelector('.ht-form[data-fk="' + CSS.escape(formFk) + '"] textarea');
    if (fta) { fta.focus({ preventScroll: true }); try { fta.setSelectionRange(formSel[0], formSel[1]); } catch (_) { /* ignore */ } }
    restoreScroll();
  }
  function renderAll() { patchStrip(); renderTop(); renderMain(); remember(); }

  // ---------- the sweep ----------
  function applySnapshot(payload) {
    var prevStates = S.states, prevCur = S.current && S.byPath[S.current], prevBack = S.previous && S.byPath[S.previous];
    S.raw = payload;
    S.snap = HM.normalizeSnapshot(payload);
    S.byPath = {}; S.snap.items.forEach(function (it) { if (typeof it.path === 'string' && it.path) S.byPath[it.path] = it; });
    S.machines = {}; S.snap.machines.forEach(function (m) { S.machines[m.label] = m; });
    S.order = PM.stripOrder(S.order, S.snap.machines, S.snap.items);
    S.states = {}; S.order.forEach(function (p) { S.states[p] = PM.stateOf(S.byPath[p]); });
    if (!S.pass) S.pass = PM.startPass(S.order, S.states); else PM.noteStates(S.pass, prevStates, S.states);
    Object.keys(S.tx).forEach(function (p) { if (!S.byPath[paneOf(p)]) delete S.tx[p]; });
    buildStrip();
    patchStrip();
    renderTop();
    if (S.current && !S.byPath[S.current]) {
      if (!S.closed[S.current]) toast(nameOf(S.current) + ' left herdr.');
      var nx = PM.queue(S.pass, S.current, S.resume).filter(function (q) { return S.byPath[q]; })[0] || null;
      S.current = null; S.child = null;
      setCurrent(nx);
      if (!S.confirming) renderMain();
      return;
    }
    var cur = S.current && S.byPath[S.current], t = S.current && S.tx[S.current];
    if (cur && t && t.status === 'ok' && seqsMoved(t, cur)) loadTranscript(S.current, true);
    // the pane's sub-agents and the open one: read again on every sweep
    if (cur && ((cur.children && cur.children.count) || S.child)) kids.load(S.current);
    var tc = S.child && S.tx[txKey()];
    if (tc && tc.status === 'ok') loadTranscript(txKey(), true);
    // panes you sent to: keep their transcript current, so came-back can read it
    S.order.forEach(function (q) {
      var tq = S.tx[q], iq = S.byPath[q];
      if (q !== S.current && tq && tq.status === 'ok' && !tq.inflight && iq.delivery && lastSent(q) && seqsMoved(tq, iq)) loadTranscript(q, true);
    });
    if (S.confirming) return;
    var back = S.previous && S.byPath[S.previous];
    if (!cur || JSON.stringify(cur) !== JSON.stringify(prevCur) || JSON.stringify(back) !== JSON.stringify(prevBack)) renderMain();
  }
  function refreshSnapshot() {
    if (S.refreshing) { S.again = true; return; }
    S.refreshing = fetch('/api/herdr').then(function (res) {
      if (res.status === 503) { setTimeout(refreshSnapshot, 1000); return null; }
      if (!res.ok) throw new Error('HTTP ' + res.status);
      return res.json();
    }).then(function (payload) { if (payload) applySnapshot(payload); }, function (err) {
      toast('Could not refresh the sweep: ' + err.message, 'error');
    }).then(function () {
      S.refreshing = null;
      if (S.again) { S.again = false; refreshSnapshot(); }
    });
  }
  function boot() {
    if (SH.applyThemeFromCookie) SH.applyThemeFromCookie();
    main.innerHTML = PV.messageHTML('Waiting for the first sweep…', 'The daemon reads every herdr machine before this page can show them; that takes a few seconds.', false);
    SH.waitForSession({ url: '/api/herdr', intervalMs: 500 }).then(function (payload) {
      applySnapshot(payload);
      restore();
      renderAll();
      if (C.sse) {
        C.sse.createSSE('/api/events', {
          'herdr-updated': function () { refreshSnapshot(); },
          'server-shutdown': function () { main.innerHTML = PV.messageHTML('The drover daemon stopped', 'Run drover herdr again, then reload this page.', false); },
        });
      }
    }, function (err) {
      if (err && err.status === 404) main.innerHTML = PV.messageHTML('This daemon is not a herdr session', 'Run drover herdr to start one, then open this page again.', false);
      else main.innerHTML = PV.messageHTML('Could not read the sweep', String(err && err.message ? err.message : err), true);
    });
  }

  // ---------- wiring ----------
  $('hpStrip').addEventListener('click', function (e) { var b = e.target.closest('.hp-fdot'); if (b) open(b.getAttribute('data-path')); });
  $('hpTop').addEventListener('click', function (e) {
    var t = e.target.closest('button');
    if (!t) return;
    if (t.id === 'hpArrivals') openArrival();
    else if (t.id === 'hpStartPass') startPass();
    else if (t.hasAttribute('data-go')) open(t.getAttribute('data-go'));
  });
  main.addEventListener('click', function (e) {
    var t = e.target.closest('button');
    if (!t || !main.contains(t)) return;
    if (t.id === 'backLink') goBack();
    else if (t.hasAttribute('data-go')) open(t.getAttribute('data-go'));
    else if (t.hasAttribute('data-child')) openChild(t.getAttribute('data-child'));
    else if (t.hasAttribute('data-lead')) leadView();
    else if (t.hasAttribute('data-copy')) copyText(t.getAttribute('data-copy'));
    else if (t.id === 'kidsRetry' && S.current) kids.load(S.current);
    else if (t.hasAttribute('data-focus')) focusPane();
    else if (t.id === 'finishReview') openFinish();
    else if (t.id === 'markRead') markRead();
    else if (t.id === 'skip') skip();
    else if (t.id === 'watch') toggleWatch();
    else if (t.id === 'rename') openRename();
    else if (t.id === 'exitPane') openEnd('exit');
    else if (t.id === 'closePane') openEnd('close');
    else if (t.id === 'toggleDoc') toggleDoc();
    else if (t.id === 'startPass') startPass();
    else if (t.id === 'bootRetry') boot();
    else if (t.id === 'txRetry' && S.current) { var tx = S.tx[S.current]; if (tx) { tx.status = 'loading'; renderMain(); } ensureTranscript(S.current); }
    else if (t.id === 'showNewer' && S.current && S.tx[S.current]) { settle(S.tx[S.current]); renderMain(); }
    else if (t.id === 'toReply') { var r = $('replyText'); if (r) { r.scrollIntoView({ block: 'center' }); r.focus({ preventScroll: true }); } }
  });
  main.addEventListener('input', function (e) { if (e.target.id === 'replyText' && S.current) setDraft(txKey(), e.target.value); });
  main.addEventListener('keydown', function (e) {
    if (e.target.id === 'replyText' && e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); openFinish(); }
  });
  document.addEventListener('keydown', function (e) {
    var t = e.target;
    if (document.querySelector('dialog[open]')) return;
    if ((e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === 'k') { e.preventDefault(); openFind(); return; }
    if (t && t.closest && t.closest('textarea, input, select, [contenteditable]')) { if (e.key === 'Escape' && t.blur) t.blur(); return; }
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    var k = e.key.length === 1 ? e.key.toLowerCase() : '';
    if (k === '/') { e.preventDefault(); openFind(); }
    else if (k === 'j' || k === 'k') { e.preventDefault(); step(k === 'j' ? 1 : -1); }
    else if (e.repeat) return;
    else if (k === 'n') { e.preventDefault(); skip(); }
    else if (k === 'r') { e.preventDefault(); markRead(); }
    else if (k === 'w') { e.preventDefault(); toggleWatch(); }
  });

  $('hpFindBtn').addEventListener('click', openFind);
  $('findInput').addEventListener('input', function () { finding.sel = 0; renderFind(); });
  $('findInput').addEventListener('keydown', function (e) {
    var n = finding.results.length;
    if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && n) { e.preventDefault(); finding.sel = (finding.sel + (e.key === 'ArrowDown' ? 1 : n - 1)) % n; renderFind(); }
    else if (e.key === 'Enter') { e.preventDefault(); var r = finding.results[finding.sel]; pickFind(r && r.path); }
  });
  $('findList').addEventListener('click', function (e) { var r = e.target.closest('[data-go]'); if (r) pickFind(r.getAttribute('data-go')); });
  $('findDlg').addEventListener('click', function (e) { if (e.target === $('findDlg')) $('findDlg').close(); });

  $('rn-input').addEventListener('input', showRenameResult);
  $('rn-input').addEventListener('keydown', function (e) { if (e.key === 'Enter') { e.preventDefault(); saveRename(false); } });
  $('rn-save').addEventListener('click', function () { saveRename(false); });
  $('rn-clear').addEventListener('click', function () { saveRename(true); });
  $('rn-cancel').addEventListener('click', function () { $('renameDlg').close(); });


  boot();
  window.crit.herdrPage = { state: S, refresh: refreshSnapshot };
})();
