<script setup lang="ts">
import { computed, ref } from "vue";
import {
  BotIcon,
  CopyIcon,
  CheckIcon,
  TerminalIcon,
  ChevronRightIcon,
  BrainIcon,
  FileTextIcon,
  FilePlusIcon,
  FilePenLineIcon,
  SearchIcon,
  GlobeIcon,
  WrenchIcon,
  type LucideIcon,
} from "@lucide/vue";
import { cn } from "@/lib/utils";
import { renderMarkdown } from "@/lib/markdown";
import type { ChatMessage, ToolCallView, MessageSegment } from "@/lib/api";

const props = defineProps<{
  message: ChatMessage;
  streaming?: boolean;
}>();

const isUser = computed(() => props.message.role === "user");
const toolCalls = computed(() => props.message.tool_calls ?? []);

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

// 回合中「工作中」空窗:纯派生自 streaming + segments,不依赖任何专用事件。
// 各段各自的进行态已被覆盖(首 token 前=isPending 打字点、思考中=「正在思考…」、
// 工具运行中=图标 pulse、正文流式=光标)。唯一没人管的空白是:某工具已结束、
// 但回合仍在流式(streaming 为真)——此时模型正在为下一步生成内容,填一个指示。
const showWorking = computed(() => {
  if (isUser.value || props.message.error || !props.streaming) return false;
  const last = segments.value[segments.value.length - 1];
  return !!last && last.kind === "tool" && last.tool.status !== "running";
});

// 工具调用展开状态。
const expandedTools = ref<Set<string>>(new Set());
function toggleTool(id: string) {
  if (expandedTools.value.has(id)) expandedTools.value.delete(id);
  else expandedTools.value.add(id);
}
function isToolExpanded(id: string) {
  return expandedTools.value.has(id);
}

// 工具展示描述:按工具类型给图标 + 进行时/完成时的动词,统一收敛为单行紧凑样式。
// 后续新增工具(搜索、网络等)只需在此登记一行。
interface ToolMeta {
  icon: LucideIcon;
  running: string; // 进行中文案,如「正在执行命令」
  done: string; // 完成文案,如「已执行命令」
}

const TOOL_META: Record<string, ToolMeta> = {
  bash: { icon: TerminalIcon, running: "正在执行命令", done: "已执行命令" },
  read: { icon: FileTextIcon, running: "正在读取文件", done: "已读取文件" },
  write: { icon: FilePlusIcon, running: "正在写入文件", done: "已写入文件" },
  edit: { icon: FilePenLineIcon, running: "正在编辑文件", done: "已编辑文件" },
  search: { icon: SearchIcon, running: "正在搜索", done: "已搜索" },
  web_search: { icon: GlobeIcon, running: "正在联网搜索", done: "已联网搜索" },
};

function toolMeta(name: string): ToolMeta {
  return TOOL_META[name] ?? { icon: WrenchIcon, running: `正在调用 ${name}`, done: `已调用 ${name}` };
}

function toolIcon(tc: ToolCallView): LucideIcon {
  return toolMeta(tc.name).icon;
}

// 单行标题文案:运行中/失败/完成三态。
function toolLabel(tc: ToolCallView): string {
  const meta = toolMeta(tc.name);
  if (tc.status === "running") return meta.running;
  if (tc.status === "error") return `${meta.done}（失败）`;
  return meta.done;
}

// 格式化工具输入用于展示(尝试 pretty-print JSON)。
function formatInput(input: string): string {
  try {
    return JSON.stringify(JSON.parse(input), null, 2);
  } catch {
    return input;
  }
}

// 渲染单个文本段的 markdown。
function renderSegment(text: string): string {
  return renderMarkdown(text);
}

const bodyEl = ref<HTMLElement | null>(null);
const copiedAll = ref(false);

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
  if (await copyText(props.message.content)) {
    copiedAll.value = true;
    window.setTimeout(() => (copiedAll.value = false), 1500);
  }
}
</script>

<template>
  <div :class="cn('group', isUser ? 'flex flex-row-reverse gap-3' : 'flex flex-col gap-1.5')">
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
      <div v-if="isUser" class="whitespace-pre-wrap break-words">
        {{ message.content }}
      </div>

      <!-- pending:首 token 到达前的 typing 指示器 -->
      <div v-else-if="isPending" class="flex items-center gap-1 py-1">
        <span class="typing-dot" />
        <span class="typing-dot" />
        <span class="typing-dot" />
      </div>

      <!-- 错误气泡:直接渲染 content -->
      <div
        v-else-if="isError"
        ref="bodyEl"
        class="prose-chat relative text-destructive"
        v-html="renderSegment(message.content)"
        @click="onBodyClick"
      />

      <!-- 助手消息:按段有序渲染「思考→工具→思考→回复」 -->
      <template v-else>
        <template v-for="(seg, i) in segments" :key="i">
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

          <!-- 工具段:单行紧凑样式(图标 + 状态文案),可展开查看参数/输出 -->
          <div v-else-if="seg.kind === 'tool'" class="mb-2">
            <button
              type="button"
              class="flex w-full items-center gap-1.5 rounded-lg px-1 py-1 text-left text-xs text-muted-foreground transition-colors hover:text-foreground"
              @click="toggleTool(seg.tool.id)"
            >
              <ChevronRightIcon
                :class="cn('size-3.5 shrink-0 transition-transform', isToolExpanded(seg.tool.id) && 'rotate-90')"
              />
              <component
                :is="toolIcon(seg.tool)"
                :class="cn('size-3.5 shrink-0', seg.tool.status === 'running' && 'animate-pulse')"
              />
              <span>{{ toolLabel(seg.tool) }}</span>
            </button>
            <div
              v-if="isToolExpanded(seg.tool.id)"
              class="mt-1 border-l-2 border-border pl-3"
            >
              <div v-if="seg.tool.input" class="mb-2">
                <div class="mb-1 text-[10px] uppercase tracking-wide text-muted-foreground">参数</div>
                <pre class="overflow-x-auto whitespace-pre-wrap break-all font-mono text-[11px] text-foreground/70">{{ formatInput(seg.tool.input) }}</pre>
              </div>
              <div v-if="seg.tool.output">
                <div class="mb-1 text-[10px] uppercase tracking-wide text-muted-foreground">输出</div>
                <pre class="max-h-64 overflow-auto whitespace-pre-wrap break-all font-mono text-[11px] text-foreground/70">{{ seg.tool.output }}</pre>
              </div>
            </div>
          </div>

          <!-- 正文段(markdown) -->
          <div
            v-else-if="seg.kind === 'text'"
            class="prose-chat relative text-foreground"
            v-html="renderSegment(seg.text)"
            @click="onBodyClick"
          />
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
      </template>

      <div
        v-if="!isUser && !streaming && message.content && !isError"
        class="mt-1.5 flex items-center gap-1 opacity-0 transition-opacity group-hover:opacity-100"
      >
        <button
          type="button"
          class="flex items-center gap-1 rounded-md px-1.5 py-1 text-[11px] text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          :title="copiedAll ? '已复制' : '复制回复'"
          @click="copyAll"
        >
          <CheckIcon v-if="copiedAll" class="size-3" />
          <CopyIcon v-else class="size-3" />
          {{ copiedAll ? "已复制" : "复制" }}
        </button>
      </div>
    </div>
  </div>
</template>
