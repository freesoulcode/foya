<script setup lang="ts">
import { computed } from "vue";
import {
  ChevronRightIcon,
  FilePenLineIcon,
  TriangleAlertIcon,
} from "@lucide/vue";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type { PendingFileReview } from "@/composables/useKernel";

const props = defineProps<{
  review?: PendingFileReview;
  disabled?: boolean;
}>();

const emit = defineEmits<{
  (event: "toggle-force", key: string): void;
  (event: "open-file", path: string, diff: string): void;
}>();

const files = computed(() => props.review?.files ?? []);
const actionDisabled = computed(
  () => Boolean(props.disabled || props.review?.submitting)
);

function isForced(key: string) {
  return props.review?.forceFileKeys.includes(key) ?? false;
}

function fileName(path: string) {
  return path.split("/").pop() || path;
}

function directory(path: string) {
  const index = path.lastIndexOf("/");
  return index >= 0 ? path.slice(0, index + 1) : "";
}
</script>

<template>
  <div class="min-h-0">
    <div class="no-scrollbar max-h-72 divide-y divide-border overflow-y-auto">
      <div
        v-for="file in files"
        :key="file.key"
        class="flex min-h-10 min-w-0 items-center gap-2 px-3 py-2"
      >
        <button
          type="button"
          class="flex min-w-0 flex-1 items-center gap-2 text-left"
          :title="`查看 ${file.path} 的变更`"
          @click="emit('open-file', file.path, file.diff)"
        >
          <FilePenLineIcon class="size-4 shrink-0 text-muted-foreground" />
          <span class="min-w-0 flex-1 truncate text-xs">
            <span class="font-mono text-foreground">{{ fileName(file.path) }}</span>
            <span
              v-if="directory(file.path)"
              class="ml-2 font-mono text-muted-foreground"
            >
              ./{{ directory(file.path) }}
            </span>
          </span>
        </button>

        <span class="shrink-0 font-mono text-xs text-emerald-600 dark:text-emerald-400">
          +{{ file.additions }}
        </span>
        <span class="shrink-0 font-mono text-xs text-red-500 dark:text-red-400">
          -{{ file.deletions }}
        </span>

        <Tooltip v-if="file.status === 'modified'">
          <TooltipTrigger as-child>
            <label
              class="flex shrink-0 cursor-pointer items-center gap-1.5"
              :class="isForced(file.key) ? 'text-destructive' : 'text-amber-600 dark:text-amber-400'"
              @click.stop
            >
              <TriangleAlertIcon v-if="!isForced(file.key)" class="size-4" />
              <Checkbox
                :model-value="isForced(file.key)"
                :disabled="actionDisabled"
                :aria-label="isForced(file.key) ? '取消强制撤销' : '强制撤销此文件'"
                @update:model-value="emit('toggle-force', file.key)"
              />
            </label>
          </TooltipTrigger>
          <TooltipContent side="left">
            {{ isForced(file.key) ? "将强制撤销" : "文件已修改，默认保留" }}
          </TooltipContent>
        </Tooltip>

        <Tooltip>
          <TooltipTrigger as-child>
            <Button
              type="button"
              size="icon-xs"
              variant="ghost"
              class="text-muted-foreground"
              :aria-label="`在工作区查看 ${file.path} 的变更`"
              @click="emit('open-file', file.path, file.diff)"
            >
              <ChevronRightIcon class="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="left">在工作区查看变更</TooltipContent>
        </Tooltip>
      </div>
    </div>

    <p
      v-if="review?.error"
      class="border-t border-border px-3 py-2 text-xs text-destructive"
    >
      {{ review.error }}
    </p>
  </div>
</template>
