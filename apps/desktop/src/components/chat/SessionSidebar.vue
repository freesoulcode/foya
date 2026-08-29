<script setup lang="ts">
import { computed, ref } from "vue";
import {
  ChevronDownIcon,
  ChevronRightIcon,
  FolderIcon,
  PlusIcon,
  SettingsIcon,
  Trash2Icon,
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
import SessionSidebarItem from "./SessionSidebarItem.vue";

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
  (e: "new", workspace?: string): void;
  (e: "select", id: string): void;
  (e: "rename", id: string, title: string): void;
  (e: "pin", id: string, pinned: boolean): void;
  (e: "delete", id: string): void;
  (e: "open-settings"): void;
}>();

function title(s: Session) {
  return s.title || "新对话";
}

// 删除确认:点击删除只是打开应用内 Dialog,确认后才真正发出 delete 事件。
const pendingDelete = ref<Session | null>(null);

function onDelete(s: Session) {
  pendingDelete.value = s;
}

function confirmDelete() {
  if (pendingDelete.value) emit("delete", pendingDelete.value.id);
  pendingDelete.value = null;
}

function normalizedWorkspace(workspace?: string): string {
  return workspace?.replace(/[\\/]+$/, "") ?? "";
}

function projectBaseName(workspace: string): string {
  const parts = normalizedWorkspace(workspace).split(/[\\/]/);
  return parts[parts.length - 1] || workspace;
}

function projectParentName(workspace: string): string {
  const parts = normalizedWorkspace(workspace).split(/[\\/]/);
  return parts.length > 1 ? parts[parts.length - 2] : "";
}

function sortSessions(items: Session[]): Session[] {
  return [...items].sort((a, b) => {
    if (Boolean(a.pinned) !== Boolean(b.pinned)) return a.pinned ? -1 : 1;
    const aTime = a.pinned ? a.pinned_at ?? a.updated_at : a.updated_at;
    const bTime = b.pinned ? b.pinned_at ?? b.updated_at : b.updated_at;
    return bTime.localeCompare(aTime);
  });
}

const ungroupedSessions = computed(() =>
  sortSessions(props.sessions.filter((session) => !session.workspace))
);

interface ProjectGroup {
  workspace: string;
  label: string;
  sessions: Session[];
  updatedAt: string;
}

const collapsedProjects = ref<Set<string>>(new Set());

function toggleProject(workspace: string) {
  const next = new Set(collapsedProjects.value);
  if (next.has(workspace)) next.delete(workspace);
  else next.add(workspace);
  collapsedProjects.value = next;
}

function newProjectSession(workspace: string) {
  const next = new Set(collapsedProjects.value);
  next.delete(workspace);
  collapsedProjects.value = next;
  emit("new", workspace);
}

function projectIsActive(project: ProjectGroup): boolean {
  return project.sessions.some((session) => session.id === props.activeId);
}

const projectGroups = computed<ProjectGroup[]>(() => {
  const grouped = new Map<string, Session[]>();
  for (const session of props.sessions) {
    const workspace = normalizedWorkspace(session.workspace);
    if (!workspace) continue;
    const items = grouped.get(workspace) ?? [];
    items.push(session);
    grouped.set(workspace, items);
  }

  const baseNameCounts = new Map<string, number>();
  for (const workspace of grouped.keys()) {
    const name = projectBaseName(workspace);
    baseNameCounts.set(name, (baseNameCounts.get(name) ?? 0) + 1);
  }

  return Array.from(grouped.entries())
    .map(([workspace, items]) => {
      const sorted = sortSessions(items);
      const baseName = projectBaseName(workspace);
      const parentName = projectParentName(workspace);
      return {
        workspace,
        label:
          (baseNameCounts.get(baseName) ?? 0) > 1 && parentName
            ? `${parentName}/${baseName}`
            : baseName,
        sessions: sorted,
        updatedAt: sorted.reduce(
          (latest, session) =>
            session.updated_at > latest ? session.updated_at : latest,
          ""
        ),
      };
    })
    .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
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
            <SessionSidebarItem
              v-for="session in ungroupedSessions"
              :key="session.id"
              :session="session"
              :active="session.id === activeId"
              :running="isRunning(session.id)"
              @select="emit('select', $event)"
              @rename="(id, value) => emit('rename', id, value)"
              @pin="(id, value) => emit('pin', id, value)"
              @delete="onDelete"
            />
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>

      <SidebarGroup v-if="projectGroups.length" class="gap-1 p-2 pt-3">
        <SidebarGroupLabel class="h-6 px-2 text-[11px]">
          项目
        </SidebarGroupLabel>
        <SidebarGroupContent class="space-y-2">
          <div
            v-for="project in projectGroups"
            :key="project.workspace"
            class="min-w-0"
          >
            <div
              :class="[
                'group/project flex h-8 min-w-0 items-center rounded-md transition-colors',
                projectIsActive(project)
                  ? 'bg-sidebar-accent/70 text-sidebar-accent-foreground'
                  : 'text-sidebar-foreground hover:bg-sidebar-accent/50',
              ]"
            >
              <button
                type="button"
                class="flex h-full min-w-0 flex-1 items-center gap-1.5 px-2 text-left text-sm font-medium"
                :title="project.workspace"
                :aria-expanded="!collapsedProjects.has(project.workspace)"
                @click="toggleProject(project.workspace)"
              >
                <ChevronRightIcon
                  v-if="collapsedProjects.has(project.workspace)"
                  class="size-3.5 shrink-0 text-sidebar-foreground/60"
                />
                <ChevronDownIcon
                  v-else
                  class="size-3.5 shrink-0 text-sidebar-foreground/60"
                />
                <FolderIcon class="size-4 shrink-0" />
                <span class="truncate">{{ project.label }}</span>
              </button>
              <button
                type="button"
                class="mr-1 flex size-6 shrink-0 items-center justify-center rounded-md text-sidebar-foreground/60 hover:bg-sidebar-accent hover:text-sidebar-foreground"
                :title="`在 ${project.label} 中新建对话`"
                :aria-label="`在 ${project.label} 中新建对话`"
                @click="newProjectSession(project.workspace)"
              >
                <PlusIcon class="size-3.5" />
              </button>
            </div>

            <div
              v-if="!collapsedProjects.has(project.workspace)"
              class="ml-[18px] border-l border-sidebar-border pb-1 pl-2 pt-1"
            >
              <SidebarMenu>
                <SessionSidebarItem
                  v-for="session in project.sessions"
                  :key="session.id"
                  :session="session"
                  :active="session.id === activeId"
                  :running="isRunning(session.id)"
                  @select="emit('select', $event)"
                  @rename="(id, value) => emit('rename', id, value)"
                  @pin="(id, value) => emit('pin', id, value)"
                  @delete="onDelete"
                />
              </SidebarMenu>
            </div>
          </div>
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
