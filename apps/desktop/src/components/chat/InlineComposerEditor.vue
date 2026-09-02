<script setup lang="ts">
import {
  createApp,
  h,
  nextTick,
  onMounted,
  onUnmounted,
  ref,
  watch,
  type App as VueApp,
} from "vue";
import { MousePointer2Icon } from "@lucide/vue";
import type { BrowserElementSelection } from "@/lib/api";

const props = withDefaults(
  defineProps<{
    modelValue?: string;
    browserElements?: BrowserElementSelection[];
    disabled?: boolean;
    placeholder?: string;
  }>(),
  {
    modelValue: "",
    browserElements: () => [],
    disabled: false,
    placeholder: "",
  }
);

const emit = defineEmits<{
  (event: "update:modelValue", value: string): void;
  (event: "remove-browser-element", index: number): void;
  (event: "focus"): void;
  (event: "blur"): void;
  (event: "keydown", value: KeyboardEvent): void;
  (event: "paste", value: ClipboardEvent): void;
}>();

const editor = ref<HTMLElement | null>(null);
let savedRange: Range | null = null;
let syncing = false;
const tokenApps = new Map<string, VueApp>();

function elementKey(element: BrowserElementSelection): string {
  return JSON.stringify([element.page_url, element.selector]);
}

function tokenElement(node: Node | null): HTMLElement | null {
  if (!(node instanceof HTMLElement)) return null;
  if (node.dataset.browserElementKey) return node;
  return node.closest<HTMLElement>("[data-browser-element-key]");
}

function readText(): string {
  const root = editor.value;
  if (!root) return "";

  const read = (node: Node): string => {
    if (tokenElement(node)) return "";
    if (node.nodeType === Node.TEXT_NODE) {
      return (node.textContent ?? "").replace(/\u200B/g, "");
    }
    if (!(node instanceof HTMLElement)) return "";
    if (node.tagName === "BR") return "\n";

    let value = "";
    for (const child of node.childNodes) value += read(child);
    if (
      node !== root &&
      (node.tagName === "DIV" || node.tagName === "P") &&
      value &&
      !value.endsWith("\n")
    ) {
      value += "\n";
    }
    return value;
  };

  return read(root).replace(/\n$/, "");
}

function updateValue() {
  emit("update:modelValue", readText());
}

function saveSelection() {
  const root = editor.value;
  const selection = window.getSelection();
  if (
    !root ||
    !selection?.rangeCount ||
    !root.contains(selection.anchorNode)
  ) {
    return;
  }
  savedRange = selection.getRangeAt(0).cloneRange();
}

function placeCaret(range: Range) {
  const selection = window.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
  savedRange = range.cloneRange();
}

function createToken(element: BrowserElementSelection): HTMLElement {
  const token = document.createElement("span");
  token.contentEditable = "false";
  token.dataset.browserElementKey = elementKey(element);
  token.title = `${element.page_title || element.page_url}\n${element.selector}`;
  token.className =
    "inline-flex shrink-0 items-center gap-1 rounded-md border border-border bg-muted/60 py-0.5 pl-1.5 pr-0.5 align-baseline text-xs font-medium text-foreground";

  const icon = document.createElement("span");
  icon.className = "flex size-3.5 shrink-0 items-center text-emerald-500";
  const iconApp = createApp({
    render: () => h(MousePointer2Icon, { class: "size-3.5" }),
  });
  iconApp.mount(icon);
  tokenApps.set(elementKey(element), iconApp);

  const label = document.createElement("span");
  label.className = "font-mono";
  label.textContent = element.tag.toLowerCase();

  const remove = document.createElement("button");
  remove.type = "button";
  remove.tabIndex = -1;
  remove.dataset.removeBrowserElement = "";
  remove.title = "移除页面元素";
  remove.className =
    "flex size-4 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-background hover:text-foreground";
  remove.textContent = "\u00d7";

  token.append(icon, label, remove);
  return token;
}

function insertToken(element: BrowserElementSelection) {
  const root = editor.value;
  if (!root) return;

  const token = createToken(element);
  const caret = document.createTextNode("\u200B");
  const range =
    savedRange && root.contains(savedRange.commonAncestorContainer)
      ? savedRange.cloneRange()
      : document.createRange();

  if (!savedRange || !root.contains(range.commonAncestorContainer)) {
    range.selectNodeContents(root);
    range.collapse(false);
  }

  range.deleteContents();
  range.insertNode(caret);
  range.insertNode(token);
  range.setStart(caret, 1);
  range.collapse(true);
  placeCaret(range);
  updateValue();
}

function removeToken(token: HTMLElement) {
  const root = editor.value;
  const index = props.browserElements.findIndex(
    (element) => elementKey(element) === token.dataset.browserElementKey
  );
  if (index < 0 || !root) return;

  const range = document.createRange();
  range.setStartBefore(token);
  range.collapse(true);
  tokenApps.get(token.dataset.browserElementKey ?? "")?.unmount();
  tokenApps.delete(token.dataset.browserElementKey ?? "");
  token.remove();
  if (!root.querySelector("[data-browser-element-key]") && !readText()) {
    root.replaceChildren();
    range.selectNodeContents(root);
    range.collapse(true);
  }
  placeCaret(range);
  emit("remove-browser-element", index);
  updateValue();
}

