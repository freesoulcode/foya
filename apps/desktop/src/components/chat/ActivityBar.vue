<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useEventListener } from "@vueuse/core";
import {
  CheckCheckIcon,
  ChevronsUpDownIcon,
  FileTextIcon,
  FilesIcon,
  ListChecksIcon,
  ListOrderedIcon,
  LoaderCircleIcon,
  SquareTerminalIcon,
  Undo2Icon,
  XIcon,
} from "@lucide/vue";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { Button } from "@/components/ui/button";
import type {
  BackgroundCommand,
  QueuedMessage,
  SessionTask,
  WorkflowRecord,
} from "@/lib/api";
import type { PendingFileReview } from "@/composables/useKernel";
import TaskProgress from "./TaskProgress.vue";
import BackgroundCommandsPanel from "./BackgroundCommandsPanel.vue";
import FileReviewBar from "./FileReviewBar.vue";
import QueuedMessages from "./QueuedMessages.vue";
import WorkflowPanel from "./WorkflowPanel.vue";

type ActivityKind = "workflow" | "tasks" | "commands" | "files" | "queue";

interface ActivityItem {
  kind: ActivityKind;
  label: string;
  count?: string;
  icon: typeof ListChecksIcon;
  active: boolean;
}

const props = withDefaults(
  defineProps<{
    tasks?: SessionTask[];
    taskRunning?: boolean;
    commands: BackgroundCommand[];
    review?: PendingFileReview;
    reviewDisabled?: boolean;
    queuedMessages: QueuedMessage[];
    workflow?: WorkflowRecord | null;
    canOpenWorkflowFiles?: boolean;
    streaming?: boolean;
  }>(),
  {
    tasks: () => [],
    taskRunning: false,
    review: undefined,
    reviewDisabled: false,
    workflow: null,
    canOpenWorkflowFiles: false,
    streaming: false,
  }
);

const emit = defineEmits<{
  (event: "stop-command", commandId: string): void;
  (event: "open-command", command: BackgroundCommand): void;
  (event: "keep-files"): void;
  (event: "undo-files"): void;
  (event: "toggle-force-file", key: string): void;
  (event: "open-file", path: string, diff: string): void;
  (event: "edit-queued", id: string, text: string): void;
  (event: "reorder-queued", id: string, position: number): void;
  (event: "dispatch-queued", id: string): void;
  (event: "delete-queued", id: string): void;
  (event: "open-workflow-file", path: string): void;
  (event: "approve-workflow"): void;
  (event: "close-workflow"): void;
}>();

const open = ref(false);
const selected = ref<ActivityKind | null>(null);
const runningCommands = computed(() =>
  props.commands.filter((command) => command.running)
);
const completedTasks = computed(
  () => props.tasks.filter((task) => task.status === "completed").length
);

const activities = computed<ActivityItem[]>(() => {
  const items: ActivityItem[] = [];
  if (
    props.workflow &&
    (props.workflow.status === "active" || props.workflow.status === "ready")
  ) {
    const name = {
      plan: "Plan",
      spec: "Spec",
      goal: "Goal",
    }[props.workflow.kind];
    items.push({
      kind: "workflow",
      label: `${name} ${props.workflow.status === "active" ? "生成中" : "待确认"}`,
      icon: FileTextIcon,
      active: props.workflow.status === "active",
    });
  }
  if (props.tasks.length > 0) {
    items.push({
      kind: "tasks",
      label: `${completedTasks.value}/${props.tasks.length} 个任务`,
      count: `${completedTasks.value}/${props.tasks.length}`,
      icon: ListChecksIcon,
      active: Boolean(props.taskRunning),
    });
  }
  if (runningCommands.value.length > 0) {
    items.push({
      kind: "commands",
      label: `${runningCommands.value.length} 个后台命令`,
      count: compactCount(runningCommands.value.length),
      icon: SquareTerminalIcon,
      active: true,
    });
  }
  if ((props.review?.files.length ?? 0) > 0) {
    items.push({
      kind: "files",
      label: `${props.review!.files.length} 个文件待审查`,
      count: compactCount(props.review!.files.length),
      icon: FilesIcon,
      active: Boolean(props.review?.submitting),
    });
  }
  if (props.queuedMessages.length > 0) {
    items.push({
      kind: "queue",
      label: `${props.queuedMessages.length} 条待发送消息`,
      count: compactCount(props.queuedMessages.length),
      icon: ListOrderedIcon,
      active: Boolean(props.streaming),
    });
  }
  return items;
});

const selectedActivity = computed(
  () =>
    activities.value.find((activity) => activity.kind === selected.value) ??
    activities.value[0]
);
const fileActionDisabled = computed(
  () => Boolean(props.reviewDisabled || props.review?.submitting)
);

watch(
  () => activities.value.map((activity) => activity.kind).join(","),
  () => {
    if (activities.value.length === 0) {
      selected.value = null;
      open.value = false;
      return;
    }
    if (!activities.value.some((activity) => activity.kind === selected.value)) {
      selected.value = activities.value[0].kind;
      open.value = false;
    }
  },
  { immediate: true }
);

useEventListener("keydown", (event) => {
  if (event.key === "Escape") open.value = false;
});

function compactCount(value: number) {
  return value > 99 ? "99+" : String(value);
}

function selectActivity(kind: ActivityKind) {
  if (selected.value === kind) {
    open.value = !open.value;
    return;
  }
  selected.value = kind;
  open.value = true;
}

function openCommand(command: BackgroundCommand) {
  emit("open-command", command);
}

