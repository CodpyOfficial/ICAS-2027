// ICAS site behaviour: navigation, carousel, tabs, countdown and form helpers.
// Everything here is progressive enhancement; pages work without JavaScript.
(function () {
  "use strict";

  var root = document.documentElement;
  root.classList.remove("no-js");
  root.classList.add("js");

  var reduceMotion = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  function $(sel, ctx) { return (ctx || document).querySelector(sel); }
  function $all(sel, ctx) { return Array.prototype.slice.call((ctx || document).querySelectorAll(sel)); }

  // ---- header height drives scroll-padding so anchors are not hidden ----
  var header = $(".site-header");
  function syncHeader() {
    if (header) root.style.setProperty("--header-h", header.offsetHeight + "px");
  }
  syncHeader();
  window.addEventListener("resize", syncHeader);

  // ---- mobile navigation ----
  var toggle = $(".nav-toggle");
  var nav = $("#site-nav");
  if (toggle && nav) {
    toggle.addEventListener("click", function () {
      var open = nav.classList.toggle("is-open");
      toggle.setAttribute("aria-expanded", String(open));
      toggle.querySelector(".nav-toggle-label").textContent = open ? "Close" : "Menu";
    });
  }

  // ---- sub-menus: hover on desktop is pure CSS; buttons serve touch & keyboard ----
  var subToggles = $all(".sub-toggle");
  function closeAll(except) {
    subToggles.forEach(function (b) {
      var li = b.closest("li");
      if (li !== except) {
        li.classList.remove("is-open");
        b.setAttribute("aria-expanded", "false");
      }
    });
  }
  subToggles.forEach(function (btn) {
    btn.addEventListener("click", function (e) {
      e.stopPropagation();
      var li = btn.closest("li");
      var open = !li.classList.contains("is-open");
      closeAll(li);
      li.classList.toggle("is-open", open);
      btn.setAttribute("aria-expanded", String(open));
    });
  });
  document.addEventListener("click", function (e) {
    if (!e.target.closest(".menu")) closeAll(null);
  });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") {
      var openLi = $(".menu > li.is-open");
      closeAll(null);
      if (openLi) {
        var b = openLi.querySelector(".sub-toggle");
        if (b) b.focus();
      }
    }
  });

  // ---- carousel ----
  $all("[data-carousel]").forEach(function (carousel) {
    var slides = $all(".carousel-slide", carousel);
    if (slides.length < 2) return;
    var dotsWrap = $(".carousel-dots", carousel);
    var index = 0;
    var timer = null;
    var dots = slides.map(function (slide, i) {
      var b = document.createElement("button");
      b.type = "button";
      b.setAttribute("aria-label", "Show slide " + (i + 1) + " of " + slides.length);
      b.addEventListener("click", function () { show(i); restart(); });
      dotsWrap.appendChild(b);
      return b;
    });
    function show(i) {
      index = (i + slides.length) % slides.length;
      slides.forEach(function (s, j) {
        var active = j === index;
        s.classList.toggle("is-active", active);
        s.setAttribute("aria-hidden", String(!active));
        $all("a, button", s).forEach(function (el) { el.tabIndex = active ? 0 : -1; });
      });
      dots.forEach(function (d, j) { d.setAttribute("aria-current", String(j === index)); });
    }
    function stop() { if (timer) { clearInterval(timer); timer = null; } }
    function start() { if (!reduceMotion && !timer) timer = setInterval(function () { show(index + 1); }, 7000); }
    function restart() { stop(); start(); }
    var prev = $(".carousel-prev", carousel);
    var next = $(".carousel-next", carousel);
    if (prev) prev.addEventListener("click", function () { show(index - 1); restart(); });
    if (next) next.addEventListener("click", function () { show(index + 1); restart(); });
    carousel.addEventListener("mouseenter", stop);
    carousel.addEventListener("mouseleave", start);
    carousel.addEventListener("focusin", stop);
    carousel.addEventListener("focusout", start);
    show(0);
    start();
  });

  // ---- tabs (WAI-ARIA tabs pattern) ----
  $all("[data-tabs]").forEach(function (wrap) {
    var tabs = $all('[role="tab"]', wrap);
    function select(tab, focus) {
      tabs.forEach(function (t) {
        var on = t === tab;
        t.setAttribute("aria-selected", String(on));
        t.tabIndex = on ? 0 : -1;
        var panel = document.getElementById(t.getAttribute("aria-controls"));
        if (panel) panel.hidden = !on;
      });
      if (focus) tab.focus();
    }
    tabs.forEach(function (t, i) {
      t.addEventListener("click", function () { select(t, false); });
      t.addEventListener("keydown", function (e) {
        var j = null;
        if (e.key === "ArrowRight") j = (i + 1) % tabs.length;
        if (e.key === "ArrowLeft") j = (i - 1 + tabs.length) % tabs.length;
        if (e.key === "Home") j = 0;
        if (e.key === "End") j = tabs.length - 1;
        if (j !== null) { e.preventDefault(); select(tabs[j], true); }
      });
    });
    if (tabs.length) select(tabs[0], false);
  });

  // ---- countdown ----
  $all("[data-countdown]").forEach(function (el) {
    var target = new Date(el.getAttribute("data-countdown") + "T09:00:00");
    if (isNaN(target.getTime())) return;
    var parts = {
      d: $("[data-unit=d]", el), h: $("[data-unit=h]", el),
      m: $("[data-unit=m]", el), s: $("[data-unit=s]", el)
    };
    function pad(n) { return (n < 10 ? "0" : "") + n; }
    function tick() {
      var diff = Math.max(0, target.getTime() - Date.now());
      var sec = Math.floor(diff / 1000);
      parts.d.textContent = Math.floor(sec / 86400);
      parts.h.textContent = pad(Math.floor(sec % 86400 / 3600));
      parts.m.textContent = pad(Math.floor(sec % 3600 / 60));
      parts.s.textContent = pad(sec % 60);
    }
    tick();
    el.hidden = false;
    setInterval(tick, 1000);
  });

  // ---- word counters for limited fields ----
  function countWords(text) {
    var t = text.trim();
    return t ? t.split(/\s+/).length : 0;
  }
  $all("[data-max-words]").forEach(function (field) {
    var max = parseInt(field.getAttribute("data-max-words"), 10);
    var out = document.getElementById(field.id + "-count");
    if (!out) return;
    function update() {
      var n = countWords(field.value);
      out.textContent = n + " / " + max + " words";
      out.classList.toggle("is-over", n > max);
      field.setCustomValidity(n > max ? "Please shorten this to " + max + " words or fewer." : "");
    }
    field.addEventListener("input", update);
    update();
  });

  // ---- submission form: show only the fields for the chosen track ----
  var form = $("#proposal-form");
  if (form) {
    var radios = $all('input[name="track"]', form);
    var sections = $all(".track-only", form);
    sections.forEach(function (sec) {
      $all("[required]", sec).forEach(function (el) { el.setAttribute("data-required", ""); });
    });
    function applyTrack() {
      var chosen = radios.filter(function (r) { return r.checked; })[0];
      var key = chosen ? chosen.value : "";
      sections.forEach(function (sec) {
        var on = key !== "" && sec.getAttribute("data-track").split(" ").indexOf(key) !== -1;
        sec.hidden = !on;
        $all("[data-required]", sec).forEach(function (el) { el.required = on; });
      });
      $all("[data-track-text]", form).forEach(function (el) {
        var text = el.getAttribute("data-" + (key || "none"));
        if (text) el.textContent = text;
      });
    }
    radios.forEach(function (r) { r.addEventListener("change", applyTrack); });
    applyTrack();
  }

  // ---- print buttons ----
  $all("[data-print]").forEach(function (b) {
    b.addEventListener("click", function () { window.print(); });
  });

  // ---- back to top ----
  var topBtn = $(".back-to-top");
  if (topBtn) {
    var onScroll = function () { topBtn.classList.toggle("is-visible", window.scrollY > 600); };
    window.addEventListener("scroll", onScroll, { passive: true });
    onScroll();
  }
})();
