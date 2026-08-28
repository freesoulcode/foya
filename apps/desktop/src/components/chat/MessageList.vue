<script setup lang="ts">
import { ref, nextTick, watch, onMounted, onBeforeUnmount, computed } from "vue";
import { BotIcon, ArrowDownIcon, RefreshCwIcon } from "@lucide/vue";
import MessageBubble from "./MessageBubble.vue";
import type { ChatMessage } from "@/lib/api";

const props = defineProps<{
  messages: ChatMessage[];
  streaming: boolean;
  compacting?: boolean;
  activeTurn?: number;
}>();

const emit = defineEmits<{
  "update:activeTurn": [value: number];
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

// 每个 user 消息开启一个回合;圆点按回合顺序对应这些位置。
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

// 根据当前滚动位置计算可视回合:取顶部锚点之上最后一个 user 消息。
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

// 跳转到指定回合:平滑滚动到该 user 消息;最后一回合贴底。
function scrollToTurn(turnIndex: number) {
  const el = scrollEl.value;
  const msgIdx = turnIndices.value[turnIndex];
  const node = msgIdx !== undefined ? itemRefs.value[msgIdx] : undefined;
  if (!el || !node) return;
  stickToBottom.value = turnIndex === turnIndices.value.length - 1;
  node.scrollIntoView({ behavior: "smooth", block: "start" });
}

// 回到底部:用户点「跳转最下面」按钮。恢复贴底并平滑滚到底,
// 之后流式增量会继续自动跟随。
function scrollToBottom() {
  stickToBottom.value = true;
  const el = scrollEl.value;
  if (!el) return;
  el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
}

// 是否显示「回到底部」按钮:用户上翻看历史(离底)时出现。
const showJumpButton = computed(() => !stickToBottom.value);

defineExpose({ scrollToTurn });

// 切换会话(消息数组引用变化):总是贴底看最新。
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

// 消息条数增长:仅当新增的是用户刚发出的消息(或首次填充)才贴底;
// 流式过程中 tool 结果等消息增长不应把正在看历史的用户强制拉回底部。
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
        // 内容长度随正文/思考/工具输出增长而变化,任一变化都触发贴底滚动。
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
  <div ref="scrollEl" class="no-scrollbar relative min-h-0 flex-1 overflow-y-auto">
    <!-- 回到底部:用户上翻看历史时出现;运行中带高亮与跳动,提示有新内容。 -->
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
        :title="streaming ? '有新内容，回到底部' : '回到底部'"
        @click="scrollToBottom"
      >
        <ArrowDownIcon class="size-4" :class="streaming ? 'animate-bounce' : ''" />
      </button>
    </Transition>

    <div v-if="messages.length === 0" class="flex h-full flex-col items-center justify-center gap-4">
      <div class="flex size-14 items-center justify-center rounded-2xl bg-muted">
        <BotIcon class="size-6 text-muted-foreground/70" />
      </div>
      <div class="text-center">
        <p class="text-base font-medium">有什么可以帮你的？</p>
        <p class="mt-1 text-sm text-muted-foreground">输入消息开始对话</p>
      </div>
    </div>

    <div v-else class="relative mx-auto max-w-3xl space-y-6 px-4 py-6">
      <div
        v-for="(m, i) in messages"
        :key="i"
        :ref="(el) => setItemRef(el as HTMLElement | null, i)"
      >
        <MessageBubble
          :message="m"
          :streaming="
            streaming && !compacting && m.role === 'assistant' && i === messages.length - 1
          "
        />
      </div>

      <div
        v-if="compacting"
        class="flex h-7 items-center gap-2 text-xs text-muted-foreground"
        role="status"
        aria-live="polite"
      >
        <RefreshCwIcon class="size-3.5 animate-spin" />
        <span>正在压缩上下文</span>
      </div>
    </div>
  </div>
</template>
