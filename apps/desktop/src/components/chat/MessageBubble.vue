<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
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
  PackageIcon,
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
const { t } = useI18n();

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

// Prefer ordered segments; synthesize a compatible sequence for legacy data.
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

// A reasoning segment streams only when it is the last segment in a streaming message.
function isReasoningStreaming(index: number): boolean {
  return !!props.streaming && index === segments.value.length - 1;
}

// Track expanded reasoning segments by index.
const expandedReasoning = ref<Set<number>>(new Set());
function toggleReasoning(index: number) {
  if (expandedReasoning.value.has(index)) expandedReasoning.value.delete(index);
  else expandedReasoning.value.add(index);
}
function isReasoningExpanded(index: number) {
  return expandedReasoning.value.has(index);
}

// A pending bubble exists before the first streamed token arrives.
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
      return t("Output stopped by user");
    case "queue_dispatch":
      return t("Switched to the next queued message");
    case "session_deleted":
      return t("Response cancelled when the chat was deleted");
    default:
      return t("Response cancelled");
  }
});

// Cover the gap after a tool finishes while the model prepares its next segment.
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

// In completed tool turns, the final text after the last tool is the response.
// Earlier reasoning, tool calls, and intermediate text form the collapsible process.
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
    !!props.message.skill_ref ||
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
    btn.textContent = t("Copied");
    btn.classList.add("is-copied");
    window.setTimeout(() => {
      btn.textContent = t("Copy");
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
      <!-- User message. -->
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
              (message.command || message.skill_ref || message.browser_elements?.length) && 'min-w-0'
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
            v-if="message.skill_ref"
            class="mt-0.5 inline-flex max-w-48 shrink-0 items-center gap-1 rounded-md border border-border bg-background/70 px-1.5 py-0.5 text-xs font-medium text-foreground"
            :title="message.skill_ref"
          >
            <PackageIcon class="size-3.5 shrink-0" />
            <span class="truncate">
              {{ message.skill_ref.split(':').slice(-1)[0] }}
            </span>
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
                (message.command || message.skill_ref || message.browser_elements?.length) && 'flex-1'
              )
            "
          >
            <div class="whitespace-pre-wrap break-words">
              {{ message.content }}
            </div>
          </div>
        </div>
      </template>

      <!-- Typing indicator before the first token. -->
      <div v-else-if="isPending" class="flex items-center gap-1 py-1">
        <span class="typing-dot" />
        <span class="typing-dot" />
        <span class="typing-dot" />
      </div>

      <!-- Error message. -->
      <MarkdownContent
        v-else-if="isError"
        :source="message.content"
        :render-mermaid="!streaming"
        class="prose-chat relative text-destructive"
        @click="onBodyClick"
      />

      <!-- Assistant message rendered in segment order. -->
      <template v-else>
        <div v-if="isCompletedTask" class="mb-3 border-b border-border pb-2">
          <button
            type="button"
            class="flex items-center gap-1 rounded-lg px-1 py-1 text-left text-xs text-muted-foreground transition-colors hover:text-foreground"
            :aria-expanded="processExpanded"
            @click="processExpanded = !processExpanded"
          >
            <span>{{ $t("Task completed in {duration}", { duration: formatDuration(taskDurationMS ?? 0) }) }}</span>
            <ChevronRightIcon
              :class="cn('size-3.5 shrink-0 transition-transform', processExpanded && 'rotate-90')"
            />
          </button>
        </div>

        <template v-for="(seg, i) in segments" :key="i">
          <template v-if="shouldRenderSegment(i)">
            <!-- Collapsible reasoning segment. -->
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
                <span>{{ isReasoningStreaming(i) ? $t("Thinking") : $t("Thought deeply") }}</span>
              </button>
              <div
                v-if="isReasoningExpanded(i)"
                class="mt-1 whitespace-pre-wrap break-words border-l-2 border-border pl-3 text-xs leading-relaxed text-muted-foreground"
              >{{ seg.text }}</div>
            </div>

            <!-- Consecutive tool segments from one model step. -->
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

            <!-- Markdown text segment. -->
            <MarkdownContent
              v-else-if="seg.kind === 'text'"
              :source="seg.text"
              :render-mermaid="!streaming"
              class="prose-chat relative text-foreground"
              @click="onBodyClick"
            />
          </template>
        </template>

        <!-- Working indicator between streamed segments. -->
        <div v-if="showWorking" class="flex items-center gap-1 py-1">
          <span class="typing-dot" />
          <span class="typing-dot" />
          <span class="typing-dot" />
        </div>

        <!-- Cursor shown while the final text segment is streaming. -->
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
          :title="$t('Fork chat from here')"
          :aria-label="$t('Fork chat from here')"
          @click="forkAtMessage"
        >
          <GitForkIcon class="size-3.5" />
        </button>
        <button
          v-if="message.content"
          type="button"
          class="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          :title="copiedAll ? $t('Copied') : $t('Copy response')"
          :aria-label="copiedAll ? $t('Copied') : $t('Copy response')"
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
        :title="copiedAll ? $t('Copied') : $t('Copy message')"
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
            ? $t('Messages with attachments, browser context, or commands cannot be rewound')
            : editable
              ? $t('Rewind to composer')
              : $t('Cannot rewind while the chat is running')
        "
        :aria-label="
          rewindUnsupported
            ? $t('Messages with attachments, browser context, or commands cannot be rewound')
            : $t('Rewind to composer')
        "
        @click="rewindMessage"
      >
        <Undo2Icon class="size-3.5" />
      </button>
    </div>
  </div>
</template>
