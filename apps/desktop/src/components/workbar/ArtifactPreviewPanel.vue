<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import {
  AlertCircleIcon,
  DownloadIcon,
  FileIcon,
  LoaderCircleIcon,
  SaveIcon,
} from "@lucide/vue";
import { api, type AttachmentRef } from "@/lib/api";
import MarkdownContent from "@/components/chat/MarkdownContent.vue";

const CodePreview = defineAsyncComponent(() => import("./CodePreview.vue"));

const props = defineProps<{
  sessionId?: string;
  attachment?: AttachmentRef;
  active?: boolean;
}>();

const { t } = useI18n();
const MAX_TEXT_PREVIEW_BYTES = 2 * 1024 * 1024;

const loading = ref(false);
const saving = ref(false);
const error = ref("");
const objectURL = ref("");
const content = ref("");
const bytes = ref<Uint8Array>();
let request = 0;

const name = computed(() => props.attachment?.name || props.attachment?.id || "generated");
const media = computed(() =>
  (props.attachment?.media_type || "application/octet-stream")
    .split(";")[0]
    .trim()
    .toLowerCase()
);
const bytesLabel = computed(() => formatBytes(props.attachment?.bytes));
const extension = computed(() => {
  const filename = name.value.toLowerCase();
  const dot = filename.lastIndexOf(".");
  return dot >= 0 ? filename.slice(dot + 1) : "";
});

const isImage = computed(() =>
  props.attachment?.kind === "image" || media.value.startsWith("image/")
);
const isHTML = computed(() =>
  media.value === "text/html" || extension.value === "html" || extension.value === "htm"
);
const isMarkdown = computed(() =>
  media.value === "text/markdown" ||
  ["md", "markdown", "mdown", "mkd"].includes(extension.value)
);
const isText = computed(() => {
  if (media.value.startsWith("text/")) return true;
  if (
    media.value.includes("json") ||
    media.value.includes("xml") ||
    media.value.includes("javascript") ||
    media.value.includes("typescript")
  ) {
    return true;
  }
  return [
    "csv",
    "json",
    "jsonc",
    "xml",
    "svg",
    "yaml",
    "yml",
    "toml",
    "ini",
    "env",
    "txt",
    "log",
    "css",
    "js",
    "ts",
    "tsx",
    "jsx",
    "go",
    "rs",
    "py",
    "java",
    "rb",
    "php",
    "c",
    "cpp",
    "h",
    "hpp",
  ].includes(extension.value);
});

function releaseObjectURL() {
  if (objectURL.value) URL.revokeObjectURL(objectURL.value);
  objectURL.value = "";
}

function formatBytes(value?: number): string {
  if (!Number.isFinite(value) || !value || value <= 0) return "";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / (1024 * 1024)).toFixed(1)} MB`;
}

async function loadArtifact() {
  const current = ++request;
  releaseObjectURL();
  bytes.value = undefined;
  content.value = "";
  error.value = "";
  if (!props.active || !props.sessionId || !props.attachment) return;
  loading.value = true;
  try {
    const data = await api.readArtifact(props.sessionId, props.attachment.id);
    if (current !== request) return;
    bytes.value = data;
    if (isImage.value || isHTML.value) {
      objectURL.value = URL.createObjectURL(
        new Blob([data], { type: props.attachment.media_type || "application/octet-stream" })
      );
      return;
    }
    if (isText.value || isMarkdown.value) {
      if (data.byteLength > MAX_TEXT_PREVIEW_BYTES) {
        error.value = t("Files larger than 2 MiB cannot be previewed");
        return;
      }
      content.value = new TextDecoder("utf-8").decode(data);
      return;
    }
    error.value = t("Binary file previews are not supported");
  } catch {
    if (current === request) error.value = t("Failed to load artifact");
  } finally {
    if (current === request) loading.value = false;
  }
}

async function downloadAttachment() {
  if (!props.attachment || !props.sessionId) return;
  error.value = "";
  try {
    const data = bytes.value ?? await api.readArtifact(props.sessionId, props.attachment.id);
    const url = URL.createObjectURL(
      new Blob([data], { type: props.attachment.media_type || "application/octet-stream" })
    );
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = name.value;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch {
    error.value = t("Failed to download artifact");
  }
}

async function saveAttachment() {
  if (!props.attachment || !props.sessionId) return;
  error.value = "";
  saving.value = true;
  try {
    await api.saveArtifact(props.sessionId, props.attachment);
  } catch {
    error.value = t("Failed to save artifact");
  } finally {
    saving.value = false;
  }
}

watch(
  () => [props.active, props.sessionId, props.attachment?.id, props.attachment?.media_type],
  () => void loadArtifact(),
  { immediate: true }
);

onBeforeUnmount(() => {
  request += 1;
  releaseObjectURL();
});
</script>

<template>
  <section class="flex h-full min-h-0 flex-col bg-background">
    <header class="flex h-12 shrink-0 items-center gap-2 border-b border-border px-3">
      <FileIcon class="size-4 shrink-0 text-primary" />
      <div class="min-w-0 flex-1">
        <div class="truncate font-mono text-sm text-foreground">{{ name }}</div>
        <div class="truncate text-[11px] text-muted-foreground">
          {{ media }}
          <span v-if="bytesLabel"> - {{ bytesLabel }}</span>
        </div>
      </div>
      <button
        type="button"
        class="flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        :title="$t('Download generated file')"
        :aria-label="$t('Download generated file')"
        @click="downloadAttachment"
      >
        <DownloadIcon class="size-4" />
      </button>
      <button
        type="button"
        class="flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
        :disabled="saving"
        :title="$t('Save generated file')"
        :aria-label="$t('Save generated file')"
        @click="saveAttachment"
      >
        <LoaderCircleIcon v-if="saving" class="size-4 animate-spin" />
        <SaveIcon v-else class="size-4" />
      </button>
    </header>

    <div
      v-if="loading"
      class="flex min-h-0 flex-1 items-center justify-center text-muted-foreground"
    >
      <LoaderCircleIcon class="size-5 animate-spin" />
    </div>
    <div
      v-else-if="error"
      class="flex min-h-0 flex-1 flex-col items-center justify-center gap-2 px-8 text-center"
    >
      <AlertCircleIcon class="size-5 text-muted-foreground" />
      <p class="text-xs text-muted-foreground">{{ error }}</p>
    </div>
    <div
      v-else-if="isImage && objectURL"
      class="flex min-h-0 flex-1 items-center justify-center overflow-auto bg-muted/20 p-4"
    >
      <img :src="objectURL" :alt="name" class="max-h-full max-w-full object-contain" />
    </div>
    <iframe
      v-else-if="isHTML && objectURL"
      :src="objectURL"
      :title="name"
      sandbox="allow-scripts allow-forms"
      class="min-h-0 flex-1 border-0 bg-background"
    />
    <MarkdownContent
      v-else-if="isMarkdown"
      :source="content"
      class="prose-chat min-h-0 flex-1 overflow-auto px-6 py-4"
    />
    <CodePreview
      v-else
      :path="name"
      :content="content"
    />
  </section>
</template>
