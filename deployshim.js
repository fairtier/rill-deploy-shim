// deploy-shim.js — injected into the Rill editor UI by rill-deploy-shim.
//
// Rill Developer's built-in "Deploy" button is upstream's Rill Cloud CTA:
// it navigates to `${loginUrl}?redirect=…?deploy=true`, where loginUrl is
// hardcoded to http://localhost:9009/auth (Rill hardcodes localhost). On a
// remote box that points at the visitor's own machine and is a dead end.
//
// This shim's "deploy" instead triggers the configured snapshot/publish
// service, which rill-deploy-shim exposes same-origin at POST /__ft/deploy
// (authenticated by the fronting proxy session).
//
// So we intercept the Deploy click and call /__ft/deploy instead of ever
// letting the localhost:9009 navigation fire.
(function () {
  "use strict";

  var ENDPOINT = "/__ft/deploy";
  // The button renders a <Rocket/> icon (no text) + the label "Deploy", so
  // the button's textContent is exactly "Deploy". Anchored+exact so a
  // "Deploy anyway" confirm button in a dialog does NOT match.
  var DEPLOY_LABEL = /^\s*Deploy\s*$/;

  // rill.<host>  ->  dashboards.<host>
  function viewerUrl() {
    try {
      var host = location.hostname.replace(/^rill\./, "dashboards.");
      if (host === location.hostname) return null;
      return location.protocol + "//" + host + "/";
    } catch (e) {
      return null;
    }
  }

  var toastEl = null;
  var toastTimer = null;
  function toast(msg, opts) {
    opts = opts || {};
    if (!toastEl) {
      toastEl = document.createElement("div");
      var s = toastEl.style; // CSSOM styling — not blocked by CSP style-src.
      s.position = "fixed";
      s.zIndex = "2147483647";
      s.bottom = "20px";
      s.right = "20px";
      s.maxWidth = "360px";
      s.padding = "12px 16px";
      s.borderRadius = "8px";
      s.font = "14px/1.4 system-ui, -apple-system, sans-serif";
      s.color = "#fff";
      s.boxShadow = "0 4px 16px rgba(0,0,0,.25)";
      s.transition = "opacity .2s ease";
      document.body.appendChild(toastEl);
    }
    toastEl.style.background = opts.error ? "#b42318" : "#087443";
    toastEl.textContent = "";
    var text = document.createElement("span");
    text.textContent = msg;
    toastEl.appendChild(text);
    if (opts.href) {
      toastEl.appendChild(document.createTextNode("  "));
      var link = document.createElement("a");
      link.href = opts.href;
      link.target = "_blank";
      link.rel = "noopener";
      link.textContent = "View →";
      link.style.color = "#fff";
      link.style.textDecoration = "underline";
      toastEl.appendChild(link);
    }
    toastEl.style.opacity = "1";
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(function () {
      if (toastEl) toastEl.style.opacity = "0";
    }, opts.sticky ? 12000 : 6000);
  }

  var busy = false;
  function publish(btn) {
    if (busy) return;
    busy = true;
    var wasDisabled = btn.disabled;
    try {
      btn.disabled = true;
    } catch (e) {}
    toast("Publishing to dashboards…");

    fetch(ENDPOINT, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
    })
      .then(function (r) {
        return r
          .json()
          .catch(function () {
            return {};
          })
          .then(function (body) {
            return { ok: r.ok, status: r.status, body: body };
          });
      })
      .then(function (res) {
        var st = res.body && res.body.status;
        if (!res.ok || (res.body && res.body.error)) {
          var why = (res.body && res.body.error) || "HTTP " + res.status;
          toast("Publish failed: " + why, { error: true });
          return;
        }
        if (st === "unchanged") {
          toast("Already up to date — nothing to publish.");
        } else if (st === "busy") {
          toast("A publish is already in progress — try again shortly.", {
            error: true,
          });
        } else {
          toast("Published ✓ Dashboards update in a few seconds.", {
            href: viewerUrl(),
            sticky: true,
          });
        }
      })
      .catch(function (e) {
        toast("Publish failed: " + (e && e.message ? e.message : "network error"), {
          error: true,
        });
      })
      .then(function () {
        busy = false;
        try {
          btn.disabled = wasDisabled;
        } catch (e) {}
      });
  }

  // Capture phase + stopImmediatePropagation so we run BEFORE Svelte's own
  // onClick (which is a target-phase listener on the same button) and stop
  // the localhost:9009 navigation from ever happening.
  document.addEventListener(
    "click",
    function (e) {
      var btn = e.target && e.target.closest ? e.target.closest("button") : null;
      if (!btn) return;
      if (!DEPLOY_LABEL.test(btn.textContent || "")) return;
      e.preventDefault();
      e.stopImmediatePropagation();
      publish(btn);
    },
    true
  );
})();
