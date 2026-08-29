<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { EditorState } from "@codemirror/state";
import { languages } from "@codemirror/language-data";
import { basicSetup, EditorView } from "codemirror";

const props = defineProps<{
  path: string;
  content: string;
}>();

const host = ref<HTMLElement>();
let editor: EditorView | undefined;
let renderVersion = 0;

function languageForPath(path: string) {
  const filename = path.split("/").pop() ?? path;
  const exact = languages.find((language) => {
    if (!language.filename) return false;
    language.filename.lastIndex = 0;
    return language.filename.test(filename);
  });
  if (exact) return exact;

  const extension = filename.includes(".")
    ? filename.split(".").pop()?.toLowerCase()
    : undefined;
  return extension
    ? languages.find((language) => language.extensions.includes(extension))
    : undefined;
}

const viewerTheme = EditorView.theme({
  "&": {
    height: "100%",
    backgroundColor: "var(--background)",
    color: "var(--foreground)",
    fontSize: "12px",
  },
  ".cm-scroller": {
    overflow: "auto",
    fontFamily: "var(--font-mono)",
    lineHeight: "1.55",
  },
  ".cm-content": {
    minWidth: "max-content",
    padding: "8px 0",
    caretColor: "transparent",
  },
  ".cm-line": {
    padding: "0 12px 0 6px",
  },
  ".cm-gutters": {
    backgroundColor: "var(--background)",
    color: "var(--muted-foreground)",
    borderColor: "var(--border)",
  },
  ".cm-gutterElement": {
    paddingLeft: "6px",
    paddingRight: "6px",
  },
  ".cm-activeLine, .cm-activeLineGutter": {
    backgroundColor: "transparent",
  },
  ".cm-foldPlaceholder": {
    backgroundColor: "var(--muted)",
    borderColor: "var(--border)",
    color: "var(--muted-foreground)",
  },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground": {
    backgroundColor: "color-mix(in oklch, var(--primary) 18%, transparent)",
  },
});

async function renderEditor() {
  const version = ++renderVersion;
  const description = languageForPath(props.path);
  const language = description ? await description.load() : undefined;
  await nextTick();

  if (version !== renderVersion || !host.value) return;
  editor?.destroy();
  editor = new EditorView({
    doc: props.content,
    extensions: [
      basicSetup,
      EditorState.readOnly.of(true),
      EditorView.editable.of(false),
      viewerTheme,
      ...(language ? [language] : []),
    ],
    parent: host.value,
  });
}

watch(
  () => [props.path, props.content] as const,
  () => void renderEditor()
);

onMounted(() => void renderEditor());

onBeforeUnmount(() => {
  renderVersion += 1;
  editor?.destroy();
});
</script>

<template>
  <div ref="host" class="min-h-0 flex-1 overflow-hidden" />
</template>
