use std::collections::{HashMap, HashSet};
use std::sync::{LazyLock, Mutex};

use base64::Engine;
use tauri::{Emitter, Manager};

#[cfg(unix)]
use crate::kernel;

const BROWSER_VIEW_PREFIX: &str = "foya-workbar-browser";
static ACTIVE_BROWSER_PICKERS: LazyLock<Mutex<HashSet<String>>> =
    LazyLock::new(|| Mutex::new(HashSet::new()));
static PENDING_BROWSER_ACTIONS: LazyLock<
    Mutex<HashMap<String, tokio::sync::oneshot::Sender<BrowserActionResult>>>,
> = LazyLock::new(|| Mutex::new(HashMap::new()));

#[derive(serde::Deserialize)]
pub(crate) struct BrowserViewport {
    x: f64,
    y: f64,
    width: f64,
    height: f64,
}

#[derive(Clone, serde::Serialize)]
struct BrowserPageLoad {
    browser_id: String,
    url: String,
    status: &'static str,
}

#[derive(Clone, serde::Serialize)]
struct BrowserTitleChanged {
    browser_id: String,
    title: String,
}

#[derive(Clone, serde::Deserialize, serde::Serialize)]
struct BrowserElementSelection {
    page_url: String,
    page_title: String,
    tag: String,
    selector: String,
    text: String,
    html: String,
}

#[derive(Clone, serde::Serialize)]
struct BrowserElementSelected {
    browser_id: String,
    element: BrowserElementSelection,
}

#[derive(Clone, serde::Serialize)]
struct BrowserElementPickerState {
    browser_id: String,
    active: bool,
}

#[derive(Clone, serde::Deserialize, serde::Serialize)]
pub(crate) struct BrowserActionRequest {
    id: String,
    session_id: String,
    browser_id: String,
    action: String,
    #[serde(default)]
    url: String,
    #[serde(rename = "ref", default)]
    element_ref: String,
    #[serde(default)]
    observation_id: String,
    #[serde(default)]
    text: String,
    #[serde(default)]
    key: String,
    #[serde(default)]
    direction: String,
    #[serde(default)]
    amount: i64,
    #[serde(default)]
    timeout_ms: u64,
    #[serde(default)]
    clear: bool,
    #[serde(default)]
    full_page: bool,
}

#[derive(Clone, Default, serde::Deserialize, serde::Serialize)]
pub(crate) struct BrowserActionResult {
    #[serde(default)]
    url: String,
    #[serde(default)]
    title: String,
    #[serde(default)]
    revision: u64,
    #[serde(default)]
    observation_id: String,
    #[serde(default)]
    snapshot: String,
    #[serde(default)]
    screenshot_base64: String,
    #[serde(default)]
    media_type: String,
    #[serde(default)]
    code: String,
    #[serde(default)]
    message: String,
    #[serde(default)]
    pre_url: String,
    #[serde(default)]
    post_url: String,
    #[serde(default)]
    verified: Option<bool>,
    #[serde(default)]
    actual_text: String,
    #[serde(default)]
    trace: serde_json::Value,
    #[serde(default)]
    error: String,
}

fn browser_view_label(browser_id: &str) -> Result<String, String> {
    if browser_id.is_empty()
        || browser_id.len() > 64
        || !browser_id
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || c == b'-' || c == b'_')
    {
        return Err("浏览器实例 ID 无效".into());
    }
    Ok(format!("{BROWSER_VIEW_PREFIX}-{browser_id}"))
}

fn truncate_chars(value: &str, max: usize) -> String {
    value.chars().take(max).collect()
}

fn sanitize_browser_element(
    mut element: BrowserElementSelection,
) -> Result<BrowserElementSelection, String> {
    let parsed = element
        .page_url
        .parse::<tauri::Url>()
        .map_err(|_| "元素来源网址无效")?;
    if !matches!(parsed.scheme(), "http" | "https") {
        return Err("元素来源只允许 HTTP 或 HTTPS 地址".into());
    }
    element.page_url = truncate_chars(parsed.as_str(), 2_048);
    element.page_title = truncate_chars(element.page_title.trim(), 300);
    element.tag = truncate_chars(element.tag.trim().to_ascii_lowercase().as_str(), 64);
    element.selector = truncate_chars(element.selector.trim(), 1_024);
    element.text = truncate_chars(element.text.trim(), 2_000);
    element.html = truncate_chars(element.html.trim(), 8_000);
    if element.tag.is_empty() || element.selector.is_empty() {
        return Err("选中的页面元素信息不完整".into());
    }
    Ok(element)
}

