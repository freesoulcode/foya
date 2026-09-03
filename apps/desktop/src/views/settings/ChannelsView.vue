<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { PlusIcon } from "@lucide/vue";
import { api, type FeishuBotSettings } from "@/lib/api";
import SettingsPage from "@/layouts/settings/SettingsPage.vue";
import FeishuChannelSettings from "@/components/settings/channels/FeishuChannelSettings.vue";
import { Button } from "@/components/ui/button";

const channels = ref<FeishuBotSettings[]>([]);
const selectedID = ref("");
const creating = ref(false);
const loading = ref(false);
const error = ref("");

const selectedChannel = computed(
  () => channels.value.find((item) => item.id === selectedID.value) ?? null
);

function statusLabel(channel: FeishuBotSettings) {
  if (channel.status === "running") return "运行中";
  if (channel.status === "error") return "异常";
  return channel.enabled ? "待连接" : "已停止";
}

function statusClass(channel: FeishuBotSettings) {
  if (channel.status === "running") return "bg-emerald-500";
  if (channel.status === "error") return "bg-destructive";
  return "bg-muted-foreground/50";
}

async function loadChannels(preserveSelection = true) {
  loading.value = true;
  error.value = "";
  try {
    const items = await api.listChannels();
    channels.value = items;
    if (
      !preserveSelection ||
      (!creating.value && !items.some((item) => item.id === selectedID.value))
    ) {
      selectedID.value = items[0]?.id ?? "";
    }
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

function selectChannel(id: string) {
  creating.value = false;
  selectedID.value = id;
}

function createFeishuBot() {
  creating.value = true;
  selectedID.value = "";
}

function handleSaved(channel: FeishuBotSettings) {
  const index = channels.value.findIndex((item) => item.id === channel.id);
  if (index >= 0) {
    channels.value[index] = channel;
  } else {
    channels.value.push(channel);
  }
  creating.value = false;
  selectedID.value = channel.id;
}

async function handleDeleted(channelID: string) {
  channels.value = channels.value.filter((item) => item.id !== channelID);
  creating.value = false;
  selectedID.value = channels.value[0]?.id ?? "";
}

onMounted(() => {
  void loadChannels(false);
});
</script>

<template>
  <SettingsPage title="消息渠道" content-class="min-h-0 flex-1">
    <div class="grid min-h-0 h-full overflow-hidden sm:grid-cols-[10rem_minmax(0,1fr)]">
      <nav
        class="flex min-h-0 flex-col border-b border-border pb-2 pr-3 sm:border-b-0 sm:border-r"
      >
        <div class="min-h-0 flex-1 overflow-y-auto">
          <button
            v-for="channel in channels"
            :key="channel.id"
            type="button"
            :class="[
              'flex h-11 w-full items-center justify-between gap-2 rounded-md px-2.5 text-left transition-colors',
              !creating && selectedID === channel.id
                ? 'bg-muted text-foreground'
                : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground',
            ]"
            @click="selectChannel(channel.id)"
          >
            <span class="min-w-0">
              <span class="block truncate text-sm font-medium">{{ channel.name }}</span>
              <span class="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
                <span :class="['size-1.5 rounded-full', statusClass(channel)]" />
                <span>{{ statusLabel(channel) }}</span>
              </span>
            </span>
          </button>
          <button
            v-if="creating"
            type="button"
            class="flex h-11 w-full items-center rounded-md bg-muted px-2.5 text-left"
          >
            <span>
              <span class="block text-sm font-medium">新建飞书 Bot</span>
              <span class="block text-xs text-muted-foreground">未保存</span>
            </span>
          </button>
        </div>
        <Button
          variant="ghost"
          class="mt-2 w-full justify-start"
          :disabled="loading || creating"
          @click="createFeishuBot"
        >
          <PlusIcon class="size-4" />
          新建 Bot
        </Button>
      </nav>

      <div class="min-h-0 min-w-0 overflow-x-hidden overflow-y-auto pt-3 sm:pl-5 sm:pt-0">
        <FeishuChannelSettings
          v-if="creating || selectedChannel"
          :key="creating ? 'new' : selectedChannel?.id"
          :channel="selectedChannel"
          :creating="creating"
          @saved="handleSaved"
          @deleted="handleDeleted"
          @refresh="loadChannels()"
        />
        <div
          v-else-if="!loading"
          class="flex h-40 flex-col items-center justify-center gap-3 text-center"
        >
          <p class="text-sm text-muted-foreground">尚未配置消息渠道</p>
          <Button size="sm" variant="outline" @click="createFeishuBot">
            <PlusIcon class="size-4" />
            新建飞书 Bot
          </Button>
        </div>
        <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
      </div>
    </div>
  </SettingsPage>
</template>
