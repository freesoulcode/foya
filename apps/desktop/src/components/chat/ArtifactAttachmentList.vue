<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import {
  Code2Icon,
  DownloadIcon,
  FileIcon,
  ImageIcon,
  LoaderCircleIcon,
  SaveIcon,
  type LucideIcon,
} from "@lucide/vue";
import { api, type AttachmentRef } from "@/lib/api";
import { cn } from "@/lib/utils";

const props = withDefaults(
  defineProps<{
    sessionId: string;
    attachments: AttachmentRef[];
    compact?: boolean;
    showPreview?: boolean;
  }>(),
  {
    compact: false,
    showPreview: true,
  }
);
const { t } = useI18n();

const emit = defineEmits<{
  (event: "open", attachment: AttachmentRef): void;
}>();

const previewURLs = ref<Record<string, string>>({});
const errors = ref<Record<string, string>>({});
const saving = ref<Record<string, boolean>>({});
const attachmentKey = computed(() =>
  props.attachments.map((item) => `${item.id}:${item.media_type}`).join(",")
);
let previewLoad = 0;

function releasePreviewURLs() {
  for (const url of Object.values(previewURLs.value)) URL.revokeObjectURL(url);
  previewURLs.value = {};
}

function mediaType(attachment: AttachmentRef): string {
  return (attachment.media_type || "application/octet-stream").split(";")[0].trim().toLowerCase();
}

function isImageAttachment(attachment: AttachmentRef): boolean {
  return attachment.kind === "image" || mediaType(attachment).startsWith("image/");
}

function isHTMLAttachment(attachment: AttachmentRef): boolean {
  const name = attachment.name.toLowerCase();
  return mediaType(attachment) === "text/html" || name.endsWith(".html") || name.endsWith(".htm");
}

function isPreviewable(attachment: AttachmentRef): boolean {
  return props.showPreview && (isImageAttachment(attachment) || isHTMLAttachment(attachment));
}

async function loadPreviews() {
  const load = ++previewLoad;
  releasePreviewURLs();
  if (!props.sessionId) return;
  for (const attachment of props.attachments) {
    if (!isPreviewable(attachment) || previewURLs.value[attachment.id]) continue;
    try {
      const bytes = await api.readArtifact(props.sessionId, attachment.id);
      if (load !== previewLoad) return;
      previewURLs.value = {
        ...previewURLs.value,
        [attachment.id]: URL.createObjectURL(
          new Blob([bytes], { type: attachment.media_type || "application/octet-stream" })
        ),
      };
    } catch {
      // The file row remains available even if preview bytes cannot be loaded.
    }
  }
}

watch(
  () => [props.sessionId, attachmentKey.value, props.showPreview],
  () => void loadPreviews(),
  { immediate: true }
);

onBeforeUnmount(() => {
  previewLoad++;
  releasePreviewURLs();
});

function fileIcon(attachment: AttachmentRef): LucideIcon {
  if (isImageAttachment(attachment)) return ImageIcon;
  if (isHTMLAttachment(attachment)) return Code2Icon;
  return FileIcon;
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function blobName(attachment: AttachmentRef): string {
  return attachment.name || attachment.id || "generated";
}

async function downloadAttachment(attachment: AttachmentRef) {
  errors.value = { ...errors.value, [attachment.id]: "" };
  try {
    const bytes = await api.readArtifact(props.sessionId, attachment.id);
    const url = URL.createObjectURL(
      new Blob([bytes], { type: attachment.media_type || "application/octet-stream" })
    );
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = blobName(attachment);
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch {
    errors.value = {
      ...errors.value,
      [attachment.id]: t("Failed to download artifact"),
    };
  }
}

async function saveAttachment(attachment: AttachmentRef) {
  errors.value = { ...errors.value, [attachment.id]: "" };
  saving.value = { ...saving.value, [attachment.id]: true };
  try {
    await api.saveArtifact(props.sessionId, attachment);
  } catch {
    errors.value = {
      ...errors.value,
      [attachment.id]: t("Failed to save artifact"),
    };
  } finally {
    saving.value = { ...saving.value, [attachment.id]: false };
  }
}
</script>

<template>
  <div
    :class="cn(
      'grid gap-2',
      compact ? 'grid-cols-1' : 'grid-cols-1 sm:grid-cols-2'
    )"
  >
    <article
      v-for="attachment in attachments"
      :key="attachment.id"
      class="overflow-hidden rounded-md border border-border bg-background"
    >
      <div
        role="button"
        tabindex="0"
        class="block w-full cursor-pointer text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
        :title="$t('Open generated file')"
        :aria-label="$t('Open generated file')"
        @click="emit('open', attachment)"
        @keydown.enter.prevent="emit('open', attachment)"
        @keydown.space.prevent="emit('open', attachment)"
      >
        <div
          v-if="isPreviewable(attachment)"
          class="flex h-40 items-center justify-center overflow-hidden bg-muted/50"
        >
          <img
            v-if="isImageAttachment(attachment) && previewURLs[attachment.id]"
            :src="previewURLs[attachment.id]"
            :alt="attachment.name"
            class="max-h-40 w-full object-contain"
          />
          <iframe
            v-else-if="isHTMLAttachment(attachment) && previewURLs[attachment.id]"
            :src="previewURLs[attachment.id]"
            :title="attachment.name"
            sandbox=""
            class="h-40 w-full border-0 bg-background"
          />
          <LoaderCircleIcon v-else class="size-4 animate-spin text-muted-foreground" />
        </div>
        <div v-else class="flex h-20 items-center justify-center bg-muted/50">
          <component :is="fileIcon(attachment)" class="size-6 text-muted-foreground" />
        </div>
      </div>

      <div class="flex min-w-0 items-center gap-2 px-2 py-1.5">
        <component :is="fileIcon(attachment)" class="size-3.5 shrink-0 text-primary" />
        <button
          type="button"
          class="min-w-0 flex-1 text-left"
          :title="$t('Open generated file')"
          @click="emit('open', attachment)"
        >
          <div class="truncate font-mono text-[11px] text-foreground">
            {{ blobName(attachment) }}
          </div>
          <div class="truncate text-[10px] text-muted-foreground">
            {{ mediaType(attachment) }}
            <span v-if="formatBytes(attachment.bytes)"> - {{ formatBytes(attachment.bytes) }}</span>
          </div>
        </button>
        <button
          type="button"
          class="flex size-6 shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          :title="$t('Download generated file')"
          :aria-label="$t('Download generated file')"
          @click.stop="downloadAttachment(attachment)"
        >
          <DownloadIcon class="size-3.5" />
        </button>
        <button
          type="button"
          class="flex size-6 shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
          :disabled="saving[attachment.id]"
          :title="$t('Save generated file')"
          :aria-label="$t('Save generated file')"
          @click.stop="saveAttachment(attachment)"
        >
          <LoaderCircleIcon
            v-if="saving[attachment.id]"
            class="size-3.5 animate-spin"
          />
          <SaveIcon v-else class="size-3.5" />
        </button>
      </div>
      <p v-if="errors[attachment.id]" class="px-2 pb-2 text-[10px] text-destructive">
        {{ errors[attachment.id] }}
      </p>
    </article>
  </div>
</template>
