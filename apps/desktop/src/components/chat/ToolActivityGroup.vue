<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import {
  BotIcon,
  ChevronRightIcon,
  FilePenLineIcon,
  FilePlusIcon,
  FileTextIcon,
  GlobeIcon,
  LoaderCircleIcon,
  SearchIcon,
  TerminalIcon,
  WrenchIcon,
  type LucideIcon,
} from "@lucide/vue";
import { cn } from "@/lib/utils";
import { diffFileName, diffStats } from "@/lib/diff";
import { api } from "@/lib/api";
import type { ToolCallView } from "@/lib/api";
import SubAgentActivity from "./SubAgentActivity.vue";

const props = defineProps<{
  sessionId: string;
  tools: ToolCallView[];
}>();

const attachmentURLs = ref<Record<string, string>>({});
const attachmentKey = computed(() =>
  props.tools.flatMap((tool) => tool.attachments ?? []).map((item) => item.id).join(",")
);
let attachmentLoad = 0;

function releaseAttachmentURLs() {
  for (const url of Object.values(attachmentURLs.value)) URL.revokeObjectURL(url);
  attachmentURLs.value = {};
}

async function loadAttachmentPreviews() {
  const load = ++attachmentLoad;
  releaseAttachmentURLs();
  if (!props.sessionId) return;
  const attachments = props.tools.flatMap((tool) => tool.attachments ?? []);
  for (const attachment of attachments) {
    if (attachment.kind !== "image" || attachmentURLs.value[attachment.id]) continue;
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
      // The tool output remains readable when an artifact cannot be loaded.
    }
  }
}

watch(
  () => [props.sessionId, attachmentKey.value],
  () => void loadAttachmentPreviews(),
  { immediate: true }
);
onBeforeUnmount(() => {
  attachmentLoad++;
  releaseAttachmentURLs();
});

const emit = defineEmits<{
  (e: "open-diff", diff: string): void;
}>();

interface ToolMeta {
  icon: LucideIcon;
  queued: string;
  running: string;
  done: string;
}

const TOOL_META: Record<string, ToolMeta> = {
  bash: { icon: TerminalIcon, queued: "等待执行命令", running: "正在执行命令", done: "已执行命令" },
  read: { icon: FileTextIcon, queued: "等待读取文件", running: "正在读取文件", done: "已读取文件" },
  write: { icon: FilePlusIcon, queued: "等待写入文件", running: "正在写入文件", done: "已写入文件" },
  edit: { icon: FilePenLineIcon, queued: "等待编辑文件", running: "正在编辑文件", done: "已编辑文件" },
  search: { icon: SearchIcon, queued: "等待搜索", running: "正在搜索", done: "已搜索" },
  web_search: { icon: GlobeIcon, queued: "等待联网搜索", running: "正在联网搜索", done: "已联网搜索" },
  web_fetch: { icon: GlobeIcon, queued: "等待读取网页", running: "正在读取网页", done: "已读取网页" },
  agent: { icon: BotIcon, queued: "子 Agent 等待执行", running: "子 Agent 正在执行", done: "子 Agent 已完成" },
  spawn_agent: { icon: BotIcon, queued: "等待派发子 Agent", running: "正在派发子 Agent", done: "已派发子 Agent" },
  wait_agents: { icon: BotIcon, queued: "等待子 Agent", running: "正在等待子 Agent", done: "子 Agent 已返回" },
  read_agent_output: { icon: BotIcon, queued: "等待读取子 Agent", running: "正在读取子 Agent", done: "已读取子 Agent" },
  cancel_agent: { icon: BotIcon, queued: "等待取消子 Agent", running: "正在取消子 Agent", done: "已取消子 Agent" },
  list_agents: { icon: BotIcon, queued: "等待检查子任务", running: "正在检查子任务", done: "已检查子任务" },
};

const isBatch = computed(() => props.tools.length > 1);
const batchExpanded = ref(true);
const runningCount = computed(
  () => props.tools.filter((tool) => tool.status === "running").length
);
const queuedCount = computed(
  () => props.tools.filter((tool) => tool.status === "queued").length
);
const errorCount = computed(
  () => props.tools.filter((tool) => tool.status === "error").length
);

