// herdr-mode.model.js — pure model for the herdr control center (a code-review
// session with review_type "herdr": one markdown file per agent pane, served
// by the normal review page, described by GET /api/herdr).
//
// No DOM, no fetch: everything here turns the /api/herdr payload
// ({collected_at, interval_seconds, machines[], items[], need_you}) into what
// herdr-mode.js and app.js render. Items are internal/herdr viewItem: Item
// plus `path` ("<machine>/<pane id>.md", the review file) and `viewed` (the
// owner marked the pane's current turn read; the server keeps that mark).
//
// Mounted on window.crit.herdrModel; required directly by the Node tests.
// Dependencies: none.
'use strict';
(function (root, factory) {
  var api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else {
    root.crit = root.crit || {};
    root.crit.herdrModel = api;
  }
})(typeof window !== 'undefined' ? window : globalThis, function () {

  // Every control that would send something to a pane is disabled with this
  // tooltip until delivery exists (step 3 of the plan).
  var STEP3_TOOLTIP = 'delivery arrives in step 3';

  function isObject(v) { return v !== null && typeof v === 'object'; }

  // normalizeSnapshot accepts the /api/herdr payload, a bare item array, or
  // junk and always returns the same shape.
  function normalizeSnapshot(payload) {
    var out = { collectedAt: '', intervalSeconds: 0, machines: [], items: [], needYou: 0, error: '' };
    if (Array.isArray(payload)) {
      out.items = payload.filter(isObject);
      out.needYou = out.items.filter(needsYou).length;
      return out;
    }
    if (!isObject(payload)) return out;
    out.collectedAt = typeof payload.collected_at === 'string' ? payload.collected_at : '';
    out.intervalSeconds = typeof payload.interval_seconds === 'number' ? payload.interval_seconds : 0;
    out.machines = Array.isArray(payload.machines) ? payload.machines.filter(isObject) : [];
    out.items = Array.isArray(payload.items) ? payload.items.filter(isObject) : [];
    out.needYou = typeof payload.need_you === 'number' ? payload.need_you : out.items.filter(needsYou).length;
    out.error = typeof payload.instabox_error === 'string' ? payload.instabox_error : '';
    return out;
  }

  // index: review path -> item, so app.js can look a pane up by file path.
  function index(payload) {
    var byPath = Object.create(null);
    normalizeSnapshot(payload).items.forEach(function (it) {
      if (typeof it.path === 'string' && it.path) byPath[it.path] = it;
    });
    return byPath;
  }

  // A pane needs the owner when it asks something and its current turn is
  // not marked read.
  function needsYou(item) {
    return !!(item && item.needs_you && !item.viewed);
  }

  function needsYouCount(payload) {
    var items = normalizeSnapshot(payload).items;
    return { needs: items.filter(needsYou).length, total: items.length };
  }

  // "k of N panes need you" for the header slot that shows "n / N files viewed".
  function needsYouLabel(payload) {
    var c = needsYouCount(payload);
    if (c.total === 0) return 'no panes';
    return c.needs + ' of ' + c.total + ' pane' + (c.total === 1 ? '' : 's') + ' need' + (c.needs === 1 ? 's' : '') + ' you';
  }

  // machineIsPaused: the collector does not contact a suspended instabox, so
  // the box shows up only as a machine with state "paused" and no items.
  function machineIsPaused(machine) {
    if (!isObject(machine)) return false;
    if (machine.state === 'paused') return true;
    var bs = machine.instabox_state;
    return bs === 'suspended' || bs === 'paused';
  }

  function machineByLabel(payload, label) {
    var ms = normalizeSnapshot(payload).machines;
    for (var i = 0; i < ms.length; i++) if (ms[i].label === label) return ms[i];
    return null;
  }

  // machineNotices: machines the tree would otherwise not show because they
  // contributed no pane (paused, unreachable, error, or simply empty), plus
  // reachable machines that answered with a partial error. Each notice names
  // the disabled action the owner will get in a later step.
  function machineNotices(payload) {
    var snap = normalizeSnapshot(payload);
    var paneCount = Object.create(null);
    snap.items.forEach(function (it) {
      var label = typeof it.machine === 'string' ? it.machine : '';
      paneCount[label] = (paneCount[label] || 0) + 1;
    });
    var out = [];
    snap.machines.forEach(function (m) {
      if (typeof m.label !== 'string' || !m.label) return;
      var panes = paneCount[m.label] || 0;
      var paused = machineIsPaused(m);
      var error = m.error || m.gateway_error || m.discovery_error || '';
      var state = paused ? 'paused' : (m.state || 'reachable');
      if (!paused && state === 'reachable' && !error && panes > 0) return;
      var text;
      if (paused) text = 'box paused, not contacted';
      else if (state === 'unreachable') text = 'unreachable' + (error ? ': ' + error : '');
      else if (state === 'error') text = 'error' + (error ? ': ' + error : '');
      else if (error) text = error;
      else text = 'no agent panes';
      out.push({
        label: m.label,
        box: typeof m.instabox === 'string' ? m.instabox : '',
        state: state,
        text: text,
        panes: panes,
        action: paused ? { label: 'Wake', kind: 'wake' } : null,
      });
    });
    return out;
  }

  // badgeFor maps an item (plus its machine row, when known) to one badge.
  // Priority: paused box > exited > unreadable > blocked (menu open) >
  // question > working > background > idle/done > unknown. `action` is the
  // disabled button the header shows next to the badge.
  function badgeFor(item, machine) {
    item = item || {};
    if (machineIsPaused(machine) || item.status === 'paused_box') {
      return { key: 'paused', label: 'box paused', cls: 'paused', action: { label: 'Wake', kind: 'wake' } };
    }
    if (item.status === 'exited') {
      return {
        key: 'exited', label: 'exited', cls: 'exited',
        action: { label: 'Resume', kind: 'resume', command: item.resume_command || '' },
      };
    }
    if (item.status === 'unreadable_by_gateway') {
      return { key: 'unreadable', label: 'unreadable', cls: 'unreadable', action: null };
    }
    if (item.status === 'blocked' || item.attention === 'dialog' || isObject(item.dialog) || item.dialog_screen) {
      return { key: 'blocked', label: 'blocked', cls: 'blocked', action: null };
    }
    if (item.attention === 'question') {
      return { key: 'question', label: 'question', cls: 'question', action: null };
    }
    if (item.status === 'working') {
      return { key: 'working', label: 'working', cls: 'working', action: null };
    }
    if (item.status === 'background') {
      return { key: 'background', label: 'background', cls: 'background', action: null };
    }
    if (item.status === 'idle' || item.status === 'done') {
      return { key: 'idle', label: item.status, cls: 'idle', action: null };
    }
    return { key: 'unknown', label: item.status || 'unknown', cls: 'unknown', action: null };
  }

  // paneTitleParts: "machine · pane · agent · title" with empty parts dropped.
  // The file header already shows the path (machine/pane.md), so the meta
  // line under it is agent · title.
  function paneTitleParts(item) {
    item = item || {};
    return [item.machine, item.pane_id, item.agent, item.title || item.name]
      .filter(function (p) { return typeof p === 'string' && p.trim(); });
  }
  function paneTitle(item) { return paneTitleParts(item).join(' · '); }
  function headerMeta(item) {
    item = item || {};
    return [item.agent, item.title || item.name]
      .filter(function (p) { return typeof p === 'string' && p.trim(); })
      .join(' · ');
  }

  // dialogCardModel turns item.dialog (or dialog_screen) into what the
  // agent-authored card renders. null when the pane has no dialog.
  function dialogCardModel(item) {
    item = item || {};
    var d = item.dialog;
    if (isObject(d)) {
      var options = (Array.isArray(d.options) ? d.options : []).map(function (o, i) {
        o = isObject(o) ? o : {};
        return {
          n: typeof o.n === 'number' ? o.n : i + 1,
          label: typeof o.label === 'string' ? o.label : String(o.n || i + 1),
          current: !!o.current,
          checked: !!o.checked,
          freeText: !!o.free_text,
        };
      });
      var multi = !!d.multi;
      return {
        author: item.agent || 'agent',
        dialogId: d.dialog_id || '',
        kind: d.kind || 'question',
        text: typeof d.text === 'string' ? d.text : '',
        options: options,
        multi: multi,
        freeText: !!d.free_text || options.some(function (o) { return o.freeText; }),
        goAhead: typeof d.go_ahead === 'number' ? d.go_ahead : null,
        submitLabel: multi ? 'Submit' : null,
        screen: null,
        tooltip: STEP3_TOOLTIP,
      };
    }
    if (typeof item.dialog_screen === 'string' && item.dialog_screen.trim()) {
      return {
        author: item.agent || 'agent',
        dialogId: '', kind: 'unparsed', text: '', options: [], multi: false, freeText: false,
        goAhead: null, submitLabel: null, screen: item.dialog_screen, tooltip: STEP3_TOOLTIP,
      };
    }
    return null;
  }

  // classifyGhost reads item.input_line. Ghost text is a suggestion the agent
  // drew dim (ESC[2m): shown, never sent. null ghost means the collector could
  // not tell (no ANSI read), so the line is shown as unverified.
  function classifyGhost(inputLine) {
    if (!isObject(inputLine) || typeof inputLine.text !== 'string' || !inputLine.text.trim()) {
      return { state: 'none', label: '', text: '' };
    }
    if (inputLine.ghost === true) return { state: 'ghost', label: 'suggested by agent', text: inputLine.text };
    if (inputLine.ghost === false) return { state: 'typed', label: 'typed, not sent', text: inputLine.text };
    return { state: 'unverified', label: 'unverified', text: inputLine.text };
  }

  // planInfo: the plan named on an ExitPlanMode dialog. render.go inlines it
  // in the pane file under "## Plan", so its lines are commentable there;
  // this only feeds the Save-as-.md controls and the tree label.
  function planInfo(item) {
    item = item || {};
    var p = item.plan;
    if (!isObject(p)) return null;
    var path = typeof p.path === 'string' ? p.path : '';
    var slug = path.split('/').pop().replace(/\.md$/, '');
    return {
      path: path,
      slug: slug,
      title: 'plan: ' + (item.title || item.name || slug || 'plan'),
      hasText: typeof p.text === 'string' && p.text.trim() !== '',
      error: typeof p.error === 'string' ? p.error : '',
      defaultSavePath: 'docs/plans/' + (slug || 'plan') + '.md',
    };
  }

  // paneExtrasModel: everything the block above a pane's document shows.
  // null when there is nothing to show for this pane.
  function paneExtrasModel(item, machine) {
    if (!isObject(item)) return null;
    var badge = badgeFor(item, machine);
    var m = {
      path: item.path || '',
      badge: badge,
      dialog: dialogCardModel(item),
      ghost: classifyGhost(item.input_line),
      plan: planInfo(item),
      resumeCommand: badge.key === 'exited' ? (item.resume_command || '') : '',
      notes: Array.isArray(item.notes) ? item.notes.filter(function (n) { return typeof n === 'string' && n; }) : [],
      tooltip: STEP3_TOOLTIP,
    };
    if (!m.dialog && m.ghost.state === 'none' && !m.plan && !m.resumeCommand && !badge.action) return null;
    return m;
  }

  return {
    STEP3_TOOLTIP: STEP3_TOOLTIP,
    normalizeSnapshot: normalizeSnapshot,
    index: index,
    needsYou: needsYou,
    needsYouCount: needsYouCount,
    needsYouLabel: needsYouLabel,
    machineIsPaused: machineIsPaused,
    machineByLabel: machineByLabel,
    machineNotices: machineNotices,
    badgeFor: badgeFor,
    paneTitleParts: paneTitleParts,
    paneTitle: paneTitle,
    headerMeta: headerMeta,
    dialogCardModel: dialogCardModel,
    classifyGhost: classifyGhost,
    planInfo: planInfo,
    paneExtrasModel: paneExtrasModel,
  };
});
