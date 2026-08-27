<script setup lang="ts">
import { computed, ref } from "vue";
import { BotIcon, CopyIcon, CheckIcon } from "@lucide/vue";
import { cn } from "@/lib/utils";
import { renderMarkdown } from "@/lib/markdown";
import type { ChatMessage } from "@/lib/api";

const props = defineProps<{
  message: ChatMessage;
  streaming?: boolean;
}>();

const isUser = computed(() => props.message.role === "user");

const renderedContent = computed(() => {
  if (isUser.value) return props.message.content;
  return renderMarkdown(props.message.content);
});

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
      <div v-if="isUser" class="whitespace-pre-wrap break-words">
        {{ message.content }}
      </div>
      <div
        v-else
        ref="bodyEl"
        class="prose-chat relative text-foreground"
        v-html="renderedContent"
        @click="onBodyClick"
      />
      <span
        v-if="streaming"
        class="ml-0.5 inline-block h-4 w-1.5 translate-y-0.5 animate-pulse bg-current align-baseline"
      />

      <div
        v-if="!isUser && !streaming && message.content"
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
