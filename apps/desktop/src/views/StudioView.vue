<script setup lang="ts">
import { ImageIcon, LoaderCircleIcon, PlusIcon } from "@lucide/vue";
import StudioCanvas from "@/components/studio/StudioCanvas.vue";
import { Button } from "@/components/ui/button";
import { useStudioWorkspace } from "@/composables/useStudioWorkspace";

const {
  activeProject,
  creating,
  createProject,
  updateProject,
} = useStudioWorkspace();
</script>

<template>
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
        <h1 class="mt-5 text-xl font-semibold">{{ $t("Creative workspace") }}</h1>
        <p class="mt-2 text-sm leading-6 text-muted-foreground">
          {{ $t("Select a project from the sidebar or create one to start.") }}
        </p>
        <Button class="mt-6" :disabled="creating" @click="createProject">
          <LoaderCircleIcon v-if="creating" class="size-4 animate-spin" />
          <PlusIcon v-else class="size-4" />
          {{ $t("New project") }}
        </Button>
      </div>
    </div>
  </main>
</template>
