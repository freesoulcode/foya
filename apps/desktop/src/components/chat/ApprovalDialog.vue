<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
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
import { localizeRuntimeText } from "@/i18n";

const { activeId, pendingApprovals, resolveApproval } = useKernel();
const { t } = useI18n();

const open = computed({
  get: () => Object.keys(pendingApprovals.value).length > 0,
  set: () => {}, // resolveApproval owns closing this dialog.
});

// Process one request at a time, preferring the active session.
const current = computed(() => {
  const keys = Object.keys(pendingApprovals.value);
  if (keys.length === 0) return null;
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
      return t("run a command");
    case "write":
      return t("write a file");
    case "delete":
      return t("move a file to Trash");
    case "read":
      return t("read a file");
    case "network":
      return t("access the network");
    default:
      return current.value.action;
  }
});
const detail = computed(() =>
  current.value?.detail ? localizeRuntimeText(current.value.detail) : ""
);
</script>

<template>
  <Dialog :open="open">
    <DialogContent class="max-w-md" @escape-key-down.prevent @pointer-down-outside.prevent>
      <DialogHeader>
        <div class="flex items-center gap-2">
          <ShieldAlertIcon class="size-5 text-warning" />
          <DialogTitle>{{ $t("Approval required") }}</DialogTitle>
        </div>
        <DialogDescription>
          {{ $t("The assistant wants to {action}. Confirm whether to allow it.", { action: actionLabel }) }}
        </DialogDescription>
      </DialogHeader>

      <div v-if="current" class="space-y-3 py-1">
        <div class="rounded-lg bg-muted/60 p-3">
          <div class="mb-1 text-xs font-medium text-muted-foreground">{{ $t("Tool") }}</div>
          <div class="font-mono text-sm">{{ current.tool_name }}</div>
        </div>
        <div v-if="detail" class="rounded-lg bg-muted/60 p-3">
          <div class="mb-1 text-xs font-medium text-muted-foreground">{{ $t("Content") }}</div>
          <pre class="whitespace-pre-wrap break-all font-mono text-xs">{{ detail }}</pre>
        </div>
        <div v-if="current.scope" class="rounded-lg bg-muted/60 p-3">
          <div class="mb-1 text-xs font-medium text-muted-foreground">{{ $t("Permission scope") }}</div>
          <div class="break-all font-mono text-xs">{{ current.scope }}</div>
        </div>
      </div>

      <DialogFooter class="flex-wrap gap-2">
        <Button variant="outline" @click="deny">{{ $t("Deny") }}</Button>
        <Button variant="outline" @click="approve">{{ $t("Allow once") }}</Button>
        <Button @click="approveForSession">{{ $t("Allow for this session") }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
