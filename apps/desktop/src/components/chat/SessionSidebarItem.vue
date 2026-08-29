<script setup lang="ts">
import { nextTick, ref } from "vue";
import {
  Loader2Icon,
  MessageSquareIcon,
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
}>();

const emit = defineEmits<{
  (event: "select", id: string): void;
  (event: "rename", id: string, title: string): void;
  (event: "pin", id: string, pinned: boolean): void;
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
        class="pr-14"
        @click="!editing && emit('select', session.id)"
      >
        <MessageSquareIcon />
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
        <Loader2Icon
          v-if="running && !editing"
          class="ml-auto size-3.5 animate-spin text-primary transition-opacity group-hover/menu-item:opacity-0"
          aria-label="AI 正在运行"
        />
      </SidebarMenuButton>
      <span
        v-if="!editing"
        class="absolute right-1 top-1/2 flex -translate-y-1/2 items-center gap-0.5 opacity-0 transition-opacity group-hover/menu-item:opacity-100 group-focus-within/menu-item:opacity-100"
      >
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