const BROWSER_ELEMENT_PICKER_SCRIPT: &str = r##"
(() => {
  const key = "__foyaElementPicker";
  const existing = window[key];
  if (existing) {
    existing.enable();
    return;
  }

  let active = false;
  let selectedTarget = null;
  let selectedElement = null;
  const overlay = document.createElement("div");
  overlay.setAttribute("data-foya-element-picker", "");
  Object.assign(overlay.style, {
    position: "fixed",
    zIndex: "2147483647",
    pointerEvents: "none",
    display: "none",
    border: "2px solid #3b82f6",
    background: "rgba(59, 130, 246, 0.14)",
    boxSizing: "border-box",
  });
  const toolbar = document.createElement("div");
  toolbar.setAttribute("data-foya-element-picker", "");
  Object.assign(toolbar.style, {
    position: "fixed",
    zIndex: "2147483647",
    display: "none",
    alignItems: "center",
    gap: "4px",
    padding: "4px",
    border: "1px solid rgba(0, 0, 0, 0.12)",
    borderRadius: "8px",
    background: "rgba(255, 255, 255, 0.96)",
    boxShadow: "0 6px 18px rgba(0, 0, 0, 0.18)",
    color: "#171717",
    font: "600 13px -apple-system, BlinkMacSystemFont, sans-serif",
  });
  const addButton = document.createElement("button");
  addButton.type = "button";
  addButton.textContent = "添加到对话";
  Object.assign(addButton.style, {
    height: "30px",
    padding: "0 10px",
    border: "0",
    borderRadius: "6px",
    background: "#f1f3f5",
    color: "#171717",
    cursor: "pointer",
    font: "inherit",
  });
  const cancelButton = document.createElement("button");
  cancelButton.type = "button";
  cancelButton.textContent = "×";
  cancelButton.title = "取消选择";
  Object.assign(cancelButton.style, {
    width: "30px",
    height: "30px",
    padding: "0",
    border: "0",
    borderRadius: "6px",
    background: "transparent",
    color: "#666",
    cursor: "pointer",
    font: "20px/30px -apple-system, BlinkMacSystemFont, sans-serif",
  });
  toolbar.append(addButton, cancelButton);

  const cleanText = (value, max) =>
    String(value || "").replace(/\s+/g, " ").trim().slice(0, max);

  const escapeSelector = (value) => {
    if (window.CSS && typeof window.CSS.escape === "function") {
      return window.CSS.escape(value);
    }
    return String(value).replace(/[^a-zA-Z0-9_-]/g, "\\$&");
  };

  const selectorFor = (element) => {
    if (element.id) return `#${escapeSelector(element.id)}`;
    const parts = [];
    let current = element;
    while (current && current.nodeType === Node.ELEMENT_NODE && parts.length < 8) {
      let part = current.tagName.toLowerCase();
      const classes = Array.from(current.classList || [])
        .filter((name) => !name.startsWith("foya-"))
        .slice(0, 2);
      if (classes.length) {
        part += classes.map((name) => `.${escapeSelector(name)}`).join("");
      }
      const parent = current.parentElement;
      if (parent) {
        const siblings = Array.from(parent.children).filter(
          (item) => item.tagName === current.tagName
        );
        if (siblings.length > 1) {
          part += `:nth-of-type(${siblings.indexOf(current) + 1})`;
        }
      }
      parts.unshift(part);
      if (current === document.body) break;
      current = parent;
    }
    return parts.join(" > ");
  };

  const positionSelection = (element, selected = false) => {
    const rect = element.getBoundingClientRect();
    Object.assign(overlay.style, {
      display: rect.width > 0 && rect.height > 0 ? "block" : "none",
      left: `${rect.left}px`,
      top: `${rect.top}px`,
      width: `${rect.width}px`,
      height: `${rect.height}px`,
      borderColor: selected ? "#16a36a" : "#3b82f6",
      background: selected
        ? "rgba(22, 163, 106, 0.12)"
        : "rgba(59, 130, 246, 0.14)",
    });
    if (!selected) return;
    toolbar.style.display = "flex";
    const toolbarWidth = 142;
    const left = Math.max(
      8,
      Math.min(rect.left, window.innerWidth - toolbarWidth - 8)
    );
    const below = rect.bottom + 8;
    const top = below + 40 <= window.innerHeight
      ? below
      : Math.max(8, rect.top - 40);
    toolbar.style.left = `${left}px`;
    toolbar.style.top = `${top}px`;
  };

  const onMove = (event) => {
    if (selectedTarget) return;
    const target = event.target;
    if (
      !(target instanceof Element) ||
      target === overlay ||
      toolbar.contains(target)
    ) {
      return;
    }
    positionSelection(target);
  };

  const send = (kind, payload = {}) => {
    const encoded = encodeURIComponent(JSON.stringify(payload));
    window.location.href = `foya-element://${kind}?payload=${encoded}`;
  };

  const disable = () => {
    if (!active) return;
    active = false;
    selectedTarget = null;
    selectedElement = null;
    overlay.remove();
    toolbar.remove();
    document.removeEventListener("mousemove", onMove, true);
    document.removeEventListener("click", onClick, true);
    document.removeEventListener("keydown", onKeyDown, true);
    window.removeEventListener("scroll", onViewportChange, true);
    window.removeEventListener("resize", onViewportChange, true);
    document.documentElement.style.cursor = "";
  };

  const onClick = (event) => {
    if (!active) return;
    const target = event.target;
    if (
      !(target instanceof Element) ||
      target === overlay ||
      toolbar.contains(target)
    ) {
      return;
    }
    event.preventDefault();
    event.stopImmediatePropagation();
    selectedTarget = target;
    selectedElement = {
      page_url: location.href,
      page_title: cleanText(document.title, 300),
      tag: target.tagName.toLowerCase(),
      selector: selectorFor(target),
      text: cleanText(target.innerText || target.textContent, 2000),
      html: String(target.outerHTML || "").slice(0, 8000),
    };
    positionSelection(target, true);
    document.documentElement.style.cursor = "";
  };

  const confirmSelection = (event) => {
    event.preventDefault();
    event.stopPropagation();
    if (!selectedElement) return;
    const element = selectedElement;
    disable();
    send("selected", element);
  };

  const cancelSelection = (event) => {
    event.preventDefault();
    event.stopPropagation();
    disable();
    send("cancelled");
  };

  const onViewportChange = () => {
    if (selectedTarget && document.contains(selectedTarget)) {
      positionSelection(selectedTarget, true);
    }
  };

  const onKeyDown = (event) => {
    if (!active || event.key !== "Escape") return;
    event.preventDefault();
    event.stopImmediatePropagation();
    cancelSelection(event);
  };

  const enable = () => {
    if (active) return;
    active = true;
    document.documentElement.appendChild(overlay);
    document.documentElement.appendChild(toolbar);
    document.addEventListener("mousemove", onMove, true);
    document.addEventListener("click", onClick, true);
    document.addEventListener("keydown", onKeyDown, true);
    window.addEventListener("scroll", onViewportChange, true);
    window.addEventListener("resize", onViewportChange, true);
    document.documentElement.style.cursor = "crosshair";
  };

  addButton.addEventListener("click", confirmSelection);
  cancelButton.addEventListener("click", cancelSelection);
  window[key] = { enable, disable };
  enable();
})();
"##;

