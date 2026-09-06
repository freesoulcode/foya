<script setup lang="ts">
import { ref, computed, nextTick, watch, onUnmounted } from "vue";
import { onClickOutside } from "@vueuse/core";
import {
  ArrowUpIcon,
  SquareIcon,
  PlusIcon,
  ShieldIcon,
  ChevronDownIcon,
  ChevronLeftIcon,
  FolderIcon,
  FolderOpenIcon,
  XIcon,
  CheckIcon,
  RefreshCwIcon,
  PaperclipIcon,
  FileTextIcon,
  PackageIcon,
  RouteIcon,
  TargetIcon,
} from "@lucide/vue";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  type ApprovalMode,
  type ConnectionModelGroup,
  type ReasoningEffort,
  type ContextUsage as ContextUsageData,
  type CommandInfo,
  type ProjectInfo,
  type SkillInfo,
  type BrowserElementSelection,
  api,
} from "@/lib/api";
import ContextUsage from "./ContextUsage.vue";
import InlineComposerEditor from "./InlineComposerEditor.vue";

interface RestoreTextSignal {
  sessionId: string;
  text: string;
  nonce: number;
}

const props = withDefaults(
  defineProps<{
    disabled?: boolean;
    streaming?: boolean;
    connectionId?: string;
    model?: string;
    reasoningEffort?: ReasoningEffort;
    projectId?: string;
    projects?: ProjectInfo[];
    approval?: ApprovalMode;
    connections?: ConnectionModelGroup[];
    modelsLoading?: boolean;
    modelsError?: string;
    contextUsage?: ContextUsageData;
    contextWindow?: number;
    hasSession?: boolean;
    sessionId?: string;
    projectLocked?: boolean;
    browserElements?: BrowserElementSelection[];
    supportsImage?: boolean;
    restoreText?: RestoreTextSignal | null;
  }>(),
  {
    disabled: false,
    streaming: false,
    connectionId: "",
    model: "",
    reasoningEffort: "",
    projectId: "",
    projects: () => [],
    approval: "manual",
    connections: () => [],
    modelsLoading: false,
    modelsError: "",
    contextUsage: undefined,
    contextWindow: 0,
    hasSession: false,
    sessionId: "",
    projectLocked: false,
    browserElements: () => [],
    supportsImage: false,
    restoreText: null,
  }
);

const emit = defineEmits<{
  (
    e: "send",
    text: string,
    files: File[],
    browserElements: BrowserElementSelection[],
    skillRef: string,
    restore: () => void
  ): void;
  (e: "command", name: string, args: string): void;
  (e: "stop"): void;
  (
    e: "update:model-config",
    value: { connectionID: string; model: string; reasoningEffort: ReasoningEffort }
  ): void;
  (e: "update:project-id", value: string): void;
  (e: "add-project"): void;
  (e: "update:approval", value: ApprovalMode): void;
  (e: "refresh-models"): void;
  (e: "remove-browser-element", index: number): void;
  (e: "clear-browser-elements"): void;
  (e: "restore-browser-elements", elements: BrowserElementSelection[]): void;
  (e: "restore-consumed", nonce: number): void;
}>();

const input = ref("");
const editorRef = ref<InstanceType<typeof InlineComposerEditor> | null>(null);
const commandMenuRef = ref<HTMLElement | null>(null);
const inputFocused = ref(false);
const fileInputRef = ref<HTMLInputElement | null>(null);
const attachmentError = ref("");
const pendingImages = ref<Array<{ file: File; url: string }>>([]);
const lastRestoreNonce = ref(0);

function clearPendingImages() {
  for (const item of pendingImages.value) URL.revokeObjectURL(item.url);
  pendingImages.value = [];
}

function addImages(files: File[]) {
  attachmentError.value = "";
  if (!props.supportsImage) {
    attachmentError.value = "当前模型未声明支持视觉输入";
    return;
  }
  for (const file of files) {
    if (!file.type.startsWith("image/")) continue;
    if (file.size > 20 * 1024 * 1024) {
      attachmentError.value = `${file.name} 超过 20MB`;
      continue;
    }
    if (pendingImages.value.length >= 8) {
      attachmentError.value = "每条消息最多添加 8 张图片";
      break;
    }
    pendingImages.value.push({ file, url: URL.createObjectURL(file) });
  }
}

function removeImage(index: number) {
  const [removed] = pendingImages.value.splice(index, 1);
  if (removed) URL.revokeObjectURL(removed.url);
}