function edgeNode(
  container: Node,
  offset: number,
  direction: "before" | "after"
): Node | null {
  const root = editor.value;
  if (!root) return null;

  let node: Node | null = container;
  if (node.nodeType === Node.TEXT_NODE) {
    const content = node.textContent ?? "";
    if (
      (direction === "before" &&
        content.slice(0, offset).replace(/\u200B/g, "").length > 0) ||
      (direction === "after" &&
        content.slice(offset).replace(/\u200B/g, "").length > 0)
    ) {
      return null;
    }
  } else if (node instanceof Element) {
    const childIndex = direction === "before" ? offset - 1 : offset;
    const child = node.childNodes[childIndex];
    if (child) return child;
  }

  while (node && node !== root) {
    const sibling =
      direction === "before" ? node.previousSibling : node.nextSibling;
    if (sibling) return sibling;
    node = node.parentNode;
  }
  return null;
}

function onKeydown(event: KeyboardEvent) {
  if (!event.isComposing && (event.key === "Backspace" || event.key === "Delete")) {
    const selection = window.getSelection();
    if (selection?.rangeCount && selection.isCollapsed) {
      const range = selection.getRangeAt(0);
      const adjacent = edgeNode(
        range.startContainer,
        range.startOffset,
        event.key === "Backspace" ? "before" : "after"
      );
      const token = tokenElement(adjacent);
      if (token) {
        event.preventDefault();
        removeToken(token);
        return;
      }
    }
  }
  emit("keydown", event);
}

function onInput() {
  if (syncing) return;

  const present = new Set(
    Array.from(
      editor.value?.querySelectorAll<HTMLElement>("[data-browser-element-key]") ??
        []
    ).map((node) => node.dataset.browserElementKey)
  );
  for (let index = props.browserElements.length - 1; index >= 0; index -= 1) {
    if (!present.has(elementKey(props.browserElements[index]))) {
      tokenApps.get(elementKey(props.browserElements[index]))?.unmount();
      tokenApps.delete(elementKey(props.browserElements[index]));
      emit("remove-browser-element", index);
    }
  }
  saveSelection();
  updateValue();
}

function onPaste(event: ClipboardEvent) {
  emit("paste", event);
  if (event.defaultPrevented) return;

  const text = event.clipboardData?.getData("text/plain");
  if (!text) return;

  const root = editor.value;
  const selection = window.getSelection();
  if (!root || !selection?.rangeCount || !root.contains(selection.anchorNode)) {
    return;
  }

  event.preventDefault();
  const range = selection.getRangeAt(0);
  const node = document.createTextNode(text);
  range.deleteContents();
  range.insertNode(node);
  range.setStartAfter(node);
  range.collapse(true);
  placeCaret(range);
  updateValue();
}

function onClick(event: MouseEvent) {
  const target = event.target;
  if (!(target instanceof HTMLElement)) return;
  const remove = target.closest<HTMLElement>("[data-remove-browser-element]");
  if (!remove) {
    saveSelection();
    return;
  }
  event.preventDefault();
  const token = tokenElement(remove);
  if (token) removeToken(token);
}

function replaceText(value: string) {
  const root = editor.value;
  if (!root) return;

  for (const node of Array.from(root.childNodes)) {
    if (!tokenElement(node)) node.remove();
  }
  if (value) root.append(document.createTextNode(value));
}

watch(
  () => props.modelValue,
  (value) => {
    if (value === readText()) return;
    syncing = true;
    replaceText(value);
    syncing = false;
  }
);

watch(
  () => props.browserElements.map(elementKey),
  async (keys) => {
    await nextTick();
    const root = editor.value;
    if (!root) return;

    syncing = true;
    const expected = new Set(keys);
    for (const token of root.querySelectorAll<HTMLElement>(
      "[data-browser-element-key]"
    )) {
      const key = token.dataset.browserElementKey ?? "";
      if (!expected.has(key)) {
        tokenApps.get(key)?.unmount();
        tokenApps.delete(key);
        token.remove();
      }
    }

    const present = new Set(
      Array.from(
        root.querySelectorAll<HTMLElement>("[data-browser-element-key]")
      ).map((token) => token.dataset.browserElementKey)
    );
    for (const element of props.browserElements) {
      if (!present.has(elementKey(element))) insertToken(element);
    }
    syncing = false;
  },
  { immediate: true }
);

onMounted(() => replaceText(props.modelValue));
onUnmounted(() => {
  for (const app of tokenApps.values()) app.unmount();
  tokenApps.clear();
});

function focus() {
  editor.value?.focus();
}

function orderedElements(): BrowserElementSelection[] {
  const byKey = new Map(
    props.browserElements.map((element) => [elementKey(element), element])
  );
  return Array.from(
    editor.value?.querySelectorAll<HTMLElement>("[data-browser-element-key]") ??
      []
  )
    .map((token) => byKey.get(token.dataset.browserElementKey ?? ""))
    .filter((element): element is BrowserElementSelection => Boolean(element));
}

defineExpose({ focus, orderedElements });
</script>

<template>
  <div
    ref="editor"
    role="textbox"
    aria-multiline="true"
    :aria-disabled="disabled"
    :data-placeholder="placeholder"
    :contenteditable="disabled ? 'false' : 'true'"
    class="inline-composer-editor max-h-60 min-h-[56px] min-w-0 flex-1 overflow-y-auto whitespace-pre-wrap break-words py-3 text-sm outline-none empty:before:pointer-events-none empty:before:text-muted-foreground empty:before:content-[attr(data-placeholder)]"
    @blur="
      saveSelection();
      emit('blur');
    "
    @click="onClick"
    @focus="
      saveSelection();
      emit('focus');
    "
    @input="onInput"
    @keydown="onKeydown"
    @keyup="saveSelection"
    @paste="onPaste"
  />
</template>