const BROWSER_ACTION_RUNTIME_SCRIPT: &str = r##"
(() => {
  const runtimeVersion = 3;
  if (window.__foyaBrowserRuntime?.version === runtimeVersion) return;

  const state = {
    revision: Number(window.__foyaBrowserRuntime?.revision || 0),
    observationSeq: Number(window.__foyaBrowserRuntime?.observationSeq || 0),
  };
  const selector = [
    "a[href]", "button", "input", "textarea", "select", "summary",
    "[role]", "[contenteditable=true]", "[tabindex]", "label"
  ].join(",");
  const clean = (value, max = 180) =>
    String(value ?? "").replace(/\s+/g, " ").trim().slice(0, max);
  const readableText = (node, max = 50000) =>
    String(node?.innerText || node?.textContent || "")
      .replace(/\r/g, "")
      .replace(/[ \t]+\n/g, "\n")
      .replace(/\n{3,}/g, "\n\n")
      .trim()
      .slice(0, max);
  const cleanUrl = (value) => {
    const text = String(value || "").trim();
    return text.startsWith("`") && text.endsWith("`")
      ? text.slice(1, -1).trim()
      : text;
  };
  const cssEscape = (value) =>
    window.CSS?.escape ? CSS.escape(String(value || "")) :
      String(value || "").replace(/["\\]/g, "\\$&");
  const pageUrl = () => cleanUrl(location.href);
  const baseResult = (overrides = {}) => ({
    code: "ok",
    url: pageUrl(),
    title: document.title,
    revision: state.revision,
    ...overrides,
  });
  const send = (requestId, result) => {
    const invoke = window.__TAURI__?.core?.invoke || window.__TAURI_INTERNALS__?.invoke;
    if (typeof invoke !== "function") {
      console.error("Foya browser action result channel is unavailable");
      return;
    }
    const payload = {
      code: result.code || (result.error ? "error" : "ok"),
      message: result.message || "",
      ...result,
    };
    invoke("browser_action_result", { requestId, result: payload }).catch((error) => {
      console.error("Foya browser action result failed", error);
    });
  };
  const fail = (request, code, message, trace = {}) => {
    send(request.id, baseResult({
      code,
      message,
      error: message,
      trace: { action: request.action, ref: request.ref || "", ...trace },
    }));
  };
  const visible = (node) => {
    const view = node.ownerDocument?.defaultView || window;
    const style = view.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== "none" && style.visibility !== "hidden" &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0;
  };
  const nodeName = (node) =>
    clean(
      node.getAttribute?.("aria-label") ||
      node.getAttribute?.("placeholder") ||
      node.getAttribute?.("alt") ||
      node.getAttribute?.("title") ||
      node.innerText ||
      node.textContent
    );
  const absoluteHref = (node) => {
    const href = node.getAttribute?.("href");
    if (!href) return "";
    try {
      return cleanUrl(new URL(href, node.ownerDocument.location.href).href);
    } catch {
      return cleanUrl(href);
    }
  };
  const rectInfo = (node) => {
    const rect = node.getBoundingClientRect();
    return {
      x: Math.round(rect.left),
      y: Math.round(rect.top),
      width: Math.round(rect.width),
      height: Math.round(rect.height),
    };
  };
  const digestFor = (node) => {
    const rect = rectInfo(node);
    return [
      node.tagName?.toLowerCase?.() || "",
      clean(node.getAttribute?.("role")),
      nodeName(node),
      absoluteHref(node),
      clean(node.getAttribute?.("type")),
      node.isContentEditable ? "editable" : "",
      node.disabled ? "disabled" : "",
      `${rect.width}x${rect.height}`,
    ].join("|");
  };
  const isClickable = (node) => Boolean(
    node.closest?.("a[href],button,input,label,select,textarea,summary,[role=button],[role=link],[tabindex]") ||
    node.onclick
  );
  const walkRoots = (root, visit) => {
    try {
      visit(root);
      const nodes = root.querySelectorAll?.("*") || [];
      for (const node of nodes) {
        if (node.shadowRoot) walkRoots(node.shadowRoot, visit);
        if (node.tagName?.toLowerCase() === "iframe") {
          try {
            if (node.contentDocument) walkRoots(node.contentDocument, visit);
          } catch {
            // Cross-origin iframes are intentionally skipped.
          }
        }
      }
    } catch {
      // Some browser internals can throw while a page is navigating.
    }
  };
  const clearRefs = () => {
    walkRoots(document, (root) => {
      root.querySelectorAll?.("[data-foya-agent-ref]").forEach((node) => {
        node.removeAttribute("data-foya-agent-ref");
        node.removeAttribute("data-foya-agent-digest");
        node.removeAttribute("data-foya-agent-observation");
      });
    });
  };
  const byRef = (ref) => {
    const query = `[data-foya-agent-ref="${cssEscape(ref)}"]`;
    let found = null;
    walkRoots(document, (root) => {
      if (!found) found = root.querySelector?.(query) || null;
    });
    return found;
  };
  const activeRef = () =>
    document.activeElement?.getAttribute?.("data-foya-agent-ref") || "";
  const pageSignature = () =>
    `${pageUrl()}|${document.title}|${document.body?.innerText?.length || 0}|${document.body?.children?.length || 0}|${activeRef()}`;
  const waitForReady = async (timeout = 1500) => {
    const started = Date.now();
    while (document.readyState === "loading" && Date.now() - started < timeout) {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  };
  const waitForChange = async (before, timeout = 1200) => {
    const started = Date.now();
    while (Date.now() - started < timeout) {
      await new Promise((resolve) => setTimeout(resolve, 80));
      if (pageSignature() !== before) return true;
    }
    return false;
  };
  const snapshot = () => {
    clearRefs();
    state.revision += 1;
    state.observationSeq += 1;
    const observationId = `obs_${Date.now().toString(36)}_${state.observationSeq}`;
    const elements = [];
    walkRoots(document, (root) => {
      if (elements.length >= 300) return;
      for (const node of root.querySelectorAll?.(selector) || []) {
        if (elements.length >= 300 || !visible(node)) continue;
        const ref = `e${elements.length + 1}`;
        const digest = digestFor(node);
        node.setAttribute("data-foya-agent-ref", ref);
        node.setAttribute("data-foya-agent-digest", digest);
        node.setAttribute("data-foya-agent-observation", observationId);
        const attrs = {};
        for (const key of ["href", "type", "value", "checked", "disabled", "aria-expanded", "placeholder"]) {
          if (!node.hasAttribute?.(key)) continue;
          if (key === "href") attrs.href = absoluteHref(node);
          else if (key === "checked" || key === "disabled") attrs[key] = Boolean(node[key]);
          else attrs[key] = clean(node.getAttribute(key));
        }
        elements.push({
          ref,
          digest,
          tag: node.tagName.toLowerCase(),
          role: clean(node.getAttribute("role")),
          name: nodeName(node),
          rect: rectInfo(node),
          clickable: isClickable(node),
          disabled: Boolean(node.disabled || node.getAttribute("aria-disabled") === "true"),
          attrs,
        });
      }
    });
    return baseResult({
      observation_id: observationId,
      snapshot: JSON.stringify({
        observation_id: observationId,
        url: pageUrl(),
        title: document.title,
        revision: state.revision,
        viewport: {
          width: Math.round(window.innerWidth),
          height: Math.round(window.innerHeight),
          scroll_x: Math.round(window.scrollX),
          scroll_y: Math.round(window.scrollY),
        },
        elements,
      }),
    });
  };
  const assertFresh = (request, node) => {
    const expectedObservation = node.getAttribute("data-foya-agent-observation") || "";
    if (request.observation_id && expectedObservation && request.observation_id !== expectedObservation) {
      throw Object.assign(new Error("element ref is from an older browser snapshot"), { code: "stale_ref" });
    }
    const expectedDigest = node.getAttribute("data-foya-agent-digest") || "";
    if (expectedDigest && expectedDigest !== digestFor(node)) {
      throw Object.assign(new Error("element changed since the last browser_snapshot"), { code: "stale_ref" });
    }
  };
  const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const nextFrame = () => new Promise((resolve) => requestAnimationFrame(resolve));
  const clickableTarget = (node) =>
    node.closest?.("a[href],button,input,label,select,textarea,summary,[role=button],[role=link],[tabindex]") || node;
  const containsNode = (root, child) =>
    root === child || Boolean(root?.contains?.(child));
  const clickPoint = (node) => {
    const view = node.ownerDocument?.defaultView || window;
    const rect = node.getBoundingClientRect();
    const x = Math.min(Math.max(rect.left + rect.width / 2, 1), Math.max(view.innerWidth - 1, 1));
    const y = Math.min(Math.max(rect.top + rect.height / 2, 1), Math.max(view.innerHeight - 1, 1));
    return { x, y, rect, view };
  };
  const dispatchPointerClick = (node, x, y, view = window) => {
    const base = {
      bubbles: true,
      cancelable: true,
      composed: true,
      view,
      clientX: x,
      clientY: y,
      button: 0,
      buttons: 1,
    };
    let allowed = true;
    if (typeof view.PointerEvent === "function") {
      allowed = node.dispatchEvent(new view.PointerEvent("pointerdown", {
        ...base,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })) && allowed;
    }
    allowed = node.dispatchEvent(new view.MouseEvent("mousedown", base)) && allowed;
    node.focus?.({ preventScroll: true });
    if (typeof view.PointerEvent === "function") {
      node.dispatchEvent(new view.PointerEvent("pointerup", {
        ...base,
        buttons: 0,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      }));
    }
    node.dispatchEvent(new view.MouseEvent("mouseup", { ...base, buttons: 0 }));
    allowed = node.dispatchEvent(new view.MouseEvent("click", { ...base, buttons: 0 })) && allowed;
    return allowed;
  };
  const readValue = (node) => {
    if (node.isContentEditable) return readableText(node, 20000);
    if (node instanceof HTMLSelectElement) {
      return clean(node.options[node.selectedIndex]?.text || node.value, 20000);
    }
    if ("value" in node) return String(node.value || "");
    return readableText(node, 20000);
  };
  const editableTarget = (node) =>
    node.matches?.("input,textarea,select,[contenteditable=true]") ? node :
      node.querySelector?.("input,textarea,select,[contenteditable=true]") || node;
  const run = async (request) => {
    const started = performance.now();
    try {
      const action = request.action;
      await waitForReady();
      if (action === "snapshot") {
        send(request.id, snapshot());
        return;
      }
      if (action === "click") {
        const node = byRef(request.ref);
        if (!node) throw Object.assign(new Error("element ref is stale or missing"), { code: "missing_ref" });
        assertFresh(request, node);
        const target = clickableTarget(node);
        target.scrollIntoView({ block: "center", inline: "center" });
        await nextFrame();
        await nextFrame();
        const { x, y, rect, view } = clickPoint(target);
        if (rect.width <= 0 || rect.height <= 0) {
          throw Object.assign(new Error("element is not clickable"), { code: "not_clickable" });
        }
        const hit = target.ownerDocument.elementFromPoint(x, y);
        if (hit && !containsNode(target, hit) && !containsNode(hit, target)) {
          throw Object.assign(
            new Error(`element is covered by ${hit.tagName?.toLowerCase?.() || "another element"}`),
            { code: "covered" }
          );
        }
        const trace = {
          action,
          ref: request.ref || "",
          target_tag: target.tagName?.toLowerCase?.() || "",
          hit_tag: hit?.tagName?.toLowerCase?.() || "",
          duration_ms: Math.round(performance.now() - started),
        };
        const anchor = target.closest?.("a[href]");
        if (anchor?.href && !anchor.href.startsWith("javascript:")) {
          const targetUrl = cleanUrl(anchor.href);
          send(request.id, baseResult({
            code: "navigation_requested",
            message: "click resolved to link navigation",
            url: targetUrl,
            pre_url: pageUrl(),
            post_url: targetUrl,
            trace: { ...trace, target_url: targetUrl },
          }));
          return;
        }
        const before = pageSignature();
        dispatchPointerClick(target, x, y, view);
        const changed = await waitForChange(before, 1200);
        const result = snapshot();
        result.pre_url = pageUrl();
        result.post_url = result.url;
        result.trace = { ...trace, changed, duration_ms: Math.round(performance.now() - started) };
        if (!changed && document.activeElement !== target) {
          result.code = "verification_failed";
          result.message = "click produced no observable page, focus, or URL change";
          result.error = result.message;
        }
        send(request.id, result);
        return;
      }
      if (action === "type") {
        const node = byRef(request.ref);
        if (!node) throw Object.assign(new Error("element ref is stale or missing"), { code: "missing_ref" });
        assertFresh(request, node);
        const target = editableTarget(node);
        if (!target.matches?.("input,textarea,select,[contenteditable=true]")) {
          throw Object.assign(new Error("element is not editable"), { code: "not_editable" });
        }
        target.focus();
        const previous = readValue(target);
        if (target.isContentEditable) {
          if (request.clear) target.textContent = "";
          document.execCommand("insertText", false, request.text || "");
        } else if (target instanceof HTMLSelectElement) {
          target.value = String(request.text || "");
        } else {
          const prototype = target instanceof HTMLTextAreaElement
            ? HTMLTextAreaElement.prototype
            : HTMLInputElement.prototype;
          const setter = Object.getOwnPropertyDescriptor(prototype, "value")?.set;
          setter?.call(target, request.clear ? "" : previous);
          setter?.call(target, String(target.value || "") + String(request.text || ""));
        }
        target.dispatchEvent(new InputEvent("input", {
          bubbles: true,
          inputType: "insertText",
          data: request.text || "",
        }));
        target.dispatchEvent(new Event("change", { bubbles: true }));
        await delay(120);
        const actual = readValue(target);
        const expected = request.clear ? String(request.text || "") : previous + String(request.text || "");
        const verified = actual === expected || (!request.clear && actual.endsWith(String(request.text || "")));
        const result = snapshot();
        result.verified = verified;
        result.actual_text = actual.slice(0, 2000);
        result.trace = {
          action,
          ref: request.ref || "",
          target_tag: target.tagName?.toLowerCase?.() || "",
          duration_ms: Math.round(performance.now() - started),
        };
        if (!verified) {
          result.code = "verification_failed";
          result.message = "typed text did not match field readback";
          result.error = result.message;
        }
        send(request.id, result);
        return;
      }
      if (action === "press_key") {
        const target = request.ref
          ? byRef(request.ref)
          : document.activeElement || document.body;
        if (!target) throw Object.assign(new Error("element ref is stale or missing"), { code: "missing_ref" });
        if (request.ref) assertFresh(request, target);
        target.focus?.();
        const key = String(request.key || "");
        if (key === "Enter" && target.form?.action) {
          target.form.requestSubmit();
          send(request.id, baseResult({
            code: "navigation_started",
            message: "submitted form with Enter",
            trace: { action, key, ref: request.ref || "" },
          }));
          return;
        }
        const before = pageSignature();
        if (key === "Tab") {
          const items = Array.from(document.querySelectorAll(
            "a[href],button,input,textarea,select,[tabindex]"
          )).filter(visible);
          const current = items.indexOf(target);
          items[(current + 1) % items.length]?.focus();
        } else {
          target.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true }));
          target.dispatchEvent(new KeyboardEvent("keyup", { key, bubbles: true }));
        }
        const changed = await waitForChange(before, 800);
        const result = snapshot();
        result.trace = { action, key, ref: request.ref || "", changed };
        send(request.id, result);
        return;
      }
      if (action === "scroll") {
        const amount = Number(request.amount) || 600;
        const vectors = {
          up: [0, -amount],
          down: [0, amount],
          left: [-amount, 0],
          right: [amount, 0],
        };
        const [x, y] = vectors[request.direction || "down"] || vectors.down;
        window.scrollBy({ left: x, top: y, behavior: "instant" });
        await delay(120);
        const result = snapshot();
        result.trace = { action, direction: request.direction || "down", amount };
        send(request.id, result);
        return;
      }
      if (action === "wait") {
        const timeout = Math.min(Math.max(Number(request.timeout_ms) || 30000, 100), 60000);
        const startedAt = Date.now();
        const check = () => {
          const refReady = !request.ref || Boolean(byRef(request.ref));
          const textReady = !request.text ||
            String(document.body?.innerText || "").includes(request.text);
          if (refReady && textReady) {
            send(request.id, snapshot());
          } else if (Date.now() - startedAt >= timeout) {
            fail(request, "timeout", "browser wait timed out", { timeout_ms: timeout });
          } else {
            setTimeout(check, 100);
          }
        };
        check();
        return;
      }
      if (action === "extract") {
        const timeout = Math.min(Math.max(Number(request.timeout_ms) || 30000, 100), 60000);
        const startedAt = Date.now();
        const emitExtract = () => {
          const root = request.ref ? byRef(request.ref) : document.body;
          if (!root) throw Object.assign(new Error("element ref is stale or missing"), { code: "missing_ref" });
          state.revision += 1;
          const observationId = `obs_${Date.now().toString(36)}_${++state.observationSeq}`;
          send(request.id, baseResult({
            observation_id: observationId,
            snapshot: JSON.stringify({
              observation_id: observationId,
              url: pageUrl(),
              title: document.title,
              revision: state.revision,
              text: readableText(root),
            }),
          }));
        };
        const check = () => {
          const textReady = !request.text ||
            String(document.body?.innerText || "").includes(request.text);
          if (textReady) {
            try {
              emitExtract();
            } catch (error) {
              fail(request, error?.code || "error", String(error?.message || error));
            }
          } else if (Date.now() - startedAt >= timeout) {
            fail(request, "timeout", "browser extract timed out", { timeout_ms: timeout });
          } else {
            setTimeout(check, 100);
          }
        };
        check();
        return;
      }
      if (action === "back" || action === "reload") {
        send(request.id, baseResult({
          code: "navigation_started",
          message: `browser ${action} requested`,
          trace: { action },
        }));
        setTimeout(() => {
          if (action === "back") history.back();
          else location.reload();
        }, 0);
        return;
      }
      if (action === "screenshot") {
        throw Object.assign(new Error("Webview screenshot is not implemented in this script"), { code: "unsupported_action" });
      }
      throw Object.assign(new Error(`unsupported browser action: ${action}`), { code: "unsupported_action" });
    } catch (error) {
      fail(request, error?.code || "error", String(error?.message || error));
    }
  };
  window.__foyaBrowserRuntime = { version: runtimeVersion, run, revision: state.revision, observationSeq: state.observationSeq };
})();
"##;

