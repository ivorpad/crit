// herdr-mode.extras.js — DOM builders for the block above a herdr pane's
// document: the agent-authored dialog card, the input line (ghost text), the
// Save-as-.md bar, plus the disabled-button helper every parked control uses.
// herdr-mode.js composes these per pane (buildPaneExtras). Pure DOM: no
// fetch, no state; every string from a pane goes through textContent.
//
// Dependencies: none at load time; buildDialogCard takes app.js's
// buildCommentCard through deps. Mounted on window.crit.herdrExtras.
'use strict';
(function (root, factory) {
  var api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else {
    root.crit = root.crit || {};
    root.crit.herdrExtras = api;
  }
})(typeof window !== 'undefined' ? window : globalThis, function () {

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

  return {
    tip: tip,
    disabledButton: disabledButton,
    buildDialogCard: buildDialogCard,
    buildGhostBlock: buildGhostBlock,
    useGhost: useGhost,
    buildSaveBar: buildSaveBar,
  };
});
