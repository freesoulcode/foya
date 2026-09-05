<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import {
  BotIcon,
  CircleStopIcon,
  CopyIcon,
  CheckIcon,
  Undo2Icon,
  ChevronRightIcon,
  GitForkIcon,
  BrainIcon,
  FileTextIcon,
  MousePointer2Icon,
  RouteIcon,
  TargetIcon,
} from "@lucide/vue";
import { cn } from "@/lib/utils";
import { api } from "@/lib/api";
import type { ChatMessage, ToolCallView, MessageSegment } from "@/lib/api";
import MarkdownContent from "./MarkdownContent.vue";
import ToolActivityGroup from "./ToolActivityGroup.vue";
import TaskArtifacts from "./TaskArtifacts.vue";

const props = defineProps<{
  sessionId: string;
  projectPath?: string;
  message: ChatMessage;
  streaming?: boolean;
  editable?: boolean;
}>();

const attachmentURLs = ref<Record<string, string>>({});
let attachmentLoad = 0;

function releaseAttachmentURLs() {
  for (const url of Object.values(attachmentURLs.value)) URL.revokeObjectURL(url);
  attachmentURLs.value = {};
}

async function loadAttachmentPreviews() {
  const load = ++attachmentLoad;
  releaseAttachmentURLs();
  if (!props.sessionId) return;
  for (const attachment of props.message.attachments ?? []) {
    if (attachment.kind !== "image") continue;
    try {
      const bytes = await api.readArtifact(props.sessionId, attachment.id);
      if (load !== attachmentLoad) return;
      attachmentURLs.value = {
        ...attachmentURLs.value,
        [attachment.id]: URL.createObjectURL(
          new Blob([bytes], { type: attachment.media_type })
        ),
      };
    } catch {
      // Keep the named attachment visible even when its preview cannot load.
    }
  }
}

watch(
  () => [props.sessionId, props.message.attachments],
  () => void loadAttachmentPreviews(),
  { immediate: true, deep: true }
);
onBeforeUnmount(() => {
  attachmentLoad++;
  releaseAttachmentURLs();
});

const emit = defineEmits<{
  (e: "rewind", messageSeq: number): void;
  (e: "fork", messageSeq: number): void;
  (e: "open-diff", diff: string): void;
  (e: "cancel-tool", toolCallId: string): void;
  (e: "background-tool", toolCallId: string): void;
  (e: "terminal-tool", toolCallId: string): void;
  (e: "open-link", url: string): void;
}>();

const isUser = computed(() => props.message.role === "user");
const toolCalls = computed(() => props.message.tool_calls ?? []);
const commandIcon = computed(() => {
  if (props.message.command === "plan") return RouteIcon;
  if (props.message.command === "spec") return FileTextIcon;
  return TargetIcon;
});

// 有序段落:优先用 segments;缺失时(旧数据/兜底)由扁平字段合成一个近似序列。
const segments = computed<MessageSegment[]>(() => {
  if (isUser.value) return [];
  if (props.message.segments && props.message.segments.length > 0) {
    return props.message.segments;
  }
  const segs: MessageSegment[] = [];
  if (props.message.reasoning) segs.push({ kind: "reasoning", text: props.message.reasoning });
  for (const tc of toolCalls.value) segs.push({ kind: "tool", tool: tc });
  if (props.message.content) segs.push({ kind: "text", text: props.message.content });
  return segs;
});

// 某个 reasoning 段是否正处于流式生成中:仅当它是整条消息的最后一段且仍在流式。
function isReasoningStreaming(index: number): boolean {
  return !!props.streaming && index === segments.value.length - 1;
}

// reasoning 段的展开状态(按段索引记录)。
const expandedReasoning = ref<Set<number>>(new Set());
function toggleReasoning(index: number) {
  if (expandedReasoning.value.has(index)) expandedReasoning.value.delete(index);
  else expandedReasoning.value.add(index);
}
function isReasoningExpanded(index: number) {
  return expandedReasoning.value.has(index);
}

// pending:助手气泡已乐观插入但首 token 还没到(无任何段、非错误、流式中)。
const isPending = computed(
  () => !isUser.value && !props.message.error && segments.value.length === 0 && !!props.streaming
);

const isError = computed(() => !isUser.value && props.message.error === true);
const isCancelledTurn = computed(
  () => !isUser.value && props.message.turn_status === "cancelled"
);
const cancelledLabel = computed(() => {
  switch (props.message.turn_reason) {
    case "user_stop":
      return "用户终止输出";
    case "queue_dispatch":
      return "已切换到队列中的下一条消息";
    case "session_deleted":
      return "会话删除时已取消本次回复";
    default:
      return "本次回复已取消";
  }
});

