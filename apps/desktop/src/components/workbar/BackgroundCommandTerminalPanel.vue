<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { AlertCircleIcon, LoaderCircleIcon, SquareIcon } from "@lucide/vue";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal, type ITheme } from "@xterm/xterm";
import { api, type BackgroundCommand } from "@/lib/api";
import { useTheme, type Theme } from "@/composables/useTheme";
import { highlightCode } from "@/lib/markdown";
import "@xterm/xterm/css/xterm.css";

const props = defineProps<{
  sessionId: string;
  commandId: string;
  active: boolean;
}>();

const { theme } = useTheme();
const host = ref<HTMLElement | null>(null);
const snapshot = ref<BackgroundCommand | null>(null);
const error = ref("");
const stopping = ref(false);
let terminal: Terminal | null = null;
let fit: FitAddon | null = null;
let resizeObserver: ResizeObserver | null = null;
let poll: number | undefined;
let renderedStdout = "";
let renderedStderr = "";
let stderrStarted = false;
let exitRendered = false;

const highlightedCommand = computed(() =>
  highlightCode(snapshot.value?.command ?? "", "bash")
);

function terminalTheme(value: Theme): ITheme {
  return value === "light"
    ? {
        background: "#ffffff",
        foreground: "#27272a",
        cursor: "#18181b",
        selectionBackground: "#71717a40",
      }
    : {
        background: "#09090b",
        foreground: "#c7c7cc",
        cursor: "#e5e5e5",
        selectionBackground: "#52525b80",
      };
}

function terminalText(value: string): string {
  return value.replace(/\r?\n/g, "\r\n");
}

function ensureTerminal() {
  if (!host.value || terminal) return;
  terminal = new Terminal({
    disableStdin: true,
    cursorBlink: false,
    fontFamily:
      'SFMono-Regular, "SF Mono", Menlo, Monaco, Consolas, "Liberation Mono", monospace',
    fontSize: 12,
    letterSpacing: 0,
    lineHeight: 1.15,
    scrollback: 5000,
    theme: terminalTheme(theme.value),
  });
  fit = new FitAddon();
  terminal.loadAddon(fit);
  terminal.open(host.value);
  resizeObserver = new ResizeObserver(() => {
    if (props.active) fit?.fit();
  });
  resizeObserver.observe(host.value);
}

function render(next: BackgroundCommand) {
  ensureTerminal();
  if (!terminal) return;
  const stdout = next.stdout ?? "";
  const stderr = next.stderr ?? "";
  if (stdout.startsWith(renderedStdout) && stderr.startsWith(renderedStderr)) {
    const stdoutDelta = stdout.slice(renderedStdout.length);
    const stderrDelta = stderr.slice(renderedStderr.length);
    if (stdoutDelta) {
      terminal.write(`\x1b[0m${terminalText(stdoutDelta)}`);
    }
    if (stderrDelta) {
      if (!stderrStarted) {
        terminal.write("\r\n\x1b[1;31mstderr\x1b[0m\r\n");
        stderrStarted = true;
      }
      terminal.write(`\x1b[31m${terminalText(stderrDelta)}\x1b[0m`);
    }
  } else {
    terminal.reset();
    if (stdout) terminal.write(terminalText(stdout));
    if (stderr) {
      terminal.write("\r\n\x1b[1;31mstderr\x1b[0m\r\n");
      terminal.write(`\x1b[31m${terminalText(stderr)}\x1b[0m`);
    }
    stderrStarted = Boolean(stderr);
    exitRendered = false;
  }
  renderedStdout = stdout;
  renderedStderr = stderr;
  if (!next.running && !exitRendered) {
    terminal.write(
      `\r\n\x1b[2m[进程已退出${
        next.exit_code !== undefined ? `，退出码 ${next.exit_code}` : ""
      }]\x1b[0m\r\n`
    );
    exitRendered = true;
  }
  snapshot.value = next;
}

async function refresh() {
  try {
    const next = await api.getBackgroundCommand(props.sessionId, props.commandId);
    error.value = "";
    render(next);
    if (!next.running && poll !== undefined) {
      window.clearInterval(poll);
      poll = undefined;
    }
  } catch (cause) {
    const message = String(cause);
    error.value = message.includes("background_command_not_found")
      ? "命令记录已结束或内核已重启"
      : message;
    if (message.includes("background_command_not_found")) stopPolling();
  }
}

async function stop() {
  if (stopping.value || !snapshot.value?.running) return;
  stopping.value = true;
  try {
    render(await api.stopBackgroundCommand(props.sessionId, props.commandId));
  } catch (cause) {
    error.value = String(cause);
  } finally {
    stopping.value = false;
  }
}

function startPolling() {
  if (poll !== undefined) window.clearInterval(poll);
  poll = window.setInterval(() => void refresh(), 500);
}

function stopPolling() {
  if (poll === undefined) return;
  window.clearInterval(poll);
  poll = undefined;
}

watch(theme, (value) => {
  if (terminal) terminal.options.theme = terminalTheme(value);
});

watch(
  () => props.active,
  async (active) => {
    if (!active) {
      stopPolling();
      return;
    }
    await nextTick();
    ensureTerminal();
    fit?.fit();
    await refresh();
    if (snapshot.value?.running) startPolling();
  },
  { immediate: true }
);

onBeforeUnmount(() => {
  stopPolling();
  resizeObserver?.disconnect();
  terminal?.dispose();
});
</script>

<template>
  <div class="flex h-full min-h-0 flex-col bg-background text-foreground">
    <div class="flex h-9 shrink-0 items-center justify-between border-b border-border px-3">
      <div class="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
        <LoaderCircleIcon
          v-if="snapshot?.running"
          class="size-3.5 shrink-0 animate-spin"
        />
        <span class="truncate">
          {{
            snapshot?.running
              ? snapshot.backgrounded_by
                ? "后台运行中"
                : "命令运行中"
              : "命令已结束"
          }}
        </span>
      </div>
      <button
        v-if="snapshot?.running"
        type="button"
        class="flex size-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-destructive/10 hover:text-destructive disabled:opacity-50"
        title="终止后台命令"
        aria-label="终止后台命令"
        :disabled="stopping"
        @click="stop"
      >
        <SquareIcon class="size-3 fill-current" />
      </button>
    </div>
    <div
      v-if="error"
      class="flex items-start gap-2 border-b border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive"
    >
      <AlertCircleIcon class="mt-0.5 size-3.5 shrink-0" />
      <span class="break-all">{{ error }}</span>
    </div>
    <div
      v-if="snapshot"
      class="flex shrink-0 items-start gap-2 border-b border-border bg-muted/30 px-3 py-2 font-mono text-xs leading-5"
    >
      <span class="shrink-0 font-semibold text-emerald-600 dark:text-emerald-400">$</span>
      <code
        class="hljs min-w-0 flex-1 whitespace-pre-wrap break-all bg-transparent p-0"
        v-html="highlightedCommand"
      />
    </div>
    <div ref="host" class="min-h-0 flex-1 p-3" />
  </div>
</template>
