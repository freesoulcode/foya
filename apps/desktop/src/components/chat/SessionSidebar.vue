<script setup lang="ts">
import { computed, ref, nextTick } from "vue";
import { PlusIcon, MessageSquareIcon, SettingsIcon } from "@lucide/vue";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarTrigger,
} from "@/components/ui/sidebar";
import type { Session } from "@/lib/api";

const props = defineProps<{
  isMac: boolean;
  sessions: Session[];
  activeId: string;
  isDraft?: boolean;
}>();

const emit = defineEmits<{
  (e: "new"): void;
  (e: "select", id: string): void;
  (e: "rename", id: string, title: string): void;
  (e: "open-settings"): void;
}>();

function title(s: Session) {
  return s.title || "新对话";
}

// 双击改名:本地维护编辑态,回车提交、Esc 取消。
const editingId = ref("");
const editingText = ref("");
const editInput = ref<HTMLInputElement | null>(null);

async function startRename(s: Session) {
  editingId.value = s.id;
  editingText.value = s.title || "";
  await nextTick();
  editInput.value?.focus();
  editInput.value?.select();
}

function commitRename() {
  const id = editingId.value;
  const text = editingText.value.trim();
  editingId.value = "";
  if (id && text) emit("rename", id, text);
}

function cancelRename() {
  editingId.value = "";
}

function isSameDay(a: Date, b: Date) {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  );
}

function groupLabel(date: Date): string {
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const yesterday = new Date(today.getTime() - 86400000);
  const weekAgo = new Date(today.getTime() - 7 * 86400000);

  if (isSameDay(date, today)) return "今天";
  if (isSameDay(date, yesterday)) return "昨天";
  if (date >= weekAgo) return "前 7 天";
  return "更早";
}

const groups = computed(() => {
  const sorted = [...props.sessions].sort((a, b) =>
    b.updated_at.localeCompare(a.updated_at)
  );
  const map = new Map<string, Session[]>();
  for (const s of sorted) {
    const label = groupLabel(new Date(s.updated_at));
    if (!map.has(label)) map.set(label, []);
    map.get(label)!.push(s);
  }
  return Array.from(map.entries());
});
</script>

<template>
  <Sidebar collapsible="offcanvas">
    <SidebarHeader
      data-tauri-drag-region
      class="flex h-9 flex-row items-center gap-1 p-2"
      :class="isMac ? 'pl-[72px]' : ''"
    >
      <SidebarTrigger class="no-drag text-sidebar-foreground/70" />
    </SidebarHeader>

    <SidebarContent>
      <SidebarGroup class="p-2 pt-1 pb-0">
        <div class="flex h-7 items-center px-2 text-sm font-semibold tracking-tight text-sidebar-foreground">
          Foya
        </div>
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton
                class="no-drag"
                :is-active="isDraft"
                tooltip="新建对话"
                @click="emit('new')"
              >
                <PlusIcon />
                <span>新建对话</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>

      <SidebarGroup v-for="[label, items] in groups" :key="label" class="p-2 pt-1">
        <SidebarGroupLabel class="h-6 text-[11px]">{{ label }}</SidebarGroupLabel>
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem v-for="s in items" :key="s.id">
              <SidebarMenuButton
                :is-active="s.id === activeId"
                :tooltip="title(s)"
                @click="emit('select', s.id)"
              >
                <MessageSquareIcon />
                <input
                  v-if="editingId === s.id"
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
                  class="truncate"
                  @dblclick.stop="startRename(s)"
                  >{{ title(s) }}</span
                >
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
      <p
        v-if="sessions.length === 0"
        class="px-4 py-6 text-center text-xs text-muted-foreground"
      >
        暂无对话
      </p>
    </SidebarContent>

    <SidebarFooter class="p-2">
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton class="no-drag" tooltip="设置" @click="emit('open-settings')">
            <SettingsIcon />
            <span>设置</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </SidebarFooter>
  </Sidebar>
</template>
