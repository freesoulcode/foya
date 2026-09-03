// Learn more about Tauri commands at https://tauri.app/develop/calling-rust/
use std::collections::{HashMap, HashSet};
use std::fs;
use std::path::{Component, Path, PathBuf};
use std::sync::{Arc, LazyLock, Mutex};

use base64::Engine;
use tauri::ipc::Channel;
use tauri::{Emitter, Manager};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

const BROWSER_VIEW_PREFIX: &str = "foya-workbar-browser";
static ACTIVE_BROWSER_PICKERS: LazyLock<Mutex<HashSet<String>>> =
    LazyLock::new(|| Mutex::new(HashSet::new()));
static PENDING_BROWSER_ACTIONS: LazyLock<
    Mutex<HashMap<String, tokio::sync::oneshot::Sender<BrowserActionResult>>>,
> = LazyLock::new(|| Mutex::new(HashMap::new()));
const MAX_PROJECT_ENTRIES: usize = 10_000;
const MAX_PREVIEW_BYTES: u64 = 2 * 1024 * 1024;
const IGNORED_PROJECT_DIRS: &[&str] = &[
    ".git",
    ".idea",
    ".next",
    ".nuxt",
    ".turbo",
    "coverage",
    "dist",
    "node_modules",
    "target",
    "vendor",
];

fn encode_query_component(value: &str) -> String {
    let mut encoded = String::with_capacity(value.len());
    for byte in value.bytes() {
        if byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.' | b'~') {
            encoded.push(byte as char);
        } else {
            encoded.push_str(&format!("%{byte:02X}"));
        }
    }
    encoded
}

#[derive(serde::Deserialize)]
struct BrowserViewport {
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
struct BrowserActionRequest {
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
struct BrowserActionResult {
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

#[derive(serde::Serialize)]
struct ProjectEntry {
    path: String,
    name: String,
    is_dir: bool,
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

fn project_root(project_path: &str) -> Result<PathBuf, String> {
    let root = fs::canonicalize(project_path).map_err(|e| format!("无法访问项目目录: {e}"))?;
    if !root.is_dir() {
        return Err("项目路径不是目录".into());
    }
    Ok(root)
}

fn project_relative_path(relative_path: &str) -> Result<&Path, String> {
    let relative = Path::new(relative_path);
    if relative.as_os_str().is_empty()
        || relative
            .components()
            .any(|component| !matches!(component, Component::Normal(_)))
    {
        return Err("项目路径无效".into());
    }
    Ok(relative)
}

fn safe_project_entry(project_path: &str, relative_path: &str) -> Result<PathBuf, String> {
    let relative = project_relative_path(relative_path)?;
    let root = project_root(project_path)?;
    let candidate = root.join(relative);
    let metadata =
        fs::symlink_metadata(&candidate).map_err(|e| format!("无法访问项目条目: {e}"))?;
    if metadata.file_type().is_symlink() {
        return Err("不支持操作符号链接".into());
    }
    let entry = fs::canonicalize(candidate).map_err(|e| format!("无法访问项目条目: {e}"))?;
    if !entry.starts_with(&root) {
        return Err("项目条目不在当前项目中".into());
    }
    Ok(entry)
}

fn safe_project_file(project_path: &str, relative_path: &str) -> Result<PathBuf, String> {
    let file = safe_project_entry(project_path, relative_path)?;
    if !file.is_file() {
        return Err("项目条目不是文件".into());
    }
    Ok(file)
}

fn safe_project_destination(project_path: &str, relative_path: &str) -> Result<PathBuf, String> {
    let relative = project_relative_path(relative_path)?;
    let root = project_root(project_path)?;
    let candidate = root.join(relative);
    let file_name = candidate.file_name().ok_or("项目路径无效")?;
    let parent = candidate.parent().ok_or("项目路径无效")?;
    let parent = fs::canonicalize(parent).map_err(|e| format!("无法访问父目录: {e}"))?;
    if !parent.starts_with(&root) || !parent.is_dir() {
        return Err("父目录不在当前项目中".into());
    }
    let destination = parent.join(file_name);
    match fs::symlink_metadata(&destination) {
        Ok(_) => Err("同名文件或文件夹已存在".into()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(destination),
        Err(error) => Err(format!("无法检查目标路径: {error}")),
    }
}

fn safe_entry_name(name: &str) -> Result<&str, String> {
    let path = Path::new(name);
    let mut components = path.components();
    if name.is_empty()
        || !matches!(components.next(), Some(Component::Normal(_)))
        || components.next().is_some()
    {
        return Err("名称不能包含路径分隔符".into());
    }
    Ok(name)
}

fn project_relative_string(root: &Path, path: &Path) -> Result<String, String> {
    path.strip_prefix(root)
        .map(|relative| relative.to_string_lossy().replace('\\', "/"))
        .map_err(|_| "项目条目不在当前项目中".into())
}

fn collect_project_entries(
    root: &Path,
    directory: &Path,
    entries: &mut Vec<ProjectEntry>,
) -> Result<(), String> {
    if entries.len() >= MAX_PROJECT_ENTRIES {
        return Ok(());
    }
    let mut children = fs::read_dir(directory)
        .map_err(|e| format!("无法读取项目目录: {e}"))?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|e| format!("无法读取项目目录项: {e}"))?;
    children.sort_by_key(|entry| entry.file_name().to_string_lossy().to_lowercase());

    for child in children {
        if entries.len() >= MAX_PROJECT_ENTRIES {
            break;
        }
        let file_type = child
            .file_type()
            .map_err(|e| format!("无法读取文件类型: {e}"))?;
        if file_type.is_symlink() {
            continue;
        }
        let name = child.file_name().to_string_lossy().into_owned();
        if file_type.is_dir() && IGNORED_PROJECT_DIRS.contains(&name.as_str()) {
            continue;
        }
        let path = child.path();
        let relative = path
            .strip_prefix(root)
            .map_err(|e| e.to_string())?
            .to_string_lossy()
            .replace('\\', "/");
        entries.push(ProjectEntry {
            path: relative,
            name,
            is_dir: file_type.is_dir(),
        });
        if file_type.is_dir() {
            collect_project_entries(root, &path, entries)?;
        }
    }
    Ok(())
}

/// 内核 Unix socket 路径,须与 Go 端 config.DefaultSocketPath 保持一致。
fn kernel_socket_path() -> Option<PathBuf> {
    // 与 Go 的 os.UserConfigDir()/foya/kernel.sock 对齐。
    dirs_config_dir().map(|d| d.join("foya").join("kernel.sock"))
}

/// 跨平台用户配置目录(对齐 Go os.UserConfigDir)。
#[cfg(target_os = "macos")]
fn dirs_config_dir() -> Option<PathBuf> {
    std::env::var_os("HOME").map(|h| PathBuf::from(h).join("Library").join("Application Support"))
}

#[cfg(target_os = "linux")]
fn dirs_config_dir() -> Option<PathBuf> {
    std::env::var_os("XDG_CONFIG_HOME")
        .map(PathBuf::from)
        .or_else(|| std::env::var_os("HOME").map(|h| PathBuf::from(h).join(".config")))
}

#[cfg(target_os = "windows")]
fn dirs_config_dir() -> Option<PathBuf> {
    std::env::var_os("APPDATA").map(PathBuf::from)
}

// ============ 以下为 Unix 平台的内核连接实现 ============
// 使用 hyper(事实标准 HTTP 实现)+ hyperlocal(Unix socket 连接器)。
// chunked、keep-alive、header 解析全部由库负责,避免手写 HTTP 客户端的边界 bug。
// Windows 传输(named pipe / AF_UNIX)后续单独实现。

#[cfg(unix)]
mod kernel {
    use super::kernel_socket_path;
    use bytes::{Buf, Bytes};
    use futures_util::StreamExt;
    use http_body_util::{BodyDataStream, BodyExt, Full};
    use hyper::{Method, Request, StatusCode};
    use hyper_util::client::legacy::Client;
    use hyper_util::rt::TokioExecutor;
    use hyperlocal::{UnixConnector, Uri as HyperlocalUri};
    use std::path::Path;
    use tauri::ipc::Channel;
    use tokio::sync::oneshot;

