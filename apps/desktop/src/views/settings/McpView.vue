<script setup lang="ts">
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  BlocksIcon,
  DownloadIcon,
  FileJsonIcon,
  PlusIcon,
  RefreshCwIcon,
  SearchIcon,
  Trash2Icon,
} from "@lucide/vue";
import {
  api,
  type McpConfig,
  type McpRegistryServer,
  type McpServerConfig,
  type McpStatus,
} from "@/lib/api";
import SettingsPage from "@/layouts/settings/SettingsPage.vue";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

const mcpConfig = ref<McpConfig>({ version: 1, servers: [] });
const { t } = useI18n();
const mcpStatuses = ref<McpStatus[]>([]);
const pendingMcpDelete = ref<McpServerConfig | null>(null);
const mcpAddOpen = ref(false);
const mcpAddMode = ref<"market" | "json">("market");
const mcpJSON = ref("");
const mcpAddError = ref("");
const mcpMarketQuery = ref("");
const mcpMarketLoading = ref(false);
const mcpMarketResults = ref<McpRegistryServer[]>([]);
const loading = ref(false);
const saving = ref(false);
const error = ref("");

async function loadMcp() {
  loading.value = true;
  error.value = "";
  try {
    [mcpConfig.value, mcpStatuses.value] = await Promise.all([
      api.getMcpConfig(),
      api.getMcpStatus(),
    ]);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

function mcpStateLabel(state?: McpStatus["state"]): string {
  const keys: Record<McpStatus["state"], string> = {
    connected: "Connected",
    connecting: "Connecting",
    disconnected: "Disconnected",
    disabled: "Disabled",
    error: "Connection failed",
  };
  return t(keys[state ?? "disconnected"]);
}

function mcpStateClass(state?: McpStatus["state"]): string {
  if (state === "connected") return "bg-emerald-500";
  if (state === "connecting") return "bg-amber-500";
  if (state === "error") return "bg-destructive";
  return "bg-muted-foreground/40";
}

function mcpStatus(serverID: string): McpStatus | undefined {
  return mcpStatuses.value.find((item) => item.id === serverID);
}

async function updateMcpServers(servers: McpServerConfig[]) {
  saving.value = true;
  error.value = "";
  try {
    mcpConfig.value = await api.updateMcpConfig({ version: 1, servers });
    mcpStatuses.value = await api.getMcpStatus();
  } catch (cause) {
    error.value = String(cause);
    throw cause;
  } finally {
    saving.value = false;
  }
}

async function toggleMcpServer(server: McpServerConfig) {
  await updateMcpServers(
    mcpConfig.value.servers.map((item) =>
      item.id === server.id ? { ...item, enabled: !item.enabled } : item
    )
  ).catch(() => undefined);
}

async function deleteMcpServer() {
  const server = pendingMcpDelete.value;
  if (!server) return;
  try {
    await updateMcpServers(
      mcpConfig.value.servers.filter((item) => item.id !== server.id)
    );
    pendingMcpDelete.value = null;
  } catch {}
}

function openMcpAdd(mode: "market" | "json" = "market") {
  mcpAddMode.value = mode;
  mcpAddError.value = "";
  mcpAddOpen.value = true;
  if (mode === "market" && mcpMarketResults.value.length === 0) {
    void searchMcpMarket();
  }
}

function stringRecord(value: unknown): Record<string, string> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const entries = Object.entries(value as Record<string, unknown>)
    .filter(([, item]) => typeof item === "string") as Array<[string, string]>;
  return entries.length ? Object.fromEntries(entries) : undefined;
}

function normalizeMcpServer(id: string, value: unknown): McpServerConfig {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(t("Invalid configuration format for {id}", { id }));
  }
  const item = value as Record<string, unknown>;
  const command = typeof item.command === "string" ? item.command : "";
  const url = typeof item.url === "string" ? item.url : "";
  const rawTransport =
    typeof item.transport === "string"
      ? item.transport
      : typeof item.type === "string"
        ? item.type
        : "";
  const transport = command
    ? "stdio"
    : rawTransport.replace("-", "_") === "sse"
      ? "sse"
      : "streamable_http";
  if (!command && !url) {
    throw new Error(t("{id} is missing command or url", { id }));
  }
  return {
    id,
    name: typeof item.name === "string" ? item.name : id,
    enabled: item.enabled !== false,
    transport,
    ...(command
      ? {
          command,
          args: Array.isArray(item.args)
            ? item.args.filter((arg): arg is string => typeof arg === "string")
            : [],
          cwd: typeof item.cwd === "string" ? item.cwd : undefined,
          env: stringRecord(item.env),
        }
      : {
          url,
          headers: stringRecord(item.headers),
          bearer_token:
            typeof item.bearer_token === "string" ? item.bearer_token : undefined,
        }),
  };
}

