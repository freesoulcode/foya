<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import {
  CheckIcon,
  ChevronDownIcon,
  Code2Icon,
  LoaderCircleIcon,
} from "@lucide/vue";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { api, type ExternalEditor } from "@/lib/api";

const props = defineProps<{
  projectPath: string;
}>();

const EDITOR_KEY = "foya-external-editor-v1";
const editors = ref<ExternalEditor[]>([]);
const selectedID = ref(localStorage.getItem(EDITOR_KEY) ?? "");
const loading = ref(true);
const opening = ref(false);
const error = ref("");

const selectedEditor = computed(
  () =>
    editors.value.find((editor) => editor.id === selectedID.value) ??
    editors.value[0]
);

async function loadEditors() {
  loading.value = true;
  error.value = "";
  try {
    editors.value = await api.listExternalEditors();
    if (
      editors.value.length > 0 &&
      !editors.value.some((editor) => editor.id === selectedID.value)
    ) {
      selectedID.value = editors.value[0].id;
    }
  } catch (cause) {
    editors.value = [];
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

async function openProject(editor = selectedEditor.value) {
  if (!editor || opening.value) return;
  opening.value = true;
  error.value = "";
  try {
    await api.openProjectInExternalEditor(props.projectPath, editor.id);
    selectedID.value = editor.id;
    localStorage.setItem(EDITOR_KEY, editor.id);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    opening.value = false;
  }
}

onMounted(() => void loadEditors());
</script>

<template>
  <div
    v-if="loading || editors.length"
    class="mr-1 flex h-8 shrink-0 overflow-hidden rounded-md border border-border bg-background"
    :title="error || undefined"
  >
    <button
      type="button"
      class="flex min-w-0 items-center gap-2 px-2.5 text-sm font-medium transition-colors hover:bg-muted disabled:opacity-50"
      :disabled="loading || opening || !selectedEditor"
      :aria-label="
        selectedEditor
          ? `使用 ${selectedEditor.name} 打开项目`
          : '正在查找外部编辑器'
      "
      @click="openProject()"
    >
      <span class="flex size-4 shrink-0 items-center justify-center overflow-hidden rounded-[4px] bg-foreground text-background">
        <LoaderCircleIcon v-if="loading || opening" class="size-2.5 animate-spin" />
        <img
          v-else-if="selectedEditor?.icon_data_url"
          :src="selectedEditor.icon_data_url"
          alt=""
          class="size-full object-contain"
        />
        <Code2Icon v-else class="size-2.5" />
      </span>
      <span>打开</span>
    </button>

    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <button
          type="button"
          class="flex w-8 items-center justify-center border-l border-border text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
          :disabled="loading || opening || !editors.length"
          aria-label="选择外部编辑器"
        >
          <ChevronDownIcon class="size-4" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        side="bottom"
        align="end"
        :side-offset="6"
        class="w-52"
      >
        <DropdownMenuItem
          v-for="editor in editors"
          :key="editor.id"
          class="gap-2"
          @select="openProject(editor)"
        >
          <img
            v-if="editor.icon_data_url"
            :src="editor.icon_data_url"
            alt=""
            class="size-5 shrink-0 object-contain"
          />
          <Code2Icon v-else class="size-4 text-muted-foreground" />
          <span class="min-w-0 flex-1 truncate">{{ editor.name }}</span>
          <CheckIcon
            v-if="editor.id === selectedEditor?.id"
            class="size-4 text-primary"
          />
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  </div>
</template>
