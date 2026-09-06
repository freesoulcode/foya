<script setup lang="ts">
import { computed, ref, type Component } from "vue";
import { useRoute, useRouter } from "vue-router";
import { openUrl } from "@tauri-apps/plugin-opener";
import {
  BlocksIcon,
  BotIcon,
  CheckCircle2Icon,
  ChevronRightIcon,
  CircleAlertIcon,
  DownloadIcon,
  ExternalLinkIcon,
  FileTextIcon,
  GlobeIcon,
  PackageIcon,
  PaletteIcon,
  RefreshCwIcon,
  SettingsIcon,
  Trash2Icon,
  WrenchIcon,
} from "@lucide/vue";
import {
  api,
  type AgentPlugin,
  type MarketplacePlugin,
  type MarketplacePluginPreview,
  type McpStatus,
  type PluginMarketplaceCatalog,
  type SkillInfo,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

type PluginDescriptor = { name: string; description?: string };

const route = useRoute();
const router = useRouter();
const marketplaceID = computed(() => String(route.params.marketplace ?? ""));
const pluginName = computed(() => String(route.params.plugin ?? ""));
const catalog = ref<PluginMarketplaceCatalog | null>(null);
const marketPlugin = ref<MarketplacePlugin | null>(null);
const preview = ref<MarketplacePluginPreview | null>(null);
const installedPlugin = ref<AgentPlugin | null>(null);
const skills = ref<SkillInfo[]>([]);
const mcpStatuses = ref<McpStatus[]>([]);
const loading = ref(false);
const previewLoading = ref(false);
const saving = ref(false);
const error = ref("");
const previewError = ref("");
const removeConfirmOpen = ref(false);

const displayName = computed(
  () => marketPlugin.value?.name ?? installedPlugin.value?.name ?? pluginName.value
);
const description = computed(
  () =>
    marketPlugin.value?.description ??
    installedPlugin.value?.description ??
    "暂无插件描述"
);
const version = computed(
  () => marketPlugin.value?.version ?? installedPlugin.value?.version
);
const author = computed(
  () => marketPlugin.value?.author ?? installedPlugin.value?.author
);
const license = computed(
  () => marketPlugin.value?.license ?? installedPlugin.value?.license
);
const keywords = computed(
  () =>
    marketPlugin.value?.keywords ??
    installedPlugin.value?.keywords ??
    marketPlugin.value?.tags ??
    []
);
const pluginSkills = computed(() =>
  skills.value.filter((item) =>
    item.ref.startsWith(`plugin:${installedPlugin.value?.name ?? pluginName.value}:`)
  )
);
const pluginMcpStatuses = computed(() =>
  mcpStatuses.value.filter(
    (item) => item.plugin_id === (installedPlugin.value?.name ?? pluginName.value)
  )
);
const displayedSkillCount = computed(
  () => installedPlugin.value?.skill_count ?? preview.value?.skill_count
);
const displayedMCPCount = computed(
  () => installedPlugin.value?.mcp_server_count ?? preview.value?.mcp_server_count
);
const diagnostics = computed(
  () => installedPlugin.value?.diagnostics ?? preview.value?.diagnostics ?? []
);
const repositoryURL = computed(() => {
  const value =
    marketPlugin.value?.repository ??
    installedPlugin.value?.repository ??
    marketPlugin.value?.homepage ??
    installedPlugin.value?.homepage ??
    "";
  if (/^https:\/\//i.test(value)) return value;
  if (/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(value)) {
    return `https://github.com/${value}`;
  }
  if (catalog.value?.repository) {
    return `https://github.com/${catalog.value.repository}`;
  }
  return "";
});
const compatibilityLabel = computed(() => {
  if (installedPlugin.value?.valid) return "Agent Plugins 1.0";
  if (installedPlugin.value && !installedPlugin.value.valid) return "不兼容";
  if (previewLoading.value) return "正在检测";
  if (preview.value?.compatibility === "compatible") return "完全兼容";
  if (preview.value?.compatibility === "partial") return "部分兼容";
  if (preview.value?.compatibility === "unsupported") return "不兼容";
  if (previewError.value) return "预检失败";
  return "来源不受支持";
});

function unsupportedComponentLabel(component: string): string {
  return {
    agents: "Agents",
    commands: "Commands",
    hooks: "Hooks",
    lsp: "LSP Servers",
    legacy_mcp: "旧版 MCP 配置",
    legacy_skills: "旧版 Skill 路径",
    extensions: "客户端扩展",
  }[component] ?? component;
}

function pluginIcon(item: PluginDescriptor): Component {
  const text = `${item.name} ${item.description ?? ""}`.toLowerCase();
  if (/document|pdf|markdown|writing/.test(text)) return FileTextIcon;
  if (/data|database|sql|spreadsheet|analytics/.test(text)) return BlocksIcon;
  if (/design|frontend|ui|canvas|presentation/.test(text)) return PaletteIcon;
  if (/browser|web|cloud|azure|aws/.test(text)) return GlobeIcon;
  if (/agent|assistant|team|orchestration/.test(text)) return BotIcon;
  if (/tool|build|develop|code|git|test/.test(text)) return WrenchIcon;
  return PackageIcon;
}

function pluginTone(name: string): string {
  let value = 0;
  for (const character of name) value = (value + character.charCodeAt(0)) % 6;
  return [
    "bg-blue-50 text-blue-600 dark:bg-blue-950/60 dark:text-blue-300",
    "bg-emerald-50 text-emerald-600 dark:bg-emerald-950/60 dark:text-emerald-300",
    "bg-amber-50 text-amber-600 dark:bg-amber-950/60 dark:text-amber-300",
    "bg-rose-50 text-rose-600 dark:bg-rose-950/60 dark:text-rose-300",
    "bg-cyan-50 text-cyan-600 dark:bg-cyan-950/60 dark:text-cyan-300",
    "bg-violet-50 text-violet-600 dark:bg-violet-950/60 dark:text-violet-300",
  ][value];
}

function stateLabel(state: McpStatus["state"]): string {
  return {
    connected: "已连接",
    connecting: "连接中",
    disconnected: "未连接",
    disabled: "已停用",
    error: "连接失败",
  }[state];
}

function stateClass(state: McpStatus["state"]): string {
  if (state === "connected") return "bg-emerald-500";
  if (state === "connecting") return "bg-amber-500";
  if (state === "error") return "bg-destructive";
  return "bg-muted-foreground/40";
}

async function load() {
  loading.value = true;
  error.value = "";
  previewError.value = "";
  preview.value = null;
  try {
    const [installed, skillItems, statuses] = await Promise.all([
      api.listPlugins(),
      api.listSkills(),
      api.getMcpStatus(),
    ]);
    installedPlugin.value =
      installed.find((item) => item.name === pluginName.value) ?? null;
    skills.value = skillItems;
    mcpStatuses.value = statuses;

    if (marketplaceID.value !== "installed") {
      catalog.value = await api.browsePluginMarketplace(marketplaceID.value);
      marketPlugin.value =
        catalog.value.plugins.find((item) => item.name === pluginName.value) ??
        null;
      if (!installedPlugin.value && marketPlugin.value?.installable) {
        previewLoading.value = true;
        try {
          preview.value = await api.previewMarketplacePlugin(
            marketplaceID.value,
            pluginName.value
          );
        } catch (cause) {
          previewError.value = String(cause);
        } finally {
          previewLoading.value = false;
        }
      }
    }
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

async function installOrUpdate() {
  if (!marketPlugin.value || marketplaceID.value === "installed") return;
  saving.value = true;
  error.value = "";
  try {
    await api.installMarketplacePlugin(
      marketplaceID.value,
      marketPlugin.value.name,
      Boolean(installedPlugin.value)
    );
    await load();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

async function togglePlugin() {
  if (!installedPlugin.value) return;
  saving.value = true;
  error.value = "";
  try {
    const items = await api.setPluginEnabled(
      installedPlugin.value.name,
      !installedPlugin.value.enabled
    );
    installedPlugin.value =
      items.find((item) => item.name === pluginName.value) ?? null;
    mcpStatuses.value = await api.getMcpStatus();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

async function removePlugin() {
  if (!installedPlugin.value) return;
  saving.value = true;
  error.value = "";
  try {
    await api.removePlugin(installedPlugin.value.name);
    removeConfirmOpen.value = false;
    installedPlugin.value = null;
    skills.value = await api.listSkills();
    mcpStatuses.value = await api.getMcpStatus();
    if (marketplaceID.value === "installed") {
      await router.push({ name: "plugins" });
    }
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

function backToPlugins() {
  void router.push({ name: "plugins" });
}

function startChat() {
  void router.push({ name: "chat" });
}

function openRepository() {
  if (repositoryURL.value) void openUrl(repositoryURL.value);
}

void load();
</script>

<template>
  <div class="flex h-full min-h-0 min-w-0 flex-col bg-background">
    <header
      data-tauri-drag-region
      class="flex min-h-12 shrink-0 items-center border-b border-border/70 px-5"
    >
      <div class="no-drag flex min-w-0 items-center gap-2">
        <Button
          size="sm"
          variant="ghost"
          class="px-2 text-muted-foreground"
          @click="backToPlugins"
        >
          插件
        </Button>
        <ChevronRightIcon class="size-4 shrink-0 text-muted-foreground" />
        <span class="truncate text-sm font-medium">{{ displayName }}</span>
      </div>
    </header>

    <main class="min-h-0 flex-1 overflow-y-auto">
      <div class="mx-auto w-full max-w-5xl px-5 pb-16 pt-8 sm:px-8 lg:px-12">
        <div
          v-if="loading && !marketPlugin && !installedPlugin"
          class="flex items-center justify-center gap-2 py-20 text-sm text-muted-foreground"
        >
          <RefreshCwIcon class="size-4 animate-spin" />
          正在加载
        </div>

        <template v-else>
          <section class="flex flex-wrap items-start gap-5">
            <div
              :class="[
                'flex size-20 shrink-0 items-center justify-center rounded-lg border border-border/70',
                pluginTone(displayName),
              ]"
            >
              <component
                :is="pluginIcon({ name: displayName, description })"
                class="size-10"
              />
            </div>
            <div class="min-w-0 flex-1 pt-1">
              <div class="flex min-w-0 items-center gap-3">
                <h1 class="truncate text-2xl font-semibold tracking-normal">
                  {{ displayName }}
                </h1>
                <span
                  v-if="version"
                  class="shrink-0 text-sm text-muted-foreground"
                >
                  {{ version }}
                </span>
              </div>
              <p class="mt-2 max-w-3xl text-sm leading-6 text-muted-foreground">
                {{ description }}
              </p>
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <Button
                v-if="repositoryURL"
                size="sm"
                variant="outline"
                @click="openRepository"
              >
                <ExternalLinkIcon class="size-4" />
                查看来源
              </Button>
              <Button
                v-if="marketPlugin"
                size="sm"
                :disabled="
                  saving ||
                  previewLoading ||
                  !marketPlugin.installable ||
                  (!installedPlugin &&
                    (!preview || preview.compatibility === 'unsupported'))
                "
                @click="installOrUpdate"
              >
                <RefreshCwIcon
                  v-if="installedPlugin"
                  :class="['size-4', saving && 'animate-spin']"
                />
                <DownloadIcon v-else class="size-4" />
                {{ installedPlugin ? "更新" : "安装" }}
              </Button>
              <Button
                v-else-if="installedPlugin"
                size="sm"
                @click="startChat"
              >
                开始对话
              </Button>
              <DropdownMenu v-if="installedPlugin">
                <DropdownMenuTrigger as-child>
                  <Button size="icon-sm" variant="ghost" title="更多操作">
                    <SettingsIcon class="size-4" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem
                    class="text-destructive focus:text-destructive"
                    @click="removeConfirmOpen = true"
                  >
                    <Trash2Icon class="size-4" />
                    卸载插件
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          </section>

          <section class="mt-10 grid gap-x-10 border-y border-border py-5 sm:grid-cols-2 lg:grid-cols-4">
            <div class="py-2">
              <p class="text-xs text-muted-foreground">兼容性</p>
              <p class="mt-1 flex items-center gap-1.5 text-sm font-medium">
                <CheckCircle2Icon
                  v-if="
                    installedPlugin?.valid ||
                    preview?.compatibility === 'compatible'
                  "
                  class="size-4 text-emerald-500"
                />
                <CircleAlertIcon
                  v-else-if="
                    previewError ||
                    preview?.compatibility === 'partial' ||
                    preview?.compatibility === 'unsupported' ||
                    !marketPlugin?.installable
                  "
                  class="size-4 text-amber-500"
                />
                {{ compatibilityLabel }}
              </p>
            </div>
            <div class="py-2">
              <p class="text-xs text-muted-foreground">作者</p>
              <p class="mt-1 truncate text-sm font-medium">
                {{ author?.name || "未提供" }}
              </p>
            </div>
            <div class="py-2">
              <p class="text-xs text-muted-foreground">许可证</p>
              <p class="mt-1 truncate text-sm font-medium">
                {{ license || "未提供" }}
              </p>
            </div>
            <div class="py-2">
              <p class="text-xs text-muted-foreground">状态</p>
              <div class="mt-1 flex items-center gap-2">
                <Checkbox
                  v-if="installedPlugin"
                  :checked="installedPlugin.enabled"
                  :disabled="saving || !installedPlugin.valid"
                  :aria-label="`${installedPlugin.enabled ? '停用' : '启用'} ${displayName}`"
                  @update:checked="togglePlugin"
                />
                <span class="text-sm font-medium">
                  {{
                    installedPlugin
                      ? installedPlugin.enabled
                        ? "已启用"
                        : "已停用"
                      : "未安装"
                  }}
                </span>
              </div>
            </div>
          </section>

          <section v-if="keywords.length" class="mt-8">
            <div class="flex flex-wrap gap-2">
              <span
                v-for="keyword in keywords"
                :key="keyword"
                class="rounded-md bg-muted px-2 py-1 text-xs text-muted-foreground"
              >
                {{ keyword }}
              </span>
            </div>
          </section>

          <section class="mt-10">
            <h2 class="border-b border-border pb-3 text-lg font-medium">
              MCP 服务器
              <span class="ml-1 text-sm font-normal text-muted-foreground">
                {{ previewLoading ? "…" : displayedMCPCount ?? "—" }}
              </span>
            </h2>
            <div
              v-for="server in pluginMcpStatuses"
              :key="server.id"
              class="flex min-h-16 items-center gap-3 border-b border-border/70 px-2 py-3"
            >
              <span
                class="size-2 shrink-0 rounded-full"
                :class="stateClass(server.state)"
              />
              <BlocksIcon class="size-5 shrink-0 text-muted-foreground" />
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-medium">{{ server.name }}</p>
                <p class="text-xs text-muted-foreground">
                  {{ stateLabel(server.state) }} · {{ server.tool_count }} 工具
                </p>
              </div>
            </div>
            <p
              v-if="pluginMcpStatuses.length === 0"
              class="px-2 py-6 text-sm text-muted-foreground"
            >
              {{
                installedPlugin
                  ? "此插件没有 MCP 服务器"
                  : preview
                    ? "未发现 Foya 支持的 MCP 服务器"
                    : "正在检测 MCP 服务器"
              }}
            </p>
          </section>

          <section class="mt-10">
            <h2 class="border-b border-border pb-3 text-lg font-medium">
              技能
              <span class="ml-1 text-sm font-normal text-muted-foreground">
                {{ previewLoading ? "…" : displayedSkillCount ?? "—" }}
              </span>
            </h2>
            <div
              v-for="skill in pluginSkills"
              :key="skill.ref"
              class="flex min-h-20 items-center gap-3 border-b border-border/70 px-2 py-3"
            >
              <div
                :class="[
                  'flex size-9 shrink-0 items-center justify-center rounded-md',
                  pluginTone(skill.name),
                ]"
              >
                <WrenchIcon class="size-4" />
              </div>
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-medium">{{ skill.name }}</p>
                <p class="mt-1 line-clamp-2 text-xs text-muted-foreground">
                  {{ skill.description || skill.ref }}
                </p>
              </div>
              <span
                class="text-xs"
                :class="
                  skill.enabled ? 'text-emerald-600' : 'text-muted-foreground'
                "
              >
                {{ skill.enabled ? "已启用" : "已停用" }}
              </span>
            </div>
            <p
              v-if="pluginSkills.length === 0"
              class="px-2 py-6 text-sm text-muted-foreground"
            >
              {{
                installedPlugin
                  ? "此插件没有技能"
                  : preview
                    ? "未发现 Foya 支持的技能"
                    : "正在检测技能"
              }}
            </p>
          </section>

          <section
            v-if="preview?.unsupported_components?.length"
            class="mt-10"
          >
            <h2 class="border-b border-border pb-3 text-lg font-medium">
              不受支持的组件
              <span class="ml-1 text-sm font-normal text-muted-foreground">
                {{ preview.unsupported_components.length }}
              </span>
            </h2>
            <div class="flex flex-wrap gap-2 py-5">
              <span
                v-for="component in preview.unsupported_components"
                :key="component"
                class="rounded-md bg-amber-50 px-2 py-1 text-xs text-amber-700 dark:bg-amber-950/60 dark:text-amber-300"
              >
                {{ unsupportedComponentLabel(component) }}
              </span>
            </div>
          </section>

          <section
            v-if="diagnostics.length"
            class="mt-10"
          >
            <h2 class="border-b border-border pb-3 text-lg font-medium">
              诊断
              <span class="ml-1 text-sm font-normal text-muted-foreground">
                {{ diagnostics.length }}
              </span>
            </h2>
            <div
              v-for="diagnostic in diagnostics"
              :key="`${diagnostic.code}:${diagnostic.component}`"
              class="flex gap-3 border-b border-border/70 px-2 py-3"
            >
              <CircleAlertIcon
                class="mt-0.5 size-4 shrink-0"
                :class="
                  diagnostic.severity === 'error'
                    ? 'text-destructive'
                    : 'text-amber-500'
                "
              />
              <div class="min-w-0">
                <p class="text-sm font-medium">
                  {{ diagnostic.component || diagnostic.code }}
                </p>
                <p class="mt-1 break-all text-xs text-muted-foreground">
                  {{ diagnostic.message }}
                </p>
              </div>
            </div>
          </section>

          <p v-if="error" class="mt-8 break-all text-sm text-destructive">
            {{ error }}
          </p>
          <p
            v-if="previewError"
            class="mt-8 break-all text-sm text-destructive"
          >
            {{ previewError }}
          </p>
        </template>
      </div>
    </main>
  </div>

  <Dialog v-model:open="removeConfirmOpen">
    <DialogContent class="max-w-md">
      <DialogHeader>
        <DialogTitle>卸载插件</DialogTitle>
        <DialogDescription>
          将卸载“{{ displayName }}”。插件数据目录会保留。
        </DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button
          variant="outline"
          :disabled="saving"
          @click="removeConfirmOpen = false"
        >
          取消
        </Button>
        <Button variant="destructive" :disabled="saving" @click="removePlugin">
          {{ saving ? "卸载中..." : "卸载" }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
