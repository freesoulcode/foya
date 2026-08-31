<script setup lang="ts">
import { computed } from "vue";
import { ShieldAlertIcon } from "@lucide/vue";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { useKernel } from "@/composables/useKernel";

const { activeId, pendingApprovals, resolveApproval } = useKernel();

const open = computed({
  get: () => Object.keys(pendingApprovals.value).length > 0,
  set: () => {}, // 由 resolveApproval 关闭
});

// 当前(最早的一个)待审批请求;逐个处理。
const current = computed(() => {
  const keys = Object.keys(pendingApprovals.value);
  if (keys.length === 0) return null;
  // 优先显示当前会话的审批;否则取第一个。
  return (
    pendingApprovals.value[
      keys.find((k) => pendingApprovals.value[k].session === activeId.value) ??
        keys[0]
    ] ?? null
  );
});

function approve() {
  if (current.value) {
    void resolveApproval(
      current.value.session,
      current.value.id,
      "approved"
    );
  }
}

function approveForSession() {
  if (current.value) {
    void resolveApproval(
      current.value.session,
      current.value.id,
      "approved_for_session"
    );
  }
}

function deny() {
  if (current.value) {
    void resolveApproval(
      current.value.session,
      current.value.id,
      "denied"
    );
  }
}

const actionLabel = computed(() => {
  if (!current.value) return "";
  switch (current.value.action) {
    case "execute":
      return "执行命令";
    case "write":
      return "写入文件";
    case "read":
      return "读取文件";
    case "network":
      return "访问网络";
    default:
      return current.value.action;
  }
});
</script>

<template>
  <Dialog :open="open">
    <DialogContent class="max-w-md" @escape-key-down.prevent @pointer-down-outside.prevent>
      <DialogHeader>
        <div class="flex items-center gap-2">
          <ShieldAlertIcon class="size-5 text-warning" />
          <DialogTitle>需要你的确认</DialogTitle>
        </div>
        <DialogDescription>
          助手想要{{ actionLabel }},请确认是否允许。
        </DialogDescription>
      </DialogHeader>

      <div v-if="current" class="space-y-3 py-1">
        <div class="rounded-lg bg-muted/60 p-3">
          <div class="mb-1 text-xs font-medium text-muted-foreground">工具</div>
          <div class="font-mono text-sm">{{ current.tool_name }}</div>
        </div>
        <div v-if="current.detail" class="rounded-lg bg-muted/60 p-3">
          <div class="mb-1 text-xs font-medium text-muted-foreground">内容</div>
          <pre class="whitespace-pre-wrap break-all font-mono text-xs">{{ current.detail }}</pre>
        </div>
        <div v-if="current.scope" class="rounded-lg bg-muted/60 p-3">
          <div class="mb-1 text-xs font-medium text-muted-foreground">授权范围</div>
          <div class="break-all font-mono text-xs">{{ current.scope }}</div>
        </div>
      </div>

      <DialogFooter class="flex-wrap gap-2">
        <Button variant="outline" @click="deny">拒绝</Button>
        <Button variant="outline" @click="approve">仅本次允许</Button>
        <Button @click="approveForSession">本 Session 内允许</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
