// Test frame for the dashboard ↔ add-on frame bridge. Records everything it
// gets on <body data-*> so a test can read it from the frame.
(function () {
  var host = window.parent, n = 0, pending = {}, ctx = { state: {}, settings: {} };
  var out = document.getElementById("out");
  function note(s) { out.textContent = (s + "\n" + out.textContent).slice(0, 2000); }
  function call(msg) {
    msg.id = "r" + ++n;
    return new Promise(function (resolve) { pending[msg.id] = resolve; host.postMessage(msg, "*"); });
  }
  function done(r) { document.body.dataset.lastResult = JSON.stringify({ ok: r.ok, error: r.error || "", blob: r.blob ? r.blob.size : 0 }); note("result " + document.body.dataset.lastResult); }
  window.addEventListener("message", function (e) {
    if (e.source !== host) return;
    var m = e.data || {};
    if (m.t === "init") {
      ctx = m;
      for (var k in m.theme || {}) if (k.indexOf("--") === 0) document.documentElement.style.setProperty(k, m.theme[k]);
      document.body.dataset.ready = "1";
      document.body.dataset.editing = m.editing ? "1" : "0";
      document.body.classList.toggle("editing", !!m.editing);
      document.body.dataset.inits = String(Number(document.body.dataset.inits || 0) + 1);
      size(m.size);
      note("init v" + m.v + " touch=" + m.touch + " keys=" + Object.keys(m.state || {}).join(","));
    } else if (m.t === "state") {
      ctx.state = m.state;
      document.body.dataset.states = String(Number(document.body.dataset.states || 0) + 1);
      note("state " + JSON.stringify(m.state).slice(0, 80));
    } else if (m.t === "size") {
      size(m.size);
    } else if (m.t === "theme") {
      note("theme");
    } else if (m.t === "result" && pending[m.id]) {
      pending[m.id](m);
      delete pending[m.id];
    }
  });
  function size(s) { if (s) { document.body.dataset.size = s.w + "x" + s.h; document.getElementById("size").textContent = s.w + "×" + s.h; } }
  document.getElementById("act").onclick = function () {
    call({ t: "action", name: (ctx.state && ctx.state.testAction) || "next", params: { from: "frame" } }).then(done);
  };
  document.getElementById("bad").onclick = function () { call({ t: "action", name: "not_declared", params: {} }).then(done); };
  document.getElementById("res").onclick = function () {
    call({ t: "resource", path: (ctx.state && ctx.state.testResource) || "res:test.png" }).then(function (r) {
      done(r);
      if (r.ok) document.getElementById("pic").src = URL.createObjectURL(r.blob);
    });
  };
  host.postMessage({ t: "log", text: "test frame loaded" }, "*");
  host.postMessage({ t: "ready" }, "*");
})();