function onFilesSelected(event: Event) {
  const target = event.target as HTMLInputElement;
  addImages(Array.from(target.files ?? []));
  target.value = "";
}

function onPaste(event: ClipboardEvent) {
  const images = Array.from(event.clipboardData?.files ?? []).filter((file) =>
    file.type.startsWith("image/")
  );
  if (images.length === 0) return;
  event.preventDefault();
  addImages(images);
}

function onDrop(event: DragEvent) {
  const images = Array.from(event.dataTransfer?.files ?? []).filter((file) =>
    file.type.startsWith("image/")
  );
  if (images.length === 0) return;
  event.preventDefault();
  addImages(images);
}

onUnmounted(clearPendingImages);

interface SlashOption {
  kind: "command" | "skill";
  ref: string;
  value: string;
  label: string;
  description: string;
  scope?: SkillInfo["scope"];
}

const sessionCommands = ref<CommandInfo[]>([]);
const availableSkills = ref<SkillInfo[]>([]);
const selectedSlashCommand = ref<CommandInfo | null>(null);
const selectedSkill = ref<SkillInfo | null>(null);
const commandError = ref("");
const selectedCommandIndex = ref(0);
const commandMenuDismissed = ref(false);
let completingCommand = false;
const commandQuery = computed(() => input.value.trimStart());
const commandToken = computed(() => commandQuery.value.split(/\s+/, 1)[0] ?? "");
const draftCommands: CommandInfo[] = [
  { ref: "builtin:plan", name: "plan", description: "启动 Plan 工作流", scope: "builtin", kind: "workflow", builtin: true },
  { ref: "builtin:spec", name: "spec", description: "启动 Spec 工作流", scope: "builtin", kind: "workflow", builtin: true },
  { ref: "builtin:goal", name: "goal", description: "启动 Goal 工作流", scope: "builtin", kind: "workflow", builtin: true },
];
const availableCommands = computed(() =>
  props.hasSession ? sessionCommands.value : draftCommands
);
const slashCommands = computed<SlashOption[]>(() =>
  availableCommands.value.map((command) => ({
    kind: "command",
    ref: command.ref,
    value: `/${command.name}`,
    label: command.name[0].toUpperCase() + command.name.slice(1),
    description: commandDescription(command),
  }))
);
const slashSkills = computed<SlashOption[]>(() =>
  availableSkills.value
    .filter((skill) => skill.enabled)
    .map((skill) => ({
      kind: "skill",
      ref: skill.ref,
      value: `/${skill.name}`,
      label: skill.name,
      description: skill.description || "Agent Skill",
      scope: skill.scope,
    }))
);

function commandDescription(command: CommandInfo): string {
  if (command.name === "plan") return "只读探索并生成待批准的实施计划";
  if (command.name === "spec") return "生成可审阅的技术规格";
  if (command.name === "goal") return "定义持久化的完成目标";
  return command.description || "自定义 Prompt 命令";
}

function skillScopeLabel(scope?: SkillInfo["scope"]): string {
  return {
    builtin: "系统",
    plugin: "插件",
    global: "全局",
    user: "个人",
    project: "项目",
  }[scope ?? "global"];
}
const matchingOptions = computed(() => {
  const query = commandToken.value.toLowerCase();
  if (query !== commandQuery.value.toLowerCase()) return [];
  if (props.streaming || !/^\/\S*$/.test(query)) return [];
  const term = query.slice(1);
  return [...slashCommands.value, ...slashSkills.value].filter(
    (option) =>
      option.value.toLowerCase().startsWith(query) ||
      option.label.toLowerCase().includes(term) ||
      option.description.toLowerCase().includes(term) ||
      option.ref.toLowerCase().includes(term)
  );
});
const matchingGroups = computed(() =>
  (["command", "skill"] as const)
    .map((kind) => ({
      kind,
      label: kind === "command" ? "命令" : "技能",
      items: matchingOptions.value
        .map((option, index) => ({ option, index }))
        .filter((item) => item.option.kind === kind),
    }))
    .filter((group) => group.items.length > 0)
);
const commandMenuOpen = computed(
  () =>
    inputFocused.value &&
    !props.disabled &&
    !commandMenuDismissed.value &&
    matchingOptions.value.length > 0
);
const activeCommand = computed(
  () => matchingOptions.value[selectedCommandIndex.value]
);

