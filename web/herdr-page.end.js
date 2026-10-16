// herdr-page.end.js: the Exit / Close pane dialog of the /herdr page
// (web/herdr.html #endDlg). It says what the button will do, then POST
// /api/herdr/exit or /api/herdr/close, then turns into the receipt with the
// resume command to copy. The pane leaves the sweep on its own.
// Mounted on window.crit.herdrPageEnd.
// Dependencies: window.crit.herdrPageView.
(function () {
  'use strict';
  var PV = window.crit.herdrPageView;
  function $(id) { return document.getElementById(id); }

  // o: { item(path), name(path), post(url, body) -> {ok, status, body}, toast(msg, kind),
  //      ended(path) when the pane is gone, refresh() }
  function create(o) {
    var ending = null;
    function button(busy) {
      var close = ending && ending.kind === 'close';
      $('end-go').disabled = busy;
      $('end-go').textContent = busy ? (close ? 'Closing…' : 'Exiting…') : close ? 'Close pane' : 'Exit';
    }
    function open(kind, it) {
      ending = { kind: kind, path: it.path, done: false };
      $('end-title').textContent = kind === 'close' ? 'Close pane ' + it.pane_id + '?' : 'Exit ' + o.name(it.path) + '?';
      $('end-sub').textContent = [it.machine, it.pane_id, it.agent, it.herdr_status || it.status].filter(Boolean).join(' · ');
      $('end-body').innerHTML = PV.endHTML(kind, it);
      $('end-error').hidden = true;
      $('end-cancel').hidden = false;
      $('end-go').className = 'btn btn-danger';
      button(false);
      $('endDlg').showModal();
      $('end-cancel').focus();
    }
    function go() {
      var e = ending, it = e && o.item(e.path);
      if (!e || e.done) { $('endDlg').close(); return; }
      if (!it) { $('endDlg').close(); o.toast('That pane is not in the sweep any more.'); return; }
      button(true);
      $('end-error').hidden = true;
      // a dialog reopened for another pane meanwhile keeps its own text
      var fail = function (msg, resume) {
        if (ending !== e) { o.toast(msg, 'error'); return; }
        $('end-error').textContent = msg;
        $('end-error').hidden = false;
        if (resume && !$('end-body').querySelector('.hp-end-resume')) $('end-body').insertAdjacentHTML('beforeend', PV.resumeHTML(resume));
        button(false);
      };
      o.post('/api/herdr/' + e.kind, { path: e.path }).then(function (r) {
        if (!r.ok) { fail(r.body.error || 'HTTP ' + r.status, r.body.resume_command); return; }
        o.ended(e.path);
        o.refresh();
        if (ending !== e) { o.toast((e.kind === 'close' ? 'Closed ' : 'Exited ') + it.pane_id + '.', 'success'); return; }
        e.done = true;
        $('end-title').textContent = e.kind === 'close' ? 'Closed ' + it.pane_id : 'Exited ' + o.name(e.path);
        $('end-body').innerHTML = PV.endDoneHTML(e.kind, it, r.body);
        $('end-cancel').hidden = true;
        $('end-go').className = 'btn btn-primary';
        $('end-go').disabled = false;
        $('end-go').textContent = 'Done';
        $('end-go').focus();
      }, function (err) { fail((e.kind === 'close' ? 'Close' : 'Exit') + ' failed: ' + err.message); });
    }
    $('end-go').addEventListener('click', go);
    $('end-cancel').addEventListener('click', function () { $('endDlg').close(); });
    $('end-body').addEventListener('click', function (ev) {
      var b = ev.target.closest('[data-copy]');
      if (!b) return;
      navigator.clipboard.writeText(b.getAttribute('data-copy')).then(function () { o.toast('Copied.', 'success'); },
        function (err) { o.toast('Copy failed: ' + err.message, 'error'); });
    });
    return { open: open };
  }

  var api = { create: create };
  if (typeof window !== 'undefined') {
    window.crit = window.crit || {};
    window.crit.herdrPageEnd = api;
  }
  if (typeof module === 'object' && module.exports) module.exports = api;
})();
