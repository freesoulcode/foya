<script setup lang="ts">
import { computed } from "vue";
import {
  ChevronRightIcon,
  FileCheck2Icon,
  FileTextIcon,
  ListChecksIcon,
  LoaderCircleIcon,
} from "@lucide/vue";
import { Button } from "@/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type { WorkflowRecord } from "@/lib/api";

const props = defineProps<{
  workflow: WorkflowRecord;
  canOpenFiles?: boolean;
}>();

const emit = defineEmits<{
  (event: "open-file", path: string): void;
}>();

const files = computed(() => {
  if (props.workflow.kind === "spec" && props.workflow.artifacts) {
    return [
      { label: "规格", path: props.workflow.artifacts.spec, icon: FileTextIcon },
      { label: "任务", path: props.workflow.artifacts.tasks, icon: ListChecksIcon },
      { label: "验收", path: props.workflow.artifacts.checklist, icon: FileCheck2Icon },
    ];
  }
  if (props.workflow.path) {
    return [{
      label: props.workflow.kind === "plan" ? "计划" : "目标",
      path: props.workflow.path,
      icon: FileTextIcon,
    }];
  }
  return [];
});
</script>

<template>
  <div class="min-h-0">
    <div class="flex min-w-0 items-center gap-2 px-3 py-2.5">
      <LoaderCircleIcon
        v-if="workflow.status === 'active'"
        class="size-4 shrink-0 animate-spin text-primary"
      />
      <FileCheck2Icon v-else class="size-4 shrink-0 text-emerald-600 dark:text-emerald-400" />
      <span class="min-w-0 flex-1 truncate text-sm">
        {{ workflow.title || workflow.goal }}
      </span>
      <span class="shrink-0 text-xs text-muted-foreground">
        {{ workflow.status === "active" ? "生成中" : "待确认" }}
      </span>
    </div>

    <div
      v-if="workflow.status === 'ready' && files.length"
      class="divide-y divide-border border-t border-border"
    >
      <div
        v-for="file in files"
        :key="file.path"
        class="flex min-h-10 min-w-0 items-center gap-2 px-3 py-2"
      >
        <button
          type="button"
          class="flex min-w-0 flex-1 items-center gap-2 text-left disabled:cursor-default disabled:opacity-60"
          :disabled="!canOpenFiles"
          @click="emit('open-file', file.path)"
        >
          <component :is="file.icon" class="size-4 shrink-0 text-muted-foreground" />
          <span class="min-w-0 flex-1 truncate text-xs font-medium">{{ file.label }}</span>
        </button>
        <Tooltip v-if="canOpenFiles">
          <TooltipTrigger as-child>
            <Button
              type="button"
              size="icon-xs"
              variant="ghost"
              class="text-muted-foreground"
              :aria-label="`打开${file.label}`"
              @click.stop="emit('open-file', file.path)"
            >
              <ChevronRightIcon class="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="left">在工作区打开</TooltipContent>
        </Tooltip>
      </div>
    </div>
  </div>
</template>
