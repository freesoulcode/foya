<script setup lang="ts">
import { ref, computed, nextTick, watch } from "vue";
import { onClickOutside } from "@vueuse/core";
import {
  ArrowUpIcon,
  SquareIcon,
  PlusIcon,
  ShieldIcon,
  ChevronDownIcon,
  ChevronLeftIcon,
  FolderOpenIcon,
  XIcon,
  CheckIcon,
  RefreshCwIcon,
  Minimize2Icon,
} from "@lucide/vue";
import { Textarea } from "@/components/ui/textarea";
import {
  api,
  type ApprovalMode,
  type ConnectionModelGroup,
  type ReasoningEffort,
  type ContextUsage as ContextUsageData,
  type QueuedMessage,
} from "@/lib/api";
import ContextUsage from "./ContextUsage.vue";
import QueuedMessages from "./QueuedMessages.vue";

const props = withDefaults(
  defineProps<{
    disabled?: boolean;
    streaming?: boolean;
    connectionId?: string;
    model?: string;
    reasoningEffort?: ReasoningEffort;
    workspace?: string;
    approval?: ApprovalMode;
    connections?: ConnectionModelGroup[];
    modelsLoading?: boolean;
    modelsError?: string;
    queuedMessages?: QueuedMessage[];
    contextUsage?: ContextUsageData;
    contextWindow?: number;
    hasSession?: boolean;
    workspaceLocked?: boolean;
  }>(),
  {
    disabled: false,
    streaming: false,
    connectionId: "",
    model: "",
    reasoningEffort: "",
    workspace: "",
    approval: "ask",
    connections: () => [],
    modelsLoading: false,
    modelsError: "",
    queuedMessages: () => [],
    contextUsage: undefined,
    contextWindow: 0,
    hasSession: false,
    workspaceLocked: false,
  }
);

const emit = defineEmits<{
  (e: "send", text: string): void;
  (e: "stop"): void;
  (e: "edit-queued", id: string, text: string): void;
  (e: "reorder-queued", id: string, position: number): void;
  (e: "dispatch-queued", id: string): void;
  (e: "delete-queued", id: string): void;
  (
    e: "update:model-config",
    value: { connectionID: string; model: string; reasoningEffort: ReasoningEffort }
  ): void;
  (e: "update:workspace", value: string): void;
  (e: "update:approval", value: ApprovalMode): void;
  (e: "refresh-models"): void;
}>();

const input = ref("");
const textareaRef = ref<InstanceType<typeof Textarea> | null>(null);
const inputFocused = ref(false);

interface SlashCommand {
  value: string;
  label: string;
  description: string;
}

const slashCommands: SlashCommand[] = [
  {
    value: "/compact",
    label: "压缩上下文",
    description: "将已完成的对话整理为精简检查点",
  },
];
const selectedCommandIndex = ref(0);
const commandMenuDismissed = ref(false);
let completingCommand = false;
const commandQuery = computed(() => input.value.trimStart());
const matchingCommands = computed(() => {
  const query = commandQuery.value.toLowerCase();
  if (!props.hasSession || props.streaming || !/^\/\S*$/.test(query)) return [];
  return slashCommands.filter(
    (command) =>
      command.value.startsWith(query) ||
      command.label.toLowerCase().includes(query.slice(1))
  );
});
const commandMenuOpen = computed(
  () =>
    inputFocused.value &&
    !props.disabled &&
    !commandMenuDismissed.value &&
    matchingCommands.value.length > 0
);
const activeCommand = computed(
  () => matchingCommands.value[selectedCommandIndex.value]
);

watch(input, () => {
  selectedCommandIndex.value = 0;
  if (completingCommand) {
    completingCommand = false;
    return;
  }
  commandMenuDismissed.value = false;
});

function completeCommand(command: SlashCommand) {
  if (input.value !== command.value) {
    completingCommand = true;
    input.value = command.value;
  }
  commandMenuDismissed.value = true;
  void nextTick(() => textareaRef.value?.$el?.focus());
}

// ---- 审批档位 ----
const approvalOptions: { value: ApprovalMode; label: string; hint: string }[] = [
  { value: "ask", label: "询问", hint: "危险操作前逐个询问" },
  { value: "explore", label: "只读", hint: "只读探索，不执行写/执行操作" },
  { value: "bypass", label: "自动审批", hint: "自动放行所有操作，不再询问" },
];