    /// 在缓冲区中查找下一个换行符(\n)的位置。
    fn find_line_end(buf: &[u8]) -> Option<usize> {
        buf.iter().position(|&b| b == b'\n')
    }

    type HttpClient = Client<UnixConnector, Full<Bytes>>;

    fn client() -> HttpClient {
        Client::builder(TokioExecutor::new()).build(UnixConnector)
    }

    fn socket_uri(path: &str) -> Result<hyperlocal::Uri, String> {
        let sock = kernel_socket_path().ok_or("无法解析 socket 路径")?;
        Ok(HyperlocalUri::new(Path::new(&sock), path))
    }

    fn method_from_str(m: &str) -> Method {
        match m {
            "POST" => Method::POST,
            "PUT" => Method::PUT,
            "PATCH" => Method::PATCH,
            "DELETE" => Method::DELETE,
            _ => Method::GET,
        }
    }

    /// 发一个带 body 的 HTTP 请求,读完整响应,返回 body 字符串(用于短请求)。
    pub async fn request(method: &str, path: &str, body: Option<&str>) -> Result<String, String> {
        let uri = socket_uri(path)?;
        let mut builder = Request::builder().method(method_from_str(method)).uri(uri);
        let body_bytes: Bytes = match body {
            Some(b) if !b.is_empty() => {
                builder = builder.header("content-type", "application/json");
                Bytes::copy_from_slice(b.as_bytes())
            }
            _ => Bytes::new(),
        };
        let req = builder
            .body(Full::new(body_bytes))
            .map_err(|e| format!("构造请求失败: {e}"))?;

        let resp = client()
            .request(req)
            .await
            .map_err(|e| format!("连接内核失败: {e}"))?;

        let status = resp.status();
        let bytes = resp
            .into_body()
            .collect()
            .await
            .map_err(|e| format!("读取响应失败: {e}"))?
            .to_bytes();
        let text = String::from_utf8_lossy(&bytes).to_string();

        if status.is_success() {
            Ok(text)
        } else {
            Err(format!("内核返回 {}: {}", status.as_u16(), text))
        }
    }

    pub async fn request_bytes(
        method: &str,
        path: &str,
        content_type: Option<&str>,
        body: Vec<u8>,
    ) -> Result<Vec<u8>, String> {
        let uri = socket_uri(path)?;
        let mut builder = Request::builder().method(method_from_str(method)).uri(uri);
        if let Some(content_type) = content_type {
            builder = builder.header("content-type", content_type);
        }
        let req = builder
            .body(Full::new(Bytes::from(body)))
            .map_err(|e| format!("构造请求失败: {e}"))?;
        let resp = client()
            .request(req)
            .await
            .map_err(|e| format!("连接内核失败: {e}"))?;
        let status = resp.status();
        let bytes = resp
            .into_body()
            .collect()
            .await
            .map_err(|e| format!("读取响应失败: {e}"))?
            .to_bytes();
        if status.is_success() {
            Ok(bytes.to_vec())
        } else {
            Err(format!(
                "内核返回 {}: {}",
                status.as_u16(),
                String::from_utf8_lossy(&bytes)
            ))
        }
    }

    /// 订阅某会话的 SSE 事件流,逐条经 Channel 推给前端(每个 data 行一条)。
    pub async fn subscribe(
        session_id: &str,
        channel: Channel<String>,
        ready: oneshot::Sender<Result<(), String>>,
    ) -> Result<(), String> {
        subscribe_path(&format!("/sessions/{session_id}/events"), channel, ready).await
    }