watch(selectedCommandIndex, async (index) => {
  await nextTick();
  commandMenuRef.value
    ?.querySelector<HTMLElement>(`[data-option-index="${index}"]`)
    ?.scrollIntoView({ block: "nearest" });
});

watch(input, () => {
  selectedCommandIndex.value = 0;
  commandError.value = "";
  if (completingCommand) {
    completingCommand = false;
    return;
  }
  commandMenuDismissed.value = false;
});

watch(
  () => props.restoreText?.nonce,
  (nonce) => {
    const signal = props.restoreText;
    if (!signal || !nonce || signal.sessionId !== props.sessionId) return;
    if (lastRestoreNonce.value === signal.nonce) return;
    lastRestoreNonce.value = signal.nonce;
    selectedSlashCommand.value = null;
    selectedSkill.value = null;
    commandError.value = "";
    commandMenuDismissed.value = true;
    attachmentError.value = "";
    clearPendingImages();
    emit("clear-browser-elements");
    input.value = signal.text;
    void nextTick(() => editorRef.value?.focus());
    emit("restore-consumed", signal.nonce);
  },
  { immediate: true }
);

async function loadCommands() {
  if (!props.sessionId || !props.hasSession) {
    sessionCommands.value = [];
    return;
  }
  try {
    sessionCommands.value = await api.listSessionCommands(props.sessionId);
  } catch (cause) {
    sessionCommands.value = [];
    commandError.value = `无法加载命令：${String(cause)}`;
  }
}

async function loadSkills() {
  try {
    const items = await api.listAvailableSkills(props.projectId);
    availableSkills.value = items;
    if (
      selectedSkill.value &&
      !items.some((item) => item.ref === selectedSkill.value?.ref && item.enabled)
    ) {
      selectedSkill.value = null;
    }
  } catch (cause) {
    availableSkills.value = [];
    commandError.value = `无法加载技能：${String(cause)}`;
  }
}

watch(
  () => [props.sessionId, props.hasSession, props.projectId] as const,
  () => {
    void loadCommands();
    void loadSkills();
  },
  { immediate: true }
);

function completeOption(option: SlashOption) {
  if (option.kind === "command") {
    selectedSlashCommand.value =
      availableCommands.value.find((item) => item.ref === option.ref) ?? null;
    selectedSkill.value = null;
  } else {
    selectedSkill.value =
      availableSkills.value.find((item) => item.ref === option.ref) ?? null;
    selectedSlashCommand.value = null;
  }
  completingCommand = true;
  input.value = "";
  commandMenuDismissed.value = true;
  void nextTick(() => editorRef.value?.focus());
}

function clearSelectedCommand() {
  selectedSlashCommand.value = null;
  selectedSkill.value = null;
  commandError.value = "";
  void nextTick(() => editorRef.value?.focus());
}

function selectedCommandIcon() {
  if (selectedSkill.value) return PackageIcon;
  if (selectedSlashCommand.value?.name === "plan") return RouteIcon;
  if (selectedSlashCommand.value?.name === "spec") return FileTextIcon;
  return TargetIcon;
}

function commandIcon(option: SlashOption) {
  if (option.kind === "skill") return PackageIcon;
  if (option.value === "/plan") return RouteIcon;
  if (option.value === "/spec") return FileTextIcon;
  return TargetIcon;
}

// ---- 审批档位 ----
const approvalOptions: { value: ApprovalMode; label: string; hint: string }[] = [
  { value: "manual", label: "手动审批", hint: "沙箱开启，写入、执行和联网前询问" },
  { value: "auto", label: "自动审批", hint: "沙箱开启，由当前模型判断" },
  { value: "full_access", label: "完全访问", hint: "关闭沙箱并自动放行" },
];

