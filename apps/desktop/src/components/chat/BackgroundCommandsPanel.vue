<script setup lang="ts">
import { ref } from "vue";
import {
  ChevronRightIcon,
  LoaderCircleIcon,
  SquareIcon,
  SquareTerminalIcon,
  TerminalIcon,
} from "@lucide/vue";
import type { BackgroundCommand } from "@/lib/api";

defineProps<{
  commands: BackgroundCommand[];
}>();

const emit = defineEmits<{
  (event: "stop", commandId: string): void;
  (event: "open", command: BackgroundCommand): void;
}>();

const expanded = ref(false);
function output(command: BackgroundCommand): string {
  return [command.stdout, command.stderr].filter(Boolean).join("\n");
}
</script>

<template>
  <section
    v-if="commands.length"
    class="shrink-0 px-4 pt-2"
    aria-label="后台命令"
  >
    <div class="mx-auto w-full max-w-3xl overflow-hidden rounded-lg border border-border bg-muted/40">
      <button
        type="button"
        class="flex h-11 w-full min-w-0 items-center gap-2 px-3 text-left transition-colors hover:bg-background/60"
        :aria-expanded="expanded"
        @click="expanded = !expanded"
      >
        <ChevronRightIcon
          class="size-4 shrink-0 text-muted-foreground transition-transform"
          :class="{ 'rotate-90': expanded }"
        />
        <TerminalIcon class="size-4 shrink-0 text-muted-foreground" />
        <span class="min-w-0 flex-1 truncate text-sm font-medium">
          {{ commands.length }} 个后台命令
        </span>
        <span class="shrink-0 text-xs text-muted-foreground">正在运行</span>
      </button>

      <div v-if="expanded" class="max-h-64 overflow-y-auto border-t border-border">
        <article
          v-for="command in commands"
          :key="command.command_id"
          class="border-b border-border px-3 py-2.5 last:border-b-0"
        >
          <div class="flex min-w-0 items-center gap-2">
            <LoaderCircleIcon
              class="size-3.5 shrink-0 animate-spin text-muted-foreground"
            />
            <code class="min-w-0 flex-1 truncate text-xs" :title="command.command">
              {{ command.command }}
            </code>
            <span class="shrink-0 text-xs text-muted-foreground">
              {{ command.backgrounded_by === "user" ? "用户转入后台" : "运行中" }}
            </span>
            <button
              type="button"
              class="flex h-7 shrink-0 items-center gap-1 rounded px-2 text-xs text-muted-foreground hover:bg-background hover:text-foreground"
              title="在终端中查看"
              aria-label="在终端中查看"
              @click="emit('open', command)"
            >
              <SquareTerminalIcon class="size-3.5" />
              <span>终端</span>
            </button>
            <button
              type="button"
              class="flex h-7 shrink-0 items-center gap-1 rounded px-2 text-xs text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
              title="终止后台命令"
              aria-label="终止后台命令"
              @click="emit('stop', command.command_id)"
            >
              <SquareIcon class="size-3 fill-current" />
              <span>停止</span>
            </button>
          </div>
          <pre
            v-if="output(command)"
            class="mt-2 max-h-32 overflow-auto whitespace-pre-wrap break-all border-l-2 border-border pl-2 font-mono text-xs leading-5 text-muted-foreground"
          >{{ output(command) }}</pre>
        </article>
      </div>
    </div>
  </section>
</template>
