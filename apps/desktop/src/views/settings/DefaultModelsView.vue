<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { SaveIcon } from "@lucide/vue";
import SettingsPage from "@/layouts/settings/SettingsPage.vue";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  api,
  type ConnectionConfig,
  type DefaultModels,
  type ModelRef,
} from "@/lib/api";

const emptyRef = (): ModelRef => ({ connection_id: "", model: "" });
const { t } = useI18n();
const defaults = ref<DefaultModels>({
  language: emptyRef(),
  fast: emptyRef(),
  image: emptyRef(),
  video: emptyRef(),
});
const connections = ref<ConnectionConfig[]>([]);
const loading = ref(true);
const saving = ref(false);
const error = ref("");

const groups = computed(() => ({
  language: connections.value.filter((item) => item.type === "language" && item.models?.length),
  image: connections.value.filter((item) => item.type === "image" && item.models?.length),
  video: connections.value.filter((item) => item.type === "video" && item.models?.length),
}));
const fields = computed(() => [
  { key: "language", label: t("Default language model"), type: "language" },
  { key: "fast", label: t("Default fast model"), type: "language" },
  { key: "image", label: t("Default image model"), type: "image" },
  { key: "video", label: t("Default video model"), type: "video" },
] as const);

function encode(refValue: ModelRef) {
  return refValue.connection_id && refValue.model
    ? `${refValue.connection_id}\u0000${refValue.model}`
    : "__none__";
}

function decode(value: unknown): ModelRef {
  if (value === "__none__") return emptyRef();
  const [connection_id = "", model = ""] = String(value ?? "").split("\u0000");
  return { connection_id, model };
}

async function load() {
  loading.value = true;
  error.value = "";
  try {
    [connections.value, defaults.value] = await Promise.all([
      api.listConnections(),
      api.getDefaultModels(),
    ]);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

async function save() {
  saving.value = true;
  error.value = "";
  try {
    defaults.value = await api.updateDefaultModels(defaults.value);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

onMounted(load);
</script>

<template>
  <SettingsPage
    :title="$t('Default models')"
    :description="$t('Configure global default models for each workflow type.')"
  >
    <template #actions>
      <Button :disabled="loading || saving" @click="save">
        <SaveIcon class="size-4" />
        {{ saving ? $t("Saving") : $t("Save") }}
      </Button>
    </template>

    <div class="max-w-3xl divide-y divide-border">
      <section
        v-for="field in fields"
        :key="field.key"
        class="grid gap-3 py-5 sm:grid-cols-[180px_minmax(0,1fr)] sm:items-center"
      >
        <Label>{{ field.label }}</Label>
        <Select
          :model-value="encode(defaults[field.key])"
          :disabled="loading"
          @update:model-value="defaults[field.key] = decode($event)"
        >
          <SelectTrigger class="w-full">
            <SelectValue :placeholder="$t('Select an imported model')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="__none__">{{ $t("Not set") }}</SelectItem>
            <SelectGroup v-for="connection in groups[field.type]" :key="connection.id">
              <SelectLabel>{{ connection.name }}</SelectLabel>
              <SelectItem
                v-for="model in connection.models"
                :key="`${connection.id}:${model}`"
                :value="`${connection.id}\u0000${model}`"
              >
                {{ model }}
              </SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
      </section>
      <p v-if="error" class="py-4 text-sm text-destructive">{{ error }}</p>
    </div>
  </SettingsPage>
</template>
