<script setup lang="ts">
import { ref, nextTick, watch, onMounted, onBeforeUnmount, computed } from "vue";
import { BotIcon, ArrowDownIcon, RefreshCwIcon } from "@lucide/vue";
import MessageBubble from "./MessageBubble.vue";
import type { ChatMessage } from "@/lib/api";

const props = defineProps<{
  sessionId: string;
  projectPath?: string;
  messages: ChatMessage[];
  streaming: boolean;
  compacting?: boolean;
  editable?: boolean;
  activeTurn?: number;
}>();

const emit = defineEmits<{
  "update:activeTurn": [value: number];
  "rewind-message": [messageSeq: number];
  "fork-message": [messageSeq: number];
  "open-diff": [diff: string];
  "cancel-tool": [toolCallId: string];
  "background-tool": [toolCallId: string];
  "terminal-tool": [toolCallId: string];
  "open-link": [url: string];
}>();

const scrollEl = ref<HTMLElement | null>(null);
const stickToBottom = ref(true);
const itemRefs = ref<HTMLElement[]>([]);

function isNearBottom(el: HTMLElement, threshold = 80) {
  return el.scrollHeight - el.scrollTop - el.clientHeight <= threshold;
}

function onScroll() {
  const el = scrollEl.value;
  if (!el) return;
  stickToBottom.value = isNearBottom(el);
  computeActiveTurn();
}

// Each user message starts a turn and maps to one timeline marker.
const turnIndices = computed(() => {
  const idxs: number[] = [];
  props.messages.forEach((m, i) => {
    if (m.role === "user") idxs.push(i);
  });
  return idxs;
});

function setItemRef(el: HTMLElement | null, i: number) {
  if (el) itemRefs.value[i] = el;
}

// Resolve the visible turn from the last user message above the viewport anchor.
function computeActiveTurn() {
  const el = scrollEl.value;
  if (!el || turnIndices.value.length === 0) return;
  if (isNearBottom(el, 120)) {
    emit("update:activeTurn", turnIndices.value.length - 1);
    return;
  }
  const anchor = el.getBoundingClientRect().top + el.clientHeight * 0.3;
  let active = 0;
  for (let t = 0; t < turnIndices.value.length; t++) {
    const node = itemRefs.value[turnIndices.value[t]];
    if (node && node.getBoundingClientRect().top <= anchor) active = t;
  }
  emit("update:activeTurn", active);
}

// Scroll to a turn; keep the final turn pinned to the bottom.
function scrollToTurn(turnIndex: number) {
  const el = scrollEl.value;
  const msgIdx = turnIndices.value[turnIndex];
  const node = msgIdx !== undefined ? itemRefs.value[msgIdx] : undefined;
  if (!el || !node) return;
  stickToBottom.value = turnIndex === turnIndices.value.length - 1;
  node.scrollIntoView({ behavior: "smooth", block: "start" });
}

// Restore bottom pinning so later streaming updates continue to follow.
function scrollToBottom() {
  stickToBottom.value = true;
  const el = scrollEl.value;
  if (!el) return;
  el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
}

// Show the jump button after the user scrolls away from the bottom.
const showJumpButton = computed(() => !stickToBottom.value);

defineExpose({ scrollToTurn });

// A session switch always starts at the latest message.
watch(
  () => props.messages,
  async () => {
    stickToBottom.value = true;
    await nextTick();
    const el = scrollEl.value;
    if (el) el.scrollTop = el.scrollHeight;
    computeActiveTurn();
  }
);

// Pin after a user submission or first load, but not for background tool updates.
watch(
  () => props.messages.length,
  async (n, o) => {
    const prev = o ?? 0;
    const firstFill = prev === 0 && n > 0;
    const userJustSent = n > prev && props.messages[prev]?.role === "user";
    if (firstFill || userJustSent) {
      stickToBottom.value = true;
    }
    if (stickToBottom.value) {
      await nextTick();
      const el = scrollEl.value;
      if (el) el.scrollTop = el.scrollHeight;
    }
    computeActiveTurn();
  }
);