function openFile(path: string, diff: string) {
  emit("open-file", path, diff);
}
</script>

<template>
  <section
    v-if="activities.length"
    class="relative shrink-0 px-4 pt-1.5"
    aria-label="会话活动"
  >
    <div
      class="relative mx-auto w-full max-w-3xl"
      :class="open && 'drop-shadow-lg'"
    >
      <div
        v-if="open && selectedActivity"
        class="absolute inset-x-0 bottom-full z-10 overflow-hidden rounded-t-lg border border-border bg-popover text-popover-foreground"
      >
        <TaskProgress
          v-if="selected === 'tasks'"
          :tasks="tasks"
        />
        <WorkflowPanel
          v-else-if="selected === 'workflow' && workflow"
          :workflow="workflow"
          :can-open-files="canOpenWorkflowFiles"
          @open-file="emit('open-workflow-file', $event)"
        />
        <BackgroundCommandsPanel
          v-else-if="selected === 'commands'"
          :commands="runningCommands"
          @stop="emit('stop-command', $event)"
          @open="openCommand"
        />
        <FileReviewBar
          v-else-if="selected === 'files'"
          :review="review"
          :disabled="fileActionDisabled"
          @toggle-force="emit('toggle-force-file', $event)"
          @open-file="openFile"
        />
        <QueuedMessages
          v-else-if="selected === 'queue'"
          :items="queuedMessages"
          :streaming="streaming"
          @edit="(id, text) => emit('edit-queued', id, text)"
          @reorder="(id, position) => emit('reorder-queued', id, position)"
          @dispatch="(id) => emit('dispatch-queued', id)"
          @delete="(id) => emit('delete-queued', id)"
        />
      </div>

      <div
        class="flex h-11 min-w-0 items-center overflow-hidden border border-border bg-muted/30"
        :class="
          open
            ? 'rounded-b-lg border-t-0 bg-popover'
            : 'rounded-lg shadow-xs'
        "
      >
        <TooltipProvider :delay-duration="250">
          <div class="flex h-full shrink-0 items-center border-r border-border px-1">
            <Tooltip
              v-for="activity in activities"
              :key="activity.kind"
            >
              <TooltipTrigger as-child>
                <Button
                  type="button"
                  size="icon-sm"
                  variant="ghost"
                  class="relative"
                  :class="
                    selectedActivity?.kind === activity.kind
                      ? 'bg-muted text-primary'
                      : 'text-muted-foreground'
                  "
                  :aria-label="activity.label"
                  :aria-pressed="selectedActivity?.kind === activity.kind"
                  @click="selectActivity(activity.kind)"
                >
                  <component :is="activity.icon" class="size-4" />
                  <span
                    v-if="activities.length > 1 && activity.count"
                    class="absolute -right-0.5 -top-0.5 flex min-w-3.5 items-center justify-center rounded-full bg-muted-foreground px-0.5 text-[8px] font-medium leading-3.5 text-background"
                  >
                    {{ activity.count }}
                  </span>
                  <span
                    v-if="activity.active"
                    class="absolute bottom-0.5 right-0.5 size-1.5 rounded-full bg-primary ring-2 ring-background"
                    aria-hidden="true"
                  />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="top">{{ activity.label }}</TooltipContent>
            </Tooltip>
          </div>

          <button
            v-if="selectedActivity"
            type="button"
            class="flex h-full min-w-0 flex-1 items-center gap-2 px-3 text-left transition-colors hover:bg-background/60"
            :aria-expanded="open"
            @click="open = !open"
          >
            <span class="min-w-0 truncate text-sm font-medium">
              {{ selectedActivity.label }}
            </span>
            <ChevronsUpDownIcon class="size-4 shrink-0 text-muted-foreground" />
          </button>

          <div
            v-if="selected === 'workflow' && workflow"
            class="flex h-full shrink-0 items-center gap-1 px-1.5"
          >
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  type="button"
                  size="icon-sm"
                  variant="ghost"
                  class="text-muted-foreground"
                  aria-label="退出当前工作流"
                  @click="emit('close-workflow')"
                >
                  <XIcon class="size-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="top">退出当前工作流</TooltipContent>
            </Tooltip>
            <Button
              v-if="workflow.status === 'ready'"
              type="button"
              size="sm"
              aria-label="确认并执行"
              @click="emit('approve-workflow')"
            >
              <CheckCheckIcon class="size-4" />
              <span class="hidden sm:inline">确认并执行</span>
            </Button>
          </div>

          <div
            v-if="selected === 'files'"
            class="flex h-full shrink-0 items-center gap-1 px-1.5"
          >
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  :disabled="fileActionDisabled"
                  aria-label="全部撤销"
                  @click="emit('undo-files')"
                >
                  <Undo2Icon class="size-4" />
                  <span class="hidden sm:inline">全部撤销</span>
                </Button>
              </TooltipTrigger>
              <TooltipContent side="top">全部撤销</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  type="button"
                  size="sm"
                  :disabled="fileActionDisabled"
                  aria-label="全部保留"
                  @click="emit('keep-files')"
                >
                  <LoaderCircleIcon
                    v-if="review?.submitting"
                    class="size-4 animate-spin"
                  />
                  <CheckCheckIcon v-else class="size-4" />
                  <span class="hidden sm:inline">全部保留</span>
                </Button>
              </TooltipTrigger>
              <TooltipContent side="top">全部保留</TooltipContent>
            </Tooltip>
          </div>
        </TooltipProvider>
      </div>
    </div>
  </section>
</template>
