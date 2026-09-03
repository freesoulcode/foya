<script setup lang="ts">
import { computed, ref, nextTick } from "vue";
import { PanelRightIcon } from "@lucide/vue";
import { SidebarTrigger, useSidebar } from "@/components/ui/sidebar";
import WindowControls from "@/components/WindowControls.vue";
import { usePlatform } from "@/composables/usePlatform";
import { useWorkbar } from "@/composables/useWorkbar";
import type { Session } from "@/lib/api";

const props = defineProps<{
  session?: Session;
}>();

const emit = defineEmits<{
  (e: "rename", id: string, title: string): void;
}>();

const { isMac, showCustomWindowControls } = usePlatform();
const { state } = useSidebar();
const { open: workbarOpen, toggle: toggleWorkbar } = useWorkbar();

const isCollapsed = computed(() => state.value === "collapsed");

const displayTitle = computed(() => props.session?.title || "新对话");

// 双击改名:标题文字标记 no-drag,避免与窗口拖拽冲突。
const editing = ref(false);
const editText = ref("");
const inputEl = ref<HTMLInputElement | null>(null);

async function startEdit() {
  if (!props.session) return;
  editing.value = true;
  editText.value = props.session.title || "";
  await nextTick();
  inputEl.value?.focus();
  inputEl.value?.select();
}
function commit() {
  const id = props.session?.id;
  const text = editText.value.trim();
  editing.value = false;
  if (id && text) emit("rename", id, text);
}
function cancel() {
  editing.value = false;
}
</script>

<template>
  <div
    data-tauri-drag-region
    class="flex h-9 shrink-0 items-center gap-2 pr-2"
    :class="isMac && isCollapsed ? 'pl-[72px]' : 'pl-2'"
  >
    <SidebarTrigger
      v-if="isCollapsed"
      class="no-drag text-muted-foreground"
    />

    <!-- 会话标题,靠左;容器空白区可拖,文字本身 no-drag 可双击改名 -->
    <div data-tauri-drag-region class="flex min-w-0 flex-1 items-center">
      <input
        v-if="editing"
        ref="inputEl"
        v-model="editText"
        class="no-drag min-w-0 max-w-xs rounded-md bg-transparent px-1.5 py-0.5 text-sm font-medium outline-none ring-1 ring-ring"
        @keydown.enter.prevent="commit"
        @keydown.esc.prevent="cancel"
        @blur="commit"
      />
      <button
        v-else
        type="button"
        class="no-drag flex min-w-0 items-center rounded-md px-1.5 py-0.5"
        :title="displayTitle"
        @dblclick="startEdit"
      >
        <span class="truncate text-sm font-medium">{{ displayTitle }}</span>
      </button>
    </div>

    <div v-if="!workbarOpen" class="no-drag flex h-full items-center">
      <button
        type="button"
        class="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        title="打开右侧工作区"
        aria-label="打开右侧工作区"
        @click="toggleWorkbar"
      >
        <PanelRightIcon class="size-4" />
      </button>
      <WindowControls v-if="showCustomWindowControls" />
    </div>
  </div>
</template>
