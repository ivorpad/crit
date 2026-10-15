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
// window.crit.shared (waitForSession), window.crit.commentCard (via deps).
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

  // tip wraps a disabled control so the tooltip still shows (Safari drops
  // title on disabled buttons).
  function tip(doc, text) {
    var span = doc.createElement('span');
    span.className = 'crit-herdr-tip';
    span.title = text;
    return span;
  }

  function disabledButton(doc, label, tooltip, extraClass) {
    var wrap = tip(doc, tooltip);
    var btn = doc.createElement('button');
    btn.type = 'button';
    btn.className = 'btn btn-sm' + (extraClass ? ' ' + extraClass : '');
    btn.disabled = true;
    btn.setAttribute('aria-disabled', 'true');
    btn.textContent = label;
    wrap.appendChild(btn);
    return wrap;
  }

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
      header.insertBefore(disabledButton(doc, badge.action.label, M.STEP3_TOOLTIP, 'crit-herdr-action'), anchor);
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

    if (m.dialog) root.appendChild(buildDialogCard(m, deps, doc));
    if (m.ghost.state !== 'none') root.appendChild(buildGhostBlock(m, deps, doc));
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
      ex.appendChild(disabledButton(doc, 'Resume', m.tooltip, 'crit-herdr-action'));
      root.appendChild(ex);
    }
    root.appendChild(buildSaveBar(m, doc));
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

  // The dialog as a comment card the agent wrote: question text as the body,
  // options as disabled buttons (multi-select: disabled checkboxes plus a
  // disabled Submit), a disabled text input under the free-text option. An
  // unparsed menu (dialog_screen) renders as a monospace block instead.
  function buildDialogCard(m, deps, doc) {
    var d = m.dialog;
    var comment = {
      id: 'herdr-dialog:' + m.path,
      author: d.author,
      body: d.screen ? '' : d.text,
      created_at: '',
      replies: [],
      resolved: false,
      scope: 'file',
    };
    var parts = null;
    if (typeof deps.buildCommentCard === 'function') {
      parts = deps.buildCommentCard(comment, m.path, {
        wrapperClass: 'comment-block crit-herdr-dialog-block',
        cardClassExtra: 'crit-herdr-dialog',
        collapseDefault: false,
        showLineRef: false,
        showReplyInput: false,
      });
    }
    var wrapper, card;
    if (parts) {
      wrapper = parts.wrapper;
      card = parts.card;
      var headerLeft = card.querySelector('.comment-header-left');
      if (headerLeft) {
        var kind = doc.createElement('span');
        kind.className = 'crit-herdr-dialog-kind';
        kind.textContent = d.kind;
        headerLeft.appendChild(kind);
      }
    } else {
      wrapper = doc.createElement('div');
      wrapper.className = 'comment-block crit-herdr-dialog-block';
      card = doc.createElement('div');
      card.className = 'comment-card crit-herdr-dialog';
      var head = doc.createElement('div');
      head.className = 'comment-header';
      head.textContent = '@' + d.author + ' \u00b7 ' + d.kind;
      card.appendChild(head);
      if (d.text) {
        var body = doc.createElement('div');
        body.className = 'comment-body';
        body.textContent = d.text;
        card.appendChild(body);
      }
      wrapper.appendChild(card);
    }
    card.setAttribute('role', 'group');
    card.setAttribute('aria-label', d.author + ' ' + d.kind);

    if (d.screen) {
      var pre = doc.createElement('pre');
      pre.className = 'crit-herdr-screen';
      pre.textContent = d.screen;
      card.appendChild(pre);
    }

    if (d.options.length) {
      var opts = doc.createElement('div');
      opts.className = 'crit-herdr-dialog-options' + (d.multi ? ' crit-herdr-dialog-options--multi' : '');
      opts.setAttribute('role', 'group');
      opts.setAttribute('aria-label', 'Options');
      d.options.forEach(function (o) {
        var row = doc.createElement('div');
        row.className = 'crit-herdr-option' + (o.current ? ' current' : '');
        if (d.multi) {
          var lab = doc.createElement('label');
          lab.className = 'crit-herdr-option-check';
          lab.title = m.tooltip;
          var cb = doc.createElement('input');
          cb.type = 'checkbox';
          cb.disabled = true;
          cb.checked = o.checked;
          cb.setAttribute('aria-disabled', 'true');
          cb.dataset.n = String(o.n);
          lab.appendChild(cb);
          var txt = doc.createElement('span');
          txt.textContent = o.n + '. ' + o.label;
          lab.appendChild(txt);
          row.appendChild(lab);
        } else {
          var btn = disabledButton(doc, o.n + '. ' + o.label, m.tooltip, 'crit-herdr-option-btn' + (o.current ? ' current' : ''));
          btn.firstChild.dataset.n = String(o.n);
          row.appendChild(btn);
        }
        if (o.freeText) {
          var inWrap = tip(doc, m.tooltip);
          inWrap.className += ' crit-herdr-free-text';
          var input = doc.createElement('input');
          input.type = 'text';
          input.disabled = true;
          input.setAttribute('aria-disabled', 'true');
          input.setAttribute('aria-label', 'Free-text answer for option ' + o.n);
          input.placeholder = 'type your own answer';
          inWrap.appendChild(input);
          row.appendChild(inWrap);
        }
        opts.appendChild(row);
      });
      if (d.multi) {
        opts.appendChild(disabledButton(doc, d.submitLabel || 'Submit', m.tooltip, 'btn-primary crit-herdr-submit'));
      }
      card.appendChild(opts);
      if (d.goAhead !== null) {
        var ga = doc.createElement('div');
        ga.className = 'crit-herdr-dialog-note';
        ga.textContent = 'Go-ahead menu: option ' + d.goAhead + ' is the usual answer.';
        card.appendChild(ga);
      }
    }
    var note = doc.createElement('div');
    note.className = 'crit-herdr-dialog-note';
    note.textContent = 'Nothing is sent on click. Picks and replies queue until Finish Review (' + m.tooltip + ').';
    card.appendChild(note);
    return wrapper;
  }

  // The input line: dim when it is the agent's own suggestion, with a `use`
  // link that copies it into the pane's open comment form (never sends it).
  function buildGhostBlock(m, deps, doc) {
    var g = m.ghost;
    var block = doc.createElement('div');
    block.className = 'crit-herdr-ghost crit-herdr-ghost--' + g.state;
    var label = doc.createElement('span');
    label.className = 'crit-herdr-ghost-label';
    label.textContent = g.label;
    var text = doc.createElement('span');
    text.className = 'crit-herdr-ghost-text';
    text.textContent = '\u276f ' + g.text;
    var use = doc.createElement('button');
    use.type = 'button';
    use.className = 'crit-herdr-ghost-use';
    use.textContent = 'use';
    use.title = 'Copy this line into the comment form (it is not sent)';
    use.addEventListener('click', function () {
      useGhost(m.path, g.text, deps);
    });
    block.appendChild(label);
    block.appendChild(text);
    block.appendChild(use);
    return block;
  }

  // useGhost copies text into an open comment form on this pane, opening the
  // pane's file-level form first when none is open.
  function useGhost(path, text, deps) {
    var ta = findPaneTextarea(path);
    if (ta) { fillTextarea(ta, text); return; }
    if (deps && typeof deps.openFileCommentForm === 'function') {
      deps.openFileCommentForm(path);
      requestAnimationFrame(function () {
        requestAnimationFrame(function () {
          var again = findPaneTextarea(path);
          if (again) fillTextarea(again, text);
        });
      });
    }
  }

  function findPaneTextarea(path) {
    var focused = document.activeElement;
    if (focused && focused.tagName === 'TEXTAREA' && focused.closest('.comment-form')) return focused;
    var sel = '.comment-form textarea[data-form-key^="' + cssEscape(path + ':') + '"]';
    var list = document.querySelectorAll(sel);
    return list.length ? list[list.length - 1] : null;
  }

  function fillTextarea(ta, text) {
    var cur = ta.value;
    ta.value = cur && !/\s$/.test(cur) ? cur + '\n' + text : cur + text;
    ta.dispatchEvent(new Event('input', { bubbles: true }));
    ta.focus();
  }

  function cssEscape(s) {
    return (typeof CSS !== 'undefined' && CSS.escape) ? CSS.escape(s) : String(s).replace(/["\\]/g, '\\$&');
  }

  // Save as .md: two modes from the brief, both explicit clicks, both parked
  // until step 4. "Ask agent to write it" sends one prompt; "Save here"
  // downloads the rendered text with its comment thread.
  function buildSaveBar(m, doc) {
    var bar = doc.createElement('div');
    bar.className = 'crit-herdr-save';
    var label = doc.createElement('span');
    label.className = 'crit-herdr-save-label';
    label.textContent = m.plan ? m.plan.title + ' \u00b7 Save as .md' : 'Save as .md';
    bar.appendChild(label);
    if (m.plan && m.plan.path) {
      var p = doc.createElement('code');
      p.className = 'crit-herdr-save-path';
      p.textContent = m.plan.path;
      bar.appendChild(p);
    }
    if (m.plan && !m.plan.hasText && m.plan.error) {
      var err = doc.createElement('span');
      err.className = 'crit-herdr-save-error';
      err.textContent = 'not read: ' + m.plan.error;
      bar.appendChild(err);
    }
    var ask = disabledButton(doc, 'Ask agent to write it', m.tooltip);
    ask.title = 'One prompt asks the agent to save it to ' + (m.plan ? m.plan.defaultSavePath : 'docs/plans/<slug>.md') + ' in its cwd. ' + m.tooltip + '.';
    var here = disabledButton(doc, 'Save here', m.tooltip);
    here.title = 'Downloads the rendered text plus its comment thread. ' + m.tooltip + '.';
    bar.appendChild(ask);
    bar.appendChild(here);
    return bar;
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
      if (n.action) row.appendChild(disabledButton(document, n.action.label, model().STEP3_TOOLTIP, 'crit-herdr-action'));
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
    hasExtras: hasDialog,
    useGhost: useGhost,
    renderMachineNotices: renderMachineNotices,
    countLabel: countLabel,
    needsYou: needsYou,
  };
});
