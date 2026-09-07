<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { MinusIcon, SquareIcon, CopyIcon, XIcon } from "@lucide/vue";
import { getCurrentWindow } from "@tauri-apps/api/window";

const maximized = ref(false);
let unlistenResize: (() => void) | null = null;

onMounted(async () => {
  try {
    const appWindow = getCurrentWindow();
    maximized.value = await appWindow.isMaximized();
    unlistenResize = await appWindow.onResized(async () => {
      maximized.value = await appWindow.isMaximized();
    });
  } catch {
    // Ignore non-Tauri environments.
  }
});

onUnmounted(() => unlistenResize?.());

function minimize() {
  getCurrentWindow().minimize();
}
function toggleMaximize() {
  getCurrentWindow().toggleMaximize();
}
function close() {
  getCurrentWindow().close();
}
</script>

<template>
  <div class="no-drag flex h-full items-center">
    <button
      type="button"
      class="flex h-full w-11 items-center justify-center text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
      :title="$t('Minimize')"
      @click="minimize"
    >
      <MinusIcon class="size-4" />
    </button>
    <button
      type="button"
      class="flex h-full w-11 items-center justify-center text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
      :title="maximized ? $t('Restore') : $t('Maximize')"
      @click="toggleMaximize"
    >
      <CopyIcon v-if="maximized" class="size-3.5" />
      <SquareIcon v-else class="size-3.5" />
    </button>
    <button
      type="button"
      class="flex h-full w-11 items-center justify-center text-muted-foreground transition-colors hover:bg-destructive hover:text-white"
      :title="$t('Close')"
      @click="close"
    >
      <XIcon class="size-4" />
    </button>
  </div>
</template>
