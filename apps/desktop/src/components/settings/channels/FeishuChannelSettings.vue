<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { openUrl } from "@tauri-apps/plugin-opener";
import {
  ExternalLinkIcon,
  EyeIcon,
  EyeOffIcon,
  RefreshCwIcon,
  SaveIcon,
  Trash2Icon,
} from "@lucide/vue";
import {
  api,
  type ConnectionConfig,
  type FeishuBotSettings,
  type FeishuBotUpdate,
  type ProjectInfo,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import { ButtonGroup } from "@/components/ui/button-group";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";

const props = defineProps<{
  channel: FeishuBotSettings | null;
  creating?: boolean;
}>();

const emit = defineEmits<{
  saved: [channel: FeishuBotSettings];
  deleted: [channelId: string];
  refresh: [];
}>();

const emptySettings = (): FeishuBotSettings => ({
  id: "",
  kind: "feishu",
  name: "飞书 Bot",
  enabled: false,
  app_id: "",
  has_app_secret: false,
  approval_mode: "auto",
  allowed_users: [],
  allowed_chats: [],
  allow_all: false,
  status: "stopped",
});

const settings = ref<FeishuBotSettings>(emptySettings());
const appSecret = ref("");
const allowedUsers = ref("");
const allowedChats = ref("");
const connections = ref<ConnectionConfig[]>([]);
const projects = ref<ProjectInfo[]>([]);
const showSecret = ref(false);
const loading = ref(false);
const saving = ref(false);
const deleting = ref(false);
const deleteConfirmOpen = ref(false);
const error = ref("");

const languageConnections = computed(() =>
  connections.value.filter(
    (item): item is ConnectionConfig & { id: string } =>
      item.type === "language" && typeof item.id === "string" && item.id.length > 0
  )
);

const statusLabel = computed(() => {
  if (props.creating) return "未保存";
  if (settings.value.status === "running") return "运行中";
  if (settings.value.status === "error") return "连接异常";
  return "已停止";
});

const statusClass = computed(() => {
  if (settings.value.status === "running") return "bg-emerald-500";
  if (settings.value.status === "error") return "bg-destructive";
  return "bg-muted-foreground/50";
});

function parseIDs(value: string): string[] {
  return [...new Set(
    value
      .split(/[\s,]+/)
      .map((item) => item.trim())
      .filter(Boolean)
  )];
}

function applySettings(value: FeishuBotSettings | null) {
  settings.value = value
    ? {
        ...emptySettings(),
        ...value,
        allowed_users: value.allowed_users ?? [],
        allowed_chats: value.allowed_chats ?? [],
      }
    : emptySettings();
  allowedUsers.value = settings.value.allowed_users.join("\n");
  allowedChats.value = settings.value.allowed_chats.join("\n");
  appSecret.value = "";
  error.value = "";
}

function buildUpdate(): FeishuBotUpdate {
  return {
    name: settings.value.name.trim() || "飞书 Bot",
    enabled: settings.value.enabled,
    app_id: settings.value.app_id.trim(),
    ...(appSecret.value.trim() ? { app_secret: appSecret.value.trim() } : {}),
    connection_id: settings.value.connection_id || undefined,
    model: settings.value.model?.trim() || undefined,
    project_id: settings.value.project_id || undefined,
    approval_mode: settings.value.approval_mode,
    allowed_users: parseIDs(allowedUsers.value),
    allowed_chats: parseIDs(allowedChats.value),
    allow_all: settings.value.allow_all,
  };
}

async function save() {
  error.value = "";
  const update = buildUpdate();
  if (update.enabled && !update.app_id) {
    error.value = "启用前请填写 App ID";
    return;
  }
  if (update.enabled && !update.app_secret && !settings.value.has_app_secret) {
    error.value = "启用前请填写 App Secret";
    return;
  }
  if (
    update.enabled &&
    !update.allow_all &&
    update.allowed_users.length === 0 &&
    update.allowed_chats.length === 0
  ) {
    error.value = "启用前请至少添加一个用户或群聊白名单";
    return;
  }

  saving.value = true;
  try {
    const saved = props.creating || !settings.value.id
      ? await api.createChannel({ kind: "feishu", ...update })
      : await api.updateChannel(settings.value.id, update);
    applySettings(saved);
    emit("saved", saved);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

async function remove() {
  if (!settings.value.id) return;
  deleting.value = true;
  error.value = "";
  try {
    const id = settings.value.id;
    await api.deleteChannel(id);
    deleteConfirmOpen.value = false;
    emit("deleted", id);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    deleting.value = false;
  }
}

function selectConnection(value: unknown) {
  settings.value.connection_id = value === "__default__" ? "" : String(value ?? "");
  settings.value.model = "";
}

function selectProject(value: unknown) {
  settings.value.project_id = value === "__none__" ? "" : String(value ?? "");
}

watch(
  () => props.channel,
  (value) => applySettings(value),
  { immediate: true }
);

onMounted(async () => {
  loading.value = true;
  try {
    [connections.value, projects.value] = await Promise.all([
      api.listConnections(),
      api.listProjects(),
    ]);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
});
</script>

<template>
  <div class="w-full space-y-5 pb-5">
    <header class="flex flex-wrap items-center justify-between gap-3">
      <div class="min-w-0">
        <Input
          v-model="settings.name"
          class="h-8 max-w-64 border-transparent px-0 text-base font-semibold shadow-none hover:border-input focus-visible:px-2.5"
          aria-label="Bot 名称"
          :disabled="loading || saving"
        />
        <div class="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
          <span :class="['size-1.5 rounded-full', statusClass]" />
          <span>{{ statusLabel }}</span>
        </div>
      </div>
      <div class="flex items-center gap-2">
        <Tooltip>
          <TooltipTrigger as-child>
            <Button
              variant="ghost"
              size="icon-sm"
              :disabled="loading || saving"
              aria-label="刷新状态"
              @click="emit('refresh')"
            >
              <RefreshCwIcon class="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>刷新状态</TooltipContent>
        </Tooltip>
        <Tooltip v-if="settings.id">
          <TooltipTrigger as-child>
            <Button
              variant="ghost"
              size="icon-sm"
              :disabled="saving || deleting"
              aria-label="删除 Bot"
              @click="deleteConfirmOpen = true"
            >
              <Trash2Icon class="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>删除 Bot</TooltipContent>
        </Tooltip>
        <label class="flex items-center gap-2 text-sm font-medium">
          <span>{{ settings.enabled ? "已启用" : "未启用" }}</span>
          <Checkbox
            :model-value="settings.enabled"
            :disabled="loading || saving"
            aria-label="启用飞书"
            @update:model-value="settings.enabled = $event === true"
          />
        </label>
      </div>
    </header>

    <section>
      <div class="mb-3 flex items-center justify-between gap-3">
        <div>
          <h4 class="text-sm font-semibold">应用凭证</h4>
          <p class="mt-0.5 text-xs text-muted-foreground">企业自建应用的身份信息</p>
        </div>
        <Button
          variant="ghost"
          size="sm"
          :disabled="saving"
          @click="openUrl('https://open.feishu.cn/app')"
        >
          <ExternalLinkIcon class="size-4" />
          开发者后台
        </Button>
      </div>
      <div class="grid gap-3 sm:grid-cols-2">
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-app-id">App ID</Label>
          <Input
            id="feishu-app-id"
            v-model="settings.app_id"
            class="font-mono"
            placeholder="cli_xxx"
            :disabled="loading || saving"
          />
        </div>
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-app-secret">App Secret</Label>
          <div class="relative">
            <Input
              id="feishu-app-secret"
              v-model="appSecret"
              class="pr-10 font-mono"
              :type="showSecret ? 'text' : 'password'"
              :placeholder="settings.has_app_secret ? '已保存，留空则保持不变' : '输入 App Secret'"
              :disabled="loading || saving"
            />
            <button
              type="button"
              class="absolute inset-y-0 right-0 flex w-9 items-center justify-center text-muted-foreground hover:text-foreground"
              :aria-label="showSecret ? '隐藏 App Secret' : '显示 App Secret'"
              :disabled="loading || saving"
              @click="showSecret = !showSecret"
            >
              <EyeOffIcon v-if="showSecret" class="size-4" />
              <EyeIcon v-else class="size-4" />
            </button>
          </div>
        </div>
      </div>
    </section>

    <section>
      <div class="mb-3">
        <h4 class="text-sm font-semibold">运行配置</h4>
        <p class="mt-0.5 text-xs text-muted-foreground">为飞书会话选择模型、项目和工具权限</p>
      </div>
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-connection">语言模型连接</Label>
          <Select
            :model-value="settings.connection_id || '__default__'"
            :disabled="loading || saving"
            @update:model-value="selectConnection"
          >
            <SelectTrigger id="feishu-connection" class="w-full">
              <SelectValue placeholder="默认语言模型" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__default__">默认语言模型</SelectItem>
              <SelectItem
                v-for="connection in languageConnections"
                :key="connection.id"
                :value="connection.id"
              >
                {{ connection.name }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-model">模型</Label>
          <Input
            id="feishu-model"
            v-model="settings.model"
            class="font-mono"
            placeholder="默认模型"
            :disabled="loading || saving"
          />
        </div>
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-project">工作项目</Label>
          <Select
            :model-value="settings.project_id || '__none__'"
            :disabled="loading || saving"
            @update:model-value="selectProject"
          >
            <SelectTrigger id="feishu-project" class="w-full">
              <SelectValue placeholder="不绑定项目" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__none__">不绑定项目</SelectItem>
              <SelectItem
                v-for="project in projects"
                :key="project.id"
                :value="project.id"
              >
                {{ project.name }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div class="min-w-0 space-y-1.5">
          <Label>工具审批</Label>
          <ButtonGroup class="w-full" aria-label="工具审批">
            <Button
              class="flex-1 shadow-none"
              size="sm"
              variant="outline"
              :class="settings.approval_mode === 'auto' && 'bg-accent text-accent-foreground'"
              :aria-pressed="settings.approval_mode === 'auto'"
              :disabled="loading || saving"
              @click="settings.approval_mode = 'auto'"
            >
              自动判断
            </Button>
            <Button
              class="flex-1 shadow-none"
              size="sm"
              variant="outline"
              :class="settings.approval_mode === 'full_access' && 'bg-accent text-accent-foreground'"
              :aria-pressed="settings.approval_mode === 'full_access'"
              :disabled="loading || saving"
              @click="settings.approval_mode = 'full_access'"
            >
              完全访问
            </Button>
          </ButtonGroup>
        </div>
      </div>
    </section>

    <section>
      <div class="mb-3 flex items-center justify-between gap-4">
        <div>
          <h4 class="text-sm font-semibold">访问范围</h4>
          <p class="mt-0.5 text-xs text-muted-foreground">限制可以调用本机 Agent 的会话</p>
        </div>
        <label class="flex items-center gap-2 text-sm">
          <span>允许全部</span>
          <Checkbox
            :model-value="settings.allow_all"
            :disabled="loading || saving"
            aria-label="允许所有用户和群聊"
            @update:model-value="settings.allow_all = $event === true"
          />
        </label>
      </div>
      <div v-if="!settings.allow_all" class="grid gap-3 sm:grid-cols-2">
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-users">用户 Open ID</Label>
          <Textarea
            id="feishu-users"
            v-model="allowedUsers"
            class="min-h-20 resize-y font-mono text-xs"
            placeholder="ou_xxx&#10;ou_yyy"
            :disabled="loading || saving"
          />
        </div>
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-chats">群聊 ID</Label>
          <Textarea
            id="feishu-chats"
            v-model="allowedChats"
            class="min-h-20 resize-y font-mono text-xs"
            placeholder="oc_xxx"
            :disabled="loading || saving"
          />
        </div>
      </div>
    </section>

    <p v-if="settings.last_error" class="text-sm text-destructive">
      {{ settings.last_error }}
    </p>
    <p v-if="error" class="text-sm text-destructive">{{ error }}</p>

    <div class="flex justify-end">
      <Button :disabled="loading || saving" @click="save">
        <SaveIcon class="size-4" />
        {{ saving ? "保存中..." : settings.enabled ? "保存并启动" : "保存" }}
      </Button>
    </div>

    <Dialog v-model:open="deleteConfirmOpen">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>删除 {{ settings.name || "飞书 Bot" }}？</DialogTitle>
        </DialogHeader>
        <p class="text-sm text-muted-foreground">
          将停止该 Bot 并删除它的渠道配置，不会删除已有 Foya 会话。
        </p>
        <DialogFooter>
          <Button variant="outline" :disabled="deleting" @click="deleteConfirmOpen = false">
            取消
          </Button>
          <Button variant="destructive" :disabled="deleting" @click="remove">
            {{ deleting ? "删除中..." : "删除" }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