    /// Subscribe to an SSE endpoint and forward each data frame to the renderer.
    pub async fn subscribe_path(
        path: &str,
        channel: Channel<String>,
        ready: oneshot::Sender<Result<(), String>>,
    ) -> Result<(), String> {
        let setup = async {
            let uri = socket_uri(path)?;
            let req = Request::builder()
                .method(Method::GET)
                .uri(uri)
                .header("accept", "text/event-stream")
                .body(Full::new(Bytes::new()))
                .map_err(|e| format!("构造请求失败: {e}"))?;

            let resp = client()
                .request(req)
                .await
                .map_err(|e| format!("连接内核失败: {e}"))?;

            if resp.status() != StatusCode::OK {
                return Err(format!("订阅失败: HTTP {}", resp.status().as_u16()));
            }
            Ok(resp)
        }
        .await;

        let resp = match setup {
            Ok(resp) => {
                let _ = ready.send(Ok(()));
                resp
            }
            Err(error) => {
                let _ = ready.send(Err(error.clone()));
                return Err(error);
            }
        };

        // 直接从 body 流读取,累积到缓冲区后按行切分 SSE。
        // 每个 SSE data: 行是一条 JSON 事件,经 Channel 推给前端。
        let mut stream = BodyDataStream::new(resp.into_body());
        let mut buf = Vec::new();
        while let Some(chunk) = stream.next().await {
            match chunk {
                Ok(mut data) => {
                    buf.extend_from_slice(data.copy_to_bytes(data.remaining()).as_ref());
                    while let Some(pos) = find_line_end(&buf) {
                        let line: Vec<u8> = buf.drain(..=pos).collect();
                        // 去掉行尾 \n 及可能的 \r。
                        let end = line
                            .iter()
                            .rposition(|&b| b != b'\n' && b != b'\r')
                            .map(|p| p + 1)
                            .unwrap_or(0);
                        if let Ok(text) = std::str::from_utf8(&line[..end]) {
                            if let Some(payload) = text.strip_prefix("data: ") {
                                let _ = channel.send(payload.to_string());
                            }
                        }
                    }
                }
                Err(e) => return Err(format!("读取事件流失败: {e}")),
            }
        }
        Ok(())
    }
}

/// 建会话,返回会话 JSON。options 为可选的创建参数(model/project_id/approval_mode)。
#[cfg(unix)]
#[tauri::command]
async fn create_session(options: Option<serde_json::Value>) -> Result<String, String> {
    let body = options.map(|v| v.to_string());
    kernel::request("POST", "/sessions", body.as_deref()).await
}

/// 局部更新会话(模型/工作目录/审批档位),返回更新后的会话 JSON。
#[cfg(unix)]
#[tauri::command]
async fn update_session(session_id: String, patch: serde_json::Value) -> Result<String, String> {
    kernel::request(
        "PATCH",
        &format!("/sessions/{session_id}"),
        Some(&patch.to_string()),
    )
    .await
}

/// 提交一轮对话。
#[cfg(unix)]
#[tauri::command]
async fn submit_turn(
    session_id: String,
    message: String,
    attachments: Option<Vec<serde_json::Value>>,
    browser_elements: Option<Vec<serde_json::Value>>,
) -> Result<String, String> {
    let body = serde_json::json!({
        "message": message,
        "attachments": attachments.unwrap_or_default(),
        "browser_elements": browser_elements.unwrap_or_default(),
    })
    .to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/turns"),
        Some(&body),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn upload_image(session_id: String, name: String, data: Vec<u8>) -> Result<String, String> {
    let boundary = "foya-image-upload-boundary";
    let safe_name = name.replace(['"', '\r', '\n'], "_");
    let mut body = format!(
        "--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{safe_name}\"\r\nContent-Type: application/octet-stream\r\n\r\n"
    )
    .into_bytes();
    body.extend_from_slice(&data);
    body.extend_from_slice(format!("\r\n--{boundary}--\r\n").as_bytes());
    let response = kernel::request_bytes(
        "POST",
        &format!("/sessions/{session_id}/artifacts"),
        Some(&format!("multipart/form-data; boundary={boundary}")),
        body,
    )
    .await?;
    String::from_utf8(response).map_err(|e| format!("附件响应不是 UTF-8: {e}"))
}

#[cfg(unix)]
#[tauri::command]
async fn read_artifact(session_id: String, artifact_id: String) -> Result<Vec<u8>, String> {
    kernel::request_bytes(
        "GET",
        &format!("/sessions/{session_id}/artifacts/{artifact_id}"),
        None,
        Vec::new(),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn delete_artifact(session_id: String, artifact_id: String) -> Result<(), String> {
    kernel::request(
        "DELETE",
        &format!("/sessions/{session_id}/artifacts/{artifact_id}"),
        None,
    )
    .await
    .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn list_canvases(session_id: Option<String>) -> Result<String, String> {
    let path = session_id
        .filter(|value| !value.is_empty())
        .map(|value| format!("/canvases?session_id={}", encode_query_component(&value)))
        .unwrap_or_else(|| "/canvases".to_string());
    kernel::request("GET", &path, None).await
}

#[cfg(unix)]
#[tauri::command]
async fn create_canvas(
    session_id: Option<String>,
    project_id: Option<String>,
    title: Option<String>,
) -> Result<String, String> {
    let body = serde_json::json!({
        "session_id": session_id.unwrap_or_default(),
        "project_id": project_id.unwrap_or_default(),
        "title": title.unwrap_or_default(),
    })
    .to_string();
    kernel::request("POST", "/canvases", Some(&body)).await
}

#[cfg(unix)]
#[tauri::command]
async fn get_canvas(canvas_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/canvases/{canvas_id}"), None).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_canvas(canvas_id: String, patch: serde_json::Value) -> Result<String, String> {
    kernel::request(
        "PATCH",
        &format!("/canvases/{canvas_id}"),
        Some(&patch.to_string()),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn delete_canvas(canvas_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/canvases/{canvas_id}"), None)
        .await
        .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn upload_canvas_asset(
    canvas_id: String,
    name: String,
    media_type: String,
    data: Vec<u8>,
) -> Result<String, String> {
    let boundary = "foya-canvas-asset-boundary";
    let safe_name = name.replace(['"', '\r', '\n'], "_");
    let safe_type = media_type.replace(['\r', '\n'], "");
    let mut body = format!(
        "--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{safe_name}\"\r\nContent-Type: {safe_type}\r\n\r\n"
    ).into_bytes();
    body.extend_from_slice(&data);
    body.extend_from_slice(format!("\r\n--{boundary}--\r\n").as_bytes());
    let response = kernel::request_bytes(
        "POST",
        &format!("/canvases/{canvas_id}/assets"),
        Some(&format!("multipart/form-data; boundary={boundary}")),
        body,
    )
    .await?;
    String::from_utf8(response).map_err(|e| format!("画布资产响应不是 UTF-8: {e}"))
}

#[cfg(unix)]
#[tauri::command]
async fn read_canvas_asset(canvas_id: String, asset_id: String) -> Result<Vec<u8>, String> {
    kernel::request_bytes(
        "GET",
        &format!("/canvases/{canvas_id}/assets/{asset_id}"),
        None,
        Vec::new(),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn generate_canvas_image(
    canvas_id: String,
    request: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/canvases/{canvas_id}/generate-image"),
        Some(&request.to_string()),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn generate_canvas_video(
    canvas_id: String,
    request: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/canvases/{canvas_id}/generate-video"),
        Some(&request.to_string()),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn subscribe_canvas_events(
    canvas_id: String,
    channel: Channel<String>,
) -> Result<(), String> {
    let (ready_tx, ready_rx) = tokio::sync::oneshot::channel();
    tauri::async_runtime::spawn(async move {
        let path = format!("/canvases/{canvas_id}/events");
        let _ = kernel::subscribe_path(&path, channel, ready_tx).await;
    });
    ready_rx
        .await
        .map_err(|_| "画布事件订阅在连接前意外结束".to_string())?
}

/// 编辑一条已完成的用户消息并从该位置创建新分支。
#[cfg(unix)]
#[tauri::command]
async fn edit_turn(
    session_id: String,
    message_seq: u64,
    message: String,
    confirm_effects: bool,
    expected_head_seq: u64,
) -> Result<String, String> {
    let body = serde_json::json!({
        "message": message,
        "confirm_effects": confirm_effects,
        "expected_head_seq": expected_head_seq,
    })
    .to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/turns/{message_seq}/edit"),
        Some(&body),
    )
    .await
}

/// 手动压缩会话的已完成历史。
#[cfg(unix)]
#[tauri::command]
async fn compact_session(session_id: String) -> Result<String, String> {
    kernel::request("POST", &format!("/sessions/{session_id}/compact"), None).await
}

/// 读取会话的待发送队列。
#[cfg(unix)]
#[tauri::command]
async fn list_queued_messages(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/queue"), None).await
}

/// 显式追加一条待发送消息。
#[cfg(unix)]
#[tauri::command]
async fn enqueue_message(
    session_id: String,
    message: String,
    attachments: Option<Vec<serde_json::Value>>,
    browser_elements: Option<Vec<serde_json::Value>>,
) -> Result<String, String> {
    let body = serde_json::json!({
        "message": message,
        "attachments": attachments.unwrap_or_default(),
        "browser_elements": browser_elements.unwrap_or_default(),
    })
    .to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/queue"),
        Some(&body),
    )
    .await
}

/// 编辑待发送消息正文或顺序。
#[cfg(unix)]
#[tauri::command]
async fn update_queued_message(
    session_id: String,
    message_id: String,
    patch: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "PATCH",
        &format!("/sessions/{session_id}/queue/{message_id}"),
        Some(&patch.to_string()),
    )
    .await
}

/// 删除一条待发送消息。
#[cfg(unix)]
#[tauri::command]
async fn delete_queued_message(session_id: String, message_id: String) -> Result<(), String> {
    kernel::request(
        "DELETE",
        &format!("/sessions/{session_id}/queue/{message_id}"),
        None,
    )
    .await
    .map(|_| ())
}

/// 取消当前回合并优先发送选中的队列消息。
#[cfg(unix)]
#[tauri::command]
async fn dispatch_queued_message(session_id: String, message_id: String) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/queue/{message_id}/dispatch"),
        None,
    )
    .await
}

/// 列出所有会话。
#[cfg(unix)]
#[tauri::command]
async fn list_sessions() -> Result<String, String> {
    kernel::request("GET", "/sessions", None).await
}

/// 列出一个父会话的直接子 Agent 会话。
#[cfg(unix)]
#[tauri::command]
async fn list_child_sessions(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/children"), None).await
}

/// 列出父会话创建的异步 Agent runs。
#[cfg(unix)]
#[tauri::command]
async fn list_agent_runs(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/agents"), None).await
}

/// 启动一个异步 Agent run。
#[cfg(unix)]
#[tauri::command]
async fn start_agent(session_id: String, request: serde_json::Value) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/agents"),
        Some(&request.to_string()),
    )
    .await
}

/// 取消一个异步 Agent run。
#[cfg(unix)]
#[tauri::command]
async fn cancel_agent(session_id: String, run_id: String) -> Result<(), String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/agents/{run_id}/cancel"),
        None,
    )
    .await
    .map(|_| ())
}

/// 读取父任务树的 token 预算。
#[cfg(unix)]
#[tauri::command]
async fn load_agent_budget(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/agent-budget"), None).await
}

/// 加载某会话的对话历史。
#[cfg(unix)]
#[tauri::command]
async fn load_history(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/history"), None).await
}

/// 读取会话最近一次模型请求的 token 使用情况。
#[cfg(unix)]
#[tauri::command]
async fn load_usage(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/usage"), None).await
}

/// 列出已配置的模型连接(key 脱敏)。
#[cfg(unix)]
#[tauri::command]
async fn list_connections() -> Result<String, String> {
    kernel::request("GET", "/connections", None).await
}

/// 新建一个 API Key 模型连接。
#[cfg(unix)]
#[tauri::command]
async fn create_connection(config: serde_json::Value) -> Result<String, String> {
    kernel::request("POST", "/connections", Some(&config.to_string())).await
}

/// 局部更新一个模型连接。
#[cfg(unix)]
#[tauri::command]
async fn update_connection(
    connection_id: String,
    config: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "PATCH",
        &format!("/connections/{connection_id}"),
        Some(&config.to_string()),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn delete_connection(connection_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/connections/{connection_id}"), None)
        .await
        .map(|_| ())
}

/// 拉取指定连接可用的模型目录。
#[cfg(unix)]
#[tauri::command]
async fn list_connection_models(connection_id: String, refresh: bool) -> Result<String, String> {
    let path = if refresh {
        format!("/connections/{connection_id}/models?refresh=true")
    } else {
        format!("/connections/{connection_id}/models")
    };
    kernel::request("GET", &path, None).await
}

#[cfg(unix)]
#[tauri::command]
async fn get_default_models() -> Result<String, String> {
    kernel::request("GET", "/settings/default-models", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_default_models(defaults: serde_json::Value) -> Result<String, String> {
    kernel::request(
        "PUT",
        "/settings/default-models",
        Some(&defaults.to_string()),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn list_skills() -> Result<String, String> {
    kernel::request("GET", "/skills?all=true", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn list_agents() -> Result<String, String> {
    kernel::request("GET", "/agents", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn get_agent_limits() -> Result<String, String> {
    kernel::request("GET", "/settings/agent-limits", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_agent_limits(limits: serde_json::Value) -> Result<String, String> {
    kernel::request("PUT", "/settings/agent-limits", Some(&limits.to_string())).await
}

#[cfg(unix)]
#[tauri::command]
async fn get_feishu_bot_settings() -> Result<String, String> {
    kernel::request("GET", "/settings/feishu-bot", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_feishu_bot_settings(settings: serde_json::Value) -> Result<String, String> {
    kernel::request(
        "PUT",
        "/settings/feishu-bot",
        Some(&settings.to_string()),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn list_channels() -> Result<String, String> {
    kernel::request("GET", "/channels", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn create_channel(settings: serde_json::Value) -> Result<String, String> {
    kernel::request("POST", "/channels", Some(&settings.to_string())).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_channel(
    channel_id: String,
    settings: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "PUT",
        &format!("/channels/{channel_id}"),
        Some(&settings.to_string()),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn delete_channel(channel_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/channels/{channel_id}"), None)
        .await
        .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn list_automations() -> Result<String, String> {
    kernel::request("GET", "/automations", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn create_automation(input: serde_json::Value) -> Result<String, String> {
    kernel::request("POST", "/automations", Some(&input.to_string())).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_automation(
    automation_id: String,
    input: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "PUT",
        &format!("/automations/{automation_id}"),
        Some(&input.to_string()),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn delete_automation(automation_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/automations/{automation_id}"), None)
        .await
        .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn run_automation(automation_id: String) -> Result<String, String> {
    kernel::request("POST", &format!("/automations/{automation_id}/run"), None).await
}

#[cfg(unix)]
#[tauri::command]
async fn get_memory_settings() -> Result<String, String> {
    kernel::request("GET", "/settings/memory", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_memory_settings(settings: serde_json::Value) -> Result<String, String> {
    kernel::request("PUT", "/settings/memory", Some(&settings.to_string())).await
}

#[cfg(unix)]
#[tauri::command]
async fn get_hooks(scope: String, project_id: String) -> Result<String, String> {
    let mut path = format!("/hooks?scope={}", encode_query_component(&scope));
    if !project_id.is_empty() {
        path.push_str("&project_id=");
        path.push_str(&encode_query_component(&project_id));
    }
    kernel::request("GET", &path, None).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_hooks(request: serde_json::Value) -> Result<String, String> {
    kernel::request("PUT", "/hooks", Some(&request.to_string())).await
}

#[cfg(unix)]
#[tauri::command]
async fn list_commands(scope: String, project_id: String) -> Result<String, String> {
    let mut path = format!("/commands?scope={}", encode_query_component(&scope));
    if !project_id.is_empty() {
        path.push_str("&project_id=");
        path.push_str(&encode_query_component(&project_id));
    }
    kernel::request("GET", &path, None).await
}

#[cfg(unix)]
#[tauri::command]
async fn create_command(request: serde_json::Value) -> Result<String, String> {
    kernel::request("POST", "/commands", Some(&request.to_string())).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_command(command_ref: String, request: serde_json::Value) -> Result<String, String> {
    kernel::request(
        "PATCH",
        &format!("/commands/{}", encode_query_component(&command_ref)),
        Some(&request.to_string()),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn delete_command(
    command_ref: String,
    scope: String,
    project_id: String,
) -> Result<(), String> {
    let mut path = format!(
        "/commands/{}?scope={}",
        encode_query_component(&command_ref),
        encode_query_component(&scope),
    );
    if !project_id.is_empty() {
        path.push_str("&project_id=");
        path.push_str(&encode_query_component(&project_id));
    }
    kernel::request("DELETE", &path, None).await.map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn list_session_commands(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/commands"), None).await
}

#[cfg(unix)]
#[tauri::command]
async fn execute_command(session_id: String, name: String, args: String) -> Result<String, String> {
    let body = serde_json::json!({ "args": args }).to_string();
    kernel::request(
        "POST",
        &format!(
            "/sessions/{session_id}/commands/{}",
            encode_query_component(&name)
        ),
        Some(&body),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn get_workflow(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/workflow"), None).await
}

#[cfg(unix)]
#[tauri::command]
async fn approve_workflow(session_id: String, workflow_id: String) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/workflow/{workflow_id}/approve"),
        None,
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn list_projects() -> Result<String, String> {
    kernel::request("GET", "/projects", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn register_project(path: String, name: String) -> Result<String, String> {
    let body = serde_json::json!({ "path": path, "name": name }).to_string();
    kernel::request("POST", "/projects", Some(&body)).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_project(project_id: String, patch: serde_json::Value) -> Result<String, String> {
    kernel::request(
        "PATCH",
        &format!("/projects/{project_id}"),
        Some(&patch.to_string()),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn delete_project(project_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/projects/{project_id}"), None)
        .await
        .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn list_project_skills(project_id: String) -> Result<String, String> {
    let path = format!("/projects/{}/skills", encode_query_component(&project_id));
    kernel::request("GET", &path, None).await
}

#[cfg(unix)]
#[tauri::command]
async fn list_project_agents(project_id: String) -> Result<String, String> {
    let path = format!("/projects/{}/agents", encode_query_component(&project_id));
    kernel::request("GET", &path, None).await
}

#[cfg(unix)]
fn context_resource(kind: &str) -> Result<&'static str, String> {
    match kind {
        "rule" => Ok("rules"),
        "memory" => Ok("memories"),
        _ => Err("未知上下文类型".into()),
    }
}

#[cfg(unix)]
#[tauri::command]
async fn list_context_items(
    kind: String,
    scope: String,
    project_id: Option<String>,
) -> Result<String, String> {
    let resource = context_resource(&kind)?;
    let mut path = format!("/{resource}?scope={}", encode_query_component(&scope));
    if let Some(project_id) = project_id.filter(|value| !value.is_empty()) {
        path.push_str("&project_id=");
        path.push_str(&encode_query_component(&project_id));
    }
    kernel::request("GET", &path, None).await
}

#[cfg(unix)]
#[tauri::command]
async fn create_context_item(kind: String, item: serde_json::Value) -> Result<String, String> {
    let resource = context_resource(&kind)?;
    kernel::request("POST", &format!("/{resource}"), Some(&item.to_string())).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_context_item(
    kind: String,
    item_id: String,
    item: serde_json::Value,
) -> Result<String, String> {
    let resource = context_resource(&kind)?;
    kernel::request(
        "PATCH",
        &format!("/{resource}/{item_id}"),
        Some(&item.to_string()),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn delete_context_item(kind: String, item_id: String) -> Result<(), String> {
    let resource = context_resource(&kind)?;
    kernel::request("DELETE", &format!("/{resource}/{item_id}"), None)
        .await
        .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn subscribe_context_events(channel: Channel<String>) -> Result<(), String> {
    let (ready_tx, ready_rx) = tokio::sync::oneshot::channel();
    tauri::async_runtime::spawn(async move {
        let _ = kernel::subscribe_path("/context/events", channel, ready_tx).await;
    });
    ready_rx
        .await
        .map_err(|_| "规则与记忆事件订阅在连接前意外结束".to_string())?
}

#[cfg(unix)]
#[tauri::command]
async fn set_skill_enabled(skill_ref: String, enabled: bool) -> Result<(), String> {
    let body = serde_json::json!({ "enabled": enabled }).to_string();
    let path = format!("/skills/{}", encode_query_component(&skill_ref));
    kernel::request("PATCH", &path, Some(&body))
        .await
        .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn set_skill_pinned(skill_ref: String, pinned: bool) -> Result<(), String> {
    let body = serde_json::json!({ "pinned": pinned }).to_string();
    let path = format!("/skills/{}/pinned", encode_query_component(&skill_ref));
    kernel::request("PATCH", &path, Some(&body))
        .await
        .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn get_web_search_settings() -> Result<String, String> {
    kernel::request("GET", "/web-search", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_web_search_settings(settings: serde_json::Value) -> Result<String, String> {
    kernel::request("PUT", "/web-search", Some(&settings.to_string())).await
}

#[cfg(unix)]
#[tauri::command]
async fn test_web_search(provider_id: String, query: String) -> Result<String, String> {
    let body = serde_json::json!({ "provider_id": provider_id, "query": query }).to_string();
    kernel::request("POST", "/web-search/test", Some(&body)).await
}

#[cfg(unix)]
#[tauri::command]
async fn resolve_browser_action(
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

#[cfg(unix)]
#[tauri::command]
async fn get_mcp_config() -> Result<String, String> {
    kernel::request("GET", "/mcp", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_mcp_config(config: serde_json::Value) -> Result<String, String> {
    kernel::request("PUT", "/mcp", Some(&config.to_string())).await
}

#[cfg(unix)]
#[tauri::command]
async fn get_mcp_status() -> Result<String, String> {
    kernel::request("GET", "/mcp/status", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn search_mcp_registry(query: String) -> Result<String, String> {
    let path = format!("/mcp/registry?search={}", encode_query_component(&query));
    kernel::request("GET", &path, None).await
}

/// 订阅会话事件流。在后台异步任务持续把 SSE 事件经 Channel 推给前端。
#[cfg(unix)]
#[tauri::command]
async fn subscribe_events(session_id: String, channel: Channel<String>) -> Result<(), String> {
    let (ready_tx, ready_rx) = tokio::sync::oneshot::channel();
    tauri::async_runtime::spawn(async move {
        let _ = kernel::subscribe(&session_id, channel, ready_tx).await;
    });
    ready_rx
        .await
        .map_err(|_| "事件订阅在连接前意外结束".to_string())?
}

#[cfg(unix)]
#[tauri::command]
async fn start_terminal(session_id: String, cols: u16, rows: u16) -> Result<String, String> {
    let body = serde_json::json!({ "cols": cols, "rows": rows }).to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/terminals"),
        Some(&body),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn attach_terminal(session_id: String, terminal_ref: String) -> Result<String, String> {
    kernel::request(
        "GET",
        &format!("/sessions/{session_id}/terminals/{terminal_ref}"),
        None,
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn write_terminal(
    session_id: String,
    terminal_ref: String,
    input: String,
) -> Result<(), String> {
    let body = serde_json::json!({ "input": input }).to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/terminals/{terminal_ref}/input"),
        Some(&body),
    )
    .await
    .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn resize_terminal(
    session_id: String,
    terminal_ref: String,
    cols: u16,
    rows: u16,
) -> Result<(), String> {
    let body = serde_json::json!({ "cols": cols, "rows": rows }).to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/terminals/{terminal_ref}/resize"),
        Some(&body),
    )
    .await
    .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn stop_terminal(session_id: String, terminal_ref: String) -> Result<(), String> {
    kernel::request(
        "DELETE",
        &format!("/sessions/{session_id}/terminals/{terminal_ref}"),
        None,
    )
    .await
    .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn subscribe_terminal(
    session_id: String,
    terminal_ref: String,
    after: u64,
    channel: Channel<String>,
) -> Result<(), String> {
    let path = format!("/sessions/{session_id}/terminals/{terminal_ref}/events?after={after}");
    let (ready_tx, ready_rx) = tokio::sync::oneshot::channel();
    tauri::async_runtime::spawn(async move {
        let _ = kernel::subscribe_path(&path, channel, ready_tx).await;
    });
    ready_rx
        .await
        .map_err(|_| "终端订阅在连接前意外结束".to_string())?
}

/// 回执审批决策(批准/拒绝)。
#[cfg(unix)]
#[tauri::command]
async fn resolve_approval(
    session_id: String,
    request_id: String,
    decision: String,
) -> Result<(), String> {
    let body = serde_json::json!({ "request_id": request_id, "decision": decision }).to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/approvals/{request_id}"),
        Some(&body),
    )
    .await
    .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn answer_questions(
    session_id: String,
    batch_id: String,
    answers: serde_json::Value,
) -> Result<(), String> {
    let body = serde_json::json!({ "answers": answers }).to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/questions/{batch_id}/answer"),
        Some(&body),
    )
    .await
    .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn cancel_questions(session_id: String, batch_id: String) -> Result<(), String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/questions/{batch_id}/cancel"),
        None,
    )
    .await
    .map(|_| ())
}

/// 中断当前会话正在运行的回合(用户点停止)。
#[cfg(unix)]
#[tauri::command]
async fn cancel_turn(session_id: String) -> Result<(), String> {
    kernel::request("POST", &format!("/sessions/{session_id}/cancel"), None)
        .await
        .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn cancel_tool(session_id: String, tool_call_id: String) -> Result<(), String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/tools/{tool_call_id}/cancel"),
        None,
    )
    .await
    .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn background_tool(session_id: String, tool_call_id: String) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/tools/{tool_call_id}/background"),
        None,
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn reveal_tool_command(session_id: String, tool_call_id: String) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/tools/{tool_call_id}/reveal"),
        None,
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn list_background_commands(session_id: String) -> Result<String, String> {
    kernel::request(
        "GET",
        &format!("/sessions/{session_id}/background-commands"),
        None,
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn get_background_command(session_id: String, command_id: String) -> Result<String, String> {
    kernel::request(
        "GET",
        &format!("/sessions/{session_id}/background-commands/{command_id}"),
        None,
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn stop_background_command(session_id: String, command_id: String) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/background-commands/{command_id}/cancel"),
        None,
    )
    .await
}

/// 删除会话(中断回合、清除元数据与历史、广播移除)。
#[cfg(unix)]
#[tauri::command]
async fn delete_session(session_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/sessions/{session_id}"), None)
        .await
        .map(|_| ())
}

#[tauri::command]
async fn list_project_files(project_path: String) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let root = project_root(&project_path)?;
        let mut entries = Vec::new();
        collect_project_entries(&root, &root, &mut entries)?;
        serde_json::to_string(&entries).map_err(|e| e.to_string())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn read_project_file(project_path: String, path: String) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let file = safe_project_file(&project_path, &path)?;
        let metadata = fs::metadata(&file).map_err(|e| format!("无法读取文件信息: {e}"))?;
        if metadata.len() > MAX_PREVIEW_BYTES {
            return Err("文件超过 2 MiB，无法预览".into());
        }
        let bytes = fs::read(file).map_err(|e| format!("无法读取文件: {e}"))?;
        String::from_utf8(bytes).map_err(|_| "二进制文件暂不支持预览".into())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn create_project_file(project_path: String, path: String) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let root = project_root(&project_path)?;
        let file = safe_project_destination(&project_path, &path)?;
        fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&file)
            .map_err(|e| format!("无法创建文件: {e}"))?;
        project_relative_string(&root, &file)
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn create_project_directory(project_path: String, path: String) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let root = project_root(&project_path)?;
        let directory = safe_project_destination(&project_path, &path)?;
        fs::create_dir(&directory).map_err(|e| format!("无法创建文件夹: {e}"))?;
        project_relative_string(&root, &directory)
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn rename_project_entry(
    project_path: String,
    path: String,
    new_name: String,
) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let root = project_root(&project_path)?;
        let entry = safe_project_entry(&project_path, &path)?;
        let new_name = safe_entry_name(&new_name)?;
        let destination = entry.with_file_name(new_name);

        if destination != entry {
            if let Ok(existing) = fs::canonicalize(&destination) {
                if existing != entry {
                    return Err("同名文件或文件夹已存在".into());
                }
            }
            fs::rename(&entry, &destination).map_err(|e| format!("无法重命名: {e}"))?;
        }

        project_relative_string(&root, &destination)
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn delete_project_entry(project_path: String, path: String) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        let entry = safe_project_entry(&project_path, &path)?;
        if entry.is_dir() {
            fs::remove_dir_all(entry).map_err(|e| format!("无法删除文件夹: {e}"))
        } else {
            fs::remove_file(entry).map_err(|e| format!("无法删除文件: {e}"))
        }
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn resolve_project_path(project_path: String, path: String) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let entry = if path.is_empty() {
            project_root(&project_path)?
        } else {
            safe_project_entry(&project_path, &path)?
        };
        Ok(entry.to_string_lossy().into_owned())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn navigate_browser(
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
fn set_browser_viewport(
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
fn browser_back(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview.eval("history.back()").map_err(|e| e.to_string())?;
    }
    Ok(())
}

#[tauri::command]
fn browser_forward(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview
            .eval("history.forward()")
            .map_err(|e| e.to_string())?;
    }
    Ok(())
}

#[tauri::command]
fn browser_reload(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview.reload().map_err(|e| e.to_string())?;
    }
    Ok(())
}

#[tauri::command]
fn set_browser_element_picker(
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
async fn execute_browser_action(
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
fn browser_action_result(request_id: String, result: BrowserActionResult) -> Result<(), String> {
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
fn hide_browser(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview.hide().map_err(|e| e.to_string())?;
    }
    Ok(())
}

#[tauri::command]
fn close_browser(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Ok(mut active) = ACTIVE_BROWSER_PICKERS.lock() {
        active.remove(&browser_id);
    }
    if let Some(webview) = app.get_webview(&label) {
        webview.close().map_err(|e| e.to_string())?;
    }
    Ok(())
}

// Windows 占位。
#[cfg(not(unix))]
#[tauri::command]
fn create_session(_options: Option<serde_json::Value>) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn update_session(_session_id: String, _patch: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn submit_turn(
    _session_id: String,
    _message: String,
    _attachments: Option<Vec<serde_json::Value>>,
    _browser_elements: Option<Vec<serde_json::Value>>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn upload_image(_session_id: String, _name: String, _data: Vec<u8>) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn read_artifact(_session_id: String, _artifact_id: String) -> Result<Vec<u8>, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn delete_artifact(_session_id: String, _artifact_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn list_canvases(_session_id: Option<String>) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}
#[cfg(not(unix))]
#[tauri::command]
fn create_canvas(
    _session_id: Option<String>,
    _project_id: Option<String>,
    _title: Option<String>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}
#[cfg(not(unix))]
#[tauri::command]
fn get_canvas(_canvas_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}
#[cfg(not(unix))]
#[tauri::command]
fn update_canvas(_canvas_id: String, _patch: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}
#[cfg(not(unix))]
#[tauri::command]
fn delete_canvas(_canvas_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}
#[cfg(not(unix))]
#[tauri::command]
fn upload_canvas_asset(
    _canvas_id: String,
    _name: String,
    _media_type: String,
    _data: Vec<u8>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}
#[cfg(not(unix))]
#[tauri::command]
fn read_canvas_asset(_canvas_id: String, _asset_id: String) -> Result<Vec<u8>, String> {
    Err("Windows 传输尚未实现".into())
}
#[cfg(not(unix))]
#[tauri::command]
fn generate_canvas_image(
    _canvas_id: String,
    _request: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}
#[cfg(not(unix))]
#[tauri::command]
fn generate_canvas_video(
    _canvas_id: String,
    _request: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}
#[cfg(not(unix))]
#[tauri::command]
fn subscribe_canvas_events(_canvas_id: String, _channel: Channel<String>) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn edit_turn(
    _session_id: String,
    _message_seq: u64,
    _message: String,
    _confirm_effects: bool,
    _expected_head_seq: u64,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn compact_session(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn list_queued_messages(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn enqueue_message(
    _session_id: String,
    _message: String,
    _attachments: Option<Vec<serde_json::Value>>,
    _browser_elements: Option<Vec<serde_json::Value>>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn update_queued_message(
    _session_id: String,
    _message_id: String,
    _patch: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn delete_queued_message(_session_id: String, _message_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn dispatch_queued_message(_session_id: String, _message_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn list_sessions() -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn list_child_sessions(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn load_history(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn load_usage(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn list_connections() -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn create_connection(_config: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn update_connection(_connection_id: String, _config: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn delete_connection(_connection_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn list_connection_models(_connection_id: String, _refresh: bool) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn get_default_models() -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn update_default_models(_defaults: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn subscribe_events(_session_id: String, _channel: Channel<String>) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn start_terminal(_session_id: String, _cols: u16, _rows: u16) -> Result<String, String> {
    Err("Windows 终端尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn attach_terminal(_session_id: String, _terminal_ref: String) -> Result<String, String> {
    Err("Windows 终端尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn write_terminal(
    _session_id: String,
    _terminal_ref: String,
    _input: String,
) -> Result<(), String> {
    Err("Windows 终端尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn resize_terminal(
    _session_id: String,
    _terminal_ref: String,
    _cols: u16,
    _rows: u16,
) -> Result<(), String> {
    Err("Windows 终端尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn stop_terminal(_session_id: String, _terminal_ref: String) -> Result<(), String> {
    Err("Windows 终端尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn subscribe_terminal(
    _session_id: String,
    _terminal_ref: String,
    _after: u64,
    _channel: Channel<String>,
) -> Result<(), String> {
    Err("Windows 终端尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn resolve_approval(
    _session_id: String,
    _request_id: String,
    _decision: String,
) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn answer_questions(
    _session_id: String,
    _batch_id: String,
    _answers: serde_json::Value,
) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn cancel_questions(_session_id: String, _batch_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn cancel_turn(_session_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn cancel_tool(_session_id: String, _tool_call_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn background_tool(_session_id: String, _tool_call_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn reveal_tool_command(_session_id: String, _tool_call_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_background_commands(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn get_background_command(
    _session_id: String,
    _command_id: String,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn stop_background_command(
    _session_id: String,
    _command_id: String,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn delete_session(_session_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_skills() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_agents() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn get_agent_limits() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_agent_limits(_limits: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn get_feishu_bot_settings() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_feishu_bot_settings(_settings: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_channels() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn create_channel(_settings: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_channel(
    _channel_id: String,
    _settings: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn delete_channel(_channel_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_automations() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn create_automation(_input: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_automation(
    _automation_id: String,
    _input: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn delete_automation(_automation_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn run_automation(_automation_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn get_memory_settings() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_memory_settings(_settings: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn get_hooks(_scope: String, _project_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_hooks(_request: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_commands(_scope: String, _project_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn create_command(_request: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_command(
    _command_ref: String,
    _request: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn delete_command(
    _command_ref: String,
    _scope: String,
    _project_id: String,
) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_session_commands(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn execute_command(
    _session_id: String,
    _name: String,
    _args: String,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn get_workflow(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn approve_workflow(_session_id: String, _workflow_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_projects() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn register_project(_path: String, _name: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_project(_project_id: String, _patch: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn delete_project(_project_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_project_skills(_project_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_project_agents(_project_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_context_items(
    _kind: String,
    _scope: String,
    _project_id: Option<String>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn create_context_item(_kind: String, _item: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_context_item(
    _kind: String,
    _item_id: String,
    _item: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn delete_context_item(_kind: String, _item_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn subscribe_context_events(_channel: Channel<String>) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn set_skill_enabled(_skill_ref: String, _enabled: bool) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn set_skill_pinned(_skill_ref: String, _pinned: bool) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn get_web_search_settings() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_web_search_settings(_settings: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn test_web_search(_provider_id: String, _query: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn resolve_browser_action(
    _session_id: String,
    _request_id: String,
    _result: BrowserActionResult,
) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn get_mcp_config() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_mcp_config(_config: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn get_mcp_status() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn search_mcp_registry(_query: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let sidecar_child = Arc::new(Mutex::new(None::<CommandChild>));
    let setup_child = Arc::clone(&sidecar_child);
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .setup(move |app| {
            // macOS 用 titleBarStyle=Overlay(在 tauri.conf.json)保留红绿灯;
            // 其他平台关闭原生装饰,改用前端自绘标题栏(WindowControls)。
            #[cfg(not(target_os = "macos"))]
            {
                if let Some(window) = app.get_webview_window("main") {
                    window.set_decorations(false)?;
                }
            }

            // 启动时把打包进来的 Go 内核 sidecar 拉起(connect-or-spawn 的 spawn 部分)。
            // Tauri 会自动解析当前平台对应的二进制(如 foya-aarch64-apple-darwin)。
            let mut sidecar = app
                .shell()
                .sidecar("foya")?
                .env("FOYA_PARENT_PID", std::process::id().to_string());
            // BYOK:把 provider 配置从当前进程环境透传给内核 sidecar。
            // 脚手架阶段靠环境变量注入(启动 app 前 export FOYA_PROVIDER_*);
            // 后续改为从设置界面写入、key 存 OS keychain。
            for key in [
                "FOYA_PROVIDER_BASE_URL",
                "FOYA_PROVIDER_API_KEY",
                "FOYA_PROVIDER_MODEL",
            ] {
                if let Ok(val) = std::env::var(key) {
                    sidecar = sidecar.env(key, val);
                }
            }
            let (mut rx, child) = sidecar.spawn()?;
            *setup_child.lock().expect("sidecar child lock poisoned") = Some(child);
            tauri::async_runtime::spawn(async move {
                while let Some(event) = rx.recv().await {
                    match event {
                        CommandEvent::Stdout(line) => {
                            println!("[foya-kernel] {}", String::from_utf8_lossy(&line));
                        }
                        CommandEvent::Stderr(line) => {
                            eprintln!("[foya-kernel] {}", String::from_utf8_lossy(&line));
                        }
                        CommandEvent::Error(error) => {
                            eprintln!("[foya-kernel] process error: {error}");
                        }
                        CommandEvent::Terminated(status) => {
                            eprintln!(
                                "[foya-kernel] exited: code={:?} signal={:?}",
                                status.code, status.signal
                            );
                        }
                        _ => {}
                    }
                }
            });
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            create_session,
            update_session,
            submit_turn,
            upload_image,
            read_artifact,
            delete_artifact,
            list_canvases,
            create_canvas,
            get_canvas,
            update_canvas,
            delete_canvas,
            upload_canvas_asset,
            read_canvas_asset,
            generate_canvas_image,
            generate_canvas_video,
            subscribe_canvas_events,
            edit_turn,
            compact_session,
            list_queued_messages,
            enqueue_message,
            update_queued_message,
            delete_queued_message,
            dispatch_queued_message,
            list_sessions,
            list_child_sessions,
            list_agent_runs,
            start_agent,
            cancel_agent,
            load_agent_budget,
            load_history,
            load_usage,
            list_connections,
            create_connection,
            update_connection,
            delete_connection,
            list_connection_models,
            get_default_models,
            update_default_models,
            list_skills,
            list_agents,
            get_agent_limits,
            update_agent_limits,
            get_feishu_bot_settings,
            update_feishu_bot_settings,
            list_channels,
            create_channel,
            update_channel,
            delete_channel,
            list_automations,
            create_automation,
            update_automation,
            delete_automation,
            run_automation,
            get_memory_settings,
            update_memory_settings,
            get_hooks,
            update_hooks,
            list_commands,
            create_command,
            update_command,
            delete_command,
            list_session_commands,
            execute_command,
            get_workflow,
            approve_workflow,
            list_projects,
            register_project,
            update_project,
            delete_project,
            list_project_skills,
            list_project_agents,
            list_context_items,
            create_context_item,
            update_context_item,
            delete_context_item,
            subscribe_context_events,
            set_skill_enabled,
            set_skill_pinned,
            get_web_search_settings,
            update_web_search_settings,
            test_web_search,
            resolve_browser_action,
            get_mcp_config,
            update_mcp_config,
            get_mcp_status,
            search_mcp_registry,
            subscribe_events,
            start_terminal,
            attach_terminal,
            write_terminal,
            resize_terminal,
            stop_terminal,
            subscribe_terminal,
            navigate_browser,
            set_browser_viewport,
            browser_back,
            browser_forward,
            browser_reload,
            set_browser_element_picker,
            execute_browser_action,
            browser_action_result,
            hide_browser,
            close_browser,
            list_project_files,
            read_project_file,
            create_project_file,
            create_project_directory,
            rename_project_entry,
            delete_project_entry,
            resolve_project_path,
            resolve_approval,
            answer_questions,
            cancel_questions,
            cancel_turn,
            cancel_tool,
            background_tool,
            reveal_tool_command,
            list_background_commands,
            get_background_command,
            stop_background_command,
            delete_session
        ])
        .build(tauri::generate_context!())
        .expect("error while building tauri application");

    app.run(move |_app_handle, event| {
        if matches!(event, tauri::RunEvent::Exit) {
            if let Some(child) = sidecar_child
                .lock()
                .expect("sidecar child lock poisoned")
                .take()
            {
                let _ = child.kill();
            }
        }
    });
}
