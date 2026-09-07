<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import {
  ArrowDownToLineIcon,
  BotIcon,
  ChevronRightIcon,
  FilePenLineIcon,
  FilePlusIcon,
  FileTextIcon,
  Trash2Icon,
  GlobeIcon,
  ListChecksIcon,
  LoaderCircleIcon,
  MessageSquareTextIcon,
  SearchIcon,
  SquareIcon,
  SquareTerminalIcon,
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
const { t } = useI18n();

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
  (e: "cancel-tool", toolCallId: string): void;
  (e: "background-tool", toolCallId: string): void;
  (e: "terminal-tool", toolCallId: string): void;
}>();

interface ToolMeta {
  icon: LucideIcon;
  queued: string;
  running: string;
  done: string;
}

const TOOL_META: Record<string, ToolMeta> = {
  bash: { icon: TerminalIcon, queued: "Waiting to run command", running: "Running command", done: "Ran command" },
  read: { icon: FileTextIcon, queued: "Waiting to read file", running: "Reading file", done: "Read file" },
  write: { icon: FilePlusIcon, queued: "Waiting to write file", running: "Writing file", done: "Wrote file" },
  edit: { icon: FilePenLineIcon, queued: "Waiting to edit file", running: "Editing file", done: "Edited file" },
  delete: { icon: Trash2Icon, queued: "Waiting to move item to Trash", running: "Moving item to Trash", done: "Moved item to Trash" },
  search: { icon: SearchIcon, queued: "Waiting to search", running: "Searching", done: "Searched" },
  web_search: { icon: GlobeIcon, queued: "Waiting to search the web", running: "Searching the web", done: "Searched the web" },
  web_fetch: { icon: GlobeIcon, queued: "Waiting to read web page", running: "Reading web page", done: "Read web page" },
  ask_user: { icon: MessageSquareTextIcon, queued: "Waiting for your answer", running: "Waiting for your answer", done: "Received answer" },
  read_tasks: { icon: ListChecksIcon, queued: "Waiting to read tasks", running: "Reading tasks", done: "Read tasks" },
  update_tasks: { icon: ListChecksIcon, queued: "Waiting to update tasks", running: "Updating tasks", done: "Updated tasks" },
  agent: { icon: BotIcon, queued: "Sub-agent queued", running: "Sub-agent running", done: "Sub-agent completed" },
  spawn_agent: { icon: BotIcon, queued: "Waiting to dispatch sub-agent", running: "Dispatching sub-agent", done: "Dispatched sub-agent" },
  wait_agents: { icon: BotIcon, queued: "Waiting for sub-agent", running: "Waiting for sub-agent", done: "Sub-agent returned" },
  read_agent_output: { icon: BotIcon, queued: "Waiting to read sub-agent", running: "Reading sub-agent", done: "Read sub-agent" },
  cancel_agent: { icon: BotIcon, queued: "Waiting to cancel sub-agent", running: "Cancelling sub-agent", done: "Cancelled sub-agent" },
  list_agents: { icon: BotIcon, queued: "Waiting to inspect subtasks", running: "Inspecting subtasks", done: "Inspected subtasks" },
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
  let filesChanged = 0;
  let filesRead = 0;
  let commands = 0;
  let webSearches = 0;
  let webPages = 0;
  let agents = 0;
  let others = 0;

  for (const tool of tools) {
    if (tool.name === "write" || tool.name === "edit" || tool.name === "delete") filesChanged++;
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
  if (filesChanged > 0) parts.push(t("Changed {count} files", { count: filesChanged }));
  if (filesRead > 0) parts.push(t("Read {count} files", { count: filesRead }));
  if (commands > 0) parts.push(t("Ran {count} commands", { count: commands }));
  if (webSearches > 0) parts.push(t("Searched the web {count} times", { count: webSearches }));
  if (webPages > 0) parts.push(t("Read {count} web pages", { count: webPages }));
  if (agents > 0) parts.push(t("Dispatched {count} sub-agents", { count: agents }));
  if (others > 0) parts.push(t("Called {count} tools", { count: others }));
  return parts.join(t(", "));
}

const batchTitle = computed(() => {
  const summary = actionSummary(props.tools);
  if (runningCount.value > 0 || queuedCount.value > 0) {
    const states: string[] = [];
    if (runningCount.value > 0) {
      states.push(t("Running item count", { count: runningCount.value }));
    }
    if (queuedCount.value > 0) {
      states.push(t("Queued item count", { count: queuedCount.value }));
    }
    return `${states.join(t(", "))} · ${summary}`;
  }
  if (errorCount.value > 0) {
    return t("Completed: {summary} ({count} failed)", {
      summary,
      count: errorCount.value,
    });
  }
  return t("Completed: {summary}", { summary });
});

const expandedTools = ref<Set<string>>(new Set());
const collapsedAgentTools = ref<Set<string>>(new Set());

function toolMeta(name: string): ToolMeta {
  return TOOL_META[name] ?? {
    icon: WrenchIcon,
    queued: "Waiting to call {name}",
    running: "Calling {name}",
    done: "Called {name}",
  };
}

function toolIcon(tool: ToolCallView): LucideIcon {
  return toolMeta(tool.name).icon;
}

function isAgentTool(tool: ToolCallView): boolean {
  return tool.name === "agent" || tool.name === "spawn_agent";
}

function isFileChangeTool(tool: ToolCallView): boolean {
  return tool.name === "write" || tool.name === "edit" || tool.name === "delete";
}

function toolTargetPath(tool: ToolCallView): string {
  for (const raw of [tool.input, tool.output]) {
    if (!raw) continue;
    try {
      const value = JSON.parse(raw) as { path?: unknown };
      if (typeof value.path === "string" && value.path.trim()) return value.path.trim();
    } catch {
      // Non-JSON output is not a structured file result.
    }
  }
  return "";
}

function toolTargetKind(tool: ToolCallView): string {
  if (!tool.output) return "";
  try {
    const value = JSON.parse(tool.output) as { kind?: unknown };
    return typeof value.kind === "string" ? value.kind : "";
  } catch {
    return "";
  }
}

function toolTargetName(tool: ToolCallView): string {
  const path = toolTargetPath(tool).replace(/[\\/]+$/, "");
  return path.split(/[\\/]/).pop() || path;
}

function showRawDetails(tool: ToolCallView): boolean {
  return !isFileChangeTool(tool) || tool.status === "error";
}

function toolLabel(tool: ToolCallView): string {
  const meta = toolMeta(tool.name);
  const agentSuffix = isAgentTool(tool) && tool.agent_name ? ` · ${tool.agent_name}` : "";
  const values = { name: tool.name };
  if (tool.status === "queued") return t(meta.queued, values) + agentSuffix;
  if (tool.status === "running") return t(meta.running, values) + agentSuffix;
  if (tool.status === "error") {
    return t("{action} (failed)", { action: t(meta.done, values) + agentSuffix });
  }
  if (tool.name === "bash" && tool.output?.includes('"running_in_background"')) {
    return t("Command moved to background");
  }
  if (tool.name === "delete") {
    return toolTargetKind(tool) === "directory"
      ? t("Deleted {count} folders", { count: 1 })
      : t("Deleted {count} files", { count: 1 });
  }
  if (tool.diff && tool.name !== "delete") {
    return t("Edited {count} files", { count: 1 });
  }
  return t(meta.done, values) + agentSuffix;
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
        <div class="flex min-w-0 items-center">
          <button
            type="button"
            class="flex min-w-0 flex-1 items-center gap-1.5 rounded-lg px-1 py-1 text-left text-xs text-muted-foreground transition-colors hover:text-foreground"
            :title="toolLabel(tool)"
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
              :aria-label="$t('Running')"
            />
            <span
              v-else-if="tool.status === 'queued'"
              class="size-1.5 shrink-0 rounded-full bg-muted-foreground/30"
              :aria-label="$t('Waiting')"
            />
          </button>
          <button
            v-if="tool.name === 'bash' && tool.status === 'running'"
            type="button"
            class="flex h-6 shrink-0 items-center gap-1 rounded px-1.5 text-[11px] text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            :title="$t('Run and view in terminal')"
            @click="emit('terminal-tool', tool.id)"
          >
            <SquareTerminalIcon class="size-3.5" />
            <span>{{ $t("Terminal") }}</span>
          </button>
          <button
            v-if="tool.name === 'bash' && tool.status === 'running'"
            type="button"
            class="flex h-6 shrink-0 items-center gap-1 rounded px-1.5 text-[11px] text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            :title="$t('Move to background')"
            @click="emit('background-tool', tool.id)"
          >
            <ArrowDownToLineIcon class="size-3.5" />
            <span>{{ $t("Background") }}</span>
          </button>
          <button
            v-if="tool.name === 'bash' && tool.status === 'running'"
            type="button"
            class="flex h-6 shrink-0 items-center gap-1 rounded px-1.5 text-[11px] text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
            :title="$t('Stop this command only')"
            @click="emit('cancel-tool', tool.id)"
          >
            <SquareIcon class="size-3 fill-current" />
            <span>{{ $t("Stop") }}</span>
          </button>
        </div>

          <div
            v-if="isToolExpanded(tool)"
            class="ml-2 mt-0.5 border-l-2 border-border pl-3"
          >
            <SubAgentActivity
              v-if="isAgentTool(tool) && tool.agent_run"
              :run="tool.agent_run"
              :messages="tool.child_messages"
            />
            <div v-if="showRawDetails(tool) && tool.input" class="mb-2">
              <div class="mb-1 text-[10px] uppercase text-muted-foreground">{{ $t("Arguments") }}</div>
              <pre class="overflow-x-auto whitespace-pre-wrap break-all font-mono text-[11px] text-foreground/70">{{ formatInput(tool.input) }}</pre>
            </div>
            <div v-if="showRawDetails(tool) && tool.output">
              <div class="mb-1 text-[10px] uppercase text-muted-foreground">{{ $t("Output") }}</div>
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
            <div
              v-if="tool.name === 'delete' && !tool.diff && toolTargetPath(tool)"
              class="mt-1 flex h-8 w-full items-center gap-2 px-1 text-xs text-muted-foreground"
              :title="toolTargetPath(tool)"
            >
              <Trash2Icon class="size-3.5 shrink-0 text-primary" />
              <span class="min-w-0 flex-1 truncate font-mono">
                {{ toolTargetName(tool) }}
              </span>
              <span class="shrink-0">{{ $t("Trash") }}</span>
            </div>
            <button
              v-if="isFileChangeTool(tool) && tool.diff"
              type="button"
              class="mt-1 flex h-8 w-full items-center gap-2 px-1 text-left text-xs text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
              :title="$t('View complete changes for {name}', { name: diffFileName(tool.diff) })"
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
