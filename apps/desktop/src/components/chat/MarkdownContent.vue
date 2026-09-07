<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import {
  CheckIcon,
  CopyIcon,
  RotateCcwIcon,
  XIcon,
  ZoomInIcon,
  ZoomOutIcon,
} from "@lucide/vue";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@/components/ui/dialog";
import { renderMarkdown } from "@/lib/markdown";
import { renderMermaid as renderMermaidDiagram } from "@/lib/mermaid";

const props = withDefaults(
  defineProps<{
    source: string;
    renderMermaid?: boolean;
  }>(),
  {
    renderMermaid: true,
  }
);
const { locale, t } = useI18n();

const root = ref<HTMLElement | null>(null);
const previewOpen = ref(false);
const previewSource = ref("");
const previewSvg = ref("");
const previewZoom = ref(1);
const previewCopied = ref(false);
const diagrams = new Map<string, { source: string; svg: string }>();
let generation = 0;
let nextDiagramKey = 1;
let themeObserver: MutationObserver | undefined;
let copiedTimer: ReturnType<typeof setTimeout> | undefined;
let gestureStartZoom = 1;

function diagramElement(key: string, svg: string): HTMLElement {
  const wrapper = document.createElement("div");
  wrapper.className = "mermaid-diagram";
  wrapper.dataset.mermaidKey = key;

  const header = document.createElement("div");
  header.className = "mermaid-diagram-header";
  const label = document.createElement("span");
  label.textContent = "mermaid";
  const copy = document.createElement("button");
  copy.type = "button";
  copy.className = "mermaid-copy-source";
  copy.dataset.mermaidKey = key;
  copy.textContent = t("Copy source");
  header.append(label, copy);

  const preview = document.createElement("button");
  preview.type = "button";
  preview.className = "mermaid-preview-trigger";
  preview.dataset.mermaidKey = key;
  preview.title = t("Preview Mermaid diagram");
  preview.setAttribute("aria-label", t("Preview Mermaid diagram"));
  preview.innerHTML = svg;

  wrapper.append(header, preview);
  return wrapper;
}

function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    return navigator.clipboard.writeText(text);
  }
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand("copy");
  textarea.remove();
  return copied ? Promise.resolve() : Promise.reject(new Error("Copy failed"));
}

async function copyPreviewSource() {
  if (!previewSource.value) return;
  try {
    await copyText(previewSource.value);
    previewCopied.value = true;
    if (copiedTimer) clearTimeout(copiedTimer);
    copiedTimer = setTimeout(() => {
      previewCopied.value = false;
    }, 1_500);
  } catch {
    previewCopied.value = false;
  }
}

function openPreview(key: string) {
  const diagram = diagrams.get(key);
  if (!diagram) return;
  previewSource.value = diagram.source;
  previewSvg.value = diagram.svg;
  previewZoom.value = 1;
  previewCopied.value = false;
  previewOpen.value = true;
}

async function onContentClick(event: MouseEvent) {
  const target = event.target as HTMLElement;
  const copy = target.closest<HTMLButtonElement>(".mermaid-copy-source");
  if (copy) {
    const diagram = diagrams.get(copy.dataset.mermaidKey ?? "");
    if (!diagram) return;
    try {
      await copyText(diagram.source);
      copy.textContent = t("Copied");
      window.setTimeout(() => {
        if (copy.isConnected) copy.textContent = t("Copy source");
      }, 1_500);
    } catch {
      copy.textContent = t("Copy failed");
    }
    return;
  }

  const preview = target.closest<HTMLButtonElement>(
    ".mermaid-preview-trigger"
  );
  if (preview) openPreview(preview.dataset.mermaidKey ?? "");
}

function setZoom(value: number) {
  previewZoom.value = Math.min(3, Math.max(0.5, value));
}

function changeZoom(delta: number) {
  setZoom(Number((previewZoom.value + delta).toFixed(2)));
}

function onPreviewWheel(event: WheelEvent) {
  if (!event.ctrlKey && !event.metaKey) return;
  event.preventDefault();
  setZoom(previewZoom.value * Math.exp(-event.deltaY * 0.01));
}

function onPreviewGestureStart(event: Event) {
  event.preventDefault();
  gestureStartZoom = previewZoom.value;
}

function onPreviewGestureChange(event: Event) {
  event.preventDefault();
  const scale = Number((event as Event & { scale?: number }).scale ?? 1);
  setZoom(gestureStartZoom * scale);
}

