<script setup lang="ts">
import { onMounted } from "vue";
import { RouterView } from "vue-router";
import { useKernel } from "@/composables/useKernel";
import ApprovalDialog from "@/components/chat/ApprovalDialog.vue";
import HistoryRewindDialog from "@/components/chat/HistoryRewindDialog.vue";

const { connect } = useKernel();

onMounted(() => {
  void connect();
});
</script>

<template>
  <RouterView />
  <ApprovalDialog />
  <HistoryRewindDialog />
</template>