const approvalLabel = computed(
  () => approvalOptions.find((o) => o.value === props.approval)?.label ?? "询问"
);

const approvalOpen = ref(false);
const approvalRef = ref<HTMLElement | null>(null);
onClickOutside(approvalRef, () => (approvalOpen.value = false));

function selectApproval(m: ApprovalMode) {
  emit("update:approval", m);
  approvalOpen.value = false;
}

// ---- 模型与推理强度选择 ----
// 两步选择共用一个浮层：先选择模型，再选择该会话的推理强度。
// 最终触发器统一展示为“模型名 + 强度”，避免两个独立设置分散注意力。
const modelPickerOpen = ref(false);
const modelPickerStep = ref<"model" | "reasoning">("model");
const pickerModel = ref<string | null>(null);
const pickerConnectionID = ref<string | null>(null);
const pickerReasoningEffort = ref<ReasoningEffort | null>(null);
const modelRef = ref<HTMLElement | null>(null);
onClickOutside(modelRef, () => (modelPickerOpen.value = false));
watch(modelPickerOpen, (v) => {
  if (v) {
    pickerModel.value = null;
    pickerConnectionID.value = null;
    pickerReasoningEffort.value = null;
    modelPickerStep.value = "model";
    // 列表为空时触发上层拉取;已有列表则直接展示。
    if (props.connections.length === 0 && !props.modelsLoading) {
      emit("refresh-models");
    }
  }
});

function selectModel(connectionID: string, m: string) {
  pickerModel.value = m;
  // 切换模型时默认清除旧强度覆盖，然后直接下钻至强度选择。
  pickerReasoningEffort.value = "";
  pickerConnectionID.value = connectionID;
  modelPickerStep.value = "reasoning";
}

// ---- 推理强度 ----
const reasoningOptions: {
  value: ReasoningEffort;
  label: string;
  hint: string;
}[] = [
  { value: "", label: "默认推理", hint: "跟随模型服务默认设置" },
  { value: "low", label: "低推理", hint: "更快，适合简单任务" },
  { value: "medium", label: "中推理", hint: "平衡速度与深度" },
  { value: "high", label: "高推理", hint: "更深入，可能更慢" },
];
const reasoningDisplayLabel = computed(
  () =>
    reasoningOptions
      .find((option) => option.value === (pickerReasoningEffort.value ?? props.reasoningEffort))
      ?.label ?? "默认推理"
);
const pickerConnectionName = computed(() => {
  const id = pickerConnectionLabel.value;
  return props.connections.find((connection) => connection.id === id)?.name ?? "";
});
const modelPickerLabel = computed(() =>
  [
    pickerConnectionName.value,
    (pickerModel.value ?? props.model) || "选择模型",
    reasoningDisplayLabel.value,
  ]
    .filter(Boolean)
    .join(" · ")
);
const pickerModelLabel = computed(() => pickerModel.value ?? props.model);
const pickerConnectionLabel = computed(
  () => pickerConnectionID.value ?? props.connectionId
);
const pickerReasoningLabel = computed(
  () => pickerReasoningEffort.value ?? props.reasoningEffort
);

function selectReasoningEffort(value: ReasoningEffort) {
  pickerReasoningEffort.value = value;
  emit("update:model-config", {
    connectionID: pickerConnectionLabel.value,
    model: pickerModelLabel.value,
    reasoningEffort: value,
  });
  modelPickerOpen.value = false;
}

// ---- 文件夹绑定 ----
async function pickFolder() {
  if (props.disabled || props.workspaceLocked) return;
  try {
    const picked = await api.pickFolder();
    if (picked) emit("update:workspace", picked);
  } catch (e) {
    console.error("选择文件夹失败:", e);
  }
}

function clearWorkspace() {
  if (props.workspaceLocked) return;
  emit("update:workspace", "");
}

function basename(p: string) {
  if (!p) return "";
  const parts = p.replace(/\/+$/, "").split("/");
  return parts[parts.length - 1] || p;
}

// ---- 发送 ----
function submit() {
  const text = input.value.trim();
  if (!text || props.disabled) return;
  input.value = "";
  emit("send", text);
}

