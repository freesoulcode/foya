<script setup lang="ts">
import { ref, nextTick, watch, onMounted, onBeforeUnmount, computed } from "vue";
import { BotIcon } from "@lucide/vue";
import MessageBubble from "./MessageBubble.vue";
import type { ChatMessage } from "@/lib/api";

const props = defineProps<{
  messages: ChatMessage[];
  streaming: boolean;
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

defineExpose({ scrollToTurn });

watch(
  () => props.messages.length,
  async () => {
    stickToBottom.value = true;
    await nextTick();
    scrollEl.value?.scrollTo({ top: scrollEl.value.scrollHeight });
    computeActiveTurn();
  }
);

watch(
  () => props.messages.map((m) => m.content).join(""),
  async () => {
    if (!stickToBottom.value) return;
    await nextTick();
    const el = scrollEl.value;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
    computeActiveTurn();
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
  <div ref="scrollEl" class="no-scrollbar min-h-0 flex-1 overflow-y-auto">
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
          :streaming="streaming && m.role === 'assistant' && i === messages.length - 1"
        />
      </div>
    </div>
  </div>
</template>
