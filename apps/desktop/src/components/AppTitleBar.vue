<script setup lang="ts">
import { computed } from "vue";
import { SidebarTrigger, useSidebar } from "@/components/ui/sidebar";
import WindowControls from "@/components/WindowControls.vue";
import { usePlatform } from "@/composables/usePlatform";

const { isMac, showCustomWindowControls } = usePlatform();
const { state } = useSidebar();

const isCollapsed = computed(() => state.value === "collapsed");

const showBar = computed(() => showCustomWindowControls.value || isCollapsed.value);

const macPadding = computed(() =>
  isMac.value && isCollapsed.value ? "pl-[72px]" : "pl-2"
);
</script>

<template>
  <div
    v-if="showBar"
    data-tauri-drag-region
    class="flex h-9 shrink-0 items-center pr-2"
    :class="macPadding"
  >
    <SidebarTrigger v-if="isCollapsed" class="no-drag text-muted-foreground" />
    <div data-tauri-drag-region class="flex-1" />
    <WindowControls v-if="showCustomWindowControls" />
  </div>
</template>