function parseMcpJSON(source: string): McpServerConfig[] {
  const parsed = JSON.parse(source) as unknown;
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error(t("The top level of JSON must be an object"));
  }
  const root = parsed as Record<string, unknown>;
  if (Array.isArray(root.servers)) {
    return root.servers.map((item, index) => {
      const value = item as Record<string, unknown>;
      const id = typeof value.id === "string" ? value.id : `server-${index + 1}`;
      return normalizeMcpServer(id, value);
    });
  }
  const map =
    root.mcpServers && typeof root.mcpServers === "object"
      ? (root.mcpServers as Record<string, unknown>)
      : root.servers && typeof root.servers === "object"
        ? (root.servers as Record<string, unknown>)
        : root;
  return Object.entries(map).map(([id, value]) => normalizeMcpServer(id, value));
}

async function addMcpFromJSON() {
  mcpAddError.value = "";
  try {
    const incoming = parseMcpJSON(mcpJSON.value);
    if (incoming.length === 0) throw new Error(t("JSON contains no MCP servers"));
    const merged = new Map(mcpConfig.value.servers.map((item) => [item.id, item]));
    for (const item of incoming) merged.set(item.id, item);
    await updateMcpServers(Array.from(merged.values()));
    mcpJSON.value = "";
    mcpAddOpen.value = false;
  } catch (cause) {
    mcpAddError.value = String(cause);
  }
}

async function searchMcpMarket() {
  mcpMarketLoading.value = true;
  mcpAddError.value = "";
  try {
    mcpMarketResults.value = await api.searchMcpRegistry(mcpMarketQuery.value);
  } catch (cause) {
    mcpAddError.value = String(cause);
  } finally {
    mcpMarketLoading.value = false;
  }
}

function mcpInstalled(id: string): boolean {
  return mcpConfig.value.servers.some((item) => item.id === id);
}

async function installMarketMcp(item: McpRegistryServer) {
  if (!item.installable || mcpInstalled(item.id)) return;
  mcpAddError.value = "";
  try {
    await updateMcpServers([...mcpConfig.value.servers, item.config]);
  } catch (cause) {
    mcpAddError.value = String(cause);
  }
}

void loadMcp();
</script>

