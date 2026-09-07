<script setup lang="ts">
import { computed, ref, nextTick } from "vue";
import { useI18n } from "vue-i18n";
import { PanelRightIcon } from "@lucide/vue";
import { SidebarTrigger, useSidebar } from "@/components/ui/sidebar";
import WindowControls from "@/components/WindowControls.vue";
import { usePlatform } from "@/composables/usePlatform";
import { useWorkbar } from "@/composables/useWorkbar";
import type { Session } from "@/lib/api";
import ExternalEditorButton from "@/components/workbar/ExternalEditorButton.vue";

const props = defineProps<{
  session?: Session;
  projectPath?: string;
}>();
const { t } = useI18n();

const emit = defineEmits<{
  (e: "rename", id: string, title: string): void;
}>();

const { isMac, showCustomWindowControls } = usePlatform();
const { state } = useSidebar();
const { open: workbarOpen, toggle: toggleWorkbar } = useWorkbar();

const isCollapsed = computed(() => state.value === "collapsed");

const displayTitle = computed(() => props.session?.title || t("New chat"));

// Keep the title non-draggable so double-click rename works in the drag region.
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
    class="flex h-11 shrink-0 items-center gap-2 pr-2"
    :class="isMac && isCollapsed ? 'pl-[72px]' : 'pl-2'"
  >
    <SidebarTrigger
      v-if="isCollapsed"
      class="no-drag text-muted-foreground"
    />

    <!-- The empty header area is draggable; the title remains interactive. -->
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

    <div class="no-drag flex h-full items-center">
      <ExternalEditorButton
        v-if="projectPath"
        :project-path="projectPath"
      />
      <button
        v-if="!workbarOpen"
        type="button"
        class="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        :title="$t('Open side workspace')"
        :aria-label="$t('Open side workspace')"
        @click="toggleWorkbar"
      >
        <PanelRightIcon class="size-4" />
      </button>
      <WindowControls v-if="!workbarOpen && showCustomWindowControls" />
    </div>
  </div>
</template>
