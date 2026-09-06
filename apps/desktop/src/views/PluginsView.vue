<script setup lang="ts">
import { computed, ref, type Component } from "vue";
import { useRouter } from "vue-router";
import {
  BlocksIcon,
  BotIcon,
  CheckIcon,
  ChevronDownIcon,
  ChevronRightIcon,
  FileTextIcon,
  GlobeIcon,
  PackageIcon,
  PaletteIcon,
  PlusIcon,
  RefreshCwIcon,
  SearchIcon,
  SettingsIcon,
  SlidersHorizontalIcon,
  Trash2Icon,
  TriangleAlertIcon,
  WrenchIcon,
} from "@lucide/vue";
import {
  api,
  type AgentPlugin,
  type MarketplacePlugin,
  type PluginMarketplace,
  type PluginMarketplaceCatalog,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

type CatalogScope = "marketplace" | "local";
type PluginDescriptor = { name: string; description?: string };

const router = useRouter();
const plugins = ref<AgentPlugin[]>([]);
const marketplaces = ref<PluginMarketplace[]>([]);
const catalog = ref<PluginMarketplaceCatalog | null>(null);
const selectedMarketplace = ref("");
const catalogScope = ref<CatalogScope>("marketplace");
const query = ref("");
const categoryFilter = ref("");
const loading = ref(false);
const marketplaceLoading = ref(false);
const saving = ref(false);
const installingName = ref("");
const error = ref("");
const marketplaceError = ref("");
const sourceOpen = ref(false);
const installedOpen = ref(false);
const marketplaceAddOpen = ref(false);
const marketplaceManageOpen = ref(false);
const installSource = ref("");
const replaceExisting = ref(false);
const marketplaceSource = ref("");
const marketplaceRef = ref("");
const marketplaceSparsePaths = ref("");
const pendingRemove = ref<AgentPlugin | null>(null);
const pendingMarketplaceRemove = ref<PluginMarketplace | null>(null);

const sortedPlugins = computed(() =>
  [...plugins.value].sort((left, right) => left.name.localeCompare(right.name))
);

const localPlugins = computed(() =>
  sortedPlugins.value.filter(
    (item) => !item.source?.startsWith("marketplace:")
  )
);

function inferredCategory(item: MarketplacePlugin): string {
  if (item.category) return item.category;
  const text = [
    item.name,
    item.description,
    ...(item.keywords ?? []),
    ...(item.tags ?? []),
  ]
    .join(" ")
    .toLowerCase();
  if (/document|pdf|presentation|productivity|office|meeting/.test(text)) {
    return "productivity";
  }
  if (/data|database|sql|analytics|spreadsheet|fabric/.test(text)) {
    return "data";
  }
  if (/security|review|test|quality|compliance/.test(text)) {
    return "quality";
  }
  if (/cloud|azure|aws|devops|deploy|terraform/.test(text)) {
    return "cloud";
  }
  if (/design|frontend|ui|canvas|accessibility/.test(text)) {
    return "design";
  }
  return "development";
}

function categoryLabel(category: string): string {
  return {
    productivity: "生产力",
    data: "数据",
    quality: "质量与安全",
    cloud: "云与 DevOps",
    design: "设计与前端",
    development: "开发工具",
  }[category] ?? category;
}

const categoryOptions = computed(() => {
  const categories = new Set(
    (catalog.value?.plugins ?? []).map(inferredCategory)
  );
  return [...categories].sort((left, right) =>
    categoryLabel(left).localeCompare(categoryLabel(right))
  );
});

const filteredMarketplacePlugins = computed(() => {
  const normalized = query.value.trim().toLowerCase();
  return (catalog.value?.plugins ?? []).filter((item) => {
    if (
      categoryFilter.value &&
      inferredCategory(item) !== categoryFilter.value
    ) {
      return false;
    }
    if (!normalized) return true;
    return [
      item.name,
      item.description,
      item.category,
      ...(item.keywords ?? []),
      ...(item.tags ?? []),
    ].some((value) => value?.toLowerCase().includes(normalized));
  });
});

const catalogGroups = computed(() => {
  const items = filteredMarketplacePlugins.value;
  if (query.value.trim() || categoryFilter.value) {
    return [{ id: "results", title: "搜索结果", items }];
  }
  const grouped = new Map<string, MarketplacePlugin[]>();
  for (const item of items) {
    const category = inferredCategory(item);
    const group = grouped.get(category) ?? [];
    group.push(item);
    grouped.set(category, group);
  }
  return [...grouped.entries()]
    .sort(([left], [right]) =>
      categoryLabel(left).localeCompare(categoryLabel(right))
    )
    .map(([id, groupItems]) => ({
      id,
      title: categoryLabel(id),
      items: groupItems,
    }));
});

function installedPlugin(name: string): AgentPlugin | undefined {
  return plugins.value.find((item) => item.name === name);
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

async function loadPlugins() {
  loading.value = true;
  error.value = "";
  try {
    plugins.value = await api.listPlugins();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

async function loadMarketplaces() {
  marketplaceLoading.value = true;
  marketplaceError.value = "";
  try {
    marketplaces.value = await api.listPluginMarketplaces();
    const enabled = marketplaces.value.filter((item) => item.enabled);
    if (
      !selectedMarketplace.value ||
      !enabled.some((item) => item.id === selectedMarketplace.value)
    ) {
      selectedMarketplace.value = enabled[0]?.id ?? "";
    }
    if (selectedMarketplace.value) {
      catalog.value = await api.browsePluginMarketplace(
        selectedMarketplace.value
      );
    } else {
      catalog.value = null;
    }
  } catch (cause) {
    catalog.value = null;
    marketplaceError.value = String(cause);
  } finally {
    marketplaceLoading.value = false;
  }
}

async function refreshAll() {
  await Promise.all([loadPlugins(), loadMarketplaces()]);
}

async function selectMarketplace(value: unknown) {
  if (typeof value !== "string" || value === selectedMarketplace.value) return;
  selectedMarketplace.value = value;
  categoryFilter.value = "";
  marketplaceLoading.value = true;
  marketplaceError.value = "";
  try {
    catalog.value = await api.browsePluginMarketplace(value);
  } catch (cause) {
    catalog.value = null;
    marketplaceError.value = String(cause);
  } finally {
    marketplaceLoading.value = false;
  }
}

function openSourceInstall() {
  installSource.value = "";
  replaceExisting.value = false;
  error.value = "";
  sourceOpen.value = true;
}

function openMarketplaceAdd() {
  marketplaceManageOpen.value = false;
  marketplaceSource.value = "";
  marketplaceRef.value = "";
  marketplaceSparsePaths.value = "";
  marketplaceError.value = "";
  marketplaceAddOpen.value = true;
}

function confirmMarketplaceRemove(item: PluginMarketplace) {
  marketplaceManageOpen.value = false;
  pendingMarketplaceRemove.value = item;
}

async function addMarketplace() {
  const source = marketplaceSource.value.trim();
  if (!source) return;
  saving.value = true;
  marketplaceError.value = "";
  try {
    const sparsePaths = marketplaceSparsePaths.value
      .split(/[\n,]/)
      .map((item) => item.trim())
      .filter(Boolean);
    const item = await api.addPluginMarketplace(
      source,
      marketplaceRef.value.trim(),
      sparsePaths
    );
    marketplaces.value = await api.listPluginMarketplaces();
    selectedMarketplace.value = item.id;
    catalog.value = await api.browsePluginMarketplace(item.id);
    catalogScope.value = "marketplace";
    marketplaceAddOpen.value = false;
  } catch (cause) {
    marketplaceError.value = String(cause);
  } finally {
    saving.value = false;
  }
}

async function refreshMarketplace(item: PluginMarketplace) {
  saving.value = true;
  marketplaceError.value = "";
  try {
    await api.refreshPluginMarketplace(item.id);
    await loadMarketplaces();
  } catch (cause) {
    marketplaceError.value = String(cause);
  } finally {
    saving.value = false;
  }
}

async function toggleMarketplace(item: PluginMarketplace) {
  saving.value = true;
  marketplaceError.value = "";
  try {
    marketplaces.value = await api.setPluginMarketplaceEnabled(
      item.id,
      !item.enabled
    );
    await loadMarketplaces();
  } catch (cause) {
    marketplaceError.value = String(cause);
  } finally {
    saving.value = false;
  }
}

async function removeMarketplace() {
  if (!pendingMarketplaceRemove.value) return;
  saving.value = true;
  marketplaceError.value = "";
  try {
    await api.removePluginMarketplace(pendingMarketplaceRemove.value.id);
    pendingMarketplaceRemove.value = null;
    await loadMarketplaces();
  } catch (cause) {
    marketplaceError.value = String(cause);
  } finally {
    saving.value = false;
  }
}

async function installFromSource() {
  const source = installSource.value.trim();
  if (!source) return;
  saving.value = true;
  error.value = "";
  try {
    await api.installPlugin(source, replaceExisting.value);
    await loadPlugins();
    sourceOpen.value = false;
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

async function installFromMarketplace(item: MarketplacePlugin) {
  if (!item.installable || !selectedMarketplace.value) return;
  installingName.value = item.name;
  marketplaceError.value = "";
  try {
    await api.installMarketplacePlugin(
      selectedMarketplace.value,
      item.name,
      Boolean(installedPlugin(item.name))
    );
    await loadPlugins();
  } catch (cause) {
    marketplaceError.value = String(cause);
  } finally {
    installingName.value = "";
  }
}

async function updatePlugin(item: AgentPlugin) {
  const match = item.source?.match(/^marketplace:([^:]+):([^:]+)$/);
  if (!match) {
    installSource.value = item.source ?? "";
    replaceExisting.value = true;
    error.value = "";
    sourceOpen.value = true;
    return;
  }
  installingName.value = item.name;
  error.value = "";
  try {
    await api.installMarketplacePlugin(match[1], match[2], true);
    await loadPlugins();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    installingName.value = "";
  }
}

async function togglePlugin(item: AgentPlugin) {
  saving.value = true;
  error.value = "";
  try {
    plugins.value = await api.setPluginEnabled(item.name, !item.enabled);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

async function removePlugin() {
  if (!pendingRemove.value) return;
  saving.value = true;
  error.value = "";
  try {
    await api.removePlugin(pendingRemove.value.name);
    pendingRemove.value = null;
    await loadPlugins();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

function openSkills() {
  void router.push({ name: "settings-skills" });
}

function openMarketplacePlugin(item: MarketplacePlugin) {
  if (!selectedMarketplace.value) return;
  void router.push({
    name: "plugin-detail",
    params: {
      marketplace: selectedMarketplace.value,
      plugin: item.name,
    },
  });
}

function openInstalledPlugin(item: AgentPlugin) {
  const match = item.source?.match(/^marketplace:([^:]+):([^:]+)$/);
  void router.push({
    name: "plugin-detail",
    params: {
      marketplace: match?.[1] ?? "installed",
      plugin: match?.[2] ?? item.name,
    },
  });
}

void refreshAll();
</script>

<template>
  <div class="flex h-full min-h-0 min-w-0 flex-col bg-background">
    <header
      data-tauri-drag-region
      class="flex min-h-12 shrink-0 items-center justify-between gap-3 border-b border-border/70 px-5"
    >
      <div class="no-drag flex items-center gap-1">
        <Button size="sm" variant="secondary">插件</Button>
        <Button size="sm" variant="ghost" class="text-muted-foreground" @click="openSkills">
          技能
        </Button>
      </div>
      <div class="no-drag flex items-center gap-1">
        <Button
          size="icon-sm"
          variant="ghost"
          :disabled="loading || marketplaceLoading"
          title="刷新"
          aria-label="刷新插件"
          @click="refreshAll"
        >
          <RefreshCwIcon
            :class="[
              'size-4',
              (loading || marketplaceLoading) && 'animate-spin',
            ]"
          />
        </Button>
        <Button
          size="icon-sm"
          variant="ghost"
          title="管理插件市场"
          aria-label="管理插件市场"
          @click="marketplaceManageOpen = true"
        >
          <SettingsIcon class="size-4" />
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger as-child>
            <Button size="sm" class="ml-2">
              添加
              <ChevronDownIcon class="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem @click="openMarketplaceAdd">
              <PlusIcon class="size-4" />
              添加插件市场
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem @click="openSourceInstall">
              <PackageIcon class="size-4" />
              从来源安装
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>

    <main class="min-h-0 flex-1 overflow-y-auto">
      <div class="mx-auto w-full max-w-6xl px-5 pb-14 pt-10 sm:px-8 lg:px-12">
        <section>
          <h1 class="text-3xl font-semibold tracking-normal">插件</h1>
          <p class="mt-2 text-base text-muted-foreground">
            扩展 Foya 的技能与工具
          </p>

          <div class="relative mt-8">
            <SearchIcon
              class="pointer-events-none absolute left-4 top-1/2 size-5 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              v-model="query"
              class="h-12 pl-12 text-base"
              placeholder="搜索插件"
            />
          </div>
        </section>

        <section class="mt-10">
          <div class="flex items-center justify-between border-b border-border pb-3">
            <h2 class="text-lg font-medium">已安装</h2>
            <Button
              size="icon-sm"
              variant="ghost"
              title="管理已安装插件"
              @click="installedOpen = true"
            >
              <SettingsIcon class="size-4" />
            </Button>
          </div>
          <div
            v-if="sortedPlugins.length"
            class="flex min-h-24 items-center gap-7 overflow-x-auto px-2 py-5"
          >
            <button
              v-for="item in sortedPlugins"
              :key="item.name"
              type="button"
              class="group flex size-14 shrink-0 items-center justify-center rounded-lg border border-border bg-background shadow-sm transition-transform hover:-translate-y-0.5"
              :title="item.name"
              @click="openInstalledPlugin(item)"
            >
              <span
                :class="[
                  'flex size-10 items-center justify-center rounded-md',
                  pluginTone(item.name),
                ]"
              >
                <component :is="pluginIcon(item)" class="size-5" />
              </span>
            </button>
          </div>
          <p
            v-else-if="!loading"
            class="flex min-h-24 items-center px-2 text-sm text-muted-foreground"
          >
            暂无已安装插件
          </p>
        </section>

        <section class="mt-6">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div class="flex items-center gap-1">
              <Button
                size="sm"
                :variant="catalogScope === 'marketplace' ? 'secondary' : 'ghost'"
                @click="catalogScope = 'marketplace'"
              >
                市场
              </Button>
              <Button
                size="sm"
                :variant="catalogScope === 'local' ? 'secondary' : 'ghost'"
                @click="catalogScope = 'local'"
              >
                本地
              </Button>
            </div>

            <div v-if="catalogScope === 'marketplace'" class="flex items-center gap-2">
              <Select
                :model-value="selectedMarketplace"
                @update:model-value="selectMarketplace"
              >
                <SelectTrigger class="h-8 w-44">
                  <SelectValue placeholder="选择市场" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem
                    v-for="item in marketplaces.filter((market) => market.enabled)"
                    :key="item.id"
                    :value="item.id"
                  >
                    {{ item.name }}
                  </SelectItem>
                </SelectContent>
              </Select>

              <DropdownMenu>
                <DropdownMenuTrigger as-child>
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    title="筛选分类"
                    aria-label="筛选分类"
                  >
                    <SlidersHorizontalIcon class="size-4" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuLabel>分类</DropdownMenuLabel>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem @click="categoryFilter = ''">
                    <CheckIcon
                      :class="['size-4', categoryFilter && 'opacity-0']"
                    />
                    全部
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    v-for="category in categoryOptions"
                    :key="category"
                    @click="categoryFilter = category"
                  >
                    <CheckIcon
                      :class="[
                        'size-4',
                        categoryFilter !== category && 'opacity-0',
                      ]"
                    />
                    {{ categoryLabel(category) }}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          </div>

          <div v-if="catalogScope === 'marketplace'" class="mt-8">
            <div
              v-if="marketplaces.length === 0 && !marketplaceLoading"
              class="flex flex-col items-center justify-center border-y border-border py-16 text-center"
            >
              <PackageIcon class="size-8 text-muted-foreground" />
              <p class="mt-4 text-sm font-medium">还没有插件市场</p>
              <Button size="sm" class="mt-5" @click="openMarketplaceAdd">
                <PlusIcon class="size-4" />
                添加插件市场
              </Button>
            </div>
            <div
              v-else-if="marketplaceLoading"
              class="flex items-center justify-center gap-2 py-16 text-sm text-muted-foreground"
            >
              <RefreshCwIcon class="size-4 animate-spin" />
              正在加载
            </div>

            <template v-else-if="catalog">
              <section
                v-for="group in catalogGroups"
                :key="group.id"
                class="mb-10"
              >
                <h3 class="border-b border-border pb-3 text-base font-medium">
                  {{ group.title }}
                </h3>
                <div class="grid grid-cols-1 gap-x-12 lg:grid-cols-2">
                  <div
                    v-for="item in group.items"
                    :key="item.name"
                    role="button"
                    tabindex="0"
                    class="flex min-h-24 cursor-pointer items-center gap-4 border-b border-border/70 py-4 outline-none transition-colors hover:bg-muted/35 focus-visible:bg-muted/50"
                    @click="openMarketplacePlugin(item)"
                    @keydown.enter="openMarketplacePlugin(item)"
                  >
                    <div
                      :class="[
                        'flex size-12 shrink-0 items-center justify-center rounded-lg border border-border/70',
                        pluginTone(item.name),
                      ]"
                    >
                      <component :is="pluginIcon(item)" class="size-6" />
                    </div>
                    <div class="min-w-0 flex-1">
                      <div class="flex min-w-0 items-center gap-2">
                        <span class="truncate text-sm font-medium">
                          {{ item.name }}
                        </span>
                        <span
                          v-if="item.version"
                          class="shrink-0 text-[11px] text-muted-foreground"
                        >
                          {{ item.version }}
                        </span>
                      </div>
                      <p class="mt-1 line-clamp-2 text-sm text-muted-foreground">
                        {{ item.description || item.source }}
                      </p>
                    </div>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      :disabled="!item.installable || Boolean(installingName)"
                      :title="
                        item.installable
                          ? installedPlugin(item.name)
                            ? `更新 ${item.name}`
                            : `查看 ${item.name}`
                          : item.reason
                      "
                      @click.stop="
                        installedPlugin(item.name)
                          ? installFromMarketplace(item)
                          : openMarketplacePlugin(item)
                      "
                    >
                      <TriangleAlertIcon
                        v-if="!item.installable"
                        class="size-4 text-amber-500"
                      />
                      <RefreshCwIcon
                        v-else-if="
                          installedPlugin(item.name) ||
                          installingName === item.name
                        "
                        :class="[
                          'size-4',
                          installingName === item.name && 'animate-spin',
                        ]"
                      />
                      <ChevronRightIcon v-else class="size-4" />
                    </Button>
                  </div>
                </div>
              </section>

              <p
                v-if="catalogGroups.length === 0"
                class="py-16 text-center text-sm text-muted-foreground"
              >
                没有匹配的插件
              </p>
            </template>

            <p
              v-if="marketplaceError"
              class="mt-4 break-all text-sm text-destructive"
            >
              {{ marketplaceError }}
            </p>
          </div>

          <div v-else class="mt-8">
            <h3 class="border-b border-border pb-3 text-base font-medium">
              本地插件
            </h3>
            <div class="grid grid-cols-1 gap-x-12 lg:grid-cols-2">
              <button
                v-for="item in localPlugins"
                :key="item.name"
                type="button"
                class="flex min-h-24 items-center gap-4 border-b border-border/70 py-4 text-left"
                @click="openInstalledPlugin(item)"
              >
                <span
                  :class="[
                    'flex size-12 shrink-0 items-center justify-center rounded-lg border border-border/70',
                    pluginTone(item.name),
                  ]"
                >
                  <component :is="pluginIcon(item)" class="size-6" />
                </span>
                <span class="min-w-0 flex-1">
                  <span class="block truncate text-sm font-medium">
                    {{ item.name }}
                  </span>
                  <span class="mt-1 block truncate text-sm text-muted-foreground">
                    {{ item.description || item.source || item.path }}
                  </span>
                </span>
                <SettingsIcon class="size-4 shrink-0 text-muted-foreground" />
              </button>
            </div>
            <p
              v-if="localPlugins.length === 0"
              class="py-16 text-center text-sm text-muted-foreground"
            >
              暂无本地插件
            </p>
          </div>
        </section>

        <p v-if="error" class="mt-6 break-all text-sm text-destructive">
          {{ error }}
        </p>
      </div>
    </main>
  </div>

  <Dialog v-model:open="installedOpen">
    <DialogContent class="flex max-h-[78vh] max-w-2xl flex-col gap-0 p-0">
      <DialogHeader class="border-b border-border px-5 py-4">
        <DialogTitle>已安装插件</DialogTitle>
        <DialogDescription class="sr-only">
          启停、更新或卸载插件
        </DialogDescription>
      </DialogHeader>
      <div class="min-h-0 flex-1 divide-y divide-border overflow-y-auto">
        <div
          v-for="item in sortedPlugins"
          :key="item.name"
          class="flex min-h-20 items-center gap-3 px-5 py-3"
        >
          <div
            :class="[
              'flex size-10 shrink-0 items-center justify-center rounded-lg border border-border/70',
              pluginTone(item.name),
            ]"
          >
            <component :is="pluginIcon(item)" class="size-5" />
          </div>
          <div class="min-w-0 flex-1">
            <div class="flex min-w-0 items-center gap-2">
              <span class="truncate text-sm font-medium">{{ item.name }}</span>
              <span
                v-if="item.version"
                class="shrink-0 text-[11px] text-muted-foreground"
              >
                {{ item.version }}
              </span>
            </div>
            <p class="truncate text-xs text-muted-foreground">
              {{ item.skill_count }} Skills ·
              {{ item.mcp_server_count }} MCP
            </p>
          </div>
          <Checkbox
            :checked="item.enabled"
            :disabled="saving || !item.valid"
            :aria-label="`${item.enabled ? '停用' : '启用'} ${item.name}`"
            @update:checked="togglePlugin(item)"
          />
          <Button
            v-if="item.source"
            size="icon-sm"
            variant="ghost"
            :disabled="saving || installingName === item.name"
            title="更新插件"
            @click="updatePlugin(item)"
          >
            <RefreshCwIcon
              :class="[
                'size-4',
                installingName === item.name && 'animate-spin',
              ]"
            />
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            class="text-muted-foreground hover:text-destructive"
            :disabled="saving"
            title="卸载插件"
            @click="pendingRemove = item"
          >
            <Trash2Icon class="size-4" />
          </Button>
        </div>
        <p
          v-if="!loading && sortedPlugins.length === 0"
          class="px-5 py-10 text-center text-sm text-muted-foreground"
        >
          暂无已安装插件
        </p>
      </div>
    </DialogContent>
  </Dialog>

  <Dialog v-model:open="marketplaceAddOpen">
    <DialogContent class="max-w-xl">
      <DialogHeader>
        <DialogTitle>添加插件市场</DialogTitle>
        <DialogDescription>
          从 GitHub 仓库、Git URL 或本地目录添加。
        </DialogDescription>
      </DialogHeader>
      <div class="space-y-4">
        <div class="space-y-1.5">
          <label class="text-sm font-medium">来源</label>
          <Input
            v-model="marketplaceSource"
            placeholder="owner/repo、Git URL 或本地目录"
            :disabled="saving"
          />
        </div>
        <div class="space-y-1.5">
          <label class="text-sm font-medium">Git 引用</label>
          <Input
            v-model="marketplaceRef"
            placeholder="主分支"
            :disabled="saving"
          />
        </div>
        <div class="space-y-1.5">
          <label class="text-sm font-medium">稀疏路径</label>
          <Textarea
            v-model="marketplaceSparsePaths"
            class="min-h-24"
            placeholder="plugins/example&#10;plugins/another"
            :disabled="saving"
          />
        </div>
        <p
          v-if="marketplaceError"
          class="break-all text-sm text-destructive"
        >
          {{ marketplaceError }}
        </p>
      </div>
      <DialogFooter>
        <Button
          variant="outline"
          :disabled="saving"
          @click="marketplaceAddOpen = false"
        >
          取消
        </Button>
        <Button
          :disabled="saving || !marketplaceSource.trim()"
          @click="addMarketplace"
        >
          {{ saving ? "添加中..." : "添加市场" }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>

  <Dialog v-model:open="marketplaceManageOpen">
    <DialogContent class="flex max-h-[78vh] max-w-2xl flex-col gap-0 p-0">
      <DialogHeader class="border-b border-border px-5 py-4">
        <DialogTitle>插件市场</DialogTitle>
        <DialogDescription class="sr-only">
          刷新、停用或移除插件市场
        </DialogDescription>
      </DialogHeader>
      <div class="min-h-0 flex-1 divide-y divide-border overflow-y-auto">
        <div
          v-for="item in marketplaces"
          :key="item.id"
          class="flex min-h-20 items-center gap-3 px-5 py-3"
        >
          <div class="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted">
            <PackageIcon class="size-4 text-muted-foreground" />
          </div>
          <div class="min-w-0 flex-1">
            <div class="flex min-w-0 items-center gap-2">
              <span class="truncate text-sm font-medium">{{ item.name }}</span>
              <span class="shrink-0 text-[11px] text-muted-foreground">
                {{ item.format }}
              </span>
            </div>
            <p class="truncate text-xs text-muted-foreground" :title="item.source">
              {{ item.source }}
              <template v-if="item.ref"> · {{ item.ref }}</template>
            </p>
            <p
              v-if="item.resolved_sha"
              class="mt-1 truncate font-mono text-[10px] text-muted-foreground"
            >
              {{ item.resolved_sha }}
            </p>
          </div>
          <Checkbox
            :checked="item.enabled"
            :disabled="saving"
            :aria-label="`${item.enabled ? '停用' : '启用'} ${item.name}`"
            @update:checked="toggleMarketplace(item)"
          />
          <Button
            size="icon-sm"
            variant="ghost"
            :disabled="saving"
            title="刷新市场"
            @click="refreshMarketplace(item)"
          >
            <RefreshCwIcon class="size-4" />
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            class="text-muted-foreground hover:text-destructive"
            :disabled="saving"
            title="移除市场"
            @click="confirmMarketplaceRemove(item)"
          >
            <Trash2Icon class="size-4" />
          </Button>
        </div>
        <p
          v-if="marketplaces.length === 0"
          class="px-5 py-10 text-center text-sm text-muted-foreground"
        >
          暂无插件市场
        </p>
      </div>
      <DialogFooter class="border-t border-border px-5 py-4">
        <Button variant="outline" @click="marketplaceManageOpen = false">
          完成
        </Button>
        <Button @click="openMarketplaceAdd">
          <PlusIcon class="size-4" />
          添加市场
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>

  <Dialog v-model:open="sourceOpen">
    <DialogContent class="max-w-lg">
      <DialogHeader>
        <DialogTitle>从来源安装</DialogTitle>
        <DialogDescription class="sr-only">
          输入 GitHub 仓库或本地插件目录
        </DialogDescription>
      </DialogHeader>
      <div class="space-y-3">
        <Input
          v-model="installSource"
          placeholder="owner/repo、Git URL 或本地目录"
          :disabled="saving"
          @keydown.enter.prevent="installFromSource"
        />
        <label class="flex items-center gap-2 text-xs text-muted-foreground">
          <Checkbox v-model:checked="replaceExisting" :disabled="saving" />
          替换同名插件
        </label>
        <p v-if="error" class="break-all text-sm text-destructive">{{ error }}</p>
      </div>
      <DialogFooter>
        <Button variant="outline" :disabled="saving" @click="sourceOpen = false">
          取消
        </Button>
        <Button
          :disabled="saving || !installSource.trim()"
          @click="installFromSource"
        >
          {{ saving ? "处理中..." : replaceExisting ? "更新" : "安装" }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>

  <Dialog
    :open="pendingMarketplaceRemove !== null"
    @update:open="(open) => { if (!open) pendingMarketplaceRemove = null; }"
  >
    <DialogContent class="max-w-md">
      <DialogHeader>
        <DialogTitle>移除插件市场</DialogTitle>
        <DialogDescription>
          将移除“{{ pendingMarketplaceRemove?.name }}”及其本地目录快照。已安装插件不会被卸载。
        </DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button
          variant="outline"
          :disabled="saving"
          @click="pendingMarketplaceRemove = null"
        >
          取消
        </Button>
        <Button
          variant="destructive"
          :disabled="saving"
          @click="removeMarketplace"
        >
          {{ saving ? "移除中..." : "移除" }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>

  <Dialog
    :open="pendingRemove !== null"
    @update:open="(open) => { if (!open) pendingRemove = null; }"
  >
    <DialogContent class="max-w-md">
      <DialogHeader>
        <DialogTitle>卸载插件</DialogTitle>
        <DialogDescription>
          将卸载“{{ pendingRemove?.name }}”。插件数据目录会保留。
        </DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button variant="outline" :disabled="saving" @click="pendingRemove = null">
          取消
        </Button>
        <Button variant="destructive" :disabled="saving" @click="removePlugin">
          {{ saving ? "卸载中..." : "卸载" }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
