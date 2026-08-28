<script setup lang="ts">
import { computed } from "vue";
import { AlertTriangleIcon, LoaderCircleIcon } from "@lucide/vue";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { useKernel } from "@/composables/useKernel";

const {
  pendingHistoryEdit,
  confirmHistoryEdit,
  cancelHistoryEdit,
} = useKernel();

const open = computed(() => pendingHistoryEdit.value !== null);
const hasEffects = computed(
  () => (pendingHistoryEdit.value?.effects.length ?? 0) > 0
);

function toolLabel(tool: string) {
  switch (tool) {
    case "bash":
      return "执行命令";
    case "write":
      return "写入文件";
    case "edit":
      return "编辑文件";
    default:
      return tool;
  }
}
</script>

<template>
  <Dialog :open="open">
    <DialogContent
      class="max-w-lg"
      @escape-key-down.prevent
      @pointer-down-outside.prevent
    >
      <DialogHeader>
        <div class="flex items-center gap-2">
          <AlertTriangleIcon class="size-5 text-amber-600 dark:text-amber-400" />
          <DialogTitle>
            {{ hasEffects ? "旧分支包含工作区操作" : "无法编辑消息" }}
          </DialogTitle>
        </div>
        <DialogDescription v-if="hasEffects">
          编辑会创建新的对话分支，但不会撤销旧分支已经产生的文件或外部变更。
        </DialogDescription>
        <DialogDescription v-else>
          {{ pendingHistoryEdit?.error }}
        </DialogDescription>
      </DialogHeader>

      <div
        v-if="hasEffects"
        class="max-h-56 divide-y divide-border overflow-y-auto rounded-md border border-border"
      >
        <div
          v-for="(effect, index) in pendingHistoryEdit?.effects"
          :key="`${effect.tool}-${index}`"
          class="flex min-w-0 items-start gap-3 px-3 py-2.5"
        >
          <span class="shrink-0 text-xs font-medium">
            {{ toolLabel(effect.tool) }}
          </span>
          <code
            v-if="effect.detail"
            class="min-w-0 flex-1 break-all text-right font-mono text-xs text-muted-foreground"
          >
            {{ effect.detail }}
          </code>
        </div>
      </div>

      <p
        v-if="pendingHistoryEdit?.error && hasEffects"
        class="text-sm text-destructive"
        role="alert"
      >
        {{ pendingHistoryEdit.error }}
      </p>

      <DialogFooter class="gap-2">
        <Button
          variant="outline"
          :disabled="pendingHistoryEdit?.submitting"
          @click="cancelHistoryEdit"
        >
          {{ hasEffects ? "取消" : "关闭" }}
        </Button>
        <Button
          v-if="hasEffects"
          :disabled="pendingHistoryEdit?.submitting"
          @click="confirmHistoryEdit"
        >
          <LoaderCircleIcon
            v-if="pendingHistoryEdit?.submitting"
            class="size-4 animate-spin"
          />
          保留当前工作区并执行
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