function actionSummary(tools: ToolCallView[]): string {
  let filesEdited = 0;
  let filesRead = 0;
  let commands = 0;
  let webSearches = 0;
  let webPages = 0;
  let agents = 0;
  let others = 0;

  for (const tool of tools) {
    if (tool.name === "write" || tool.name === "edit") filesEdited++;
    else if (tool.name === "read") filesRead++;
    else if (tool.name === "bash") commands++;
    else if (tool.name === "web_search") webSearches++;
    else if (tool.name === "web_fetch") webPages++;
    else if (
      tool.name === "agent" ||
      tool.name === "spawn_agent" ||
      tool.name === "wait_agents" ||
      tool.name === "read_agent_output" ||
      tool.name === "cancel_agent" ||
      tool.name === "list_agents"
    ) agents++;
    else others++;
  }

  const parts: string[] = [];
  if (filesEdited > 0) parts.push(`编辑 ${filesEdited} 个文件`);
  if (filesRead > 0) parts.push(`读取 ${filesRead} 个文件`);
  if (commands > 0) parts.push(`执行 ${commands} 条命令`);
  if (webSearches > 0) parts.push(`搜索 ${webSearches} 次`);
  if (webPages > 0) parts.push(`读取 ${webPages} 个网页`);
  if (agents > 0) parts.push(`调度 ${agents} 个子 Agent`);
  if (others > 0) parts.push(`调用 ${others} 个工具`);
  return parts.join("、");
}

const batchTitle = computed(() => {
  const summary = actionSummary(props.tools);
  if (runningCount.value > 0 || queuedCount.value > 0) {
    const states: string[] = [];
    if (runningCount.value > 0) states.push(`${runningCount.value} 项执行中`);
    if (queuedCount.value > 0) states.push(`${queuedCount.value} 项等待`);
    return `${states.join("，")} · ${summary}`;
  }
  if (errorCount.value > 0) return `已${summary}（${errorCount.value} 项失败）`;
  return `已${summary}`;
});

const expandedTools = ref<Set<string>>(new Set());
const collapsedAgentTools = ref<Set<string>>(new Set());

function toolMeta(name: string): ToolMeta {
  return TOOL_META[name] ?? {
    icon: WrenchIcon,
    queued: `等待调用 ${name}`,
    running: `正在调用 ${name}`,
    done: `已调用 ${name}`,
  };
}

function toolIcon(tool: ToolCallView): LucideIcon {
  return toolMeta(tool.name).icon;
}

function isAgentTool(tool: ToolCallView): boolean {
  return tool.name === "agent" || tool.name === "spawn_agent";
}

function isFileChangeTool(tool: ToolCallView): boolean {
  return tool.name === "write" || tool.name === "edit";
}

function toolLabel(tool: ToolCallView): string {
  const meta = toolMeta(tool.name);
  const agentSuffix = isAgentTool(tool) && tool.agent_name ? ` · ${tool.agent_name}` : "";
  if (tool.status === "queued") return meta.queued + agentSuffix;
  if (tool.status === "running") return meta.running + agentSuffix;
  if (tool.status === "error") return `${meta.done}${agentSuffix}（失败）`;
  if (tool.diff) return "已编辑 1 个文件";
  return meta.done + agentSuffix;
}

function toggleTool(tool: ToolCallView) {
  if (tool.agent_run) {
    if (collapsedAgentTools.value.has(tool.id)) collapsedAgentTools.value.delete(tool.id);
    else collapsedAgentTools.value.add(tool.id);
    return;
  }
  if (expandedTools.value.has(tool.id)) expandedTools.value.delete(tool.id);
  else expandedTools.value.add(tool.id);
}

function isToolExpanded(tool: ToolCallView): boolean {
  if (tool.agent_run) return !collapsedAgentTools.value.has(tool.id);
  return expandedTools.value.has(tool.id);
}

function formatInput(input: string): string {
  try {
    return JSON.stringify(JSON.parse(input), null, 2);
  } catch {
    return input;
  }
}
</script>

