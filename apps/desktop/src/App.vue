<script setup lang="ts">
import { onMounted } from "vue";
import { RouterView } from "vue-router";
import { RefreshCwIcon } from "@lucide/vue";
import { Button } from "@/components/ui/button";
import { useKernel } from "@/composables/useKernel";
import { usePlatform } from "@/composables/usePlatform";
import { localizeError } from "@/i18n";
import ApprovalDialog from "@/components/chat/ApprovalDialog.vue";
import HistoryRewindDialog from "@/components/chat/HistoryRewindDialog.vue";
import WindowControls from "@/components/WindowControls.vue";

const { ready, connecting, connectError, connect } = useKernel();
const { showCustomWindowControls } = usePlatform();

onMounted(() => {
  void connect();
});
</script>

<template>
  <template v-if="ready">
    <RouterView />
    <ApprovalDialog />
    <HistoryRewindDialog />
  </template>

  <main
    v-else
    data-tauri-drag-region
    class="relative grid h-svh place-items-center overflow-hidden bg-background text-foreground"
  >
    <div v-if="showCustomWindowControls" class="no-drag absolute right-2 top-0 z-10">
      <WindowControls />
    </div>

    <div class="flex -translate-y-4 flex-col items-center text-center">
      <h1 class="startup-word m-0 text-[34px] leading-none font-semibold">foya</h1>
      <p v-if="!connectError || connecting" class="sr-only">{{ $t("Connecting") }}</p>
      <p
        v-else
        class="mt-4 max-w-sm px-6 text-[12px] text-muted-foreground"
      >
        {{ localizeError(connectError) }}
      </p>
      <Button
        v-if="connectError && !connecting"
        variant="outline"
        size="sm"
        class="no-drag mt-4"
        @click="connect"
      >
        <RefreshCwIcon class="size-3.5" />
        {{ $t("Retry") }}
      </Button>
    </div>
  </main>
</template>

<style scoped>
@keyframes startup-word {
  from {
    background-position: 100% 50%;
  }
  to {
    background-position: -100% 50%;
  }
}

.startup-word {
  color: transparent;
  background-image: linear-gradient(
    100deg,
    var(--muted-foreground) 35%,
    var(--foreground) 50%,
    var(--muted-foreground) 65%
  );
  background-position: 100% 50%;
  background-size: 240% 100%;
  background-clip: text;
  -webkit-background-clip: text;
  animation: startup-word 1.45s ease-in-out infinite;
}

@media (prefers-reduced-motion: reduce) {
  .startup-word {
    color: var(--foreground);
    background: none;
    animation: none;
  }
}
</style>
