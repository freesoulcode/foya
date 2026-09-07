<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { FolderOpenIcon, LoaderCircleIcon } from "@lucide/vue";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

const props = withDefaults(
  defineProps<{
    open: boolean;
    busy?: boolean;
    error?: string;
  }>(),
  {
    busy: false,
    error: "",
  }
);
const { t } = useI18n();

const emit = defineEmits<{
  "update:open": [open: boolean];
  confirm: [input: { name: string; path: string }];
}>();

const name = ref("");
const path = ref("");
const pickerError = ref("");

watch(
  () => props.open,
  (open) => {
    if (open) {
      name.value = "";
      path.value = "";
      pickerError.value = "";
    }
  }
);

function setOpen(open: boolean) {
  if (!open && props.busy) return;
  emit("update:open", open);
}

async function chooseFolder() {
  pickerError.value = "";
  try {
    const picked = await api.pickFolder();
    if (picked && !Array.isArray(picked)) path.value = picked;
  } catch (cause) {
    pickerError.value = String(cause);
  }
}

function submit() {
  const sourcePath = path.value.trim();
  if (!sourcePath || props.busy) return;
  emit("confirm", {
    name: name.value.trim() || t("New project"),
    path: sourcePath,
  });
}
</script>

<template>
  <Dialog :open="open" @update:open="setOpen">
    <DialogContent class="max-w-md">
      <DialogHeader>
        <DialogTitle>{{ $t("Add project") }}</DialogTitle>
        <DialogDescription class="sr-only">
          {{ $t("Create a project and select its source folder") }}
        </DialogDescription>
      </DialogHeader>

      <form class="space-y-4" @submit.prevent="submit">
        <div class="space-y-1.5">
          <Label for="project-create-name">{{ $t("Project name") }}</Label>
          <Input
            id="project-create-name"
            v-model="name"
            :placeholder="$t('New project')"
            autocomplete="off"
            :disabled="busy"
          />
        </div>

        <div class="space-y-1.5">
          <Label for="project-create-path">{{ $t("Source folder") }}</Label>
          <div class="flex gap-2">
            <Input
              id="project-create-path"
              :model-value="path"
              class="font-mono text-xs"
              :placeholder="$t('Select folder')"
              readonly
              :disabled="busy"
            />
            <Button
              type="button"
              variant="outline"
              class="shrink-0"
              :disabled="busy"
              @click="chooseFolder"
            >
              <FolderOpenIcon class="size-4" />
              {{ $t("Select") }}
            </Button>
          </div>
        </div>

        <p
          v-if="pickerError || error"
          class="text-sm text-destructive"
          role="alert"
        >
          {{ pickerError || error }}
        </p>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            :disabled="busy"
            @click="setOpen(false)"
          >
            {{ $t("Cancel") }}
          </Button>
          <Button type="submit" :disabled="busy || !path.trim()">
            <LoaderCircleIcon v-if="busy" class="size-4 animate-spin" />
            {{ busy ? $t("Creating") : $t("Create project") }}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
