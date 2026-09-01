<script setup lang="ts">
import { computed, ref } from "vue";
import {
  ChevronRightIcon,
  FileDiffIcon,
  FilesIcon,
} from "@lucide/vue";
import type { ToolCallView } from "@/lib/api";
import { diffFileName, diffFilePath, diffStats } from "@/lib/diff";

const props = defineProps<{
  tools: ToolCallView[];
  projectPath?: string;
}>();

const emit = defineEmits<{
  (event: "open-diff", diff: string): void;
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

const totals = computed(() =>
  files.value.reduce(
    (sum, file) => ({
      additions: sum.additions + file.additions,
      deletions: sum.deletions + file.deletions,
    }),
    { additions: 0, deletions: 0 }
  )
);
</script>

<template>
  <section
    v-if="files.length"
    class="mb-3 mt-3 overflow-hidden rounded-lg border border-border bg-muted/20"
    aria-label="任务产物"
  >
    <button
      type="button"
      class="flex h-12 w-full items-center gap-2.5 px-3 text-left transition-colors hover:bg-muted/40"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >
      <FilesIcon class="size-4 shrink-0 text-primary" />
      <span class="min-w-0 flex-1 truncate text-sm font-medium">
        {{ files.length }} 个文件已更改
      </span>
      <span class="shrink-0 font-mono text-xs text-emerald-600">
        +{{ totals.additions }}
      </span>
      <span class="shrink-0 font-mono text-xs text-red-500">
        -{{ totals.deletions }}
      </span>
      <ChevronRightIcon
        class="size-4 shrink-0 text-muted-foreground transition-transform"
        :class="{ 'rotate-90': expanded }"
      />
    </button>

    <div v-if="expanded" class="divide-y divide-border border-t border-border">
      <button
        v-for="file in files"
        :key="`${file.directory}${file.name}`"
        type="button"
        class="flex h-10 w-full min-w-0 items-center gap-2 px-3 text-left text-sm hover:bg-muted/50"
        :title="`查看 ${file.name} 的变更`"
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
    </div>
  </section>
</template>
