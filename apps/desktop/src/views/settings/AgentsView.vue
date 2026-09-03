<script setup lang="ts">
import { ref } from "vue";
import { api, type AgentLimits } from "@/lib/api";
import SettingsPage from "@/layouts/settings/SettingsPage.vue";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const agentLimits = ref<AgentLimits>({
  max_global_concurrency: 4,
  max_per_root: 4,
  max_tree_tokens: 0,
});
const loading = ref(false);
const saving = ref(false);
const error = ref("");

function validInteger(value: number, min: number, max: number): boolean {
  return Number.isInteger(value) && value >= min && value <= max;
}

async function loadAgentLimits() {
  loading.value = true;
  error.value = "";
  try {
    agentLimits.value = await api.getAgentLimits();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

async function saveAgentLimits() {
  error.value = "";
  const limits = agentLimits.value;
  if (
    !validInteger(limits.max_global_concurrency, 1, 256) ||
    !validInteger(limits.max_per_root, 1, 256) ||
    !Number.isInteger(limits.max_tree_tokens) ||
    limits.max_tree_tokens < 0
  ) {
    error.value = "请检查输入范围：并发 1~256，Token 上限 >= 0";
    return;
  }
  saving.value = true;
  try {
    agentLimits.value = await api.updateAgentLimits({ ...limits });
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

void loadAgentLimits();
</script>

<template>
  <SettingsPage
    title="Agent 调度"
    description="配置子 Agent 并发与任务树资源限制。"
  >
    <div class="max-w-4xl divide-y divide-border border-y border-border">
      <label class="grid grid-cols-[minmax(0,1fr)_8rem] items-center gap-6 py-4">
        <span class="min-w-0">
          <span class="block text-sm font-medium">全局并发</span>
          <span class="block text-xs text-muted-foreground">整个内核同时运行的 Child 数</span>
        </span>
        <Input
          v-model.number="agentLimits.max_global_concurrency"
          type="number"
          min="1"
          max="256"
          step="1"
          :disabled="loading"
        />
      </label>
      <label class="grid grid-cols-[minmax(0,1fr)_8rem] items-center gap-6 py-4">
        <span class="min-w-0">
          <span class="block text-sm font-medium">单任务并发</span>
          <span class="block text-xs text-muted-foreground">单个根任务同时运行的 Child 数</span>
        </span>
        <Input
          v-model.number="agentLimits.max_per_root"
          type="number"
          min="1"
          max="256"
          step="1"
          :disabled="loading"
        />
      </label>
      <label class="grid grid-cols-[minmax(0,1fr)_8rem] items-center gap-6 py-4">
        <span class="min-w-0">
          <span class="block text-sm font-medium">任务树 Token 上限</span>
          <span class="block text-xs text-muted-foreground">0 表示不限制</span>
        </span>
        <Input
          v-model.number="agentLimits.max_tree_tokens"
          type="number"
          min="0"
          step="1000"
          :disabled="loading"
        />
      </label>
    </div>
    <p v-if="error" class="mt-4 text-sm text-destructive">{{ error }}</p>
    <div class="mt-5 flex max-w-4xl justify-end">
      <Button
        :disabled="loading || saving"
        @click="saveAgentLimits"
      >
        {{ saving ? "保存中..." : "保存" }}
      </Button>
    </div>
  </SettingsPage>
</template>