// 回合中「工作中」空窗:纯派生自 streaming + segments,不依赖任何专用事件。
// 各段各自的进行态已被覆盖(首 token 前=isPending 打字点、思考中=「正在思考…」、
// 工具运行中=图标 pulse、正文流式=光标)。唯一没人管的空白是:某工具已结束、
// 但回合仍在流式(streaming 为真)——此时模型正在为下一步生成内容,填一个指示。
const showWorking = computed(() => {
  if (isUser.value || props.message.error || !props.streaming) return false;
  const last = segments.value[segments.value.length - 1];
  return (
    !!last &&
    last.kind === "tool" &&
    (last.tool.status === "done" || last.tool.status === "error")
  );
});

function isToolBatchStart(index: number): boolean {
  return segments.value[index]?.kind === "tool" && segments.value[index - 1]?.kind !== "tool";
}

function toolBatchAt(index: number): ToolCallView[] {
  const batch: ToolCallView[] = [];
  for (let i = index; i < segments.value.length; i++) {
    const segment = segments.value[i];
    if (segment.kind !== "tool") break;
    batch.push(segment.tool);
  }
  return batch;
}

const processExpanded = ref(false);

// 使用过工具的已完成回合中，最后一个工具之后的最后一段正文是最终回复；
// 其余思考、工具和中间正文统一归入可折叠的任务过程。
const finalTextSegmentIndex = computed(() => {
  let lastToolIndex = -1;
  for (let i = segments.value.length - 1; i >= 0; i--) {
    if (segments.value[i].kind === "tool") {
      lastToolIndex = i;
      break;
    }
  }
  for (let i = segments.value.length - 1; i > lastToolIndex; i--) {
    if (segments.value[i].kind === "text") return i;
  }
  return -1;
});

const taskDurationMS = computed<number | null>(() => {
  if (!props.message.turn_started_at || !props.message.turn_completed_at) return null;
  const startedAt = Date.parse(props.message.turn_started_at);
  const completedAt = Date.parse(props.message.turn_completed_at);
  if (!Number.isFinite(startedAt) || !Number.isFinite(completedAt)) return null;
  return Math.max(completedAt - startedAt, 0);
});

const isCompletedTask = computed(
  () =>
    !props.streaming &&
    toolCalls.value.length > 0 &&
    finalTextSegmentIndex.value >= 0 &&
    taskDurationMS.value !== null
);

const copyContent = computed(() => {
  if (!isCompletedTask.value) return props.message.content;
  const segment = segments.value[finalTextSegmentIndex.value];
  return segment?.kind === "text" ? segment.text : props.message.content;
});

function shouldRenderSegment(index: number): boolean {
  return (
    !isCompletedTask.value ||
    processExpanded.value ||
    index === finalTextSegmentIndex.value
  );
}

function formatDuration(durationMS: number): string {
  if (durationMS < 1000) return "<1s";
  const totalSeconds = Math.round(durationMS / 1000);
  if (totalSeconds < 60) return `${totalSeconds}s`;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return seconds > 0 ? `${minutes}m ${seconds}s` : `${minutes}m`;
}

const copiedAll = ref(false);
const rewindUnsupported = computed(
  () =>
    !!props.message.command ||
    !!props.message.attachments?.length ||
    !!props.message.browser_elements?.length
);

async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

async function onBodyClick(e: MouseEvent) {
  const target = e.target as HTMLElement;
  const anchor = target.closest("a[href]") as HTMLAnchorElement | null;
  const href = anchor?.getAttribute("href") ?? "";
  if (/^https?:\/\//i.test(href)) {
    e.preventDefault();
    emit("open-link", href);
    return;
  }
  const btn = target.closest(".code-block-copy") as HTMLButtonElement | null;
  if (!btn) return;
  const block = btn.closest(".code-block");
  const code = block?.querySelector("pre code");
  const text = code?.textContent ?? "";
  if (await copyText(text)) {
    btn.textContent = "已复制";
    btn.classList.add("is-copied");
    window.setTimeout(() => {
      btn.textContent = "复制";
      btn.classList.remove("is-copied");
    }, 1500);
  }
}

async function copyAll() {
  if (await copyText(copyContent.value)) {
    copiedAll.value = true;
    window.setTimeout(() => (copiedAll.value = false), 1500);
  }
}

function forkAtMessage() {
  if (!props.message.event_seq || !props.editable) return;
  emit("fork", props.message.event_seq);
}

function rewindMessage() {
  if (!props.message.event_seq || !props.editable || rewindUnsupported.value) return;
  emit("rewind", props.message.event_seq);
}
</script>