#[cfg(unix)]
#[tauri::command]
pub(crate) async fn resolve_browser_action(
    session_id: String,
    request_id: String,
    result: BrowserActionResult,
) -> Result<(), String> {
    let body = serde_json::json!({
        "request_id": request_id,
        "url": result.url,
        "title": result.title,
        "revision": result.revision,
        "observation_id": result.observation_id,
        "snapshot": result.snapshot,
        "screenshot_base64": result.screenshot_base64,
        "media_type": result.media_type,
        "code": result.code,
        "message": result.message,
        "pre_url": result.pre_url,
        "post_url": result.post_url,
        "verified": result.verified,
        "actual_text": result.actual_text,
        "trace": result.trace,
        "error": result.error,
    })
    .to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/browser-actions/{request_id}"),
        Some(&body),
    )
    .await
    .map(|_| ())
}

#[tauri::command]
pub(crate) async fn navigate_browser(
    app: tauri::AppHandle,
    browser_id: String,
    url: String,
    viewport: BrowserViewport,
) -> Result<(), String> {
    let parsed = url.parse::<tauri::Url>().map_err(|e| e.to_string())?;
    if !matches!(parsed.scheme(), "http" | "https") {
        return Err("只允许打开 HTTP 或 HTTPS 地址".into());
    }
    if viewport.width <= 0.0 || viewport.height <= 0.0 {
        return Err("浏览器预览区域尺寸无效".into());
    }
    let position = tauri::LogicalPosition::new(viewport.x, viewport.y);
    let size = tauri::LogicalSize::new(viewport.width, viewport.height);
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview.navigate(parsed).map_err(|e| e.to_string())?;
        webview
            .set_bounds(tauri::Rect {
                position: tauri::Position::Logical(position),
                size: tauri::Size::Logical(size),
            })
            .map_err(|e| e.to_string())?;
        webview.show().map_err(|e| e.to_string())?;
        webview.set_focus().map_err(|e| e.to_string())?;
        return Ok(());
    }

    let window = app.get_window("main").ok_or("主窗口不可用")?;
    let load_browser_id = browser_id.clone();
    let title_browser_id = browser_id.clone();
    let navigation_browser_id = browser_id.clone();
    let navigation_app = app.clone();
    let builder = tauri::webview::WebviewBuilder::new(&label, tauri::WebviewUrl::External(parsed))
        .on_navigation(move |target| {
            if target.scheme() == "foya-element" {
                let was_active = ACTIVE_BROWSER_PICKERS
                    .lock()
                    .map(|mut active| active.remove(&navigation_browser_id))
                    .unwrap_or(false);
                let _ = navigation_app.emit_to(
                    "main",
                    "browser-element-picker-state",
                    BrowserElementPickerState {
                        browser_id: navigation_browser_id.clone(),
                        active: false,
                    },
                );
                if was_active && target.host_str() == Some("selected") {
                    let payload = target
                        .query_pairs()
                        .find(|(key, _)| key == "payload")
                        .map(|(_, value)| value.into_owned());
                    if let Some(payload) = payload {
                        if let Ok(raw) = serde_json::from_str::<BrowserElementSelection>(&payload) {
                            if let Ok(element) = sanitize_browser_element(raw) {
                                let _ = navigation_app.emit_to(
                                    "main",
                                    "browser-element-selected",
                                    BrowserElementSelected {
                                        browser_id: navigation_browser_id.clone(),
                                        element,
                                    },
                                );
                            }
                        }
                    }
                }
                return false;
            }
            matches!(target.scheme(), "http" | "https" | "about")
        })
        .on_page_load(move |webview, payload| {
            let status = match payload.event() {
                tauri::webview::PageLoadEvent::Started => "started",
                tauri::webview::PageLoadEvent::Finished => "finished",
            };
            if status == "started" {
                if let Ok(mut active) = ACTIVE_BROWSER_PICKERS.lock() {
                    active.remove(&load_browser_id);
                }
                let _ = webview.app_handle().emit_to(
                    "main",
                    "browser-element-picker-state",
                    BrowserElementPickerState {
                        browser_id: load_browser_id.clone(),
                        active: false,
                    },
                );
            }
            let _ = webview.app_handle().emit_to(
                "main",
                "browser-page-load",
                BrowserPageLoad {
                    browser_id: load_browser_id.clone(),
                    url: payload.url().to_string(),
                    status,
                },
            );
        })
        .on_document_title_changed(move |webview, title| {
            let _ = webview.app_handle().emit_to(
                "main",
                "browser-title-changed",
                BrowserTitleChanged {
                    browser_id: title_browser_id.clone(),
                    title,
                },
            );
        });
    let webview = window
        .add_child(builder, position, size)
        .map_err(|e| e.to_string())?;
    webview.show().map_err(|e| e.to_string())?;
    webview.set_focus().map_err(|e| e.to_string())?;
    Ok(())
}

