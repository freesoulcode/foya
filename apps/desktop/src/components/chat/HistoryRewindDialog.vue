<script setup lang="ts">
import { computed } from "vue";
import { AlertTriangleIcon, FilePenLineIcon, LoaderCircleIcon } from "@lucide/vue";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { useKernel } from "@/composables/useKernel";

const {
  pendingHistoryRewind,
  confirmHistoryRewind,
  cancelHistoryRewind,
  toggleHistoryRewindForceFile,
} = useKernel();

const open = computed(() => pendingHistoryRewind.value !== null);
const hasFiles = computed(
  () => (pendingHistoryRewind.value?.files.length ?? 0) > 0
);
const canConfirm = computed(
  () => pendingHistoryRewind.value !== null && !pendingHistoryRewind.value.error
);
const hasModifiedFiles = computed(
  () => pendingHistoryRewind.value?.files.some((file) => file.status === "modified") ?? false
);
const hasForcedFiles = computed(
  () => (pendingHistoryRewind.value?.forceFileKeys.length ?? 0) > 0
);

function isForced(key: string) {
  return pendingHistoryRewind.value?.forceFileKeys.includes(key) ?? false;
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
            {{ canConfirm ? "回退这条消息？" : "无法回退消息" }}
          </DialogTitle>
        </div>
        <DialogDescription v-if="canConfirm">
          这条消息及之后的对话会从当前上下文移除，消息内容会回到输入框，现有草稿会被替换。
          <template v-if="hasFiles">
            未被修改的文件将自动恢复；不冲突的用户修改会被保留；冲突文件默认保留，可逐个选择强制恢复。已执行的命令不会撤销。
          </template>
        </DialogDescription>
        <DialogDescription v-else>
          {{ pendingHistoryRewind?.error }}
        </DialogDescription>
      </DialogHeader>

      <div
        v-if="hasFiles && canConfirm"
        class="max-h-56 divide-y divide-border overflow-y-auto rounded-md border border-border"
      >
        <div
          v-for="file in pendingHistoryRewind?.files"
          :key="file.path"
          class="flex min-w-0 items-center gap-2.5 px-3 py-2.5"
        >
          <FilePenLineIcon class="size-4 shrink-0 text-amber-600 dark:text-amber-400" />
          <code class="min-w-0 flex-1 break-all font-mono text-xs">
            {{ file.path }}
          </code>
          <span
            v-if="file.status === 'ready'"
            class="shrink-0 text-xs text-muted-foreground"
          >
            将恢复
          </span>
          <span
            v-else-if="file.status === 'mergeable'"
            class="shrink-0 text-xs text-emerald-600 dark:text-emerald-400"
          >
            将合并恢复
          </span>
          <label
            v-else
            class="flex shrink-0 cursor-pointer items-center gap-2 text-xs"
            :class="isForced(file.key) ? 'text-destructive' : 'text-muted-foreground'"
          >
            <Checkbox
              :model-value="isForced(file.key)"
              :disabled="pendingHistoryRewind?.submitting"
              @update:model-value="toggleHistoryRewindForceFile(file.key)"
            />
            <span>{{ isForced(file.key) ? "强制恢复" : "保留当前" }}</span>
          </label>
        </div>
      </div>

      <p
        v-if="canConfirm && hasModifiedFiles && hasForcedFiles"
        class="text-xs text-destructive"
      >
        强制恢复会覆盖所选文件在 Agent 修改后的全部变化。
      </p>

      <DialogFooter class="gap-2">
        <Button
          variant="outline"
          :disabled="pendingHistoryRewind?.submitting"
          @click="cancelHistoryRewind"
        >
          {{ canConfirm ? "取消" : "关闭" }}
        </Button>
        <Button
          v-if="canConfirm"
          variant="destructive"
          :disabled="pendingHistoryRewind?.submitting"
          @click="confirmHistoryRewind"
        >
          <LoaderCircleIcon
            v-if="pendingHistoryRewind?.submitting"
            class="size-4 animate-spin"
          />
          回退到输入框
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