<template>
  <section class="min-w-0" :aria-label="isBatch ? batchTitle : undefined">
    <button
      v-if="isBatch"
      type="button"
      class="flex w-full items-center gap-1.5 rounded-lg px-1 py-1 text-left text-xs text-muted-foreground transition-colors hover:text-foreground"
      :aria-expanded="batchExpanded"
      @click="batchExpanded = !batchExpanded"
    >
      <ChevronRightIcon
        :class="cn('size-3.5 shrink-0 transition-transform', batchExpanded && 'rotate-90')"
      />
      <LoaderCircleIcon
        v-if="runningCount > 0"
        class="size-3.5 shrink-0 animate-spin text-primary"
      />
      <span>{{ batchTitle }}</span>
    </button>

    <div
      v-if="!isBatch || batchExpanded"
      :class="cn(isBatch && 'mt-1 border-l-2 border-border pl-3')"
    >
      <div v-for="tool in tools" :key="tool.id" class="min-w-0">
          <button
            type="button"
            class="flex w-full items-center gap-1.5 rounded-lg px-1 py-1 text-left text-xs text-muted-foreground transition-colors hover:text-foreground"
            @click="toggleTool(tool)"
          >
            <ChevronRightIcon
              :class="cn('size-3.5 shrink-0 transition-transform', isToolExpanded(tool) && 'rotate-90')"
            />
            <component
              :is="toolIcon(tool)"
              :class="cn('size-3.5 shrink-0', tool.status === 'running' && 'animate-pulse text-primary')"
            />
            <span class="min-w-0 flex-1 truncate">{{ toolLabel(tool) }}</span>
            <LoaderCircleIcon
              v-if="tool.status === 'running'"
              class="size-3.5 shrink-0 animate-spin text-primary"
              aria-label="执行中"
            />
            <span
              v-else-if="tool.status === 'queued'"
              class="size-1.5 shrink-0 rounded-full bg-muted-foreground/30"
              aria-label="等待执行"
            />
          </button>

          <div
            v-if="isToolExpanded(tool)"
            class="ml-2 mt-0.5 border-l-2 border-border pl-3"
          >
            <SubAgentActivity
              v-if="isAgentTool(tool) && tool.agent_run"
              :run="tool.agent_run"
              :messages="tool.child_messages"
            />
            <div v-if="tool.input" class="mb-2">
              <div class="mb-1 text-[10px] uppercase text-muted-foreground">参数</div>
              <pre class="overflow-x-auto whitespace-pre-wrap break-all font-mono text-[11px] text-foreground/70">{{ formatInput(tool.input) }}</pre>
            </div>
            <div v-if="tool.output">
              <div class="mb-1 text-[10px] uppercase text-muted-foreground">输出</div>
              <pre class="max-h-64 overflow-auto whitespace-pre-wrap break-all font-mono text-[11px] text-foreground/70">{{ tool.output }}</pre>
            </div>
            <div
              v-if="tool.attachments?.length"
              class="mt-2 grid grid-cols-2 gap-2"
            >
              <figure
                v-for="attachment in tool.attachments"
                :key="attachment.id"
                class="overflow-hidden rounded-md border border-border bg-muted"
              >
                <img
                  v-if="attachmentURLs[attachment.id]"
                  :src="attachmentURLs[attachment.id]"
                  :alt="attachment.name"
                  class="max-h-64 w-full object-contain"
                />
                <figcaption class="truncate px-2 py-1 text-[10px] text-muted-foreground">
                  {{ attachment.name }}
                </figcaption>
              </figure>
            </div>
            <button
              v-if="isFileChangeTool(tool) && tool.diff"
              type="button"
              class="mt-1 flex h-8 w-full items-center gap-2 px-1 text-left text-xs text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
              :title="`查看 ${diffFileName(tool.diff)} 的完整变更`"
              @click.stop="emit('open-diff', tool.diff)"
            >
              <FileTextIcon class="size-3.5 shrink-0 text-primary" />
              <span class="min-w-0 flex-1 truncate font-mono">
                {{ diffFileName(tool.diff) }}
              </span>
              <span class="shrink-0 font-mono text-emerald-600">
                +{{ diffStats(tool.diff).additions }}
              </span>
              <span class="shrink-0 font-mono text-red-500">
                -{{ diffStats(tool.diff).deletions }}
              </span>
            </button>
          </div>
      </div>
    </div>
  </section>
</template>