const approvalLabel = computed(
  () => approvalOptions.find((o) => o.value === props.approval)?.label ?? "手动审批"
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
const availableConnections = computed(() =>
  props.connections.filter(
    (connection) => connection.models.length > 0 && !connection.models_error
  )
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

const selectedProject = computed(
  () => props.projects.find((project) => project.id === props.projectId) ?? null
);

const projectPickerOpen = ref(false);

function selectProject(projectID: string) {
  emit("update:project-id", projectID);
  projectPickerOpen.value = false;
}

function selectProjectValue(value: unknown) {
  if (
    typeof value === "string" &&
    props.projects.some((project) => project.id === value)
  ) {
    selectProject(value);
  }
}

function createProjectFromPicker(event: Event) {
  event.preventDefault();
  projectPickerOpen.value = false;
  emit("add-project");
}

// ---- 发送 ----
async function submit() {
  const text = input.value.trim();
  if (pendingImages.value.length > 0 && !props.supportsImage) {
    attachmentError.value = "当前模型未声明支持视觉输入，请移除图片或切换模型";
    return;
  }
  if (
    (!text &&
      pendingImages.value.length === 0 &&
      props.browserElements.length === 0) ||
    props.disabled
  ) {
    return;
  }
  if (
    selectedSlashCommand.value &&
    pendingImages.value.length === 0 &&
    props.browserElements.length === 0
  ) {
    emit("command", selectedSlashCommand.value.name, text);
    selectedSlashCommand.value = null;
    input.value = "";
    commandMenuDismissed.value = true;
    return;
  }
  const files = pendingImages.value.map((item) => item.file);
  const browserElements =
    editorRef.value?.orderedElements() ?? [...props.browserElements];
  const submittedSkill = selectedSkill.value;
  input.value = "";
  selectedSkill.value = null;
  clearPendingImages();
  emit("clear-browser-elements");
  emit("send", text, files, browserElements, submittedSkill?.ref ?? "", () => {
    if (!input.value) input.value = text;
    if (!selectedSkill.value) selectedSkill.value = submittedSkill;
    addImages(files);
    emit("restore-browser-elements", browserElements);
  });
}

function onKeydown(e: KeyboardEvent) {
  if (e.isComposing) return;
  if (
    e.key === "Backspace" &&
    (selectedSlashCommand.value || selectedSkill.value) &&
    input.value.length === 0
  ) {
    e.preventDefault();
    clearSelectedCommand();
    return;
  }
  if (commandMenuOpen.value) {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const direction = e.key === "ArrowDown" ? 1 : -1;
      selectedCommandIndex.value =
        (selectedCommandIndex.value + direction + matchingOptions.value.length) %
        matchingOptions.value.length;
      return;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      commandMenuDismissed.value = true;
      return;
    }
    if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) {
      const command = activeCommand.value;
      if (command) {
        e.preventDefault();
        completeOption(command);
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
      <!-- 输入卡片 -->
      <div
        class="composer-card relative rounded-2xl border border-input bg-card shadow-xs transition-[color,box-shadow] focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50"
        @dragover.prevent
        @drop="onDrop"
      >
        <div
          v-if="commandMenuOpen"
          id="composer-command-menu"
          ref="commandMenuRef"
          class="absolute bottom-full left-0 z-20 mb-2 max-h-[min(28rem,60vh)] w-full overflow-y-auto rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-lg"
          role="listbox"
          aria-label="可用命令和技能"
        >
          <template v-for="group in matchingGroups" :key="group.kind">
            <div
              class="px-2.5 pb-1 pt-2 text-xs font-medium text-muted-foreground"
            >
              {{ group.label }}
            </div>
            <button
              v-for="item in group.items"
              :id="`composer-command-${item.index}`"
              :key="`${item.option.kind}:${item.option.ref}`"
              :data-option-index="item.index"
              type="button"
              role="option"
              :aria-selected="item.index === selectedCommandIndex"
              class="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left transition-colors"
              :class="
                item.index === selectedCommandIndex
                  ? 'bg-accent text-accent-foreground'
                  : 'hover:bg-muted'
              "
              @mouseenter="selectedCommandIndex = item.index"
              @mousedown.prevent
              @click="completeOption(item.option)"
            >
              <component
                :is="commandIcon(item.option)"
                class="size-4 shrink-0 text-muted-foreground"
              />
              <span class="flex min-w-0 flex-1 items-baseline gap-2">
                <span class="min-w-0 truncate text-sm font-medium">
                  {{ item.option.label }}
                </span>
                <span class="min-w-0 flex-1 truncate text-xs text-muted-foreground">
                  {{ item.option.description }}
                </span>
              </span>
              <span class="max-w-36 shrink-0 truncate text-xs text-muted-foreground">
                {{
                  item.option.kind === "skill"
                    ? skillScopeLabel(item.option.scope)
                    : item.option.value
                }}
              </span>
            </button>
          </template>
        </div>

        <div v-if="pendingImages.length" class="flex gap-2 overflow-x-auto px-3 pt-3">
          <div
            v-for="(item, index) in pendingImages"
            :key="item.url"
            class="relative size-16 shrink-0 overflow-hidden rounded-md border border-border bg-muted"
          >
            <img :src="item.url" :alt="item.file.name" class="size-full object-cover" />
            <button
              type="button"
              class="absolute right-1 top-1 flex size-5 items-center justify-center rounded-full bg-background/90 text-foreground shadow-sm"
              title="移除图片"
              @click="removeImage(index)"
            >
              <XIcon class="size-3" />
            </button>
          </div>
        </div>
        <p v-if="commandError" class="px-4 pt-2 text-xs text-destructive">
          {{ commandError }}
        </p>
        <p v-if="attachmentError" class="px-4 pt-2 text-xs text-destructive">
          {{ attachmentError }}
        </p>
        <input
          ref="fileInputRef"
          type="file"
          accept="image/png,image/jpeg,image/webp,image/gif"
          multiple
          class="hidden"
          @change="onFilesSelected"
        />
        <div class="flex items-start px-4">
          <button
            v-if="selectedSlashCommand || selectedSkill"
            type="button"
            class="mt-3 inline-flex shrink-0 items-center gap-1 rounded-md border border-border bg-muted/60 px-1.5 py-0.5 text-xs font-medium text-foreground transition-colors hover:bg-muted"
            title="取消选择"
            @click="clearSelectedCommand"
          >
            <component :is="selectedCommandIcon()" class="size-3.5" />
            <span>
              {{
                selectedSkill?.name ||
                (selectedSlashCommand
                  ? selectedSlashCommand.name[0].toUpperCase() +
                    selectedSlashCommand.name.slice(1)
                  : "")
              }}
            </span>
            <XIcon class="size-3" />
          </button>
          <InlineComposerEditor
            ref="editorRef"
            v-model="input"
            :browser-elements="browserElements"
            :aria-activedescendant="
              commandMenuOpen ? `composer-command-${selectedCommandIndex}` : undefined
            "
            :aria-controls="commandMenuOpen ? 'composer-command-menu' : undefined"
            :aria-expanded="commandMenuOpen"
            aria-autocomplete="list"
            :placeholder="
              streaming
                ? '继续输入，发送后加入待发送队列…'
                : selectedSlashCommand
                  ? `输入 ${selectedSlashCommand.name} 的目标`
                  : selectedSkill
                    ? `输入 ${selectedSkill.name} 的任务`
                  : '帮你编写代码、调试 Bug、优化性能等开发工作，交付生产级代码产物。'
            "
            :class="(selectedSlashCommand || selectedSkill) && 'pl-2'"
            :disabled="disabled"
            @focus="inputFocused = true"
            @blur="inputFocused = false"
            @keydown="onKeydown"
            @paste="onPaste"
            @remove-browser-element="emit('remove-browser-element', $event)"
          />
        </div>

        <!-- 底部工具栏 -->
        <div class="flex min-w-0 items-center gap-2 px-2 pb-2">
          <div class="flex shrink-0 items-center gap-0.5">
            <button
              type="button"
              :disabled="disabled || !supportsImage"
              class="flex size-8 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
              :title="supportsImage ? '添加图片' : '当前模型不支持视觉输入'"
              @click="fileInputRef?.click()"
            >
              <PaperclipIcon class="size-4" />
            </button>
            <!-- 绑定文件夹(+) -->
            <button
              v-if="!projectLocked"
              type="button"
              :disabled="disabled"
              class="flex size-8 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
              title="添加项目"
              @click="emit('add-project')"
            >
              <PlusIcon class="size-4" />
            </button>

            <!-- 审批档位下拉 -->
            <div ref="approvalRef" class="relative">
              <button
                type="button"
                :disabled="disabled"
                class="flex shrink-0 items-center gap-1 whitespace-nowrap rounded-lg px-2 py-1.5 text-[13px] font-medium transition-colors hover:bg-muted disabled:opacity-50"
                :class="
                  approval === 'full_access'
                    ? 'text-amber-600 dark:text-amber-400'
                    : approval === 'auto'
                      ? 'text-sky-600 dark:text-sky-400'
                      : 'text-foreground'
                "
                @click="approvalOpen = !approvalOpen"
              >
                <ShieldIcon class="size-4" />
                <span class="approval-label">{{ approvalLabel }}</span>
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

          <div class="flex min-w-0 flex-1 items-center justify-end gap-1">
            <ContextUsage
              :usage="contextUsage"
              :context-window="contextWindow"
            />

            <!-- 两步模型选择器：模型 → 推理强度。 -->
            <div ref="modelRef" class="relative min-w-0">
              <button
                type="button"
                :disabled="disabled"
                class="flex min-w-0 max-w-full items-center gap-1 rounded-lg px-2 py-1.5 text-[13px] font-medium text-foreground transition-colors hover:bg-muted disabled:opacity-50"
                @click="modelPickerOpen = !modelPickerOpen"
              >
                <span class="truncate">{{ modelPickerLabel }}</span>
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
                      v-for="connection in availableConnections"
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
                    </section>
                  </template>

                  <p
                    v-if="!modelsLoading && !modelsError && availableConnections.length === 0"
                    class="px-2.5 py-2 text-[12px] text-muted-foreground"
                  >
                    没有可用模型，请在设置中检查模型连接。
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
              :disabled="
                disabled ||
                (!input.trim() &&
                  pendingImages.length === 0 &&
                  browserElements.length === 0)
              "
              :title="streaming ? '加入待发送队列 (Enter)' : '发送 (Enter)'"
              @click="submit"
            >
              <ArrowUpIcon class="size-4" />
            </button>
          </div>
        </div>
      </div>

      <!-- 下方：项目上下文 -->
      <div
        class="mt-1 flex items-center gap-2 rounded-xl bg-muted/40 px-3 py-2"
      >
        <FolderOpenIcon class="size-4 shrink-0 text-muted-foreground" />
        <Popover
          v-if="!projectLocked"
          v-model:open="projectPickerOpen"
        >
          <PopoverTrigger as-child>
            <button
              type="button"
              :disabled="disabled"
              class="flex h-7 min-w-0 flex-1 items-center justify-between gap-2 text-left text-[13px] text-muted-foreground outline-none disabled:opacity-50"
              aria-label="选择项目"
            >
              <span class="truncate">
                {{
                  selectedProject
                    ? `${selectedProject.name} · ${selectedProject.path}`
                    : "无项目"
                }}
              </span>
              <ChevronDownIcon class="size-3.5 shrink-0 opacity-60" />
            </button>
          </PopoverTrigger>
          <PopoverContent
            side="top"
            align="start"
            :side-offset="6"
            class="w-72 max-w-[calc(100vw-2rem)] gap-0 p-0"
          >
            <Command
              :model-value="projectId"
              @update:model-value="selectProjectValue"
            >
              <CommandInput placeholder="搜索项目" />
              <CommandList class="max-h-52 p-1">
                <CommandEmpty>未找到项目</CommandEmpty>
                <CommandGroup>
                  <CommandItem
                    v-for="project in projects"
                    :key="project.id"
                    :value="project.id"
                    class="h-9"
                  >
                    <FolderIcon class="size-4" />
                    <span class="truncate">{{ project.name }}</span>
                    <span class="sr-only">{{ project.path }}</span>
                  </CommandItem>
                </CommandGroup>
              </CommandList>
              <CommandSeparator />
              <div class="p-1">
                <button
                  type="button"
                  class="flex h-9 w-full items-center gap-2 rounded-sm px-2 text-sm outline-none hover:bg-muted focus-visible:bg-muted"
                  @click="createProjectFromPicker"
                >
                  <PlusIcon class="size-4 text-muted-foreground" />
                  新建项目
                </button>
                <button
                  type="button"
                  class="flex h-9 w-full items-center gap-2 rounded-sm px-2 text-sm outline-none hover:bg-muted focus-visible:bg-muted"
                  @click="selectProject('')"
                >
                  <XIcon class="size-4 text-muted-foreground" />
                  不在项目中工作
                </button>
              </div>
            </Command>
          </PopoverContent>
        </Popover>
        <span
          v-else
          class="min-w-0 flex-1 truncate text-[13px] text-muted-foreground"
          :title="selectedProject?.path"
        >
          {{ selectedProject?.name ?? "无项目" }}
        </span>
      </div>

      <p class="mt-2 text-center text-[11px] text-muted-foreground">
        {{ streaming ? "Enter 加入待发送队列" : "Enter 发送" }} · Shift+Enter 换行
      </p>
    </div>
  </div>
</template>

<style scoped>
.composer-card {
  container-type: inline-size;
}

@container (max-width: 520px) {
  .approval-label {
    display: none;
  }
}
</style>
