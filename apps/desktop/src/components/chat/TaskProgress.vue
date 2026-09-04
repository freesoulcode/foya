<script setup lang="ts">
import { computed } from "vue";
import {
  CheckIcon,
  CircleIcon,
  LoaderCircleIcon,
} from "@lucide/vue";
import type { SessionTask } from "@/lib/api";
import { cn } from "@/lib/utils";

const props = defineProps<{
  tasks?: SessionTask[];
}>();

const visibleTasks = computed(() => props.tasks ?? []);
const completed = computed(
  () => visibleTasks.value.filter((task) => task.status === "completed").length
);
const progress = computed(() =>
  visibleTasks.value.length > 0
    ? (completed.value / visibleTasks.value.length) * 100
    : 0
);

function taskIconClass(status: SessionTask["status"]) {
  if (status === "completed") return "text-emerald-600";
  if (status === "in_progress") return "text-primary";
  return "text-muted-foreground";
}
</script>

<template>
  <div class="min-h-0">
    <div class="flex h-9 items-center gap-3 border-b border-border px-3">
      <div class="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-muted">
        <div
          class="h-full bg-primary transition-[width]"
          :style="{ width: `${progress}%` }"
        />
      </div>
      <span class="shrink-0 text-xs tabular-nums text-muted-foreground">
        {{ completed }}/{{ visibleTasks.length }}
      </span>
    </div>

    <div class="no-scrollbar max-h-72 space-y-1.5 overflow-y-auto p-3">
      <div
        v-for="(task, index) in visibleTasks"
        :key="`${index}:${task.content}`"
        class="flex min-w-0 items-start gap-2 text-sm"
      >
        <CheckIcon
          v-if="task.status === 'completed'"
          :class="cn('mt-0.5 size-4 shrink-0', taskIconClass(task.status))"
        />
        <LoaderCircleIcon
          v-else-if="task.status === 'in_progress'"
          :class="cn('mt-0.5 size-4 shrink-0 animate-spin', taskIconClass(task.status))"
        />
        <CircleIcon
          v-else
          :class="cn('mt-0.5 size-4 shrink-0', taskIconClass(task.status))"
        />
        <span
          :class="cn(
            'min-w-0 break-words',
            task.status === 'completed' && 'text-muted-foreground line-through',
          )"
        >
          {{ task.content }}
        </span>
      </div>
    </div>
  </div>
</template>
