<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import {
  ArrowLeftIcon,
  ImageIcon,
  LoaderCircleIcon,
  PlusIcon,
  Trash2Icon,
} from "@lucide/vue";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuAction,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar";
import { api, type CanvasDocument } from "@/lib/api";
import WindowControls from "@/components/WindowControls.vue";
import { usePlatform } from "@/composables/usePlatform";
import StudioCanvas from "./StudioCanvas.vue";

const emit = defineEmits<{
  (event: "close"): void;
}>();

const { isMac, showCustomWindowControls } = usePlatform();
const projects = ref<CanvasDocument[]>([]);
const sidebarOpen = ref(true);
const activeId = ref("");
const loading = ref(true);
const creating = ref(false);
const error = ref("");
const pendingDelete = ref<CanvasDocument | null>(null);

const activeProject = computed(
  () => projects.value.find((project) => project.id === activeId.value) ?? null
);
const isSidebarCollapsed = computed(() => !sidebarOpen.value);

async function loadProjects() {
  loading.value = true;
  try {
    projects.value = await api.listCanvases();
    if (activeId.value && !projects.value.some((item) => item.id === activeId.value)) {
      activeId.value = "";
    }
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason);
  } finally {
    loading.value = false;
  }
}

async function createProject() {
  if (creating.value) return;
  creating.value = true;
  try {
    const project = await api.createCanvas(`创作项目 ${projects.value.length + 1}`);
    projects.value = [project, ...projects.value];
    activeId.value = project.id;
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason);
  } finally {
    creating.value = false;
  }
}

async function deleteProject() {
  const project = pendingDelete.value;
  if (!project) return;
  try {
    await api.deleteCanvas(project.id);
    projects.value = projects.value.filter((item) => item.id !== project.id);
    if (activeId.value === project.id) activeId.value = "";
    pendingDelete.value = null;
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason);
  }
}

function updateProject(project: CanvasDocument) {
  const index = projects.value.findIndex((item) => item.id === project.id);
  if (index >= 0) projects.value[index] = project;
}

onMounted(() => void loadProjects());
</script>

<template>
  <SidebarProvider
    :open="sidebarOpen"
    class="h-svh min-h-0 bg-background"
    @update:open="sidebarOpen = $event"
  >
    <Sidebar collapsible="offcanvas" class="border-0">
      <SidebarHeader
        data-tauri-drag-region
        class="flex h-9 flex-row items-center gap-1 p-2"
        :class="isMac ? 'pl-[72px]' : ''"
      >
        <SidebarTrigger class="no-drag text-sidebar-foreground/70" />
      </SidebarHeader>

      <SidebarContent>
        <SidebarGroup class="p-2 pt-1">
          <div class="flex h-9 items-center justify-between px-2">
            <span class="truncate text-lg font-semibold text-sidebar-foreground">创作项目</span>
            <Button
              size="icon"
              variant="ghost"
              class="no-drag size-7"
              title="新建创作项目"
              :disabled="creating"
              @click="createProject"
            >
              <LoaderCircleIcon v-if="creating" class="size-4 animate-spin" />
              <PlusIcon v-else class="size-4" />
            </Button>
          </div>
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem
                v-for="project in projects"
                :key="project.id"
                class="group/project"
              >
                <SidebarMenuButton
                  :is-active="activeId === project.id"
                  :tooltip="project.title"
                  @click="activeId = project.id"
                >
                  <ImageIcon />
                  <span>{{ project.title }}</span>
                </SidebarMenuButton>
                <SidebarMenuAction
                  show-on-hover
                  title="删除项目"
                  @click.stop="pendingDelete = project"
                >
                  <Trash2Icon />
                </SidebarMenuAction>
              </SidebarMenuItem>
            </SidebarMenu>

            <div v-if="loading" class="flex items-center gap-2 px-2 py-3 text-xs text-sidebar-foreground/60">
              <LoaderCircleIcon class="size-4 animate-spin" />
              <span>正在读取项目</span>
            </div>

            <div v-else-if="projects.length === 0" class="px-2 py-3 text-xs text-sidebar-foreground/60">
              暂无项目
            </div>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>

      <SidebarFooter>
        <Button
          variant="ghost"
          class="no-drag w-full justify-start gap-2"
          title="返回对话"
          @click="emit('close')"
        >
          <ArrowLeftIcon class="size-4" />
          <span>返回对话</span>
        </Button>
      </SidebarFooter>
    </Sidebar>

    <SidebarInset class="min-h-0 overflow-hidden">
      <header
        data-tauri-drag-region
        class="absolute inset-x-0 top-0 z-30 flex h-9 items-center gap-2 pr-2"
        :class="isMac && isSidebarCollapsed ? 'pl-[72px]' : 'pl-2'"
      >
        <SidebarTrigger
          v-if="isSidebarCollapsed"
          class="no-drag text-foreground/70"
        />
        <div v-if="activeProject" data-tauri-drag-region class="flex min-w-0 flex-1 items-center">
          <span class="truncate text-sm font-medium">{{ activeProject.title }}</span>
        </div>
        <div v-else data-tauri-drag-region class="min-w-0 flex-1" />
        <div v-if="showCustomWindowControls" class="no-drag">
          <WindowControls />
        </div>
      </header>

      <main class="relative min-h-0 flex-1">
        <StudioCanvas
          v-if="activeProject"
          :key="activeProject.id"
          :project-id="activeProject.id"
          @project-change="updateProject"
        />
        <div v-else class="grid size-full place-items-center bg-muted/10 px-6">
          <div class="flex max-w-md flex-col items-center text-center">
            <ImageIcon class="size-8 text-muted-foreground/45" />
            <h1 class="mt-5 text-xl font-semibold">创作工作台</h1>
            <p class="mt-2 text-sm leading-6 text-muted-foreground">
              从左侧选择一个项目，或新建项目开始创作。
            </p>
            <Button class="mt-6" :disabled="creating" @click="createProject">
              <LoaderCircleIcon v-if="creating" class="size-4 animate-spin" />
              <PlusIcon v-else class="size-4" />
              新建项目
            </Button>
          </div>
        </div>
      </main>
    </SidebarInset>

    <p v-if="error" class="absolute bottom-3 left-1/2 z-50 -translate-x-1/2 rounded bg-destructive px-3 py-1.5 text-xs text-destructive-foreground">
      {{ error }}
    </p>

    <Dialog :open="Boolean(pendingDelete)" @update:open="(open) => { if (!open) pendingDelete = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>删除创作项目</DialogTitle>
          <DialogDescription>
            “{{ pendingDelete?.title }}”中的画布和素材将被永久删除。
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" @click="pendingDelete = null">取消</Button>
          <Button variant="destructive" @click="deleteProject">删除</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </SidebarProvider>
</template>