#[tauri::command]
pub(crate) fn set_browser_viewport(
    app: tauri::AppHandle,
    browser_id: String,
    viewport: Option<BrowserViewport>,
) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    let Some(webview) = app.get_webview(&label) else {
        return Ok(());
    };
    let Some(viewport) = viewport else {
        return webview.hide().map_err(|e| e.to_string());
    };
    if viewport.width <= 0.0 || viewport.height <= 0.0 {
        return webview.hide().map_err(|e| e.to_string());
    }
    webview
        .set_bounds(tauri::Rect {
            position: tauri::Position::Logical(tauri::LogicalPosition::new(viewport.x, viewport.y)),
            size: tauri::Size::Logical(tauri::LogicalSize::new(viewport.width, viewport.height)),
        })
        .map_err(|e| e.to_string())?;
    webview.show().map_err(|e| e.to_string())
}

#[tauri::command]
pub(crate) fn browser_back(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview.eval("history.back()").map_err(|e| e.to_string())?;
    }
    Ok(())
}

#[tauri::command]
pub(crate) fn browser_forward(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview
            .eval("history.forward()")
            .map_err(|e| e.to_string())?;
    }
    Ok(())
}

#[tauri::command]
pub(crate) fn browser_reload(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview.reload().map_err(|e| e.to_string())?;
    }
    Ok(())
}

