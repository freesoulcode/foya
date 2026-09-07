<script setup lang="ts">
import { ref } from "vue";
import {
  api,
  type SearchProviderConfig,
  type WebSearchResult,
  type WebSearchSettings as WebSearchSettingsData,
} from "@/lib/api";
import SettingsPage from "@/layouts/settings/SettingsPage.vue";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

const webSettings = ref<WebSearchSettingsData>({ enabled: true, providers: [] });
const searchProviderKind = ref<"duckduckgo" | SearchProviderConfig["kind"]>("duckduckgo");
const searchAPIKey = ref("");
const searchEngineID = ref("");
const searchEndpoint = ref("");
const webTestQuery = ref("Foya agent");
const webTestResults = ref<WebSearchResult[]>([]);
const loading = ref(false);
const saving = ref(false);
const error = ref("");

async function loadWebSearch() {
  loading.value = true;
  error.value = "";
  try {
    webSettings.value = await api.getWebSearchSettings();
    const selected = webSettings.value.providers.find(
      (item) => item.id === webSettings.value.default_provider
    );
    selectSearchProvider(selected?.kind ?? "duckduckgo");
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

function selectSearchProvider(kind: "duckduckgo" | SearchProviderConfig["kind"]) {
  searchProviderKind.value = kind;
  const existing = webSettings.value.providers.find((item) => item.kind === kind);
  searchAPIKey.value = "";
  searchEngineID.value = existing?.search_engine_id ?? "";
  searchEndpoint.value = existing?.endpoint ?? "";
}

function selectSearchProviderValue(value: unknown) {
  if (
    value === "duckduckgo" ||
    value === "google_cse" ||
    value === "bing" ||
    value === "baidu"
  ) {
    selectSearchProvider(value);
  }
}

async function saveWebSearch() {
  saving.value = true;
  error.value = "";
  try {
    const kind = searchProviderKind.value;
    const providers = [...webSettings.value.providers];
    let defaultProvider = "";
    if (kind !== "duckduckgo") {
      const existing = providers.find((item) => item.kind === kind);
      const id = existing?.id ?? kind.replace("_cse", "");
      const provider: SearchProviderConfig = {
        id,
        kind,
        name:
          kind === "google_cse" ? "Google" : kind === "bing" ? "Bing" : "Baidu",
        enabled: true,
        ...(searchAPIKey.value.trim()
          ? { api_key: searchAPIKey.value.trim() }
          : {}),
        ...(kind === "google_cse"
          ? { search_engine_id: searchEngineID.value.trim() }
          : {}),
        ...(kind === "bing" && searchEndpoint.value.trim()
          ? { endpoint: searchEndpoint.value.trim() }
          : {}),
      };
      const index = providers.findIndex((item) => item.id === id);
      if (index >= 0) providers[index] = provider;
      else providers.push(provider);
      defaultProvider = id;
    }
    webSettings.value = await api.updateWebSearchSettings({
      enabled: webSettings.value.enabled,
      default_provider: defaultProvider,
      providers,
    });
    searchAPIKey.value = "";
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

async function testWebSearch() {
  await saveWebSearch();
  if (error.value) return;
  const id = webSettings.value.default_provider || "duckduckgo";
  loading.value = true;
  error.value = "";
  try {
    webTestResults.value = await api.testWebSearch(id, webTestQuery.value);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

void loadWebSearch();
</script>

<template>
  <SettingsPage
    :title="$t('Web search')"
    :description="$t('Configure search providers and test queries available to the agent.')"
  >
    <div class="max-w-4xl space-y-5">
      <label class="flex items-center justify-between border-b border-border py-3">
        <span class="text-sm font-medium">{{ $t("Enable web search") }}</span>
        <Checkbox
          :checked="webSettings.enabled"
          :aria-label="$t('Enable web search')"
          @update:checked="webSettings.enabled = $event === true"
        />
      </label>
      <div class="space-y-1.5">
        <Label for="search-provider">{{ $t("Search engine") }}</Label>
        <Select
          :model-value="searchProviderKind"
          @update:model-value="selectSearchProviderValue"
        >
          <SelectTrigger id="search-provider" class="w-full">
            <SelectValue :placeholder="$t('Select search engine')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="duckduckgo">DuckDuckGo</SelectItem>
            <SelectItem value="google_cse">Google</SelectItem>
            <SelectItem value="bing">Bing</SelectItem>
            <SelectItem value="baidu">Baidu</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <template v-if="searchProviderKind === 'google_cse'">
        <div class="space-y-1.5">
          <Label for="google-cx">Search Engine ID</Label>
          <Input id="google-cx" v-model="searchEngineID" placeholder="cx" />
        </div>
        <div class="space-y-1.5">
          <Label for="google-key">API Key</Label>
          <Input
            id="google-key"
            v-model="searchAPIKey"
            type="password"
            :placeholder="$t('Leave blank to keep the current value')"
          />
        </div>
      </template>

      <template v-else-if="searchProviderKind === 'bing'">
        <div class="space-y-1.5">
          <Label for="bing-key">API Key</Label>
          <Input
            id="bing-key"
            v-model="searchAPIKey"
            type="password"
            :placeholder="$t('Leave blank to keep the current value')"
          />
        </div>
        <div class="space-y-1.5">
          <Label for="bing-endpoint">Endpoint</Label>
          <Input
            id="bing-endpoint"
            v-model="searchEndpoint"
            class="font-mono"
            placeholder="https://api.bing.microsoft.com/v7.0/search"
          />
        </div>
      </template>

      <div class="flex justify-end">
        <Button :disabled="saving" @click="saveWebSearch">
          {{ saving ? $t("Saving") : $t("Save") }}
        </Button>
      </div>
      <div class="space-y-2 border-t border-border pt-4">
        <Label for="web-test-query">{{ $t("Test query") }}</Label>
        <div class="flex gap-2">
          <Input id="web-test-query" v-model="webTestQuery" />
          <Button
            variant="outline"
            :disabled="loading || !webTestQuery.trim()"
            @click="testWebSearch"
          >
            {{ $t("Test") }}
          </Button>
        </div>
        <a
          v-for="result in webTestResults"
          :key="result.url"
          :href="result.url"
          target="_blank"
          rel="noreferrer"
          class="block border-b border-border py-2 text-sm hover:text-primary"
        >
          <span class="font-medium">{{ result.title }}</span>
          <span class="block truncate text-xs text-muted-foreground">{{ result.url }}</span>
        </a>
      </div>
      <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
    </div>
  </SettingsPage>
</template>
