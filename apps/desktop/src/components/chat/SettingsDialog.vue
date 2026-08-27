<script setup lang="ts">
import { ref, watch } from "vue";
import { api, type ProviderConfig } from "@/lib/api";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

const open = defineModel<boolean>("open", { default: false });

const baseUrl = ref("");
const model = ref("");
const apiKey = ref("");
const hasKey = ref(false);
const loading = ref(false);
const saving = ref(false);
const error = ref("");

// 打开时加载当前配置(key 脱敏,只显示是否已配置)。
watch(open, async (v) => {
  if (!v) return;
  error.value = "";
  loading.value = true;
  try {
    const cfg = await api.getProvider();
    baseUrl.value = cfg.base_url ?? "";
    model.value = cfg.model ?? "";
    hasKey.value = !!cfg.has_api_key;
    apiKey.value = "";
  } catch (e) {
    error.value = String(e);
  } finally {
    loading.value = false;
  }
});

async function save() {
  saving.value = true;
  error.value = "";
  try {
    const payload: ProviderConfig = {
      kind: "openai",
      base_url: baseUrl.value.trim(),
      model: model.value.trim(),
    };
    // 仅在用户输入了新 key 时携带,否则内核保留原 key。
    if (apiKey.value.trim()) payload.api_key = apiKey.value.trim();
    const saved = await api.setProvider(payload);
    hasKey.value = !!saved.has_api_key;
    apiKey.value = "";
    open.value = false;
  } catch (e) {
    error.value = String(e);
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>模型设置</DialogTitle>
        <DialogDescription>
          配置 OpenAI 兼容的模型服务(BYOK),直连你的模型端点。
        </DialogDescription>
      </DialogHeader>

      <div class="space-y-4 py-2">
        <div class="space-y-1.5">
          <Label for="base-url">Base URL</Label>
          <Input
            id="base-url"
            v-model="baseUrl"
            placeholder="http://127.0.0.1:8000/v1"
            :disabled="loading"
          />
        </div>
        <div class="space-y-1.5">
          <Label for="model">模型</Label>
          <Input id="model" v-model="model" placeholder="Qwen3-0.6B-4bit" :disabled="loading" />
        </div>
        <div class="space-y-1.5">
          <Label for="api-key">API Key</Label>
          <Input
            id="api-key"
            v-model="apiKey"
            type="password"
            :placeholder="hasKey ? '已配置(留空则保持不变)' : 'sk-...'"
            :disabled="loading"
          />
        </div>
        <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
      </div>

      <DialogFooter>
        <Button variant="ghost" :disabled="saving" @click="open = false">取消</Button>
        <Button :disabled="saving || loading" @click="save">
          {{ saving ? "保存中…" : "保存" }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