#[tauri::command]
pub(crate) fn set_browser_element_picker(
    app: tauri::AppHandle,
    browser_id: String,
    enabled: bool,
) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    let Some(webview) = app.get_webview(&label) else {
        return Err("浏览器页面尚未打开".into());
    };
    if enabled {
        ACTIVE_BROWSER_PICKERS
            .lock()
            .map_err(|_| "无法更新元素选择状态")?
            .insert(browser_id);
        webview
            .eval(BROWSER_ELEMENT_PICKER_SCRIPT)
            .map_err(|e| e.to_string())
    } else {
        ACTIVE_BROWSER_PICKERS
            .lock()
            .map_err(|_| "无法更新元素选择状态")?
            .remove(&browser_id);
        webview
            .eval("window.__foyaElementPicker && window.__foyaElementPicker.disable();")
            .map_err(|e| e.to_string())
    }
}

#[cfg(target_os = "macos")]
async fn capture_webview_screenshot(webview: tauri::Webview) -> Result<Vec<u8>, String> {
    use block2::RcBlock;
    use objc2::runtime::AnyObject;
    use objc2_app_kit::{NSBitmapImageFileType, NSBitmapImageRep, NSImage};
    use objc2_foundation::{NSDictionary, NSError};
    use objc2_web_kit::WKWebView;

    let (sender, receiver) = tokio::sync::oneshot::channel();
    webview
        .with_webview(move |platform| unsafe {
            let sender = Mutex::new(Some(sender));
            let completion = RcBlock::new(move |image: *mut NSImage, _error: *mut NSError| {
                let result = if image.is_null() {
                    Err("Webview 截图失败".into())
                } else {
                    let image = &*image;
                    image
                        .TIFFRepresentation()
                        .ok_or_else(|| "无法读取 Webview 截图".to_string())
                        .and_then(|tiff| {
                            NSBitmapImageRep::imageRepWithData(&tiff)
                                .ok_or_else(|| "无法转换 Webview 截图".to_string())
                        })
                        .and_then(|bitmap| {
                            let properties = NSDictionary::<_, AnyObject>::new();
                            bitmap
                                .representationUsingType_properties(
                                    NSBitmapImageFileType::PNG,
                                    &properties,
                                )
                                .map(|png| png.to_vec())
                                .ok_or_else(|| "无法编码 Webview 截图".to_string())
                        })
                };
                if let Ok(mut slot) = sender.lock() {
                    if let Some(sender) = slot.take() {
                        let _ = sender.send(result);
                    }
                }
            });
            let view: &WKWebView = &*platform.inner().cast();
            view.takeSnapshotWithConfiguration_completionHandler(None, &completion);
        })
        .map_err(|e| e.to_string())?;

    tokio::time::timeout(std::time::Duration::from_secs(15), receiver)
        .await
        .map_err(|_| "Webview 截图超时".to_string())?
        .map_err(|_| "Webview 截图通道已关闭".to_string())?
}

