<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  ChevronRightIcon,
  FileDiffIcon,
  FilesIcon,
} from "@lucide/vue";
import type { AttachmentRef, ToolCallView } from "@/lib/api";
import { diffFileName, diffFilePath, diffStats } from "@/lib/diff";
import ArtifactAttachmentList from "./ArtifactAttachmentList.vue";

const props = defineProps<{
  sessionId: string;
  tools: ToolCallView[];
  projectPath?: string;
}>();
const { t } = useI18n();

const emit = defineEmits<{
  (event: "open-diff", diff: string): void;
  (event: "open-artifact", attachment: AttachmentRef): void;
}>();

const expanded = ref(false);

const files = computed(() => {
  const byName = new Map<
    string,
    {
      name: string;
      directory: string;
      diff: string;
      additions: number;
      deletions: number;
    }
  >();
  for (const tool of props.tools) {
    if (!tool.diff) continue;
    const name = diffFileName(tool.diff);
    if (!name) continue;
    const path = diffFilePath(tool.diff, props.projectPath ?? "") || name;
    const directory = path.slice(0, Math.max(0, path.lastIndexOf("/") + 1));
    const stats = diffStats(tool.diff);
    byName.set(path, {
      name,
      directory,
      diff: tool.diff,
      additions: stats.additions,
      deletions: stats.deletions,
    });
  }
  return Array.from(byName.values());
});

const generatedFiles = computed<AttachmentRef[]>(() => {
  const byID = new Map<string, AttachmentRef>();
  for (const tool of props.tools) {
    if (!["write", "edit"].includes(tool.name) || tool.diff) continue;
    for (const attachment of tool.attachments ?? []) {
      if (attachment.kind !== "file") continue;
      byID.set(attachment.id, attachment);
    }
  }
  return Array.from(byID.values());
});

const totals = computed(() =>
  files.value.reduce(
    (sum, file) => ({
      additions: sum.additions + file.additions,
      deletions: sum.deletions + file.deletions,
    }),
    { additions: 0, deletions: 0 }
  )
);

const summaryLabel = computed(() => {
  const parts: string[] = [];
  if (files.value.length > 0) {
    parts.push(t("Changed {count} files", { count: files.value.length }));
  }
  if (generatedFiles.value.length > 0) {
    parts.push(t("Generated {count} files", { count: generatedFiles.value.length }));
  }
  return parts.join(t(", "));
});
</script>

<template>
  <section
    v-if="files.length || generatedFiles.length"
    class="mb-3 mt-3 overflow-hidden rounded-lg border border-border bg-muted/20"
    :aria-label="$t('Task artifacts')"
  >
    <button
      type="button"
      class="flex h-12 w-full items-center gap-2.5 px-3 text-left transition-colors hover:bg-muted/40"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >
      <FilesIcon class="size-4 shrink-0 text-primary" />
      <span class="min-w-0 flex-1 truncate text-sm font-medium">
        {{ summaryLabel }}
      </span>
      <span v-if="files.length" class="shrink-0 font-mono text-xs text-emerald-600">
        +{{ totals.additions }}
      </span>
      <span v-if="files.length" class="shrink-0 font-mono text-xs text-red-500">
        -{{ totals.deletions }}
      </span>
      <ChevronRightIcon
        class="size-4 shrink-0 text-muted-foreground transition-transform"
        :class="{ 'rotate-90': expanded }"
      />
    </button>

    <div v-if="expanded" class="border-t border-border">
      <button
        v-for="file in files"
        :key="`${file.directory}${file.name}`"
        type="button"
        class="flex h-10 w-full min-w-0 items-center gap-2 border-b border-border px-3 text-left text-sm hover:bg-muted/50 last:border-b-0"
        :title="$t('View changes for {path}', { path: file.name })"
        @click="emit('open-diff', file.diff)"
      >
        <FileDiffIcon class="size-4 shrink-0 text-primary" />
        <span class="min-w-0 flex-1 truncate text-sm">
          <span class="font-mono">{{ file.name }}</span>
          <span v-if="file.directory" class="ml-2 text-muted-foreground">
            ./{{ file.directory }}
          </span>
        </span>
        <span class="shrink-0 font-mono text-xs text-emerald-600">
          +{{ file.additions }}
        </span>
        <span class="shrink-0 font-mono text-xs text-red-500">
          -{{ file.deletions }}
        </span>
      </button>
      <div
        v-if="generatedFiles.length"
        class="p-3"
        :class="{ 'border-t border-border': files.length }"
      >
        <div class="mb-2 text-xs font-medium text-muted-foreground">
          {{ $t("Generated files") }}
        </div>
        <ArtifactAttachmentList
          :session-id="sessionId"
          :attachments="generatedFiles"
          compact
          @open="(attachment) => emit('open-artifact', attachment)"
        />
      </div>
    </div>
  </section>
</template>