function onKeydown(e: KeyboardEvent) {
  if (e.isComposing) return;
  if (commandMenuOpen.value) {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const direction = e.key === "ArrowDown" ? 1 : -1;
      selectedCommandIndex.value =
        (selectedCommandIndex.value + direction + matchingCommands.value.length) %
        matchingCommands.value.length;
      return;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      commandMenuDismissed.value = true;
      return;
    }
    if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) {
      const command = activeCommand.value;
      if (command && commandQuery.value !== command.value) {
        e.preventDefault();
        completeCommand(command);
        return;
      }
    }
  }
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
    e.preventDefault();
    submit();
  }
}
</script>

<template>
  <div class="shrink-0 px-4 pb-4 pt-2">
    <div class="mx-auto max-w-3xl">
      <QueuedMessages
        :items="queuedMessages"
        :streaming="streaming"
        @edit="(id, text) => emit('edit-queued', id, text)"
        @reorder="(id, position) => emit('reorder-queued', id, position)"
        @dispatch="(id) => emit('dispatch-queued', id)"
        @delete="(id) => emit('delete-queued', id)"
      />

      <!-- 输入卡片 -->
      <div
        class="relative rounded-2xl border border-input bg-card shadow-xs transition-[color,box-shadow] focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50"
      >
        <div
          v-if="commandMenuOpen"
          id="composer-command-menu"
          class="absolute bottom-full left-0 right-0 z-20 mb-2 overflow-hidden rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-lg"
          role="listbox"
          aria-label="可用命令"
        >
          <button
            v-for="(command, index) in matchingCommands"
            :id="`composer-command-${index}`"
            :key="command.value"
            type="button"
            role="option"
            :aria-selected="index === selectedCommandIndex"
            class="flex w-full items-center gap-3 rounded-md px-3 py-2 text-left transition-colors"
            :class="
              index === selectedCommandIndex
                ? 'bg-accent text-accent-foreground'
                : 'hover:bg-muted'
            "
            @mouseenter="selectedCommandIndex = index"
            @mousedown.prevent
            @click="completeCommand(command)"
          >
            <Minimize2Icon class="size-4 shrink-0 text-muted-foreground" />
            <span class="min-w-0 flex-1">
              <span class="block text-sm font-medium">{{ command.label }}</span>
              <span class="block truncate text-xs text-muted-foreground">
                {{ command.description }}
              </span>
            </span>
            <code class="shrink-0 font-mono text-xs text-muted-foreground">
              {{ command.value }}
            </code>
          </button>
        </div>

        <Textarea
          ref="textareaRef"
          v-model="input"
          :aria-activedescendant="
            commandMenuOpen ? `composer-command-${selectedCommandIndex}` : undefined
          "
          :aria-controls="commandMenuOpen ? 'composer-command-menu' : undefined"
          :aria-expanded="commandMenuOpen"
          aria-autocomplete="list"
          :placeholder="
            streaming
              ? '继续输入，发送后加入待发送队列…'
              : '帮你编写代码、调试 Bug、优化性能等开发工作，交付生产级代码产物。'
          "
          class="max-h-60 min-h-[56px] resize-none border-0 bg-transparent px-4 py-3 text-sm shadow-none focus-visible:ring-0"
          rows="2"
          :disabled="disabled"
          @focus="inputFocused = true"
          @blur="inputFocused = false"
          @keydown="onKeydown"
        />

        <!-- 底部工具栏 -->
        <div class="flex items-center justify-between gap-2 px-2 pb-2">
          <div class="flex items-center gap-0.5">
            <!-- 绑定文件夹(+) -->
            <button
              v-if="!workspaceLocked"
              type="button"
              :disabled="disabled"
              class="flex size-8 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
              title="绑定工作文件夹"
              @click="pickFolder"
            >
              <PlusIcon class="size-4" />
            </button>

            <!-- 审批档位下拉 -->
            <div ref="approvalRef" class="relative">
              <button
                type="button"
                :disabled="disabled"
                class="flex items-center gap-1 rounded-lg px-2 py-1.5 text-[13px] font-medium transition-colors hover:bg-muted disabled:opacity-50"
                :class="
                  approval === 'bypass'
                    ? 'text-amber-600 dark:text-amber-400'
                    : approval === 'explore'
                      ? 'text-emerald-600 dark:text-emerald-400'
                      : 'text-foreground'
                "
                @click="approvalOpen = !approvalOpen"
              >
                <ShieldIcon class="size-4" />
                <span>{{ approvalLabel }}</span>
                <ChevronDownIcon class="size-3.5 opacity-60" />
              </button>

              <div
                v-if="approvalOpen"
                class="absolute bottom-full left-0 z-10 mb-1 w-56 overflow-hidden rounded-xl border border-border bg-popover p-1 text-popover-foreground shadow-lg"
              >
                <button
                  v-for="o in approvalOptions"
                  :key="o.value"
                  type="button"
                  class="flex w-full items-center justify-between gap-2 rounded-lg px-2.5 py-2 text-left text-[13px] transition-colors hover:bg-muted"
                  @click="selectApproval(o.value)"
                >
                  <span class="flex flex-col">
                    <span class="font-medium">{{ o.label }}</span>
                    <span class="text-xs text-muted-foreground">{{ o.hint }}</span>
                  </span>
                  <CheckIcon
                    v-if="o.value === approval"
                    class="size-4 shrink-0 text-primary"
                  />
                </button>
              </div>
            </div>
          </div>

          <div class="flex shrink-0 items-center gap-1">
            <ContextUsage
              :usage="contextUsage"
              :context-window="contextWindow"
            />

            <!-- 两步模型选择器：模型 → 推理强度。 -->
            <div ref="modelRef" class="relative">
              <button
                type="button"
                :disabled="disabled"
                class="flex shrink-0 items-center gap-1 whitespace-nowrap rounded-lg px-2 py-1.5 text-[13px] font-medium text-foreground transition-colors hover:bg-muted disabled:opacity-50"
                @click="modelPickerOpen = !modelPickerOpen"
              >
                <span>{{ modelPickerLabel }}</span>
                <ChevronDownIcon class="size-3.5 shrink-0 opacity-60" />
              </button>

              <div
                v-if="modelPickerOpen"
                class="absolute bottom-full right-0 z-10 mb-1 w-64 overflow-hidden rounded-xl border border-border bg-popover text-popover-foreground shadow-lg"
              >
                <div class="flex items-center justify-between gap-2 px-2.5 pb-1 pt-2">
                  <button
                    v-if="modelPickerStep === 'reasoning'"
                    type="button"
                    class="flex min-w-0 items-center gap-1 rounded-md px-1 py-0.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                    @click="modelPickerStep = 'model'"
                  >
                    <ChevronLeftIcon class="size-3.5" />
                    <span class="truncate">模型</span>
                  </button>
                  <span
                    v-if="modelPickerStep === 'reasoning'"
                    class="min-w-0 flex-1 truncate text-right text-xs font-medium text-muted-foreground"
                  >
                    推理强度
                  </span>
                  <span v-else class="text-xs font-medium text-muted-foreground">
                    选择模型
                  </span>
                  <button
                    v-if="modelPickerStep === 'model'"
                    type="button"
                    class="flex size-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                    title="刷新模型列表"
                    :disabled="modelsLoading"
                    @click="$emit('refresh-models')"
                  >
                    <RefreshCwIcon
                      class="size-3.5"
                      :class="modelsLoading ? 'animate-spin' : ''"
                    />
                  </button>
                </div>

                <div
                  v-if="modelPickerStep === 'model'"
                  class="max-h-64 overflow-y-auto p-1"
                >
                  <div
                    v-if="modelsLoading"
                    class="flex items-center gap-2 px-2.5 py-2 text-[13px] text-muted-foreground"
                  >
                    <RefreshCwIcon class="size-3.5 animate-spin" />
                    正在加载模型列表…
                  </div>

                  <div
                    v-else-if="modelsError"
                    class="px-2.5 py-2 text-[12px] text-destructive"
                  >
                    加载失败：{{ modelsError }}
                  </div>

                  <template v-else>
                    <section
                      v-for="connection in connections"
                      :key="connection.id"
                      class="mb-2 last:mb-0"
                    >
                      <div class="px-2.5 pb-1 pt-1">
                        <p class="truncate text-xs font-medium text-muted-foreground">
                          {{ connection.name }}
                        </p>
                        <p class="truncate text-[11px] text-muted-foreground/80">
                          {{ connection.kind }}
                        </p>
                      </div>
                      <button
                        v-for="m in connection.models"
                        :key="`${connection.id}:${m}`"
                        type="button"
                        class="flex w-full items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-left text-[13px] transition-colors hover:bg-muted"
                        @click="selectModel(connection.id ?? '', m)"
                      >
                        <span class="truncate">{{ m }}</span>
                        <span class="flex shrink-0 items-center gap-1 text-muted-foreground">
                          <CheckIcon
                            v-if="m === pickerModelLabel && connection.id === pickerConnectionLabel"
                            class="size-4 text-primary"
                          />
                          <ChevronDownIcon class="size-4 -rotate-90" />
                        </span>
                      </button>
                      <p
                        v-if="connection.models.length === 0"
                        class="px-2.5 py-1 text-[11px] text-muted-foreground"
                      >
                        {{ connection.models_error || "该连接没有可用模型" }}
                      </p>
                    </section>
                  </template>

                  <p
                    v-if="!modelsLoading && !modelsError && connections.length === 0"
                    class="px-2.5 py-2 text-[12px] text-muted-foreground"
                  >
                    尚未添加模型连接，请先在设置中添加 API 连接。
                  </p>
                </div>

                <div
                  v-else
                  class="p-1"
                >
                  <p class="px-2.5 pb-1 text-xs text-muted-foreground">
                    {{ pickerModelLabel || "当前模型" }}
                  </p>
                  <button
                    v-for="option in reasoningOptions"
                    :key="option.value || 'default'"
                    type="button"
                    class="flex w-full items-center justify-between gap-2 rounded-lg px-2.5 py-2 text-left text-[13px] transition-colors hover:bg-muted"
                    @click="selectReasoningEffort(option.value)"
                  >
                    <span class="flex min-w-0 flex-col">
                      <span class="font-medium">{{ option.label }}</span>
                      <span class="truncate text-xs text-muted-foreground">{{ option.hint }}</span>
                    </span>
                    <CheckIcon
                      v-if="option.value === pickerReasoningLabel"
                      class="size-4 shrink-0 text-primary"
                    />
                  </button>
                </div>
              </div>
            </div>

            <!-- 运行时同时保留停止与入队发送。 -->
            <button
              v-if="streaming"
              type="button"
              class="flex size-8 items-center justify-center rounded-lg bg-foreground text-background transition-opacity hover:opacity-80 disabled:opacity-30"
              title="停止"
              @click="emit('stop')"
            >
              <SquareIcon class="size-3.5 fill-current" />
            </button>
            <button
              type="button"
              class="flex size-8 items-center justify-center rounded-lg bg-foreground text-background transition-opacity hover:opacity-80 disabled:opacity-30"
              :disabled="disabled || !input.trim()"
              :title="streaming ? '加入待发送队列 (Enter)' : '发送 (Enter)'"
              @click="submit"
            >
              <ArrowUpIcon class="size-4" />
            </button>
          </div>
        </div>
      </div>

      <!-- 下方：工作文件夹 -->
      <div
        v-if="!workspaceLocked"
        class="mt-1 flex items-center gap-2 rounded-xl bg-muted/40 px-3 py-2"
      >
        <button
          type="button"
          :disabled="disabled"
          class="flex min-w-0 items-center gap-1.5 text-[13px] text-muted-foreground transition-colors hover:text-foreground disabled:opacity-50"
          :title="workspace || '选择文件夹（可选）'"
          @click="pickFolder"
        >
          <FolderOpenIcon class="size-4 shrink-0" />
          <span class="truncate">
            {{ workspace ? basename(workspace) : "选择文件夹（可选）" }}
          </span>
        </button>
        <button
          v-if="workspace"
          type="button"
          class="flex size-5 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          title="取消绑定"
          @click="clearWorkspace"
        >
          <XIcon class="size-3" />
        </button>
      </div>

      <p class="mt-2 text-center text-[11px] text-muted-foreground">
        {{ streaming ? "Enter 加入待发送队列" : "Enter 发送" }} · Shift+Enter 换行
      </p>
    </div>
  </div>
</template>
