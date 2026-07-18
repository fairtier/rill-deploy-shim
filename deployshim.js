// @ts-check
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
(() => {
  "use strict";

  const ENDPOINT = "/__ft/deploy";
  // The button renders a <Rocket/> icon (no text) + the label "Deploy", so
  // the button's textContent is exactly "Deploy". Anchored+exact so a
  // "Deploy anyway" confirm button in a dialog does NOT match.
  const DEPLOY_LABEL = /^\s*Deploy\s*$/;

  // rill.<host>  ->  dashboards.<host>
  function viewerUrl() {
    const host = location.hostname.replace(/^rill\./, "dashboards.");
    if (host === location.hostname) return null;
    return `${location.protocol}//${host}/`;
  }

  /** @type {HTMLDivElement | null} */
  let toastEl = null;
  /** @type {ReturnType<typeof setTimeout> | null} */
  let toastTimer = null;

  /**
   * @param {string} msg
   * @param {{ error?: boolean, href?: string | null, sticky?: boolean }} [opts]
   */
  function toast(msg, opts = {}) {
    if (!toastEl) {
      toastEl = document.createElement("div");
      // CSSOM styling — not blocked by CSP style-src.
      Object.assign(toastEl.style, {
        position: "fixed",
        zIndex: "2147483647",
        bottom: "20px",
        right: "20px",
        maxWidth: "360px",
        padding: "12px 16px",
        borderRadius: "8px",
        font: "14px/1.4 system-ui, -apple-system, sans-serif",
        color: "#fff",
        boxShadow: "0 4px 16px rgba(0,0,0,.25)",
        transition: "opacity .2s ease",
      });
      document.body.appendChild(toastEl);
    }
    toastEl.style.background = opts.error ? "#b42318" : "#087443";
    toastEl.textContent = "";
    const text = document.createElement("span");
    text.textContent = msg;
    toastEl.appendChild(text);
    if (opts.href) {
      toastEl.appendChild(document.createTextNode("  "));
      const link = document.createElement("a");
      link.href = opts.href;
      link.target = "_blank";
      link.rel = "noopener";
      link.textContent = "View →";
      Object.assign(link.style, { color: "#fff", textDecoration: "underline" });
      toastEl.appendChild(link);
    }
    toastEl.style.opacity = "1";
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {
      if (toastEl) toastEl.style.opacity = "0";
    }, opts.sticky ? 12000 : 6000);
  }

  let busy = false;

  /** @param {HTMLButtonElement} btn */
  async function publish(btn) {
    if (busy) return;
    busy = true;
    const wasDisabled = btn.disabled;
    btn.disabled = true;
    toast("Publishing to dashboards…");

    try {
      const res = await fetch(ENDPOINT, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok || body.error) {
        toast(`Publish failed: ${body.error || `HTTP ${res.status}`}`, { error: true });
      } else if (body.status === "unchanged") {
        toast("Already up to date — nothing to publish.");
      } else if (body.status === "busy") {
        toast("A publish is already in progress — try again shortly.", { error: true });
      } else {
        toast("Published ✓ Dashboards update in a few seconds.", {
          href: viewerUrl(),
          sticky: true,
        });
      }
    } catch (e) {
      const why = e instanceof Error ? e.message : "network error";
      toast(`Publish failed: ${why}`, { error: true });
    } finally {
      busy = false;
      btn.disabled = wasDisabled;
    }
  }

  // Capture phase + stopImmediatePropagation so we run BEFORE Svelte's own
  // onClick (which is a target-phase listener on the same button) and stop
  // the localhost:9009 navigation from ever happening.
  document.addEventListener(
    "click",
    (e) => {
      const btn = e.target instanceof Element ? e.target.closest("button") : null;
      if (!btn) return;
      if (!DEPLOY_LABEL.test(btn.textContent ?? "")) return;
      e.preventDefault();
      e.stopImmediatePropagation();
      void publish(btn);
    },
    true,
  );
})();
