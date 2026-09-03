<script setup lang="ts">
import { onMounted } from "vue";
import { RouterView } from "vue-router";
import { useKernel } from "@/composables/useKernel";
import ApprovalDialog from "@/components/chat/ApprovalDialog.vue";
import HistoryEditDialog from "@/components/chat/HistoryEditDialog.vue";

const { connect } = useKernel();

onMounted(() => {
  void connect();
});
</script>

<template>
  <RouterView />
  <ApprovalDialog />
  <HistoryEditDialog />
</template>