async function renderContent() {
  const currentGeneration = ++generation;
  await nextTick();
  const element = root.value;
  if (!element || currentGeneration !== generation) return;

  element.innerHTML = renderMarkdown(props.source);
  diagrams.clear();
  if (!props.renderMermaid) return;

  const blocks = Array.from(
    element.querySelectorAll<HTMLElement>(
      ".code-block code.language-mermaid"
    )
  );
  if (blocks.length === 0) return;

  const dark = document.documentElement.classList.contains("dark");
  for (const code of blocks) {
    if (currentGeneration !== generation) return;
    const block = code.closest<HTMLElement>(".code-block");
    if (!block) continue;
    try {
      const source = code.textContent ?? "";
      const svg = await renderMermaidDiagram(source, dark);
      if (currentGeneration !== generation || !block.isConnected) return;
      const key = `diagram-${nextDiagramKey++}`;
      diagrams.set(key, { source, svg });
      block.replaceWith(diagramElement(key, svg));
      if (previewOpen.value && previewSource.value === source) {
        previewSvg.value = svg;
      }
    } catch {
      block.classList.add("mermaid-render-failed");
    }
  }
}

watch(
  () => [props.source, props.renderMermaid, locale.value] as const,
  () => void renderContent(),
  { flush: "post" }
);

onMounted(() => {
  themeObserver = new MutationObserver(() => void renderContent());
  themeObserver.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["class"],
  });
  void renderContent();
});

onBeforeUnmount(() => {
  generation += 1;
  themeObserver?.disconnect();
  if (copiedTimer) clearTimeout(copiedTimer);
});
</script>

<template>
  <div>
    <div ref="root" @click="onContentClick" />

    <Dialog v-model:open="previewOpen">
      <DialogContent
        :show-close-button="false"
        class="grid h-[min(86vh,760px)] w-[min(92vw,1120px)] max-w-none grid-rows-[2.5rem_minmax(0,1fr)] gap-0 overflow-hidden p-0 sm:max-w-none"
      >
        <div class="flex items-center gap-0.5 border-b border-border px-2">
          <DialogTitle class="min-w-0 flex-1 truncate text-xs font-medium">
            {{ $t("Mermaid preview") }}
          </DialogTitle>
          <Button
            size="icon-xs"
            variant="ghost"
            :title="$t('Zoom out')"
            :aria-label="$t('Zoom out')"
            :disabled="previewZoom <= 0.5"
            @click="changeZoom(-0.25)"
          >
            <ZoomOutIcon class="size-3.5" />
          </Button>
          <span class="w-10 text-center font-mono text-[10px] text-muted-foreground">
            {{ Math.round(previewZoom * 100) }}%
          </span>
          <Button
            size="icon-xs"
            variant="ghost"
            :title="$t('Zoom in')"
            :aria-label="$t('Zoom in')"
            :disabled="previewZoom >= 3"
            @click="changeZoom(0.25)"
          >
            <ZoomInIcon class="size-3.5" />
          </Button>
          <Button
            size="icon-xs"
            variant="ghost"
            :title="$t('Reset zoom')"
            :aria-label="$t('Reset zoom')"
            :disabled="previewZoom === 1"
            @click="previewZoom = 1"
          >
            <RotateCcwIcon class="size-3.5" />
          </Button>
          <Button
            size="xs"
            variant="ghost"
            @click="copyPreviewSource"
          >
            <CheckIcon v-if="previewCopied" class="size-3.5" />
            <CopyIcon v-else class="size-3.5" />
            {{ previewCopied ? $t("Copied") : $t("Copy source") }}
          </Button>
          <Button
            size="icon-xs"
            variant="ghost"
            :title="$t('Close')"
            :aria-label="$t('Close')"
            @click="previewOpen = false"
          >
            <XIcon class="size-3.5" />
          </Button>
        </div>
        <div
          class="mermaid-preview-viewport"
          @wheel="onPreviewWheel"
          @gesturestart="onPreviewGestureStart"
          @gesturechange="onPreviewGestureChange"
        >
          <div
            class="mermaid-preview-canvas"
            :style="{ width: `${previewZoom * 100}%` }"
            v-html="previewSvg"
          />
        </div>
      </DialogContent>
    </Dialog>
  </div>
</template>