#[cfg(not(target_os = "macos"))]
async fn capture_webview_screenshot(_webview: tauri::Webview) -> Result<Vec<u8>, String> {
    Err("当前平台尚不支持 Webview 截图".into())
}

#[tauri::command]
pub(crate) async fn execute_browser_action(
    app: tauri::AppHandle,
    request: BrowserActionRequest,
) -> Result<BrowserActionResult, String> {
    if request.id.is_empty() || request.id.len() > 128 {
        return Err("浏览器动作 ID 无效".into());
    }
    let label = browser_view_label(&request.browser_id)?;
    let Some(webview) = app.get_webview(&label) else {
        return Err("浏览器页面尚未打开".into());
    };

    if matches!(request.action.as_str(), "open" | "navigate") {
        let parsed = request
            .url
            .parse::<tauri::Url>()
            .map_err(|e| e.to_string())?;
        if !matches!(parsed.scheme(), "http" | "https") {
            return Err("只允许打开 HTTP 或 HTTPS 地址".into());
        }
        webview
            .navigate(parsed.clone())
            .map_err(|e| e.to_string())?;
        return Ok(BrowserActionResult {
            url: parsed.to_string(),
            ..Default::default()
        });
    }

    if request.action == "screenshot" {
        let url = webview.url().map_err(|e| e.to_string())?.to_string();
        let image = capture_webview_screenshot(webview).await?;
        return Ok(BrowserActionResult {
            url,
            screenshot_base64: base64::engine::general_purpose::STANDARD.encode(image),
            media_type: "image/png".into(),
            ..Default::default()
        });
    }

    let request_id = request.id.clone();
    let request_json = serde_json::to_string(&request).map_err(|e| e.to_string())?;
    let (sender, receiver) = tokio::sync::oneshot::channel();
    PENDING_BROWSER_ACTIONS
        .lock()
        .map_err(|_| "无法创建浏览器动作")?
        .insert(request_id.clone(), sender);

    if let Err(error) = webview.eval(BROWSER_ACTION_RUNTIME_SCRIPT) {
        if let Ok(mut pending) = PENDING_BROWSER_ACTIONS.lock() {
            pending.remove(&request_id);
        }
        return Err(error.to_string());
    }
    if let Err(error) = webview.eval(format!("window.__foyaBrowserRuntime.run({request_json});")) {
        if let Ok(mut pending) = PENDING_BROWSER_ACTIONS.lock() {
            pending.remove(&request_id);
        }
        return Err(error.to_string());
    }

    let timeout_ms = match (request.action.as_str(), request.timeout_ms) {
        ("wait" | "extract", 0) => 35_000,
        ("wait" | "extract", timeout) => timeout.saturating_add(5_000).clamp(100, 65_000),
        (_, 0) => 15_000,
        (_, timeout) => timeout.clamp(100, 60_000),
    };
    match tokio::time::timeout(std::time::Duration::from_millis(timeout_ms), receiver).await {
        Ok(Ok(mut result)) => {
            if result.code == "navigation_requested" {
                let parsed = result
                    .url
                    .parse::<tauri::Url>()
                    .map_err(|e| e.to_string())?;
                if !matches!(parsed.scheme(), "http" | "https") {
                    return Err("只允许打开 HTTP 或 HTTPS 地址".into());
                }
                webview
                    .navigate(parsed.clone())
                    .map_err(|e| e.to_string())?;
                result.code = "navigation_started".into();
                result.url = parsed.to_string();
                if result.post_url.is_empty() {
                    result.post_url = result.url.clone();
                }
            }
            Ok(result)
        }
        Ok(Err(_)) => Err("浏览器动作通道已关闭".into()),
        Err(_) => {
            if let Ok(mut pending) = PENDING_BROWSER_ACTIONS.lock() {
                pending.remove(&request_id);
            }
            Err("浏览器动作超时".into())
        }
    }
}

#[tauri::command]
pub(crate) fn browser_action_result(
    request_id: String,
    result: BrowserActionResult,
) -> Result<(), String> {
    if request_id.is_empty() || request_id.len() > 128 {
        return Err("浏览器动作 ID 无效".into());
    }
    let sender = PENDING_BROWSER_ACTIONS
        .lock()
        .map_err(|_| "无法读取浏览器动作")?
        .remove(&request_id)
        .ok_or("浏览器动作已结束")?;
    let _ = sender.send(result);
    Ok(())
}

#[tauri::command]
pub(crate) fn hide_browser(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview.hide().map_err(|e| e.to_string())?;
    }
    Ok(())
}

#[tauri::command]
pub(crate) fn close_browser(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Ok(mut active) = ACTIVE_BROWSER_PICKERS.lock() {
        active.remove(&browser_id);
    }
    if let Some(webview) = app.get_webview(&label) {
        webview.close().map_err(|e| e.to_string())?;
    }
    Ok(())
}

#[cfg(not(unix))]
#[tauri::command]
pub(crate) async fn resolve_browser_action(
    _session_id: String,
    _request_id: String,
    _result: BrowserActionResult,
) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}
