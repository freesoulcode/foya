<script setup lang="ts">
import { ref, nextTick, watch, onMounted, onBeforeUnmount } from "vue";
import { BotIcon } from "@lucide/vue";
import MessageBubble from "./MessageBubble.vue";
import type { ChatMessage } from "@/lib/api";

const props = defineProps<{
  messages: ChatMessage[];
  streaming: boolean;
}>();

const scrollEl = ref<HTMLElement | null>(null);
const stickToBottom = ref(true);

function isNearBottom(el: HTMLElement, threshold = 80) {
  return el.scrollHeight - el.scrollTop - el.clientHeight <= threshold;
}

function onScroll() {
  const el = scrollEl.value;
  if (!el) return;
  stickToBottom.value = isNearBottom(el);
}

watch(
  () => props.messages.length,
  async () => {
    stickToBottom.value = true;
    await nextTick();
    scrollEl.value?.scrollTo({ top: scrollEl.value.scrollHeight });
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
  <div ref="scrollEl" class="no-scrollbar flex-1 overflow-y-auto">
    <div v-if="messages.length === 0" class="flex h-full flex-col items-center justify-center gap-4">
      <div class="flex size-14 items-center justify-center rounded-2xl bg-muted">
        <BotIcon class="size-6 text-muted-foreground/70" />
      </div>
      <div class="text-center">
        <p class="text-base font-medium">有什么可以帮你的？</p>
        <p class="mt-1 text-sm text-muted-foreground">输入消息开始对话</p>
      </div>
    </div>

    <div v-else class="mx-auto max-w-3xl px-4 py-6 space-y-6">
      <MessageBubble
        v-for="(m, i) in messages"
        :key="i"
        :message="m"
        :streaming="streaming && m.role === 'assistant' && i === messages.length - 1"
      />
    </div>
  </div>
</template>
