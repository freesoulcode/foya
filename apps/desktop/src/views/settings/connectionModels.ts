import type { ModelSettings } from "@/lib/api";

export interface ConfiguredModels {
  models: Set<string>;
  settings: Record<string, ModelSettings>;
}

export function normalizeModelID(value: string): string {
  return value.trim();
}

export function addConfiguredModels(
  currentModels: Iterable<string>,
  currentSettings: Record<string, ModelSettings>,
  modelIDs: Iterable<string>,
  defaultSettings: () => ModelSettings,
): ConfiguredModels {
  const models = new Set(currentModels);
  const settings = { ...currentSettings };

  for (const value of modelIDs) {
    const model = normalizeModelID(value);
    if (!model || models.has(model)) continue;
    models.add(model);
    settings[model] = settings[model] ?? defaultSettings();
  }

  return { models, settings };
}

export function removeConfiguredModel(
  currentModels: Iterable<string>,
  currentSettings: Record<string, ModelSettings>,
  model: string,
): ConfiguredModels {
  const models = new Set(currentModels);
  models.delete(model);
  const settings = { ...currentSettings };
  delete settings[model];
  return { models, settings };
}
