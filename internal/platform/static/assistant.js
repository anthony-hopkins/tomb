/* The assistant's script (spec 006, FR-064). Everything works without it:
   the panel is a details element, the forms post and the page shows the
   thread. This only saves the reload -- it asks with fetch and appends the
   answer in place -- and remembers whether the panel was open, so the
   conversation follows the member from page to page with the panel as they
   left it. Same origin only (connect-src 'self'). */
(function () {
  "use strict";

  var OPEN_KEY = "tomb.assistant.open";

  function remember(open) {
    try { localStorage.setItem(OPEN_KEY, open ? "1" : "0"); } catch (e) { /* private mode */ }
  }

  function wasOpen() {
    try { return localStorage.getItem(OPEN_KEY) === "1"; } catch (e) { return false; }
  }

  /** Builds one exchange the way the template does, from the JSON answer.
      The question is text; the answer is markup the server rendered with
      the same escaping renderer the page uses; a source is a link only
      when it is https. */
  function exchangeNode(ex) {
    var li = document.createElement("li");
    li.className = "exchange";
    var q = document.createElement("p");
    q.className = "exchange-q";
    q.textContent = ex.question;
    li.appendChild(q);
    var a = document.createElement("div");
    a.className = "exchange-a";
    a.innerHTML = ex.answer_html;
    li.appendChild(a);
    if (ex.sources && ex.sources.length) {
      var p = document.createElement("p");
      p.className = "exchange-sources";
      p.appendChild(document.createTextNode("Read: "));
      for (var i = 0; i < ex.sources.length; i++) {
        var s = ex.sources[i];
        if (!s.url || s.url.indexOf("https://") !== 0) continue;
        var link = document.createElement("a");
        link.href = s.url;
        link.rel = "noopener";
        link.textContent = s.title || s.url;
        p.appendChild(link);
        p.appendChild(document.createTextNode(" "));
      }
      li.appendChild(p);
    }
    return li;
  }

  function scopeOf(form) {
    return form.closest(".assistant-panel") || form.closest(".assistant-page") || document;
  }

  function noticeIn(scope, text) {
    var n = scope.querySelector(".assistant-notice");
    if (!n) {
      n = document.createElement("p");
      n.className = "notice assistant-notice";
      n.setAttribute("role", "status");
      var ask = scope.querySelector(".assistant-ask");
      if (ask) ask.parentNode.insertBefore(n, ask); else scope.appendChild(n);
    }
    n.textContent = text;
    n.hidden = !text;
  }

  function bindAsk(form, csrf) {
    form.addEventListener("submit", function (ev) {
      if (typeof fetch !== "function") return;
      ev.preventDefault();
      var scope = scopeOf(form);
      var box = form.querySelector("textarea");
      var button = form.querySelector("button[type=submit]");
      var wait = form.querySelector(".assistant-wait");
      var question = (box.value || "").trim();
      if (!question) return;
      box.disabled = true;
      button.disabled = true;
      if (wait) wait.hidden = false;
      noticeIn(scope, "");

      fetch(form.action, {
        method: "POST",
        credentials: "same-origin",
        headers: { "X-CSRF-Token": csrf, "Accept": "application/json", "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ q: question }).toString()
      }).then(function (res) {
        return res.json().then(function (data) { return { ok: res.ok, data: data }; });
      }).then(function (r) {
        if (!r.ok) {
          noticeIn(scope, r.data && r.data.error ? r.data.error : "The answer did not come back.");
          return;
        }
        var thread = scope.querySelector(".assistant-thread");
        var empty = scope.querySelector(".assistant-empty");
        if (empty) empty.remove();
        thread.appendChild(exchangeNode(r.data));
        box.value = "";
        var body = scope.querySelector(".assistant-body");
        if (body) body.scrollTop = body.scrollHeight;
      }).catch(function () {
        noticeIn(scope, "The answer did not come back. Check the connection and ask again.");
      }).then(function () {
        box.disabled = false;
        button.disabled = false;
        if (wait) wait.hidden = true;
        box.focus();
      });
    });
  }

  function bindNew(form, csrf) {
    form.addEventListener("submit", function (ev) {
      if (typeof fetch !== "function") return;
      ev.preventDefault();
      fetch(form.action, {
        method: "POST",
        credentials: "same-origin",
        headers: { "X-CSRF-Token": csrf, "Accept": "application/json" }
      }).then(function (res) {
        if (!res.ok) throw new Error("refused");
        // Every copy of the thread on the page: the panel's and the page's.
        var threads = document.querySelectorAll(".assistant-thread");
        for (var i = 0; i < threads.length; i++) threads[i].innerHTML = "";
        var notices = document.querySelectorAll(".assistant-notice");
        for (var j = 0; j < notices.length; j++) notices[j].hidden = true;
      }).catch(function () {
        form.submit();
      });
    });
  }

  function init() {
    var panel = document.getElementById("assistant");
    var csrf = panel ? panel.getAttribute("data-csrf") : "";
    if (!csrf) {
      var hidden = document.querySelector(".assistant-ask input[name=csrf_token]");
      csrf = hidden ? hidden.value : "";
    }
    if (panel) {
      if (wasOpen()) panel.open = true;
      panel.addEventListener("toggle", function () { remember(panel.open); });
    }
    var asks = document.querySelectorAll(".assistant-ask");
    for (var i = 0; i < asks.length; i++) bindAsk(asks[i], csrf);
    var news = document.querySelectorAll(".assistant-new");
    for (var j = 0; j < news.length; j++) bindNew(news[j], csrf);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
