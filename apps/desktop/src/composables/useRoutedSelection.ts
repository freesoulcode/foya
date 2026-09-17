import { computed, toValue, watch, type MaybeRefOrGetter, type Ref } from "vue";
import { useRoute, useRouter, type RouteLocationRaw } from "vue-router";
import { routeParam } from "@/router/navigation";

interface RoutedSelectionOptions {
  ready: MaybeRefOrGetter<boolean>;
  routeNames: string | readonly string[];
  paramName: string;
  activeId: Ref<string>;
  emptyRoute: MaybeRefOrGetter<"active" | "clear">;
  exists: (id: string) => boolean;
  select: (id: string) => void | Promise<void>;
  clear?: () => void;
  location: (id?: string) => RouteLocationRaw;
  onError?: (error: unknown) => void;
}

export type RoutedSelectionDecision =
  | { type: "none" }
  | { type: "clear" }
  | { type: "restore-active"; id: string }
  | { type: "select"; id: string }
  | { type: "invalid" };

export function resolveRoutedSelection(input: {
  ready: boolean;
  matchesRoute: boolean;
  routeId: string;
  activeId: string;
  emptyRoute: "active" | "clear";
  exists: (id: string) => boolean;
}): RoutedSelectionDecision {
  if (!input.ready || !input.matchesRoute) return { type: "none" };
  if (!input.routeId) {
    if (input.emptyRoute === "clear") return { type: "clear" };
    return input.activeId
      ? { type: "restore-active", id: input.activeId }
      : { type: "none" };
  }
  if (input.routeId === input.activeId) return { type: "none" };
  if (!input.exists(input.routeId)) return { type: "invalid" };
  return { type: "select", id: input.routeId };
}

export function useRoutedSelection(options: RoutedSelectionOptions) {
  const route = useRoute();
  const router = useRouter();
  const routeId = computed(() => routeParam(route.params[options.paramName]));
  const matchesRoute = () => {
    const names = Array.isArray(options.routeNames)
      ? options.routeNames
      : [options.routeNames];
    return names.includes(String(route.name ?? ""));
  };

  watch(
    [() => toValue(options.ready), () => route.name, routeId],
    ([ready, , id]) => {
      const decision = resolveRoutedSelection({
        ready,
        matchesRoute: matchesRoute(),
        routeId: id,
        activeId: options.activeId.value,
        emptyRoute: toValue(options.emptyRoute),
        exists: options.exists,
      });
      switch (decision.type) {
        case "clear":
          if (options.clear) options.clear();
          else options.activeId.value = "";
          break;
        case "restore-active":
          void router.replace(options.location(decision.id));
          break;
        case "invalid":
          void router.replace(options.location());
          break;
        case "select":
          Promise.resolve(options.select(decision.id)).catch((error) => {
            options.onError?.(error);
          });
          break;
        case "none":
          break;
      }
    },
    { immediate: true },
  );

  watch(
    options.activeId,
    (id) => {
      if (!toValue(options.ready) || !matchesRoute() || routeId.value === id) {
        return;
      }
      void router.replace(options.location(id));
    },
    { immediate: true },
  );
}
