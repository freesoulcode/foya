<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import {
  BotIcon,
  BrainIcon,
  CheckCircle2Icon,
  CircleStopIcon,
  Clock3Icon,
  LoaderCircleIcon,
  WrenchIcon,
  XCircleIcon,
} from "@lucide/vue";
import type { AgentRunSnapshot, ChatMessage } from "@/lib/api";
import MarkdownContent from "./MarkdownContent.vue";

const props = defineProps<{
  run: AgentRunSnapshot;
  messages?: ChatMessage[];
}>();

const now = ref(Date.now());
let timer: number | undefined;

onMounted(() => {
  timer = window.setInterval(() => (now.value = Date.now()), 1000);
});
onBeforeUnmount(() => {
  if (timer !== undefined) window.clearInterval(timer);
});

const statusMeta = computed(() => {
  switch (props.run.status) {
    case "queued":
      return { label: "排队中", icon: Clock3Icon, tone: "text-muted-foreground" };
    case "running":
      return { label: "执行中", icon: LoaderCircleIcon, tone: "text-primary" };
    case "completed":
      return { label: "已完成", icon: CheckCircle2Icon, tone: "text-emerald-600" };
    case "cancelled":
      return { label: "已取消", icon: CircleStopIcon, tone: "text-muted-foreground" };
    case "interrupted":
      return { label: "已中断", icon: CircleStopIcon, tone: "text-amber-600" };
    default:
      return { label: "失败", icon: XCircleIcon, tone: "text-destructive" };
  }
});

const elapsed = computed(() => {
  const start = props.run.started_at ?? props.run.created_at;
  const end = props.run.completed_at
    ? new Date(props.run.completed_at).getTime()
    : now.value;
  const seconds = Math.max(0, Math.floor((end - new Date(start).getTime()) / 1000));
  if (seconds < 60) return `${seconds}s`;
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
});
</script>

<template>
  <div class="space-y-2 py-1 text-xs">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-muted-foreground">
      <span class="flex items-center gap-1.5 font-medium text-foreground">
        <BotIcon class="size-3.5" />
        {{ run.agent_name || run.agent_ref || "worker" }}
      </span>
      <span :class="['flex items-center gap-1', statusMeta.tone]">
        <component
          :is="statusMeta.icon"
          :class="['size-3.5', run.status === 'running' && 'animate-spin']"
        />
        {{ statusMeta.label }}
      </span>
      <span>{{ elapsed }}</span>
      <span v-if="run.tokens_used">{{ run.tokens_used.toLocaleString() }} tokens</span>
    </div>

    <div
      v-for="(message, index) in messages ?? []"
      :key="`${message.event_seq ?? index}-${message.role}`"
      class="border-l border-border pl-3"
    >
      <div
        v-if="message.reasoning"
        class="mb-2 flex items-start gap-1.5 whitespace-pre-wrap text-muted-foreground"
      >
        <BrainIcon class="mt-0.5 size-3.5 shrink-0" />
        <span>{{ message.reasoning }}</span>
      </div>
      <div
        v-for="tool in message.tool_calls ?? []"
        :key="tool.id"
        class="mb-2 text-muted-foreground"
      >
        <div class="flex items-center gap-1.5">
          <WrenchIcon :class="['size-3.5', tool.status === 'running' && 'animate-pulse']" />
          <span>{{ tool.name }}</span>
          <span>{{ tool.status === "running" ? "执行中" : tool.status === "error" ? "失败" : "完成" }}</span>
        </div>
        <pre
          v-if="tool.output"
          class="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-all pl-5 font-mono text-[11px] text-foreground/70"
        >{{ tool.output }}</pre>
      </div>
      <MarkdownContent
        v-if="message.role === 'assistant' && message.content"
        :source="message.content"
        :render-mermaid="run.status !== 'running'"
        class="prose-chat text-foreground/90"
      />
    </div>

    <p v-if="run.error" class="text-destructive">{{ run.error }}</p>
  </div>
</template>
