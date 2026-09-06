<script setup lang="ts">
import { nextTick, ref } from "vue";
import {
  CircleAlertIcon,
  GitForkIcon,
  Loader2Icon,
  MessageCircleIcon,
  PinIcon,
  PinOffIcon,
  Trash2Icon,
} from "@lucide/vue";
import {
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";
import type { Session } from "@/lib/api";

const props = defineProps<{
  session: Session;
  active?: boolean;
  running?: boolean;
  unread?: boolean;
  needsAttention?: boolean;
}>();

const emit = defineEmits<{
  (event: "select", id: string): void;
  (event: "rename", id: string, title: string): void;
  (event: "pin", id: string, pinned: boolean): void;
  (event: "fork", session: Session): void;
  (event: "delete", session: Session): void;
}>();

const editing = ref(false);
const editingText = ref("");
const editInput = ref<HTMLInputElement | null>(null);

function title() {
  return props.session.title || "新对话";
}

async function startRename() {
  editing.value = true;
  editingText.value = props.session.title || "";
  await nextTick();
  editInput.value?.focus();
  editInput.value?.select();
}

function commitRename() {
  if (!editing.value) return;
  editing.value = false;
  const value = editingText.value.trim();
  if (value) emit("rename", props.session.id, value);
}

function cancelRename() {
  editing.value = false;
}

function togglePin(event: Event) {
  event.stopPropagation();
  emit("pin", props.session.id, !props.session.pinned);
}

function requestFork(event: Event) {
  event.stopPropagation();
  emit("fork", props.session);
}

function requestDelete(event: Event) {
  event.stopPropagation();
  emit("delete", props.session);
}
</script>

<template>
  <SidebarMenuItem class="group/menu-item">
    <div class="relative">
      <SidebarMenuButton
        :as="editing ? 'div' : 'button'"
        :is-active="active"
        :tooltip="title()"
        class="pr-2 group-hover/menu-item:pr-20 group-focus-within/menu-item:pr-20"
        @click="!editing && emit('select', session.id)"
      >
        <MessageCircleIcon />
        <input
          v-if="editing"
          ref="editInput"
          v-model="editingText"
          class="w-full bg-transparent outline-none"
          @click.stop
          @keydown.enter.prevent="commitRename"
          @keydown.esc.prevent="cancelRename"
          @blur="commitRename"
        />
        <span
          v-else
          class="min-w-0 flex-1 truncate"
          @dblclick.stop="startRename"
        >
          {{ title() }}
        </span>
        <CircleAlertIcon
          v-if="needsAttention && !editing"
          class="ml-auto size-3.5 shrink-0 text-amber-500 group-hover/menu-item:hidden group-focus-within/menu-item:hidden"
          aria-label="需要处理"
          title="需要处理"
        />
        <Loader2Icon
          v-else-if="running && !editing"
          class="ml-auto size-3.5 shrink-0 animate-spin text-primary group-hover/menu-item:hidden group-focus-within/menu-item:hidden"
          aria-label="AI 正在运行"
        />
        <span
          v-else-if="unread && !editing"
          class="ml-auto size-2 shrink-0 rounded-full bg-emerald-500 group-hover/menu-item:hidden group-focus-within/menu-item:hidden"
          aria-label="有未读结果"
          title="有未读结果"
        />
      </SidebarMenuButton>
      <span
        v-if="!editing"
        class="absolute right-1 top-1/2 flex -translate-y-1/2 items-center gap-0.5 opacity-0 transition-opacity group-hover/menu-item:opacity-100 group-focus-within/menu-item:opacity-100"
      >
        <button
          type="button"
          class="rounded p-1 text-sidebar-foreground/60 hover:bg-sidebar-accent hover:text-sidebar-foreground disabled:cursor-not-allowed disabled:opacity-40"
          title="复制会话"
          aria-label="复制会话"
          :disabled="running || needsAttention"
          @click="requestFork"
        >
          <GitForkIcon class="size-3.5" />
        </button>
        <button
          type="button"
          class="rounded p-1 text-sidebar-foreground/60 hover:bg-sidebar-accent hover:text-sidebar-foreground"
          :title="session.pinned ? '取消置顶' : '置顶'"
          @click="togglePin"
        >
          <PinIcon v-if="!session.pinned" class="size-3.5" />
          <PinOffIcon v-else class="size-3.5 text-primary" />
        </button>
        <button
          type="button"
          class="rounded p-1 text-sidebar-foreground/60 hover:bg-destructive/15 hover:text-destructive"
          title="删除"
          @click="requestDelete"
        >
          <Trash2Icon class="size-3.5" />
        </button>
      </span>
    </div>
  </SidebarMenuItem>
</template>
