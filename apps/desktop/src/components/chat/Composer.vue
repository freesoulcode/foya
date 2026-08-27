<script setup lang="ts">
import { ref } from "vue";
import { ArrowUpIcon, SquareIcon } from "@lucide/vue";
import { Textarea } from "@/components/ui/textarea";

const props = defineProps<{
  disabled?: boolean;
  streaming?: boolean;
}>();

const emit = defineEmits<{
  (e: "send", text: string): void;
  (e: "stop"): void;
}>();

const input = ref("");

function submit() {
  const text = input.value.trim();
  if (!text || props.disabled) return;
  input.value = "";
  emit("send", text);
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === "Enter" && !e.shiftKey) {
    e.preventDefault();
    submit();
  }
}
</script>

<template>
  <div class="shrink-0 px-4 pb-4 pt-2">
    <div class="mx-auto max-w-3xl">
      <div
        class="flex items-end gap-2 rounded-2xl border border-input bg-card px-3 py-2 shadow-xs transition-[color,box-shadow] focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50"
      >
        <Textarea
          v-model="input"
          placeholder="给 Foya 发送消息…"
          class="min-h-[24px] max-h-40 resize-none border-0 bg-transparent px-1 py-1.5 text-sm shadow-none focus-visible:ring-0"
          rows="1"
          :disabled="disabled"
          @keydown="onKeydown"
        />
        <button
          v-if="streaming"
          class="flex size-8 shrink-0 items-center justify-center rounded-lg bg-foreground text-background transition-opacity hover:opacity-80 disabled:opacity-30"
          title="停止"
          @click="emit('stop')"
        >
          <SquareIcon class="size-3.5 fill-current" />
        </button>
        <button
          v-else
          class="flex size-8 shrink-0 items-center justify-center rounded-lg bg-foreground text-background transition-opacity hover:opacity-80 disabled:opacity-30"
          :disabled="disabled || !input.trim()"
          title="发送 (Enter)"
          @click="submit"
        >
          <ArrowUpIcon class="size-4" />
        </button>
      </div>
      <p class="mt-2 text-center text-[11px] text-muted-foreground">
        Enter 发送 · Shift+Enter 换行
      </p>
    </div>
  </div>
</template>
