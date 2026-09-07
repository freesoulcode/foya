<script setup lang="ts">
import { computed } from "vue";
import {
  LoaderCircleIcon,
  SquareIcon,
  SquareTerminalIcon,
} from "@lucide/vue";
import { Button } from "@/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type { BackgroundCommand } from "@/lib/api";

const props = defineProps<{
  commands: BackgroundCommand[];
}>();

const runningCommands = computed(() =>
  props.commands.filter((command) => command.running)
);

const emit = defineEmits<{
  (event: "stop", commandId: string): void;
  (event: "open", command: BackgroundCommand): void;
}>();

function output(command: BackgroundCommand): string {
  return [command.stdout, command.stderr].filter(Boolean).join("\n");
}
</script>

<template>
  <div class="no-scrollbar max-h-72 divide-y divide-border overflow-y-auto">
    <article
      v-for="command in runningCommands"
      :key="command.command_id"
      class="px-3 py-2.5"
    >
      <div class="flex min-w-0 items-center gap-2">
        <LoaderCircleIcon
          class="size-3.5 shrink-0 animate-spin text-primary"
        />
        <code class="min-w-0 flex-1 truncate text-xs" :title="command.command">
          {{ command.command }}
        </code>
        <div class="flex shrink-0 items-center gap-0.5">
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                type="button"
                size="icon-xs"
                variant="ghost"
                :aria-label="$t('View in terminal')"
                @click="emit('open', command)"
              >
                <SquareTerminalIcon class="size-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="left">{{ $t("View in terminal") }}</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button
                type="button"
                size="icon-xs"
                variant="ghost"
                class="text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                :aria-label="$t('Stop background command')"
                @click="emit('stop', command.command_id)"
              >
                <SquareIcon class="size-3 fill-current" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="left">{{ $t("Stop background command") }}</TooltipContent>
          </Tooltip>
        </div>
      </div>
      <pre
        v-if="output(command)"
        class="mt-2 max-h-32 overflow-auto whitespace-pre-wrap break-all border-l-2 border-border pl-2 font-mono text-xs leading-5 text-muted-foreground"
      >{{ output(command) }}</pre>
    </article>
  </div>
</template>
