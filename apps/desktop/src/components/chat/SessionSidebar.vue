<script setup lang="ts">
import { computed, ref, nextTick } from "vue";
import {
  PlusIcon,
  MessageSquareIcon,
  SettingsIcon,
  PinIcon,
  PinOffIcon,
  Trash2Icon,
  Loader2Icon,
} from "@lucide/vue";
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
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import type { Session } from "@/lib/api";

const props = defineProps<{
  isMac: boolean;
  sessions: Session[];
  activeId: string;
  isDraft?: boolean;
  // 正在运行 AI 回合的会话 id 集合,侧边栏据此显示加载动画。
  running?: Record<string, boolean>;
}>();

function isRunning(id: string) {
  return !!props.running?.[id];
}

const emit = defineEmits<{
  (e: "new"): void;
  (e: "select", id: string): void;
  (e: "rename", id: string, title: string): void;
  (e: "pin", id: string, pinned: boolean): void;
  (e: "delete", id: string): void;
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

function onPin(s: Session, e: Event) {
  e.stopPropagation();
  emit("pin", s.id, !s.pinned);
}

// 删除确认:点击删除只是打开应用内 Dialog,确认后才真正发出 delete 事件。
const pendingDelete = ref<Session | null>(null);

function onDelete(s: Session, e: Event) {
  e.stopPropagation();
  pendingDelete.value = s;
}

function confirmDelete() {
  if (pendingDelete.value) emit("delete", pendingDelete.value.id);
  pendingDelete.value = null;
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
  const byUpdated = (a: Session, b: Session) =>
    b.updated_at.localeCompare(a.updated_at);
  const pinned = props.sessions
    .filter((s) => s.pinned)
    .sort((a, b) =>
      (b.pinned_at ?? b.updated_at).localeCompare(a.pinned_at ?? a.updated_at)
    );
  const map = new Map<string, Session[]>();
  if (pinned.length > 0) map.set("置顶", pinned);
  for (const s of [...props.sessions].filter((s) => !s.pinned).sort(byUpdated)) {
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
        <div class="flex h-9 items-center px-2 text-lg font-semibold text-sidebar-foreground">
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
            <SidebarMenuItem
              v-for="s in items"
              :key="s.id"
              class="group/menu-item"
            >
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
                  class="min-w-0 flex-1 truncate"
                  @dblclick.stop="startRename(s)"
                  >{{ title(s) }}</span
                >
                <span
                  v-if="editingId !== s.id"
                  class="relative flex h-6 w-12 shrink-0 items-center justify-end"
                >
                  <Loader2Icon
                    v-if="isRunning(s.id)"
                    class="mr-1 size-3.5 animate-spin text-primary transition-opacity group-hover/menu-item:opacity-0"
                    aria-label="AI 正在运行"
                  />
                  <span
                    class="absolute right-0 flex items-center gap-0.5 opacity-0 transition-opacity group-hover/menu-item:opacity-100"
                  >
                    <button
                      class="rounded p-1 text-sidebar-foreground/60 hover:bg-sidebar-accent hover:text-sidebar-foreground"
                      :title="s.pinned ? '取消置顶' : '置顶'"
                      @click="onPin(s, $event)"
                    >
                      <PinIcon v-if="!s.pinned" class="size-3.5" />
                      <PinOffIcon v-else class="size-3.5 text-primary" />
                    </button>
                    <button
                      class="rounded p-1 text-sidebar-foreground/60 hover:bg-destructive/15 hover:text-destructive"
                      title="删除"
                      @click="onDelete(s, $event)"
                    >
                      <Trash2Icon class="size-3.5" />
                    </button>
                  </span>
                </span>
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

  <Dialog :open="pendingDelete !== null" @update:open="(v) => !v && (pendingDelete = null)">
    <DialogContent class="max-w-md">
      <DialogHeader>
        <div class="flex items-center gap-2">
          <Trash2Icon class="size-5 text-destructive" />
          <DialogTitle>删除对话</DialogTitle>
        </div>
        <DialogDescription>
          确定删除对话「{{ pendingDelete ? title(pendingDelete) : "" }}」吗？此操作不可撤销。
        </DialogDescription>
      </DialogHeader>
      <DialogFooter class="gap-2">
        <Button variant="outline" @click="pendingDelete = null">取消</Button>
        <Button variant="destructive" @click="confirmDelete">删除</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
