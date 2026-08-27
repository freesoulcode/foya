<script setup lang="ts">
import { computed } from "vue";
import type { Session } from "@/lib/api";

const props = defineProps<{
  session?: Session;
}>();

const modelName = computed(() => {
  return props.session?.model || "Foya";
});
</script>

<template>
  <header class="flex h-12 shrink-0 items-center px-4">
    <span class="text-[13px] font-medium">{{ modelName }}</span>
  </header>
</template>