<template>
  <div :class="cn('group', isUser ? 'flex flex-col items-end gap-1' : 'flex flex-col gap-1.5')">
    <template v-if="!isUser">
      <div
        :class="cn(
          'flex items-center gap-2',
          streaming && 'animate-header-shimmer'
        )"
      >
        <div
          class="flex size-7 shrink-0 items-center justify-center rounded-full bg-muted text-foreground/70 select-none"
        >
          <BotIcon class="size-4" />
        </div>
        <span class="text-xs font-medium text-muted-foreground">Foya</span>
      </div>
    </template>

    <div
      :class="cn(
        'min-w-0',
        isUser
          ? 'max-w-[80%] rounded-2xl rounded-tr-md bg-secondary px-4 py-2.5 text-sm leading-relaxed'
          : 'w-full'
      )"
    >
      <!-- 用户消息:纯文本 -->
      <template v-if="isUser">
        <div
          v-if="message.attachments?.length"
          class="mb-2 grid grid-cols-2 gap-2"
        >
          <div
            v-for="attachment in message.attachments"
            :key="attachment.id"
            class="overflow-hidden rounded-md border border-border bg-muted"
          >
            <img
              v-if="attachmentURLs[attachment.id]"
              :src="attachmentURLs[attachment.id]"
              :alt="attachment.name"
              class="max-h-64 w-full object-contain"
            />
            <p v-else class="px-3 py-2 text-xs text-muted-foreground">
              {{ attachment.name }}
            </p>
          </div>
        </div>
        <div
          :class="
            cn(
              'flex items-start gap-1.5',
              (message.command || message.browser_elements?.length) && 'min-w-0'
            )
          "
        >
          <span
            v-if="message.command"
            class="mt-0.5 inline-flex shrink-0 items-center gap-1 rounded-md border border-border bg-background/70 px-1.5 py-0.5 text-xs font-medium text-foreground"
          >
            <component :is="commandIcon" class="size-3.5" />
            {{ message.command[0].toUpperCase() + message.command.slice(1) }}
          </span>
          <span
            v-for="element in message.browser_elements"
            :key="`${element.page_url}:${element.selector}`"
            class="mt-0.5 inline-flex shrink-0 items-center gap-1 rounded-md border border-border bg-background/70 px-1.5 py-0.5 text-xs font-medium text-foreground"
            :title="`${element.page_title || element.page_url}\n${element.selector}`"
          >
            <MousePointer2Icon class="size-3.5 shrink-0 text-blue-600 dark:text-blue-400" />
            <span class="font-mono">{{ element.tag.toLowerCase() }}</span>
          </span>
          <div
            :class="
              cn(
                'min-w-0',
                (message.command || message.browser_elements?.length) && 'flex-1'
              )
            "
          >
            <div class="whitespace-pre-wrap break-words">
              {{ message.content }}
            </div>
          </div>
        </div>
      </template>

      <!-- pending:首 token 到达前的 typing 指示器 -->
      <div v-else-if="isPending" class="flex items-center gap-1 py-1">
        <span class="typing-dot" />
        <span class="typing-dot" />
        <span class="typing-dot" />
      </div>

      <!-- 错误气泡:直接渲染 content -->
      <MarkdownContent
        v-else-if="isError"
        :source="message.content"
        :render-mermaid="!streaming"
        class="prose-chat relative text-destructive"
        @click="onBodyClick"
      />

      <!-- 助手消息:按段有序渲染「思考→工具→思考→回复」 -->
      <template v-else>
        <div v-if="isCompletedTask" class="mb-3 border-b border-border pb-2">
          <button
            type="button"
            class="flex items-center gap-1 rounded-lg px-1 py-1 text-left text-xs text-muted-foreground transition-colors hover:text-foreground"
            :aria-expanded="processExpanded"
            @click="processExpanded = !processExpanded"
          >
            <span>任务耗时 {{ formatDuration(taskDurationMS ?? 0) }}</span>
            <ChevronRightIcon
              :class="cn('size-3.5 shrink-0 transition-transform', processExpanded && 'rotate-90')"
            />
          </button>
        </div>

        <template v-for="(seg, i) in segments" :key="i">
          <template v-if="shouldRenderSegment(i)">
            <!-- 思考段(可折叠) -->
            <div v-if="seg.kind === 'reasoning'" class="mb-2">
              <button
                type="button"
                class="flex w-full items-center gap-1.5 rounded-lg px-1 py-1 text-left text-xs text-muted-foreground transition-colors hover:text-foreground"
                @click="toggleReasoning(i)"
              >
                <ChevronRightIcon
                  :class="cn('size-3.5 shrink-0 transition-transform', isReasoningExpanded(i) && 'rotate-90')"
                />
                <BrainIcon :class="cn('size-3.5 shrink-0', isReasoningStreaming(i) && 'animate-pulse')" />
                <span>{{ isReasoningStreaming(i) ? "正在思考…" : "已深度思考" }}</span>
              </button>
              <div
                v-if="isReasoningExpanded(i)"
                class="mt-1 whitespace-pre-wrap break-words border-l-2 border-border pl-3 text-xs leading-relaxed text-muted-foreground"
              >{{ seg.text }}</div>
            </div>

            <!-- 同一模型步骤的连续工具段聚合展示。 -->
            <div
              v-else-if="seg.kind === 'tool' && isToolBatchStart(i)"
              class="mb-2"
            >
              <ToolActivityGroup
                :session-id="sessionId"
                :tools="toolBatchAt(i)"
                @open-diff="(diff) => emit('open-diff', diff)"
                @cancel-tool="(toolCallId) => emit('cancel-tool', toolCallId)"
                @background-tool="(toolCallId) => emit('background-tool', toolCallId)"
                @terminal-tool="(toolCallId) => emit('terminal-tool', toolCallId)"
              />
            </div>

            <!-- 正文段(markdown) -->
            <MarkdownContent
              v-else-if="seg.kind === 'text'"
              :source="seg.text"
              :render-mermaid="!streaming"
              class="prose-chat relative text-foreground"
              @click="onBodyClick"
            />
          </template>
        </template>

        <!-- 工作中指示:工具已结束、回合仍在流式,模型正在为下一步生成 -->
        <div v-if="showWorking" class="flex items-center gap-1 py-1">
          <span class="typing-dot" />
          <span class="typing-dot" />
          <span class="typing-dot" />
        </div>

        <!-- 流式光标:仍在流式且最后一段是正文时显示 -->
        <span
          v-if="streaming && segments.length > 0 && segments[segments.length - 1].kind === 'text'"
          class="ml-0.5 inline-block h-4 w-1.5 translate-y-0.5 animate-pulse bg-current align-baseline"
        />

        <TaskArtifacts
          v-if="isCompletedTask"
          :tools="toolCalls"
          :project-path="projectPath"
          @open-diff="(diff) => emit('open-diff', diff)"
        />

        <div
          v-if="isCancelledTurn"
          class="mt-2 flex items-center gap-1.5 text-xs text-muted-foreground"
        >
          <CircleStopIcon class="size-3.5" />
          <span>{{ cancelledLabel }}</span>
        </div>
      </template>

      <div
        v-if="!isUser && !streaming && !isError && (message.content || message.event_seq)"
        class="mt-1.5 flex items-center gap-1 opacity-0 transition-opacity group-hover:opacity-100"
      >
        <button
          v-if="message.event_seq"
          type="button"
          class="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-35"
          :disabled="!editable"
          title="从此处复制会话"
          aria-label="从此处复制会话"
          @click="forkAtMessage"
        >
          <GitForkIcon class="size-3.5" />
        </button>
        <button
          v-if="message.content"
          type="button"
          class="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          :title="copiedAll ? '已复制' : '复制回复'"
          :aria-label="copiedAll ? '已复制' : '复制回复'"
          @click="copyAll"
        >
          <CheckIcon v-if="copiedAll" class="size-3.5" />
          <CopyIcon v-else class="size-3.5" />
        </button>
      </div>
    </div>

    <div
      v-if="isUser"
      class="flex items-center gap-0.5 opacity-100 transition-opacity sm:opacity-0 sm:group-hover:opacity-100 sm:group-focus-within:opacity-100"
    >
      <button
        type="button"
        class="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        :title="copiedAll ? '已复制' : '复制消息'"
        @click="copyAll"
      >
        <CheckIcon v-if="copiedAll" class="size-3.5" />
        <CopyIcon v-else class="size-3.5" />
      </button>
      <button
        v-if="message.event_seq"
        type="button"
        class="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-35"
        :disabled="!editable || rewindUnsupported"
        :title="
          rewindUnsupported
            ? '含附件、浏览器上下文或命令的消息暂不支持回退'
            : editable
              ? '回退到输入框'
              : '会话运行时不可回退'
        "
        :aria-label="
          rewindUnsupported
            ? '含附件、浏览器上下文或命令的消息暂不支持回退'
            : '回退到输入框'
        "
        @click="rewindMessage"
      >
        <Undo2Icon class="size-3.5" />
      </button>
    </div>
  </div>
</template>
