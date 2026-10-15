// herdr-mode.js — the herdr control center's DOM pieces on the code-review
// page. A herdr session (review_type "herdr", started by `crit herdr`) is a
// normal files-mode review whose files are agent panes rendered as markdown
// by the server (internal/herdr/render.go); app.js renders them like any
// other document, so lines are commentable and comments land in the review
// file with the right line numbers. This module decorates those panes from
// GET /api/herdr: header badges, the agent-authored dialog card, ghost text,
// the plan controls, machine notices and the "k of N panes need you" count.
//
// Nothing here sends anything to a pane. Every button that would is
// disabled with the step-3 tooltip (window.crit.herdrModel.STEP3_TOOLTIP);
// in step 3 clicks queue until Finish Review.
//
// Contract: docs/herdr-api.md. Dependencies: window.crit.herdrModel,
// window.crit.herdrExtras (card, ghost line, save bar builders),
// window.crit.shared (waitForSession, showToast), window.crit.commentCard (via deps).
'use strict';
(function (root, factory) {
  var api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else {
    root.crit = root.crit || {};
    root.crit.herdr = api;
  }
})(typeof window !== 'undefined' ? window : globalThis, function () {

  function model() { return window.crit.herdrModel; }
  function shared() { return window.crit.shared; }
  function extras() { return window.crit.herdrExtras; }

  var state = {
    entered: false,
    snapshot: null,   // normalized (herdrModel.normalizeSnapshot)
    raw: null,        // the /api/herdr payload as received
    byPath: Object.create(null),
  };

  // ----- fetch -----

  // fetchSnapshot polls GET /api/herdr through the 503 the server returns
  // before its first sweep (docs/herdr-api.md). Throws on any other failure.
  async function fetchSnapshot() {
    var payload = await shared().waitForSession({ url: '/api/herdr', intervalMs: 500, maxWaitMs: 30000 });
    state.raw = payload;
    state.snapshot = model().normalizeSnapshot(payload);
    state.byPath = model().index(payload);
    renderStatus();
    return { snapshot: state.snapshot, byPath: state.byPath };
  }

  function itemFor(path) { return state.byPath[path] || null; }
  function machineFor(item) {
    return item ? model().machineByLabel(state.raw, item.machine) : null;
  }

  // ----- page chrome -----

  // enterMode runs once, when app.js learns the session is a herdr session:
  // loads the herdr stylesheet (normal reviews never fetch it), marks <html>,
  // and parks the submit until delivery exists.
  function enterMode() {
    if (state.entered) return;
    state.entered = true;
    document.documentElement.classList.add('crit-mode-herdr');
    var link = document.createElement('link');
    link.rel = 'stylesheet';
    link.href = 'style-herdr.css';
    document.head.appendChild(link);
    var finish = document.getElementById('finishBtn');
    if (finish) {
      finish.disabled = true;
      finish.setAttribute('aria-disabled', 'true');
      finish.title = model().STEP3_TOOLTIP;
      finish.textContent = 'Finish Review';
    }
  }

  // renderStatus fills the header notify slot with the sweep time.
  function renderStatus() {
    var notify = document.getElementById('headerNotify');
    if (notify && state.snapshot) {
      var when = state.snapshot.collectedAt ? state.snapshot.collectedAt.replace('T', ' ').replace(/\.\d+Z?$|Z$/, '') : '';
      var every = state.snapshot.intervalSeconds ? ' \u00b7 every ' + state.snapshot.intervalSeconds + 's' : '';
      notify.textContent = when ? 'swept ' + when + every : '';
      notify.classList.add('crit-herdr-sweep');
    }
  }

  // ----- header -----

  // decorateHeader adds "agent · title", the status badge and the badge's
  // disabled action (Resume / Wake) to a pane's crit file header, before the
  // Viewed checkbox. Idempotent per header element.
  function decorateHeader(header, item, doc) {
    doc = doc || document;
    if (!header || !item || header.querySelector('.crit-herdr-badge')) return;
    var M = model();
    var badge = M.badgeFor(item, machineFor(item));
    var name = header.querySelector('.file-header-name');
    var anchor = name ? name.nextSibling : null;

    var meta = doc.createElement('span');
    meta.className = 'crit-herdr-header-meta';
    meta.textContent = M.headerMeta(item);
    header.insertBefore(meta, anchor);

    var b = doc.createElement('span');
    b.className = 'file-header-badge crit-herdr-badge crit-herdr-badge-' + badge.cls;
    b.textContent = badge.label;
    b.title = 'herdr: ' + (item.herdr_status || item.status || 'unknown') + (item.attention ? ' \u00b7 attention: ' + item.attention : '');
    header.insertBefore(b, anchor);

    if (badge.action) {
      header.insertBefore(extras().disabledButton(doc, badge.action.label, M.STEP3_TOOLTIP, 'crit-herdr-action'), anchor);
    }

    // Focus is live: it moves the owner's local herdr TUI to this pane and
    // sends nothing to the agent (POST /api/herdr/focus).
    var req = M.focusRequest(item);
    if (req) {
      var focus = doc.createElement('button');
      focus.type = 'button';
      focus.className = 'btn btn-sm crit-herdr-focus';
      focus.textContent = 'Focus';
      focus.title = 'Focus this pane in herdr (sends nothing to the agent)';
      focus.setAttribute('aria-label', 'Focus pane in herdr');
      focus.addEventListener('click', function (e) {
        e.stopPropagation();
        e.preventDefault();
        focusPane(req, focus);
      });
      header.insertBefore(focus, anchor);
    }
  }

  function toast(message, kind) {
    var s = shared();
    if (s && typeof s.showToast === 'function') s.showToast(message, { kind: kind || 'info', timeout: kind === 'error' ? 8000 : 3000 });
    else if (kind === 'error') console.error(message);
  }

  // focusPane posts the request; a 502 carries herdr's stderr, which is the
  // most useful thing to show.
  async function focusPane(req, btn) {
    if (btn.disabled) return;
    btn.disabled = true;
    try {
      var res = await fetch(req.url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req.body),
      });
      if (!res.ok) {
        var text = (await res.text().catch(function () { return ''; })).trim();
        throw new Error(text || ('POST ' + req.url + ': ' + res.status));
      }
      toast('Focused ' + req.body.path.replace(/\.md$/, '') + ' in herdr', 'success');
    } catch (err) {
      toast('Focus failed: ' + (err && err.message ? err.message : String(err)), 'error');
    } finally {
      btn.disabled = false;
    }
  }

  // treeDotHTML is the needs-you dot for a tree row (empty when not needed).
  function treeDotHTML(item) {
    if (!model().needsYou(item)) return '';
    return '<span class="crit-herdr-needs-dot" role="img" aria-label="Needs you" title="Needs you"></span>';
  }

  // ----- pane extras: the block above a pane's document -----

  // buildPaneExtras returns the element Pierre mounts above the pane's
  // document: the dialog card (agent-authored), the input line (ghost text),
  // the plan's Save-as-.md controls, the resume line and the collector's
  // notes. null when the pane has none of those.
  //
  // deps: buildCommentCard(comment, filePath, opts) from app.js (so the card
  // is a real comment card), openFileCommentForm(path), escapeHtml.
  function buildPaneExtras(item, deps) {
    deps = deps || {};
    var doc = deps.document || document;
    var M = model();
    var m = M.paneExtrasModel(item, machineFor(item));
    if (!m) return null;

    var root = doc.createElement('div');
    root.className = 'crit-herdr-extras';
    root.dataset.filePath = m.path;

    if (m.dialog) root.appendChild(extras().buildDialogCard(m, deps, doc));
    if (m.ghost.state !== 'none') root.appendChild(extras().buildGhostBlock(m, deps, doc));
    if (m.resumeCommand) {
      var ex = doc.createElement('div');
      ex.className = 'crit-herdr-resume';
      var lbl = doc.createElement('span');
      lbl.className = 'crit-herdr-resume-label';
      lbl.textContent = 'The agent left the pane. Resume with';
      var code = doc.createElement('code');
      code.className = 'crit-herdr-resume-cmd';
      code.textContent = m.resumeCommand;
      ex.appendChild(lbl);
      ex.appendChild(code);
      ex.appendChild(extras().disabledButton(doc, 'Resume', m.tooltip, 'crit-herdr-action'));
      root.appendChild(ex);
    }
    root.appendChild(extras().buildSaveBar(m, doc));
    if (m.notes.length) {
      var notes = doc.createElement('ul');
      notes.className = 'crit-herdr-notes';
      m.notes.forEach(function (n) {
        var li = doc.createElement('li');
        li.textContent = n;
        notes.appendChild(li);
      });
      root.appendChild(notes);
    }
    return root;
  }

  // ----- tree -----

  // renderMachineNotices appends, under the file tree, one row per machine
  // that contributed no pane or answered with an error (paused box with a
  // disabled Wake, unreachable, error). Replaces any previous block.
  function renderMachineNotices(treeBody) {
    if (!treeBody) return;
    var old = treeBody.querySelector('.crit-herdr-machines');
    if (old) old.remove();
    if (!state.raw) return;
    var notices = model().machineNotices(state.raw);
    if (!notices.length) return;
    var box = document.createElement('div');
    box.className = 'crit-herdr-machines';
    var title = document.createElement('div');
    title.className = 'crit-herdr-machines-title';
    title.textContent = 'Machines';
    box.appendChild(title);
    notices.forEach(function (n) {
      var row = document.createElement('div');
      row.className = 'crit-herdr-machine crit-herdr-machine--' + n.state;
      var name = document.createElement('span');
      name.className = 'crit-herdr-machine-name';
      name.textContent = n.label + (n.box ? ' (' + n.box + ')' : '');
      var text = document.createElement('span');
      text.className = 'crit-herdr-machine-text';
      text.textContent = n.text;
      row.appendChild(name);
      row.appendChild(text);
      if (n.action) row.appendChild(extras().disabledButton(document, n.action.label, model().STEP3_TOOLTIP, 'crit-herdr-action'));
      box.appendChild(row);
    });
    treeBody.appendChild(box);
  }

  // ----- counts -----

  function countLabel() { return model().needsYouLabel(state.raw); }
  function needsYou(item) { return model().needsYou(item); }
  function hasDialog(item) { return !!model().paneExtrasModel(item, machineFor(item)); }

  return {
    state: state,
    fetchSnapshot: fetchSnapshot,
    enterMode: enterMode,
    itemFor: itemFor,
    decorateHeader: decorateHeader,
    treeDotHTML: treeDotHTML,
    buildPaneExtras: buildPaneExtras,
    focusPane: focusPane,
    hasExtras: hasDialog,
    useGhost: function (path, text, deps) { return extras().useGhost(path, text, deps); },
    renderMachineNotices: renderMachineNotices,
    countLabel: countLabel,
    needsYou: needsYou,
  };
});
