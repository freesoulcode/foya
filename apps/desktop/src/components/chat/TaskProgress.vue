<script setup lang="ts">
import { computed, ref } from "vue";
import {
  CheckIcon,
  ChevronDownIcon,
  CircleIcon,
  LoaderCircleIcon,
} from "@lucide/vue";
import type { SessionTask } from "@/lib/api";
import { cn } from "@/lib/utils";

const props = defineProps<{
  tasks?: SessionTask[];
  running?: boolean;
}>();

const expanded = ref(false);
const visibleTasks = computed(() => props.tasks ?? []);
const incompleteTasks = computed(() =>
  visibleTasks.value.filter((task) => task.status !== "completed")
);
const completed = computed(
  () => visibleTasks.value.filter((task) => task.status === "completed").length
);
const current = computed(
  () => visibleTasks.value.find((task) => task.status === "in_progress") ?? null
);
const hasTasks = computed(() => visibleTasks.value.length > 0);

function taskIconClass(status: SessionTask["status"]) {
  if (status === "completed") return "text-emerald-600";
  if (status === "in_progress") return "text-primary";
  return "text-muted-foreground";
}
</script>

<template>
  <div
    v-if="hasTasks"
    class="mx-auto w-full max-w-3xl border-x border-t border-border bg-background/95 px-4 py-2"
  >
    <button
      type="button"
      class="flex w-full min-w-0 items-center gap-2 text-left text-sm"
      @click="expanded = !expanded"
    >
      <LoaderCircleIcon
        v-if="current && running"
        class="size-4 shrink-0 animate-spin text-primary"
      />
      <CheckIcon
        v-else-if="incompleteTasks.length === 0"
        class="size-4 shrink-0 text-emerald-600"
      />
      <CircleIcon v-else class="size-4 shrink-0 text-muted-foreground" />
      <span class="shrink-0 font-medium">Tasks {{ completed }}/{{ visibleTasks.length }}</span>
      <span v-if="current" class="min-w-0 flex-1 truncate text-muted-foreground">
        {{ current.content }}
      </span>
      <ChevronDownIcon
        :class="cn('ml-auto size-4 shrink-0 text-muted-foreground transition-transform', expanded && 'rotate-180')"
      />
    </button>

    <div v-if="expanded" class="mt-2 space-y-1.5">
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
          :class="cn('mt-0.5 size-4 shrink-0', taskIconClass(task.status), running && 'animate-spin')"
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