watch(
  () =>
    props.messages
      .map((m) => {
        // Track all streamed segment growth while bottom pinning is active.
        const segLen = (m.segments ?? []).reduce(
          (n, s) => n + (s.kind === "tool" ? (s.tool.output?.length ?? 0) : s.text.length),
          0
        );
        return m.content.length + segLen;
      })
      .join(","),
  async () => {
    if (!stickToBottom.value) return;
    await nextTick();
    const el = scrollEl.value;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
    computeActiveTurn();
  }
);

watch(
  () => props.compacting,
  async (compacting) => {
    if (!compacting || !stickToBottom.value) return;
    await nextTick();
    const el = scrollEl.value;
    if (el) el.scrollTop = el.scrollHeight;
  }
);

onMounted(() => {
  scrollEl.value?.addEventListener("scroll", onScroll, { passive: true });
});

onBeforeUnmount(() => {
  scrollEl.value?.removeEventListener("scroll", onScroll);
});
</script>

<template>
  <div class="relative min-h-0 flex-1 overflow-hidden">
    <div ref="scrollEl" class="no-scrollbar size-full overflow-y-auto">
      <div v-if="messages.length === 0" class="flex h-full flex-col items-center justify-center gap-4">
        <div class="flex size-14 items-center justify-center rounded-2xl bg-muted">
          <BotIcon class="size-6 text-muted-foreground/70" />
        </div>
        <div class="text-center">
          <p class="text-base font-medium">{{ $t("How can I help?") }}</p>
          <p class="mt-1 text-sm text-muted-foreground">{{ $t("Enter a message to start a chat") }}</p>
        </div>
      </div>

      <div v-else class="relative mx-auto max-w-3xl space-y-6 px-4 py-6">
        <div
          v-for="(m, i) in messages"
          :key="m.event_seq ?? i"
          :ref="(el) => setItemRef(el as HTMLElement | null, i)"
        >
          <MessageBubble
            :session-id="sessionId"
            :project-path="projectPath"
            :message="m"
            :editable="editable && !streaming && !compacting"
            :streaming="
              streaming && !compacting && m.role === 'assistant' && i === messages.length - 1
            "
            @rewind="(messageSeq) => emit('rewind-message', messageSeq)"
            @fork="(messageSeq) => emit('fork-message', messageSeq)"
            @open-diff="(diff) => emit('open-diff', diff)"
            @cancel-tool="(toolCallId) => emit('cancel-tool', toolCallId)"
            @background-tool="(toolCallId) => emit('background-tool', toolCallId)"
            @terminal-tool="(toolCallId) => emit('terminal-tool', toolCallId)"
            @open-link="(url) => emit('open-link', url)"
          />
        </div>

        <div
          v-if="compacting"
          class="flex h-7 items-center gap-2 text-xs text-muted-foreground"
          role="status"
          aria-live="polite"
        >
          <RefreshCwIcon class="size-3.5 animate-spin" />
          <span>{{ $t("Compressing context") }}</span>
        </div>
      </div>
    </div>

    <!-- The jump button overlays the message viewport. -->
    <Transition
      enter-active-class="transition duration-200 ease-out"
      enter-from-class="translate-y-2 opacity-0"
      enter-to-class="translate-y-0 opacity-100"
      leave-active-class="transition duration-150 ease-in"
      leave-from-class="translate-y-0 opacity-100"
      leave-to-class="translate-y-2 opacity-0"
    >
      <button
        v-if="showJumpButton"
        type="button"
        class="absolute bottom-4 left-1/2 z-10 flex size-9 -translate-x-1/2 items-center justify-center rounded-full border border-border bg-card shadow-md transition-colors hover:bg-muted"
        :class="
          streaming
            ? 'text-primary ring-2 ring-primary/30 hover:bg-primary/10'
            : 'text-muted-foreground'
        "
        :title="streaming ? $t('New content, scroll to bottom') : $t('Scroll to bottom')"
        @click="scrollToBottom"
      >
        <ArrowDownIcon class="size-4" :class="streaming ? 'animate-bounce' : ''" />
      </button>
    </Transition>
  </div>
</template>