<template>
  <SettingsPage
    :title="$t('MCP servers')"
    :description="$t('Manage external tool servers available to the agent.')"
  >
    <template #actions>
      <Button
        size="icon-sm"
        variant="ghost"
        :disabled="loading || saving"
        :title="$t('Refresh status')"
        :aria-label="$t('Refresh MCP status')"
        @click="loadMcp"
      >
        <RefreshCwIcon
          class="size-4"
          :class="loading && 'animate-spin'"
        />
      </Button>
      <Button
        size="sm"
        variant="outline"
        :disabled="saving"
        @click="openMcpAdd()"
      >
        <PlusIcon class="size-4" />
        {{ $t("Add server") }}
      </Button>
    </template>

    <div class="min-h-0 flex-1 border-y border-border">
      <section class="flex h-full min-w-0 flex-col">
        <div class="flex h-11 items-center border-b border-border px-3 text-sm font-medium">
          {{ $t("Installed") }}
        </div>
        <div class="min-h-0 flex-1 divide-y divide-border overflow-y-auto">
          <div
            v-for="server in mcpConfig.servers"
            :key="server.id"
            class="flex min-h-16 items-center gap-3 px-3 py-2"
          >
            <span
              class="size-2 shrink-0 rounded-full"
              :class="mcpStateClass(mcpStatus(server.id)?.state)"
            />
            <span class="min-w-0 flex-1">
              <span class="block truncate text-sm font-medium">
                {{ server.name || server.id }}
              </span>
              <span class="block truncate text-xs text-muted-foreground">
                {{ mcpStateLabel(mcpStatus(server.id)?.state) }}
                <template v-if="mcpStatus(server.id)?.state === 'connected'">
                  · {{ $t("{count} tools", { count: mcpStatus(server.id)?.tool_count ?? 0 }) }}
                </template>
              </span>
            </span>
            <label class="flex shrink-0 items-center gap-2 text-xs text-muted-foreground">
              <Checkbox
                :checked="server.enabled"
                :disabled="saving"
                :aria-label="server.enabled ? $t('Disable {name}', { name: server.name }) : $t('Enable {name}', { name: server.name })"
                @update:checked="toggleMcpServer(server)"
              />
            </label>
            <Button
              size="icon-sm"
              variant="ghost"
              class="text-muted-foreground hover:text-destructive"
              :disabled="saving"
              :title="$t('Delete server')"
              @click="pendingMcpDelete = server"
            >
              <Trash2Icon class="size-4" />
            </Button>
          </div>
          <p
            v-if="!loading && mcpConfig.servers.length === 0"
            class="px-3 py-8 text-center text-sm text-muted-foreground"
          >
            {{ $t("No MCP servers") }}
          </p>
        </div>
      </section>
    </div>
    <p v-if="error" class="mt-4 text-sm text-destructive">{{ error }}</p>
  </SettingsPage>

  <Dialog v-model:open="mcpAddOpen">
    <DialogContent class="flex max-h-[78vh] max-w-2xl flex-col gap-0 p-0">
      <DialogHeader class="border-b border-border px-5 py-4">
        <DialogTitle>{{ $t("Add MCP") }}</DialogTitle>
        <DialogDescription class="sr-only">
          {{ $t("Install from the marketplace or import an MCP JSON configuration") }}
        </DialogDescription>
      </DialogHeader>

      <div class="flex items-center gap-1 border-b border-border px-5 py-2">
        <Button
          size="sm"
          :variant="mcpAddMode === 'market' ? 'secondary' : 'ghost'"
          @click="openMcpAdd('market')"
        >
          <DownloadIcon class="size-4" />
          {{ $t("Marketplace") }}
        </Button>
        <Button
          size="sm"
          :variant="mcpAddMode === 'json' ? 'secondary' : 'ghost'"
          @click="openMcpAdd('json')"
        >
          <FileJsonIcon class="size-4" />
          JSON
        </Button>
      </div>

      <div v-if="mcpAddMode === 'market'" class="flex min-h-0 flex-1 flex-col">
        <div class="flex gap-2 border-b border-border p-4">
          <div class="relative min-w-0 flex-1">
            <SearchIcon class="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              v-model="mcpMarketQuery"
              class="pl-9"
              :placeholder="$t('Search MCP servers')"
              @keydown.enter.prevent="searchMcpMarket"
            />
          </div>
          <Button
            variant="outline"
            :disabled="mcpMarketLoading"
            @click="searchMcpMarket"
          >
            <RefreshCwIcon
              class="size-4"
              :class="mcpMarketLoading && 'animate-spin'"
            />
            {{ $t("Search") }}
          </Button>
        </div>

        <div class="min-h-0 flex-1 divide-y divide-border overflow-y-auto">
          <div
            v-for="item in mcpMarketResults"
            :key="`${item.id}:${item.version}`"
            class="flex items-center gap-4 px-5 py-3"
          >
            <div class="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted">
              <BlocksIcon class="size-4 text-muted-foreground" />
            </div>
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <p class="truncate text-sm font-medium">{{ item.name }}</p>
                <span v-if="item.version" class="shrink-0 text-[11px] text-muted-foreground">
                  {{ item.version }}
                </span>
              </div>
              <p class="line-clamp-2 text-xs text-muted-foreground">
                {{ item.description || item.id }}
              </p>
            </div>
            <Button
              size="sm"
              variant="outline"
              class="shrink-0"
              :disabled="
                saving ||
                mcpInstalled(item.id) ||
                !item.installable
              "
              :title="item.reason"
              @click="installMarketMcp(item)"
            >
              {{
                mcpInstalled(item.id)
                  ? $t("Installed")
                  : item.installable
                    ? $t("Install")
                    : item.reason || $t("Not installable")
              }}
            </Button>
          </div>
          <p
            v-if="!mcpMarketLoading && mcpMarketResults.length === 0"
            class="px-5 py-10 text-center text-sm text-muted-foreground"
          >
            {{ $t("No available servers found") }}
          </p>
        </div>
        <p class="border-t border-border px-5 py-2 text-[11px] text-muted-foreground">
          {{ $t("Data from the official MCP Registry (Preview)") }}
        </p>
      </div>

      <div v-else class="space-y-4 p-5">
        <Textarea
          v-model="mcpJSON"
          class="min-h-72 resize-y font-mono text-xs"
          placeholder='{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path"]
    }
  }
}'
        />
        <p v-if="mcpAddError" class="text-sm text-destructive">
          {{ mcpAddError }}
        </p>
        <div class="flex justify-end">
          <Button
            :disabled="saving || !mcpJSON.trim()"
            @click="addMcpFromJSON"
          >
            {{ saving ? $t("Adding") : $t("Add") }}
          </Button>
        </div>
      </div>

      <p
        v-if="mcpAddMode === 'market' && mcpAddError"
        class="border-t border-border px-5 py-3 text-sm text-destructive"
      >
        {{ mcpAddError }}
      </p>
    </DialogContent>
  </Dialog>

  <Dialog
    :open="pendingMcpDelete !== null"
    @update:open="(open) => { if (!open) pendingMcpDelete = null; }"
  >
    <DialogContent class="max-w-md">
      <DialogHeader>
        <DialogTitle>{{ $t("Delete MCP server") }}</DialogTitle>
        <DialogDescription>
          {{ $t("Delete MCP server confirmation", { name: pendingMcpDelete?.name || pendingMcpDelete?.id || "" }) }}
        </DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button
          variant="outline"
          :disabled="saving"
          @click="pendingMcpDelete = null"
        >
          {{ $t("Cancel") }}
        </Button>
        <Button
          variant="destructive"
          :disabled="saving"
          @click="deleteMcpServer"
        >
          {{ saving ? $t("Deleting") : $t("Delete") }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
