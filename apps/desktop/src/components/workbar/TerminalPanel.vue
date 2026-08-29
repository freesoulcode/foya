<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from "vue";
import { AlertCircleIcon } from "@lucide/vue";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal, type ITheme } from "@xterm/xterm";
import { useTheme, type Theme } from "@/composables/useTheme";
import { api, type TerminalDataEvent } from "@/lib/api";
import "@xterm/xterm/css/xterm.css";

const props = defineProps<{
  sessionId?: string;
  active: boolean;
  ensureSession: () => Promise<string>;
}>();

const { theme } = useTheme();
const terminalRefs = new Map<string, string>();
const host = ref<HTMLElement | null>(null);
const starting = ref(false);
const running = ref(false);
const error = ref("");
let terminal: Terminal | null = null;
let fit: FitAddon | null = null;
let resizeObserver: ResizeObserver | null = null;
let disposed = false;
let generation = 0;
let lastSeq = 0;

function terminalTheme(value: Theme): ITheme {
  if (value === "light") {
    return {
      background: "#ffffff",
      foreground: "#27272a",
      cursor: "#18181b",
      cursorAccent: "#ffffff",
      selectionBackground: "#71717a40",
      selectionInactiveBackground: "#a1a1aa30",
      black: "#18181b",
      red: "#b91c1c",
      green: "#15803d",
      yellow: "#a16207",
      blue: "#1d4ed8",
      magenta: "#a21caf",
      cyan: "#0e7490",
      white: "#d4d4d8",
      brightBlack: "#71717a",
      brightRed: "#dc2626",
      brightGreen: "#16a34a",
      brightYellow: "#ca8a04",
      brightBlue: "#2563eb",
      brightMagenta: "#c026d3",
      brightCyan: "#0891b2",
      brightWhite: "#f4f4f5",
    };
  }
  return {
    background: "#09090b",
    foreground: "#c7c7cc",
    cursor: "#e5e5e5",
    cursorAccent: "#18181b",
    selectionBackground: "#52525b80",
    selectionInactiveBackground: "#3f3f4650",
  };
}

function disposeView() {
  generation += 1;
  resizeObserver?.disconnect();
  resizeObserver = null;
  terminal?.dispose();
  terminal = null;
  fit = null;
  lastSeq = 0;
  running.value = false;
}

function ensureView() {
  if (!host.value || terminal) return;
  terminal = new Terminal({
    cursorBlink: true,
    cursorStyle: "bar",
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
    if (!props.active || !fit || !terminal) return;
    fit.fit();
    const sessionId = props.sessionId;
    const terminalRef = sessionId ? terminalRefs.get(sessionId) : undefined;
    if (sessionId && terminalRef) {
      void api.resizeTerminal(sessionId, terminalRef, terminal.cols, terminal.rows);
    }
  });
  resizeObserver.observe(host.value);
  terminal.onData((input) => {
    const sessionId = props.sessionId;
    const terminalRef = sessionId ? terminalRefs.get(sessionId) : undefined;
    if (sessionId && terminalRef && running.value) {
      void api.writeTerminal(sessionId, terminalRef, input);
    }
  });
}

watch(theme, (value) => {
  if (terminal) terminal.options.theme = terminalTheme(value);
});

async function attach(sessionId: string, terminalRef: string) {
  const currentGeneration = ++generation;
  ensureView();
  if (!terminal) return;
  terminal.reset();
  error.value = "";
  try {
    const snapshot = await api.attachTerminal(sessionId, terminalRef);
    if (disposed || currentGeneration !== generation) return;
    terminal.write(snapshot.buffer ?? "");
    lastSeq = snapshot.seq;
    running.value = snapshot.running;
    await api.subscribeTerminal(sessionId, terminalRef, lastSeq, (data) => {
      if (disposed || currentGeneration !== generation) return;
      let event: TerminalDataEvent;
      try {
        event = JSON.parse(data) as TerminalDataEvent;
      } catch {
        return;
      }
      if (event.seq <= lastSeq) return;
      lastSeq = event.seq;
      if (event.data) terminal?.write(event.data);
      if (event.exited) running.value = false;
    });
    await nextTick();
    fit?.fit();
    if (props.active) terminal.focus();
  } catch (cause) {
    if (currentGeneration === generation) {
      error.value = String(cause);
      running.value = false;
    }
  }
}

async function start() {
  if (starting.value) return;
  starting.value = true;
  error.value = "";
  try {
    await nextTick();
    ensureView();
    fit?.fit();
    const sessionId = props.sessionId ?? await props.ensureSession();
    const resource = await api.startTerminal(
      sessionId,
      terminal?.cols ?? 80,
      terminal?.rows ?? 24
    );
    terminalRefs.set(sessionId, resource.ref);
    await attach(sessionId, resource.ref);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    starting.value = false;
  }
}

watch(
  () => [props.sessionId, props.active] as const,
  async ([sessionId, active]) => {
    if (!active) return;
    await nextTick();
    ensureView();
    if (!sessionId) {
      await start();
      return;
    }
    const terminalRef = terminalRefs.get(sessionId);
    if (terminalRef) await attach(sessionId, terminalRef);
    else await start();
  },
  { immediate: true }
);

onBeforeUnmount(() => {
  disposed = true;
  for (const [sessionId, terminalRef] of terminalRefs) {
    void api.stopTerminal(sessionId, terminalRef).catch(() => {});
  }
  terminalRefs.clear();
  disposeView();
});
</script>

<template>
  <div class="flex h-full min-h-0 flex-col bg-background text-foreground">
    <div
      v-if="error"
      class="flex items-start gap-2 border-b border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive"
    >
      <AlertCircleIcon class="mt-0.5 size-3.5 shrink-0" />
      <span class="break-all">{{ error }}</span>
    </div>

    <div ref="host" class="min-h-0 flex-1 p-2" />
  </div>
</template>
